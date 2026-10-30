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
			pending.flush(ctx, nil)
			if sink.calls != 1 || sink.count != 10 {
				t.Fatal(sink.calls, sink.count)
			}
		})
	}
}
