// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycleprocess

import (
	"context"
	"errors"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/store"
)

func validCloseCommand() lifecycle.CloseAlertCommand {
	return lifecycle.CloseAlertCommand{OperationID: "operation-a", BKTenantID: "tenant-a", AlertID: "alert-a", OperatorKind: domain.OperatorKindUser, OperatorID: "admin", Reason: "verified", EffectiveAt: time.Now().UTC()}
}

type closeTestLocker struct {
	busy     bool
	released bool
	key      string
}

func (l *closeTestLocker) Acquire(_ context.Context, key string) (scheduler.Lease, error) {
	l.key = key
	if l.busy {
		return scheduler.Lease{}, scheduler.ErrLockBusy
	}
	return scheduler.Lease{}, nil
}

func (*closeTestLocker) Renew(context.Context, scheduler.Lease) error { return nil }

func (l *closeTestLocker) Release(ctx context.Context, _ scheduler.Lease) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.released = true
	return nil
}

func TestCloseLeaseRefusesBusyAndReleasesAfterCancellation(t *testing.T) {
	locker := &closeTestLocker{busy: true}
	_, err := closeUnderLease(context.Background(), locker, "tenant-source-fingerprint", func() (lifecycle.CloseAlertResult, error) {
		t.Fatal("busy close ran")
		return lifecycle.CloseAlertResult{}, nil
	})
	if !errors.Is(err, scheduler.ErrLockBusy) || locker.released {
		t.Fatalf("busy = %v release = %v", err, locker.released)
	}
	locker.busy = false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err = closeUnderLease(ctx, locker, "tenant-source-fingerprint", func() (lifecycle.CloseAlertResult, error) {
		cancel()
		return lifecycle.CloseAlertResult{}, context.Canceled
	})
	if !errors.Is(err, context.Canceled) || !locker.released || locker.key != "tenant-source-fingerprint" {
		t.Fatalf("cancel = %v release = %v", err, locker.released)
	}
}

func TestAlertCloserCommandValidationAndErrors(t *testing.T) {
	command := validCloseCommand()
	for _, tc := range []struct {
		name   string
		mutate func(*lifecycle.CloseAlertCommand)
	}{
		{"missing reason", func(c *lifecycle.CloseAlertCommand) { c.Reason = "" }},
		{"reason bytes", func(c *lifecycle.CloseAlertCommand) {
			for range 86 {
				c.Reason += "中"
			}
		}},
		{"system actor", func(c *lifecycle.CloseAlertCommand) { c.OperatorKind = domain.OperatorKindSystem }},
		{"future clock", func(c *lifecycle.CloseAlertCommand) { c.EffectiveAt = time.Now().Add(time.Hour) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalid := command
			tc.mutate(&invalid)
			closer := &AlertCloser{run: func(context.Context, lifecycle.CloseAlertCommand) (lifecycle.CloseAlertResult, error) {
				t.Fatal("invalid command executed")
				return lifecycle.CloseAlertResult{}, nil
			}}
			_, err := closer.CloseAlert(context.Background(), invalid)
			var target *CloseError
			if !errors.As(err, &target) || target.Status != 400 {
				t.Fatalf("error = %v", err)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{{"missing", store.ErrNotFound, 404}, {"conflict", store.ErrVersionConflict, 409}, {"terminal", store.ErrInvalidTransition, 409}, {"unknown partial", errors.New("secret infrastructure detail"), 502}} {
		t.Run(tc.name, func(t *testing.T) {
			closer := &AlertCloser{slots: make(chan struct{}, 4), run: func(_ context.Context, received lifecycle.CloseAlertCommand) (lifecycle.CloseAlertResult, error) {
				if received != command {
					t.Fatal("command changed")
				}
				return lifecycle.CloseAlertResult{}, tc.err
			}}
			_, err := closer.CloseAlert(context.Background(), command)
			var target *CloseError
			if !errors.As(err, &target) || target.Status != tc.status {
				t.Fatalf("error = %v", err)
			}
			if target.Message == tc.err.Error() {
				t.Fatal("raw dependency error leaked")
			}
		})
	}
}

func TestAlertCloserCapacityCancellationAndReuse(t *testing.T) {
	entered := make(chan struct{}, 4)
	closer := &AlertCloser{slots: make(chan struct{}, 4), run: func(ctx context.Context, _ lifecycle.CloseAlertCommand) (lifecycle.CloseAlertResult, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return lifecycle.CloseAlertResult{}, ctx.Err()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 4)
	for range 4 {
		go func() { _, err := closer.CloseAlert(ctx, validCloseCommand()); done <- err }()
	}
	for range 4 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("close did not start")
		}
	}
	_, err := closer.CloseAlert(context.Background(), validCloseCommand())
	var capacity *CloseError
	if !errors.As(err, &capacity) || capacity.Status != 429 {
		t.Fatalf("capacity error = %v", err)
	}
	cancel()
	for range 4 {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("close did not stop")
		}
	}
	if len(closer.slots) != 0 {
		t.Fatal("slots leaked")
	}
	_, err = closer.CloseAlert(ctx, validCloseCommand())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled call = %v", err)
	}
	closer.run = func(_ context.Context, command lifecycle.CloseAlertCommand) (lifecycle.CloseAlertResult, error) {
		return lifecycle.CloseAlertResult{Alert: domain.Alert{BKTenantID: command.BKTenantID, AlertID: command.AlertID, Status: domain.AlertStatusClosed}}, nil
	}
	result, err := closer.CloseAlert(context.Background(), validCloseCommand())
	if err != nil || result.Alert.Status != domain.AlertStatusClosed {
		t.Fatalf("reuse = %#v, %v", result, err)
	}
}
