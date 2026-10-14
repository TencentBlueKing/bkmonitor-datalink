// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package projection

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/store"
)

func TestInlineProjectionAndOneCompensation(t *testing.T) {
	for _, name := range []string{"success", "compensation success", "compensation failed", "ACK failed", "enqueue failed"} {
		t.Run(name, func(t *testing.T) {
			s, tasks, repo, _, sender, now, a := projectionFixture(t)
			compensator, openErr := NewCompensator(tasks, repo, s.resolver, s.sender, s.locker, s.now, 2)
			if openErr != nil {
				t.Fatal(openErr)
			}
			s = compensator
			row, err := repo.GetAlertCurrent(t.Context(), a.BKTenantID, a.AlertID)
			if err != nil {
				t.Fatal(err)
			}
			if name != "success" && name != "ACK failed" {
				sender.err = Failure{Code: "remote_unavailable", Retryable: true}
			}
			if name == "ACK failed" {
				repo.fail = true
			}
			if name == "enqueue failed" {
				tasks.failState = "pending"
			}
			projected, err := s.Project(t.Context(), row)
			if name == "success" {
				if err != nil || projected.Alert.Projection.Targets["kac"].SyncedRevision != a.Revision {
					t.Fatal("inline not confirmed", err)
				}
			} else if err == nil {
				t.Fatal("unconfirmed projection reported success")
			}
			id, _ := TaskID(a.BKTenantID, a.AlertID, "kac", a.Revision)
			task, readErr := tasks.Get(t.Context(), a.BKTenantID, id)
			if name == "enqueue failed" {
				if !errors.Is(readErr, ErrNotFound) {
					t.Fatal("failed enqueue reported queued", readErr)
				}
				return
			}
			if readErr != nil {
				t.Fatal(readErr)
			}
			if name == "success" {
				if task.Task.Progress.State != "succeeded" {
					t.Fatal(task.Task.Progress)
				}
				if _, err = s.Project(t.Context(), row); err != nil || sender.calls != 1 {
					t.Fatal("inline retry resent", err, sender.calls)
				}
				return
			}
			if name == "compensation success" {
				sender.err = nil
			}
			*now = now.Add(time.Second)
			task, err = s.Deliver(t.Context(), a.BKTenantID, id)
			if err != nil {
				t.Fatal(err)
			}
			want := "succeeded"
			if name == "compensation failed" {
				want = "failed"
			}
			if task.Task.Progress.State != want {
				t.Fatal(task.Task.Progress)
			}
			calls := sender.calls
			_, _ = s.Deliver(t.Context(), a.BKTenantID, id)
			_, _ = s.Project(t.Context(), row)
			if sender.calls != calls {
				t.Fatal("exhausted or confirmed task resent")
			}
			if name == "ACK failed" && calls != 1 {
				t.Fatal("ACK failure resent external write")
			}
		})
	}
}

func TestInlineProjectionConcurrentRequestsAndCancellation(t *testing.T) {
	s, tasks, repo, _, _, _, a := projectionFixture(t)
	row, err := repo.GetAlertCurrent(t.Context(), a.BKTenantID, a.AlertID)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	s.sender = senderFunc(func(ctx context.Context, _ Destination, q Request) (Receipt, error) {
		calls.Add(1)
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			return Receipt{}, ctx.Err()
		}
		return receiptFor(q), nil
	})
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() { _, _ = s.Project(t.Context(), row) })
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("duplicate inline send", calls.Load())
	}
	current, err := repo.GetAlertCurrent(t.Context(), a.BKTenantID, a.AlertID)
	if err != nil || current.Alert.Projection.Targets["kac"].SyncedRevision != 1 {
		t.Fatal("ACK lost", err)
	}
	id, _ := TaskID(a.BKTenantID, a.AlertID, "kac", 1)
	saved, err := tasks.Get(t.Context(), a.BKTenantID, id)
	if err != nil || saved.Task.Progress.State != "succeeded" {
		t.Fatal("confirmation lost", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _ = s.Project(ctx, row)
	if calls.Load() != 1 {
		t.Fatal("cancellation sent new write")
	}
	if _, err := s.ProjectCurrent(t.Context(), "another", a.AlertID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("tenant crossed", err)
	}
}

func TestInlineCapacityAndCompensationRestartBudget(t *testing.T) {
	s, tasks, repo, _, sender, now, a := projectionFixture(t)
	s.inlineSlots = make(chan struct{}, 1)
	s.inlineSlots <- struct{}{}
	row, err := repo.GetAlertCurrent(t.Context(), a.BKTenantID, a.AlertID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Project(t.Context(), row); err == nil || sender.calls != 0 {
		t.Fatal("capacity bypassed", err)
	}
	<-s.inlineSlots
	id, _ := TaskID(a.BKTenantID, a.AlertID, "kac", a.Revision)
	pending, err := tasks.Get(t.Context(), a.BKTenantID, id)
	if err != nil || pending.Task.Progress.State != "pending" {
		t.Fatal("capacity lost compensation", err)
	}
	sender.err = Failure{Code: "remote_unavailable", Retryable: true}
	retry, err := s.Deliver(t.Context(), a.BKTenantID, id)
	if err != nil || retry.Task.Progress.State != "retry" {
		t.Fatal(err)
	}
	s.maxAttempts = 1
	*now = now.Add(time.Minute)
	failed, err := s.Deliver(t.Context(), a.BKTenantID, id)
	if err != nil || failed.Task.Progress.State != "failed" || sender.calls != 1 {
		t.Fatal("restart reset compensation budget", err, sender.calls)
	}
}
