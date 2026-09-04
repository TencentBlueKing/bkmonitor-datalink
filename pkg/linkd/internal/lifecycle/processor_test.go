// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/memory"
)

func TestProcessEventCreateAndRepeatedTrigger(t *testing.T) {
	repo := memory.New()
	hook := &recordingHook{}
	processor := newTestProcessor(t, repo, hook)
	first := testEvent("event-1", "warning")
	created := persistAndProcess(t, repo, processor, first)
	if created.Outcome != OutcomeAlertCreated || created.AlertID == "" {
		t.Fatalf("created=%#v", created)
	}
	second := testEvent("event-2", "warning")
	second.Title = "changed title"
	second.OccurredAt = second.OccurredAt.Add(-time.Minute)
	updated := persistAndProcess(t, repo, processor, second)
	if updated.Outcome != OutcomeAlertUpdated || updated.AlertID != created.AlertID {
		t.Fatalf("updated=%#v", updated)
	}
	alert, err := repo.GetAlert(context.Background(), first.BKTenantID, created.AlertID)
	if err != nil {
		t.Fatal(err)
	}
	if alert.Alert.Title != first.Title || alert.Alert.LatestEventID != second.EventID || !alert.Alert.LastOccurredAt.Equal(second.OccurredAt) {
		t.Fatalf("alert=%#v", alert.Alert)
	}
	if len(hook.inputs) != 2 {
		t.Fatalf("hook calls=%d", len(hook.inputs))
	}
}

func TestProcessEventTerminalReplayDoesNotRepeatSideEffects(t *testing.T) {
	base := memory.New()
	repo := &eventReadTrackingRepository{Repository: base}
	hook := &recordingHook{}
	processor := newTestProcessor(t, repo, hook)
	event := testEvent("event-replayed", "warning")
	first := persistAndProcess(t, repo, processor, event)
	second, err := processor.ProcessEvent(context.Background(), mustGetStoredEvent(t, base, event))
	if err != nil {
		t.Fatal(err)
	}
	if second.EventID != first.EventID || second.AlertID != first.AlertID || second.Outcome != first.Outcome {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
	if len(hook.inputs) != 1 {
		t.Fatalf("hook calls=%d, want 1", len(hook.inputs))
	}
	if repo.eventReads != 0 {
		t.Fatalf("terminal replay event reads=%d, want 0", repo.eventReads)
	}
}

func TestProcessEventUsesInitialStoredEventUntilConflict(t *testing.T) {
	base := memory.New()
	repository := &eventReadTrackingRepository{Repository: base}
	processor := newTestProcessor(t, repository, NoopFinalHook{})
	event := testEvent("event-initial-snapshot", "warning")
	created, err := repository.CreateEvent(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	result, err := processor.ProcessEvent(context.Background(), created.StoredEvent)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeAlertCreated || repository.eventReads != 0 {
		t.Fatalf("result=%#v event_reads=%d", result, repository.eventReads)
	}
}

func TestSeverityUpgradeAndSuppression(t *testing.T) {
	repo := memory.New()
	hook := &recordingHook{}
	processor := newTestProcessor(t, repo, hook)
	warning := testEvent("event-warning", "warning")
	first := persistAndProcess(t, repo, processor, warning)
	critical := testEvent("event-critical", "critical")
	rotated := persistAndProcess(t, repo, processor, critical)
	if rotated.Outcome != OutcomeAlertRotated || rotated.AlertID == first.AlertID {
		t.Fatalf("rotated=%#v", rotated)
	}
	old, err := repo.GetAlert(context.Background(), warning.BKTenantID, first.AlertID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Alert.Status != domain.AlertStatusClosed || old.Alert.EndType != domain.AlertEndTypeSeverityUpgrade || old.Alert.LatestEventID != critical.EventID {
		t.Fatalf("old=%#v", old.Alert)
	}
	info := testEvent("event-info", "info")
	suppressed := persistAndProcess(t, repo, processor, info)
	if suppressed.Outcome != OutcomeAlertSuppressed || suppressed.EventState != domain.EventProcessStateSuppressed || suppressed.AlertID != rotated.AlertID {
		t.Fatalf("suppressed=%#v", suppressed)
	}
	stored, _ := repo.GetEvent(context.Background(), info.BKTenantID, info.EventID)
	if stored.Event.RelatedAlertID != rotated.AlertID {
		t.Fatalf("suppressed related alert=%q, want %q", stored.Event.RelatedAlertID, rotated.AlertID)
	}
	active, _ := repo.FindActiveAlert(context.Background(), store.ActiveAlertKey{BKTenantID: info.BKTenantID, EventSourceID: info.EventSourceID, Fingerprint: info.Fingerprint})
	if active.Alert.Severity != "critical" || active.Alert.LatestEventID != critical.EventID {
		t.Fatalf("active=%#v", active.Alert)
	}
	if len(hook.inputs) != 3 {
		t.Fatalf("hook calls=%d, want create + two upgrade snapshots", len(hook.inputs))
	}
}

func TestResolvedIgnoresSeverityAndOrphan(t *testing.T) {
	repo := memory.New()
	processor := newTestProcessor(t, repo, &recordingHook{})
	opening := testEvent("event-1", "warning")
	created := persistAndProcess(t, repo, processor, opening)
	resolved := testEvent("event-2", "info")
	resolved.Action = domain.EventActionResolved
	resolved.ActionReason = "source resolved"
	result := persistAndProcess(t, repo, processor, resolved)
	if result.Outcome != OutcomeAlertRecovered || result.AlertID != created.AlertID {
		t.Fatalf("resolved=%#v", result)
	}
	alert, _ := repo.GetAlert(context.Background(), opening.BKTenantID, created.AlertID)
	if alert.Alert.Status != domain.AlertStatusRecovered || alert.Alert.EndType != domain.AlertEndTypeSource {
		t.Fatalf("alert=%#v", alert.Alert)
	}
	orphan := testEvent("event-orphan", "warning")
	orphan.Fingerprint = "other"
	orphan.Action = domain.EventActionClosed
	orphanResult := persistAndProcess(t, repo, processor, orphan)
	if orphanResult.Outcome != OutcomeEventOrphaned || orphanResult.EventState != domain.EventProcessStateOrphaned {
		t.Fatalf("orphan=%#v", orphanResult)
	}
}

func TestEnricherCreationResults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		enrich     func(EnrichInput) (EnrichResult, error)
		wantStatus domain.EnrichStatus
	}{
		{name: "succeeded", enrich: func(EnrichInput) (EnrichResult, error) {
			return EnrichResult{Status: domain.EnrichStatusSucceeded, Data: domain.JSONObject{"owner": []byte(`"ops"`)}}, nil
		}, wantStatus: domain.EnrichStatusSucceeded},
		{name: "partial", enrich: func(EnrichInput) (EnrichResult, error) {
			return EnrichResult{Status: domain.EnrichStatusPartial, Data: domain.JSONObject{}}, nil
		}, wantStatus: domain.EnrichStatusPartial},
		{name: "reported failed", enrich: func(EnrichInput) (EnrichResult, error) {
			return EnrichResult{Status: domain.EnrichStatusFailed, Data: domain.JSONObject{}}, nil
		}, wantStatus: domain.EnrichStatusFailed},
		{name: "pending is reserved", enrich: func(EnrichInput) (EnrichResult, error) {
			return EnrichResult{Status: domain.EnrichStatusPending}, nil
		}, wantStatus: domain.EnrichStatusFailed},
		{name: "invalid status", enrich: func(EnrichInput) (EnrichResult, error) {
			return EnrichResult{Status: "unknown"}, nil
		}, wantStatus: domain.EnrichStatusFailed},
		{name: "error", enrich: func(EnrichInput) (EnrichResult, error) {
			return EnrichResult{}, errors.New("lookup failed")
		}, wantStatus: domain.EnrichStatusFailed},
		{name: "panic", enrich: func(EnrichInput) (EnrichResult, error) {
			panic("broken enricher")
		}, wantStatus: domain.EnrichStatusFailed},
		{name: "input mutation is isolated", enrich: func(input EnrichInput) (EnrichResult, error) {
			input.Event.Dimensions["host"] = domain.NewStringScalar("changed")
			input.Alert.Title = "changed"
			return EnrichResult{Status: domain.EnrichStatusSucceeded, Data: domain.JSONObject{}}, nil
		}, wantStatus: domain.EnrichStatusSucceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := memory.New()
			processor, err := NewProcessor(repo, NoopRecentAlertCache{}, DeterministicAlertIDGenerator{}, stubEnricher{fn: tt.enrich},
				NoopFinalHook{}, testSeverity{}, fixedClock{time.Date(2026, 9, 1, 0, 10, 0, 0, time.UTC)}, discardLogger{})
			if err != nil {
				t.Fatal(err)
			}
			event := testEvent("event-enrich", "warning")
			result := persistAndProcess(t, repo, processor, event)
			stored, err := repo.GetAlert(context.Background(), event.BKTenantID, result.AlertID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Alert.EnrichStatus != tt.wantStatus || stored.Alert.Title != event.Title {
				t.Fatalf("enriched Alert = %#v", stored.Alert)
			}
			if host, _ := stored.Alert.Dimensions["host"].StringValue(); host != "host-1" {
				t.Fatalf("enricher mutated inherited dimensions: %#v", stored.Alert.Dimensions)
			}
		})
	}
}

func TestResumePartiallyCreatedAlert(t *testing.T) {
	repo := memory.New()
	processor := newTestProcessor(t, repo, &recordingHook{})
	event := testEvent("event-1", "warning")
	stored, _ := repo.CreateEvent(context.Background(), event)
	alert, err := processor.newAlert(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateAlert(context.Background(), alert); err != nil {
		t.Fatal(err)
	}
	result, err := processor.ProcessEvent(context.Background(), stored.StoredEvent)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeAlertCreated || result.AlertID != alert.AlertID {
		t.Fatalf("result=%#v", result)
	}
	updated, _ := repo.GetEvent(context.Background(), event.BKTenantID, event.EventID)
	if updated.Version == stored.Version || updated.Processing.State != domain.EventProcessStateAccepted {
		t.Fatalf("event=%#v", updated)
	}
}

func TestCloseAlert(t *testing.T) {
	repo := memory.New()
	hook := &recordingHook{}
	processor := newTestProcessor(t, repo, hook)
	event := testEvent("event-1", "warning")
	created := persistAndProcess(t, repo, processor, event)
	effective := time.Date(2026, 9, 1, 1, 0, 0, 0, time.UTC)
	command := CloseAlertCommand{OperationID: "op-1", BKTenantID: event.BKTenantID, AlertID: created.AlertID, OperatorKind: domain.OperatorKindUser, OperatorID: "user-1", Reason: "manual", EffectiveAt: effective}
	invalid := command
	invalid.Reason = ""
	if err := invalid.Validate(); err == nil {
		t.Fatal("CloseAlertCommand without reason was accepted")
	}
	result, err := processor.CloseAlert(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if result.Alert.Status != domain.AlertStatusClosed || result.Alert.EndType != domain.AlertEndTypeUser || result.Alert.LatestEventID != event.EventID {
		t.Fatalf("closed=%#v", result.Alert)
	}
	retry, err := processor.CloseAlert(context.Background(), command)
	if err != nil || !retry.AlreadyClosed {
		t.Fatalf("retry=%#v,%v", retry, err)
	}
}

func TestProcessEventBatchesLogsBeforeFinalEventCAS(t *testing.T) {
	tests := []struct {
		name          string
		setupSeverity string
		event         domain.Event
		wantCalls     []string
		wantKinds     []domain.OperationKind
	}{
		{
			name: "create", event: testEvent("event-batch-create", "warning"),
			wantCalls: []string{"alert_create", "hook", "logs", "event_cas"},
			wantKinds: []domain.OperationKind{domain.OperationKindTrigger, domain.OperationKindPush},
		},
		{
			name: "update", setupSeverity: "warning", event: testEvent("event-batch-update", "warning"),
			wantCalls: []string{"alert_cas", "hook", "logs", "event_cas"},
			wantKinds: []domain.OperationKind{domain.OperationKindPush},
		},
		{
			name: "recover", setupSeverity: "warning", event: func() domain.Event {
				event := testEvent("event-batch-recover", "info")
				event.Action = domain.EventActionResolved
				return event
			}(),
			wantCalls: []string{"alert_cas", "hook", "logs", "event_cas"},
			wantKinds: []domain.OperationKind{domain.OperationKindRecover, domain.OperationKindPush},
		},
		{
			name: "source close", setupSeverity: "warning", event: func() domain.Event {
				event := testEvent("event-batch-close", "warning")
				event.Action = domain.EventActionClosed
				return event
			}(),
			wantCalls: []string{"alert_cas", "hook", "logs", "event_cas"},
			wantKinds: []domain.OperationKind{domain.OperationKindClose, domain.OperationKindPush},
		},
		{
			name: "suppress", setupSeverity: "critical", event: testEvent("event-batch-suppress", "info"),
			wantCalls: []string{"logs", "event_cas"},
			wantKinds: []domain.OperationKind{domain.OperationKindSuppress},
		},
		{
			name: "severity upgrade", setupSeverity: "warning", event: testEvent("event-batch-rotate", "critical"),
			wantCalls: []string{"alert_cas", "alert_create", "hook", "hook", "logs", "event_cas"},
			wantKinds: []domain.OperationKind{
				domain.OperationKindClose,
				domain.OperationKindPush,
				domain.OperationKindTrigger,
				domain.OperationKindPush,
			},
		},
		{
			name: "orphan", event: func() domain.Event {
				event := testEvent("event-batch-orphan", "warning")
				event.Action = domain.EventActionResolved
				return event
			}(),
			wantCalls: []string{"event_cas"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := memory.New()
			if tt.setupSeverity != "" {
				opening := testEvent("opening-"+tt.name, tt.setupSeverity)
				persistAndProcess(t, base, newTestProcessor(t, base, NoopFinalHook{}), opening)
			}
			repository := &trackingRepository{Repository: base, failBatchIndex: -1}
			processor := newTestProcessor(t, repository, trackingHook{repository: repository})
			persistAndProcess(t, repository, processor, tt.event)
			calls, batches := repository.snapshot()
			if !slices.Equal(calls, tt.wantCalls) {
				t.Fatalf("calls=%v, want %v", calls, tt.wantCalls)
			}
			if len(tt.wantKinds) == 0 {
				if len(batches) != 0 {
					t.Fatalf("batches=%v, want none", batches)
				}
				return
			}
			if len(batches) != 1 {
				t.Fatalf("batch count=%d, want 1", len(batches))
			}
			kinds := make([]domain.OperationKind, 0, len(batches[0]))
			for _, log := range batches[0] {
				kinds = append(kinds, log.OperationKind)
			}
			if !slices.Equal(kinds, tt.wantKinds) {
				t.Fatalf("operation kinds=%v, want %v", kinds, tt.wantKinds)
			}
		})
	}
}

func TestProcessEventDoesNotFinishEventWhenLogBatchHasItemFailure(t *testing.T) {
	base := memory.New()
	repository := &trackingRepository{Repository: base, failBatchIndex: 1}
	processor := newTestProcessor(t, repository, trackingHook{repository: repository})
	event := testEvent("event-batch-partial", "warning")
	created, err := repository.CreateEvent(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.ProcessEvent(context.Background(), created.StoredEvent); err == nil {
		t.Fatal("ProcessEvent() error = nil, want alert log item failure")
	}
	stored, err := base.GetEvent(context.Background(), event.BKTenantID, event.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Processing.State != domain.EventProcessStateUnprocessed {
		t.Fatalf("event state=%q, want unprocessed", stored.Processing.State)
	}
	calls, batches := repository.snapshot()
	if !slices.Equal(calls, []string{"alert_create", "hook", "logs"}) || len(batches) != 1 || len(batches[0]) != 2 {
		t.Fatalf("calls=%v batches=%v", calls, batches)
	}

	repository.reset(-1)
	result, err := processor.ProcessEvent(context.Background(), mustGetStoredEvent(t, repository, event))
	if err != nil {
		t.Fatal(err)
	}
	if result.EventState != domain.EventProcessStateAccepted {
		t.Fatalf("retry result=%#v", result)
	}
	calls, _ = repository.snapshot()
	if !slices.Equal(calls, []string{"hook", "logs", "event_cas"}) {
		t.Fatalf("retry calls=%v", calls)
	}
}

func TestCloseAlertBatchesOperationAndHookLogs(t *testing.T) {
	base := memory.New()
	event := testEvent("event-close-batch", "warning")
	created := persistAndProcess(t, base, newTestProcessor(t, base, NoopFinalHook{}), event)
	repository := &trackingRepository{Repository: base, failBatchIndex: -1}
	processor := newTestProcessor(t, repository, trackingHook{repository: repository})
	command := CloseAlertCommand{
		OperationID: "operation-close-batch", BKTenantID: event.BKTenantID, AlertID: created.AlertID,
		OperatorKind: domain.OperatorKindUser, OperatorID: "user-1", Reason: "manual",
		EffectiveAt: time.Date(2026, 9, 1, 1, 0, 0, 0, time.UTC),
	}
	if _, err := processor.CloseAlert(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	calls, batches := repository.snapshot()
	if !slices.Equal(calls, []string{"alert_cas", "hook", "logs"}) || len(batches) != 1 || len(batches[0]) != 2 {
		t.Fatalf("calls=%v batches=%v", calls, batches)
	}
}

func TestProcessEventUsesRecentActiveAlertBeforeRepositorySearch(t *testing.T) {
	base := memory.New()
	opening := testEvent("event-cache-opening", "warning")
	alert := storetestAlert(opening, "alert-cache")
	created, err := base.CreateAlert(context.Background(), alert)
	if err != nil {
		t.Fatal(err)
	}
	incoming := testEvent("event-cache-update", "warning")
	storedIncoming, err := base.CreateEvent(context.Background(), incoming)
	if err != nil {
		t.Fatal(err)
	}
	repository := &lookupTrackingRepository{Repository: base}
	cache := newMemoryRecentAlertCache()
	if err := cache.PutCurrent(context.Background(), created.StoredAlert); err != nil {
		t.Fatal(err)
	}
	processor := newTestProcessorWithCache(t, repository, cache, NoopFinalHook{})
	result, err := processor.ProcessEvent(context.Background(), storedIncoming.StoredEvent)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeAlertUpdated || repository.findActiveCalls != 0 {
		t.Fatalf("result=%#v find_active_calls=%d", result, repository.findActiveCalls)
	}
	cached, found, err := cache.GetCurrent(context.Background(), activeKeyForTest(incoming))
	if err != nil || !found || cached.Alert.LatestEventID != incoming.EventID {
		t.Fatalf("cached=%#v found=%t err=%v", cached, found, err)
	}
}

func TestProcessEventUsesRecentTerminalAlertForRecovery(t *testing.T) {
	base := memory.New()
	opening := testEvent("event-terminal-opening", "warning")
	alert := storetestAlert(opening, "alert-terminal")
	created, err := base.CreateAlert(context.Background(), alert)
	if err != nil {
		t.Fatal(err)
	}
	endedEvent := testEvent("event-terminal-cache", "warning")
	endedEvent.Action = domain.EventActionResolved
	terminal := alert.Clone()
	terminal.Status = domain.AlertStatusRecovered
	terminal.LatestEventID = endedEvent.EventID
	terminal.LastOccurredAt = terminal.LastOccurredAt.Add(time.Minute)
	terminal.UpdateAt = terminal.UpdateAt.Add(time.Minute)
	endAt := terminal.LastOccurredAt
	terminal.EndAt = &endAt
	terminal.EndType = domain.AlertEndTypeSource
	ended, err := base.CompareAndSetAlert(
		context.Background(), alert.BKTenantID, alert.AlertID, created.Version, terminal,
	)
	if err != nil {
		t.Fatal(err)
	}
	storedEndedEvent, err := base.CreateEvent(context.Background(), endedEvent)
	if err != nil {
		t.Fatal(err)
	}
	repository := &lookupTrackingRepository{Repository: base}
	cache := newMemoryRecentAlertCache()
	if err := cache.PutTerminal(context.Background(), ended); err != nil {
		t.Fatal(err)
	}
	processor := newTestProcessorWithCache(t, repository, cache, NoopFinalHook{})
	result, err := processor.ProcessEvent(context.Background(), storedEndedEvent.StoredEvent)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeAlertRecovered || repository.findActiveCalls != 0 || repository.findEndedCalls != 0 {
		t.Fatalf("result=%#v active_calls=%d ended_calls=%d", result, repository.findActiveCalls, repository.findEndedCalls)
	}
}

func TestProcessEventKeepsEventUnprocessedWhenRecentCacheWriteFails(t *testing.T) {
	base := memory.New()
	event := testEvent("event-cache-failure", "warning")
	storedEvent, err := base.CreateEvent(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	cache := newMemoryRecentAlertCache()
	cache.putErr = errors.New("redis unavailable")
	processor := newTestProcessorWithCache(t, base, cache, NoopFinalHook{})
	if _, err := processor.ProcessEvent(context.Background(), storedEvent.StoredEvent); err == nil ||
		!errors.Is(err, cache.putErr) {
		t.Fatalf("ProcessEvent() error=%v", err)
	}
	stored, err := base.GetEvent(context.Background(), event.BKTenantID, event.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Processing.State != domain.EventProcessStateUnprocessed {
		t.Fatalf("event state=%q", stored.Processing.State)
	}
}

func TestProcessEventDoesNotFallbackWhenRecentCacheReadFails(t *testing.T) {
	base := memory.New()
	event := testEvent("event-cache-read-failure", "warning")
	storedEvent, err := base.CreateEvent(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	repository := &lookupTrackingRepository{Repository: base}
	cache := newMemoryRecentAlertCache()
	cache.getErr = errors.New("redis unavailable")
	processor := newTestProcessorWithCache(t, repository, cache, NoopFinalHook{})
	if _, err := processor.ProcessEvent(context.Background(), storedEvent.StoredEvent); err == nil ||
		!errors.Is(err, cache.getErr) {
		t.Fatalf("ProcessEvent() error=%v", err)
	}
	if repository.findActiveCalls != 0 || repository.findEndedCalls != 0 {
		t.Fatalf("repository fallback active=%d ended=%d", repository.findActiveCalls, repository.findEndedCalls)
	}
}

func TestProcessEventRepairsRecentCacheAfterAlertCASConflict(t *testing.T) {
	base := memory.New()
	opening := testEvent("event-conflict-opening", "warning")
	alert := storetestAlert(opening, "alert-conflict")
	created, err := base.CreateAlert(context.Background(), alert)
	if err != nil {
		t.Fatal(err)
	}
	incoming := testEvent("event-conflict-update", "warning")
	storedIncoming, err := base.CreateEvent(context.Background(), incoming)
	if err != nil {
		t.Fatal(err)
	}
	repository := &conflictingLifecycleRepository{Repository: base}
	cache := newMemoryRecentAlertCache()
	if err := cache.PutCurrent(context.Background(), created.StoredAlert); err != nil {
		t.Fatal(err)
	}
	processor := newTestProcessorWithCache(t, repository, cache, NoopFinalHook{})
	result, err := processor.ProcessEvent(context.Background(), storedIncoming.StoredEvent)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeAlertUpdated || repository.conflicts != 1 || repository.currentReads != 1 ||
		repository.eventReads != 1 {
		t.Fatalf(
			"result=%#v conflicts=%d current_reads=%d event_reads=%d",
			result,
			repository.conflicts,
			repository.currentReads,
			repository.eventReads,
		)
	}
	cached, found, err := cache.GetCurrent(context.Background(), activeKeyForTest(incoming))
	if err != nil || !found || cached.Alert.LatestEventID != incoming.EventID {
		t.Fatalf("cached=%#v found=%t err=%v", cached, found, err)
	}
}

func TestProcessEventRecoversRotationFromRecentEndedAlert(t *testing.T) {
	base := memory.New()
	cache := newMemoryRecentAlertCache()
	opening := testEvent("event-rotation-opening", "warning")
	persistAndProcess(t, base, newTestProcessorWithCache(t, base, cache, NoopFinalHook{}), opening)

	upgrade := testEvent("event-rotation-upgrade", "critical")
	storedUpgrade, err := base.CreateEvent(context.Background(), upgrade)
	if err != nil {
		t.Fatal(err)
	}
	failing := &trackingRepository{Repository: base, failBatchIndex: 0}
	first := newTestProcessorWithCache(t, failing, cache, NoopFinalHook{})
	if _, err := first.ProcessEvent(context.Background(), storedUpgrade.StoredEvent); err == nil {
		t.Fatal("first rotation unexpectedly completed")
	}
	storedEvent, err := base.GetEvent(context.Background(), upgrade.BKTenantID, upgrade.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if storedEvent.Processing.State != domain.EventProcessStateUnprocessed {
		t.Fatalf("event state=%q", storedEvent.Processing.State)
	}
	if _, found, err := cache.GetEndedByEvent(context.Background(), upgrade.BKTenantID, upgrade.EventID); err != nil || !found {
		t.Fatalf("ended cache found=%t err=%v", found, err)
	}

	repository := &lookupTrackingRepository{Repository: base}
	retry := newTestProcessorWithCache(t, repository, cache, NoopFinalHook{})
	result, err := retry.ProcessEvent(context.Background(), storedEvent)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeAlertRotated || repository.findActiveCalls != 0 || repository.findEndedCalls != 0 {
		t.Fatalf("result=%#v active_calls=%d ended_calls=%d", result, repository.findActiveCalls, repository.findEndedCalls)
	}
}

func TestProcessEventUsesLifecycleAlertFastWrites(t *testing.T) {
	base := memory.New()
	repository := &fastLifecycleRepository{Repository: base}
	processor := newTestProcessor(t, repository, NoopFinalHook{})
	opening := testEvent("event-fast-opening", "warning")
	persistAndProcess(t, repository, processor, opening)
	update := testEvent("event-fast-update", "warning")
	persistAndProcess(t, repository, processor, update)
	if repository.createCalls != 1 || repository.casCalls != 1 {
		t.Fatalf("fast create=%d cas=%d", repository.createCalls, repository.casCalls)
	}
}

func newTestProcessor(t *testing.T, repo store.Repository, hook FinalHook) *Processor {
	t.Helper()
	return newTestProcessorWithCache(t, repo, NoopRecentAlertCache{}, hook)
}

func newTestProcessorWithCache(
	t *testing.T,
	repo store.Repository,
	cache RecentAlertCache,
	hook FinalHook,
) *Processor {
	t.Helper()
	processor, err := NewProcessor(repo, cache, DeterministicAlertIDGenerator{}, NoopEnricher{}, hook, testSeverity{}, fixedClock{time.Date(2026, 9, 1, 0, 10, 0, 0, time.UTC)}, discardLogger{})
	if err != nil {
		t.Fatal(err)
	}
	return processor
}

type lookupTrackingRepository struct {
	store.Repository
	findActiveCalls int
	findEndedCalls  int
}

type conflictingLifecycleRepository struct {
	store.Repository
	conflicts    int
	currentReads int
	eventReads   int
}

type eventReadTrackingRepository struct {
	store.Repository
	eventReads int
}

type fastLifecycleRepository struct {
	store.Repository
	createCalls int
	casCalls    int
}

func (r *fastLifecycleRepository) CreateAlertAfterActiveLookup(
	ctx context.Context,
	alert domain.Alert,
) (store.CreateAlertResult, error) {
	r.createCalls++
	return r.CreateAlert(ctx, alert)
}

func (r *fastLifecycleRepository) CompareAndSetAlertAfterActiveLookup(
	ctx context.Context,
	bkTenantID, alertID string,
	expected store.VersionToken,
	replacement domain.Alert,
) (store.StoredAlert, error) {
	r.casCalls++
	return r.CompareAndSetAlert(ctx, bkTenantID, alertID, expected, replacement)
}

func (r *fastLifecycleRepository) GetAlertCurrent(
	ctx context.Context,
	bkTenantID, alertID string,
) (store.StoredAlert, error) {
	return r.GetAlert(ctx, bkTenantID, alertID)
}

func (r *conflictingLifecycleRepository) CreateAlertAfterActiveLookup(
	ctx context.Context,
	alert domain.Alert,
) (store.CreateAlertResult, error) {
	return r.CreateAlert(ctx, alert)
}

func (r *conflictingLifecycleRepository) CompareAndSetAlertAfterActiveLookup(
	ctx context.Context,
	bkTenantID, alertID string,
	expected store.VersionToken,
	replacement domain.Alert,
) (store.StoredAlert, error) {
	if r.conflicts == 0 {
		r.conflicts++
		if _, err := r.CompareAndSetAlert(ctx, bkTenantID, alertID, expected, replacement); err != nil {
			return store.StoredAlert{}, err
		}
		return store.StoredAlert{}, fmt.Errorf("%w: injected conflict", store.ErrVersionConflict)
	}
	return r.CompareAndSetAlert(ctx, bkTenantID, alertID, expected, replacement)
}

func (r *conflictingLifecycleRepository) GetAlertCurrent(
	ctx context.Context,
	bkTenantID, alertID string,
) (store.StoredAlert, error) {
	r.currentReads++
	return r.GetAlert(ctx, bkTenantID, alertID)
}

func (r *conflictingLifecycleRepository) GetEvent(
	ctx context.Context,
	bkTenantID, eventID string,
) (store.StoredEvent, error) {
	r.eventReads++
	return r.Repository.GetEvent(ctx, bkTenantID, eventID)
}

func (r *eventReadTrackingRepository) GetEvent(
	ctx context.Context,
	bkTenantID, eventID string,
) (store.StoredEvent, error) {
	r.eventReads++
	return r.Repository.GetEvent(ctx, bkTenantID, eventID)
}

func (r *lookupTrackingRepository) FindActiveAlert(
	ctx context.Context,
	key store.ActiveAlertKey,
) (store.StoredAlert, error) {
	r.findActiveCalls++
	return r.Repository.FindActiveAlert(ctx, key)
}

func (r *lookupTrackingRepository) FindAlertEndedByEvent(
	ctx context.Context,
	bkTenantID, eventID string,
) (store.StoredAlert, error) {
	r.findEndedCalls++
	return r.Repository.FindAlertEndedByEvent(ctx, bkTenantID, eventID)
}

type memoryRecentAlertCache struct {
	current map[string]store.StoredAlert
	ended   map[string]store.StoredAlert
	getErr  error
	putErr  error
}

func newMemoryRecentAlertCache() *memoryRecentAlertCache {
	return &memoryRecentAlertCache{
		current: make(map[string]store.StoredAlert),
		ended:   make(map[string]store.StoredAlert),
	}
}

func (c *memoryRecentAlertCache) GetCurrent(
	_ context.Context,
	key store.ActiveAlertKey,
) (store.StoredAlert, bool, error) {
	if c.getErr != nil {
		return store.StoredAlert{}, false, c.getErr
	}
	stored, found := c.current[recentCurrentTestKey(key)]
	return stored, found, nil
}

func (c *memoryRecentAlertCache) GetEndedByEvent(
	_ context.Context,
	bkTenantID, eventID string,
) (store.StoredAlert, bool, error) {
	if c.getErr != nil {
		return store.StoredAlert{}, false, c.getErr
	}
	stored, found := c.ended[bkTenantID+"/"+eventID]
	return stored, found, nil
}

func (c *memoryRecentAlertCache) PutCurrent(_ context.Context, stored store.StoredAlert) error {
	if c.putErr != nil {
		return c.putErr
	}
	c.current[recentCurrentTestKey(activeKeyForTest(stored.Alert))] = stored
	return nil
}

func (c *memoryRecentAlertCache) PutEnded(_ context.Context, stored store.StoredAlert) error {
	if c.putErr != nil {
		return c.putErr
	}
	c.ended[stored.Alert.BKTenantID+"/"+stored.Alert.LatestEventID] = stored
	return nil
}

func (c *memoryRecentAlertCache) PutTerminal(ctx context.Context, stored store.StoredAlert) error {
	if err := c.PutCurrent(ctx, stored); err != nil {
		return err
	}
	return c.PutEnded(ctx, stored)
}

func (c *memoryRecentAlertCache) Repair(ctx context.Context, stored store.StoredAlert) error {
	if stored.Alert.Status.Terminal() {
		return c.PutTerminal(ctx, stored)
	}
	return c.PutCurrent(ctx, stored)
}

func recentCurrentTestKey(key store.ActiveAlertKey) string {
	return key.BKTenantID + "/" + key.EventSourceID + "/" + key.Fingerprint
}

func activeKeyForTest(value any) store.ActiveAlertKey {
	switch item := value.(type) {
	case domain.Event:
		return store.ActiveAlertKey{BKTenantID: item.BKTenantID, EventSourceID: item.EventSourceID, Fingerprint: item.Fingerprint}
	case domain.Alert:
		return store.ActiveAlertKey{BKTenantID: item.BKTenantID, EventSourceID: item.EventSourceID, Fingerprint: item.Fingerprint}
	default:
		panic("unsupported active key value")
	}
}

func storetestAlert(event domain.Event, alertID string) domain.Alert {
	now := event.CreateAt.Add(time.Second)
	return domain.Alert{
		AlertID: alertID, BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID,
		Fingerprint: event.Fingerprint, Title: event.Title, Severity: event.Severity,
		ConditionKey: event.ConditionKey, Dimensions: event.Dimensions.Clone(),
		SourceEventID: event.SourceEventID, SourceAlertID: event.SourceAlertID,
		Labels: domain.DimensionMap{}, ExtraData: domain.JSONObject{}, Status: domain.AlertStatusActive,
		LatestEventID: event.EventID, LastOccurredAt: event.OccurredAt, UpdateAt: now,
		TriggerEventID: event.EventID, BeginAt: event.OccurredAt, CreateAt: event.CreateAt,
		EnrichStatus: domain.EnrichStatusSucceeded, Enrich: domain.JSONObject{},
	}
}

func persistAndProcess(t *testing.T, repo store.Repository, processor *Processor, event domain.Event) ProcessResult {
	t.Helper()
	created, err := repo.CreateEvent(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	result, err := processor.ProcessEvent(context.Background(), created.StoredEvent)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustGetStoredEvent(t *testing.T, repo store.Repository, event domain.Event) store.StoredEvent {
	t.Helper()
	stored, err := repo.GetEvent(context.Background(), event.BKTenantID, event.EventID)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func testEvent(id, severity string) domain.Event {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	return domain.Event{BKTenantID: "tenant-1", EventSourceID: "source", EventID: id, Fingerprint: "fingerprint-1", Title: "CPU high", Severity: severity, Action: domain.EventActionTriggered, ConditionKey: "cpu", Dimensions: domain.DimensionMap{"host": domain.NewStringScalar("host-1")}, OccurredAt: now, ProducedAt: now, ReceivedAt: now, CreateAt: now, SourceEventID: "source-" + id, SourceAlertID: "source-alert", SourceRawData: domain.JSONObject{}, Labels: domain.DimensionMap{}, ExtraData: domain.JSONObject{}}
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type testSeverity struct{}

type stubEnricher struct {
	fn func(EnrichInput) (EnrichResult, error)
}

func (s stubEnricher) Enrich(_ context.Context, input EnrichInput) (EnrichResult, error) {
	return s.fn(input)
}

func (testSeverity) Priority(name string) (int, bool) {
	values := map[string]int{"critical": 1, "warning": 2, "info": 3}
	value, ok := values[name]
	return value, ok
}

type discardLogger struct{}

func (discardLogger) WarnContext(context.Context, string, ...any) {}

type recordingHook struct {
	mu     sync.Mutex
	inputs []FinalHookInput
}

type trackingRepository struct {
	store.Repository
	mu             sync.Mutex
	calls          []string
	batches        [][]domain.AlertLog
	failBatchIndex int
}

func (r *trackingRepository) record(call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}

func (r *trackingRepository) CreateAlert(
	ctx context.Context,
	alert domain.Alert,
) (store.CreateAlertResult, error) {
	r.record("alert_create")
	return r.Repository.CreateAlert(ctx, alert)
}

func (r *trackingRepository) CompareAndSetAlert(
	ctx context.Context,
	tenantID, alertID string,
	expected store.VersionToken,
	replacement domain.Alert,
) (store.StoredAlert, error) {
	r.record("alert_cas")
	return r.Repository.CompareAndSetAlert(ctx, tenantID, alertID, expected, replacement)
}

func (r *trackingRepository) AppendAlertLogs(
	ctx context.Context,
	logs []domain.AlertLog,
) ([]store.AppendAlertLogItemResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, "logs")
	cloned := make([]domain.AlertLog, len(logs))
	for index, log := range logs {
		cloned[index] = log.Clone()
	}
	r.batches = append(r.batches, cloned)
	failBatchIndex := r.failBatchIndex
	r.mu.Unlock()
	results, err := r.Repository.AppendAlertLogs(ctx, logs)
	if err == nil && failBatchIndex >= 0 && failBatchIndex < len(results) {
		results[failBatchIndex].Err = errors.New("injected alert log item failure")
	}
	return results, err
}

func (r *trackingRepository) CompareAndSetEventResult(
	ctx context.Context,
	tenantID, eventID string,
	expected store.VersionToken,
	result store.EventResult,
) (store.StoredEvent, error) {
	r.record("event_cas")
	return r.Repository.CompareAndSetEventResult(ctx, tenantID, eventID, expected, result)
}

func (r *trackingRepository) snapshot() ([]string, [][]domain.AlertLog) {
	r.mu.Lock()
	defer r.mu.Unlock()
	calls := append([]string(nil), r.calls...)
	batches := make([][]domain.AlertLog, len(r.batches))
	for index, batch := range r.batches {
		batches[index] = append([]domain.AlertLog(nil), batch...)
	}
	return calls, batches
}

func (r *trackingRepository) reset(failBatchIndex int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = nil
	r.batches = nil
	r.failBatchIndex = failBatchIndex
}

type trackingHook struct{ repository *trackingRepository }

func (h trackingHook) Execute(_ context.Context, input FinalHookInput) (FinalHookResult, error) {
	h.repository.record("hook")
	return FinalHookResult{
		Name: "test", Transport: "memory", Destination: "test",
		MessageID: input.Alert.AlertID + "/" + input.Alert.UpdateAt.Format(time.RFC3339Nano),
	}, nil
}

func (h *recordingHook) Execute(_ context.Context, input FinalHookInput) (FinalHookResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.inputs = append(h.inputs, input)
	return FinalHookResult{Name: "test", Transport: "memory", Destination: "test", MessageID: input.Alert.AlertID + "/" + input.Alert.UpdateAt.Format(time.RFC3339Nano)}, nil
}
