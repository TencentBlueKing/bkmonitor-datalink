// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package lifecycleprocess

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/store"
)

func TestCloseBatchStopsBeforeInsufficientNextWave(t *testing.T) {
	closer, repo, _, commands, held := closeBatchFixture(t, 4)
	closer.batchSize = 2
	ctx, cancel := context.WithTimeout(t.Context(), 8200*time.Millisecond)
	defer cancel()
	original := closer.withProcessor
	closer.withProcessor = func(ctx context.Context, a store.StoredAlert, run func(*lifecycle.Processor) error) error {
		timer := time.NewTimer(4500 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
		return original(ctx, a, run)
	}
	results := closer.CloseAlerts(ctx, commands)
	for i, result := range results {
		if i < 2 {
			if result.Err != nil {
				t.Fatal(result.Err)
			}
		} else {
			var e *CloseError
			if !errors.As(result.Err, &e) || e.Code != "not_attempted" {
				t.Fatal(result.Err)
			}
		}
	}
	if ctx.Err() != nil || repo.writes.Load() != 1 || held.Load() != 0 {
		t.Fatal("started an unaffordable wave", ctx.Err(), repo.writes.Load(), held.Load())
	}
}

func TestCloseBatchColdStartRequiresEstimatedBudget(t *testing.T) {
	closer, repo, _, commands, held := closeBatchFixture(t, 2)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	for _, result := range closer.CloseAlerts(ctx, commands) {
		var e *CloseError
		if !errors.As(result.Err, &e) || e.Code != "not_attempted" {
			t.Fatal(result.Err)
		}
	}
	if repo.reads.Load() != 0 || held.Load() != 0 {
		t.Fatal("short budget started reads or leases")
	}
}

type closeNotificationSink struct {
	calls, count int
	err          error
}

func (p *closeNotificationSink) Publish(context.Context, domain.AlertChange) error {
	panic("single publish")
}

func (p *closeNotificationSink) PublishBatch(ctx context.Context, c []domain.AlertChange) []error {
	p.calls++
	p.count += len(c)
	err := p.err
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	results := make([]error, len(c))
	for i := range results {
		results[i] = err
	}
	return results
}

func TestCloseNotificationsWaitForExplicitCompletionAndKeepBatch(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "cancelled"}[cancelled], func(t *testing.T) {
			pending := &closeBatchNotifications{}
			sink := &closeNotificationSink{err: errors.New("queue failed")}
			publisher := pending.use(sink)
			var workers sync.WaitGroup
			for range 10 {
				workers.Go(func() {
					if err := publisher.Publish(t.Context(), domain.AlertChange{BKTenantID: "tenant", AlertID: "alert"}); err != nil {
						t.Error(err)
					}
				})
			}
			workers.Wait()
			if sink.calls != 0 {
				t.Fatal("notification escaped before synchronous projection")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if cancelled {
				cancel()
			}
			pending.flush(ctx, nil)
			if sink.calls != 1 || sink.count != 10 {
				t.Fatal(sink.calls, sink.count)
			}
		})
	}
}
