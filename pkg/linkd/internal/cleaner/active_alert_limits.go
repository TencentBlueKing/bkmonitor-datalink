// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package cleaner

import (
	"context"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// ActiveAlertLimitChecker 对齐批次返回软限流诊断；nil 项表示放行，包括缓存降级。
type ActiveAlertLimitChecker interface {
	CheckBatch(context.Context, []domain.Event) []*store.ActiveAlertLimitDecision
}

func (r *Runtime) applyActiveAlertLimits(ctx context.Context, entries []*cleanerEntry) {
	if r.limitChecker == nil {
		return
	}
	events := []domain.Event{}
	pending := []*cleanerEntry{}
	for _, entry := range entries {
		if entry.limitPending {
			pending = append(pending, entry)
			events = append(events, entry.event)
		}
	}
	if len(pending) == 0 {
		return
	}
	decisions := r.limitChecker.CheckBatch(ctx, events)
	for i, entry := range pending {
		entry.limitPending = false
		if decisions[i] == nil {
			continue
		}
		result := store.EventResult{ActiveAlertLimit: decisions[i], State: domain.EventProcessStateSuppressed, Outcome: "alert_limited", ReasonCode: "active_alert_limit_exceeded", ProcessedAt: time.Now().UTC()}
		for _, e := range entry.event.Evaluations {
			result.Evaluations = append(result.Evaluations, store.EvaluationResult{Severity: e.Severity, Action: e.Action, State: result.State, Outcome: result.Outcome, ReasonCode: result.ReasonCode})
		}
		var updated store.StoredEvent
		var err error
		switch writer := r.events.(type) {
		case store.LifecycleEventSnapshotStore:
			updated, err = writer.CompareAndSetLifecycleEventResultFromSnapshot(ctx, entry.storedSnapshot, result)
		case store.EventStore:
			updated, err = writer.CompareAndSetEventResult(ctx, entry.event.BKTenantID, entry.event.EventID, entry.storedSnapshot.Version, result)
		default:
			continue
		}
		if err != nil {
			r.logEntryFailure(ctx, entry, "active_alert_limit", "limit_result_write_failed", err)
			continue
		}
		entry.event = updated.Event
		entry.skipMailbox = true
	}
}
