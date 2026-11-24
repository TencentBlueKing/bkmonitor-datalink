// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package lifecycleprocess

import (
	"context"
	"errors"
	"sync"
	"testing"

	"linkd/internal/domain"
)

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
			pending.flush(ctx, nil, nil, nil)
			if sink.calls != 1 || sink.count != 10 {
				t.Fatal(sink.calls, sink.count)
			}
		})
	}
}

type projectedNotificationSink struct {
	changes []domain.AlertChange
	err     error
}

func (p *projectedNotificationSink) PublishProjectedChanges(_ context.Context, changes []domain.AlertChange) []error {
	p.changes = append(p.changes, changes...)
	out := make([]error, len(changes))
	for i := range out {
		out[i] = p.err
	}
	return out
}

func TestCloseNotificationsDoNotReprojectSuccessfulItems(t *testing.T) {
	pending := &closeBatchNotifications{}
	ordinary := &closeNotificationSink{}
	direct := &projectedNotificationSink{err: errors.New("action queue unavailable")}
	changes := []domain.AlertChange{{BKTenantID: "tenant", AlertID: "success", Action: &domain.AlertAction{ID: "original-action", Type: "close"}}, {BKTenantID: "tenant", AlertID: "failed"}}
	publisher := pending.use(ordinary)
	for _, err := range publisher.(interface {
		PublishBatch(context.Context, []domain.AlertChange) []error
	}).PublishBatch(t.Context(), changes) {
		if err != nil {
			t.Fatal(err)
		}
	}
	pending.flush(t.Context(), nil, direct, map[string]bool{"success": true})
	if ordinary.count != 1 || len(direct.changes) != 1 || direct.changes[0].AlertID != "success" || direct.changes[0].Action.ID != "original-action" {
		t.Fatal("successful projection entered ordinary notification path", ordinary.count, direct.changes)
	}
}
