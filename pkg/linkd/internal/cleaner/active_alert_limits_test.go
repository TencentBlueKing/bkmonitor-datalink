// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package cleaner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type limitAll struct{ calls int }

func (c *limitAll) CheckBatch(_ context.Context, events []domain.Event) []*store.ActiveAlertLimitDecision {
	c.calls++
	out := make([]*store.ActiveAlertLimitDecision, len(events))
	for i := range out {
		out[i] = &store.ActiveAlertLimitDecision{Rule: "strategy", Group: map[string]string{"labels.strategy_id": "123"}, Count: 5000, MaxActive: 5000}
	}
	return out
}

type failLimitResult struct{ *memory.Repository }

func (r failLimitResult) CompareAndSetEventResult(context.Context, string, string, store.VersionToken, store.EventResult) (store.StoredEvent, error) {
	return store.StoredEvent{}, errors.New("store offline")
}

func TestCleanerLimitResultAndReplay(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{true: "diagnostic failure passes", false: "limited and replay"}[fail], func(t *testing.T) {
			repo := memory.New()
			e := storetest.Event("tenant", "e", "fp", "warning")
			created, err := repo.CreateEvent(t.Context(), e)
			if err != nil {
				t.Fatal(err)
			}
			checker := &limitAll{}
			var writer EventBatchWriter = repo
			if fail {
				writer = failLimitResult{repo}
			}
			r := &Runtime{events: writer, limitChecker: checker, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			entry := &cleanerEntry{event: created.Event, stored: true, limitPending: true, storedSnapshot: created.StoredEvent}
			r.applyActiveAlertLimits(t.Context(), []*cleanerEntry{entry})
			stored, err := repo.GetEvent(t.Context(), e.BKTenantID, e.EventID)
			if err != nil {
				t.Fatal(err)
			}
			if fail {
				if entry.skipMailbox || stored.Processing.State != domain.EventProcessStateUnprocessed {
					t.Fatal("failure blocked")
				}
				return
			}
			if !entry.skipMailbox || stored.Processing.State != domain.EventProcessStateSuppressed || stored.Processing.ActiveAlertLimit.MaxActive != 5000 {
				t.Fatal(stored)
			}
			replay, _ := repo.CreateEvent(t.Context(), e)
			r.applyActiveAlertLimits(t.Context(), []*cleanerEntry{{event: replay.Event, stored: true}})
			if checker.calls != 1 {
				t.Fatal("replay rechecked quota")
			}
		})
	}
}
