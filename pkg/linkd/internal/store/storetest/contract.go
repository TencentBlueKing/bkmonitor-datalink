// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// Factory 为每个子测试返回隔离的 Repository。
type Factory func(t *testing.T) store.Repository

// RunRepositoryContract 固定 Event/Alert/AlertLog 的跨后端行为。
func RunRepositoryContract(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("event create and processing CAS", func(t *testing.T) {
		repo := factory(t)
		ctx := context.Background()
		event := Event("tenant-1", "event-1", "fingerprint-1", "warning")
		created, err := repo.CreateEvent(ctx, event)
		if err != nil || !created.Created {
			t.Fatalf("CreateEvent=%#v,%v", created, err)
		}
		duplicate, err := repo.CreateEvent(ctx, event)
		if err != nil || duplicate.Created {
			t.Fatalf("duplicate=%#v,%v", duplicate, err)
		}
		conflict := event.Clone()
		conflict.Title = "different"
		if _, err := repo.CreateEvent(ctx, conflict); !errors.Is(err, store.ErrIdentityConflict) {
			t.Fatalf("conflict err=%v", err)
		}
		processedAt := event.CreateAt.Add(time.Minute)
		updated, err := repo.CompareAndSetEventResult(ctx, event.BKTenantID, event.EventID, created.Version, store.EventResult{State: domain.EventProcessStateAccepted, RelatedAlertID: "alert-1", Outcome: "alert_created", ProcessedAt: processedAt})
		if err != nil {
			t.Fatal(err)
		}
		if updated.Processing.State != domain.EventProcessStateAccepted || updated.Event.RelatedAlertID != "alert-1" {
			t.Fatalf("updated=%#v", updated)
		}
		processedDuplicate, err := repo.CreateEvent(ctx, event)
		if err != nil || processedDuplicate.Created || processedDuplicate.Event.RelatedAlertID != "alert-1" ||
			processedDuplicate.Processing.State != domain.EventProcessStateAccepted {
			t.Fatalf("processed duplicate=%#v,%v", processedDuplicate, err)
		}
		if _, err := repo.CompareAndSetEventResult(ctx, event.BKTenantID, event.EventID, created.Version, store.EventResult{State: domain.EventProcessStateRejected, Outcome: "rejected", ProcessedAt: processedAt}); !errors.Is(err, store.ErrVersionConflict) {
			t.Fatalf("stale CAS=%v", err)
		}
	})

	t.Run("event batch create preserves item results", func(t *testing.T) {
		repo := factory(t)
		ctx := context.Background()
		first := Event("tenant-1", "batch-event-1", "fp-1", "warning")
		second := Event("tenant-1", "batch-event-2", "fp-2", "warning")
		items, err := repo.CreateEvents(ctx, []domain.Event{first, second})
		if err != nil || len(items) != 2 || items[0].Err != nil || items[1].Err != nil ||
			!items[0].Result.Created || !items[1].Result.Created {
			t.Fatalf("CreateEvents()=%#v,%v", items, err)
		}
		items, err = repo.CreateEvents(ctx, []domain.Event{first, second})
		if err != nil || items[0].Err != nil || items[1].Err != nil || items[0].Result.Created || items[1].Result.Created {
			t.Fatalf("duplicate CreateEvents()=%#v,%v", items, err)
		}
		conflict := second.Clone()
		conflict.Title = "different"
		items, err = repo.CreateEvents(ctx, []domain.Event{first, conflict})
		if err != nil || items[0].Err != nil || !errors.Is(items[1].Err, store.ErrIdentityConflict) {
			t.Fatalf("conflicting CreateEvents()=%#v,%v", items, err)
		}
	})

	t.Run("alert active CAS and ended lookup", func(t *testing.T) {
		repo := factory(t)
		ctx := context.Background()
		alert := Alert("tenant-1", "alert-1", "event-1", "fingerprint-1", "warning")
		created, err := repo.CreateAlert(ctx, alert)
		if err != nil || !created.Created {
			t.Fatalf("CreateAlert=%#v,%v", created, err)
		}
		if active, err := repo.FindActiveAlert(ctx, store.ActiveAlertKey{BKTenantID: alert.BKTenantID, EventSourceID: alert.EventSourceID, Fingerprint: alert.Fingerprint}); err != nil || active.Alert.AlertID != alert.AlertID {
			t.Fatalf("active=%#v,%v", active, err)
		}
		other := Alert("tenant-1", "alert-2", "event-x", "fingerprint-1", "critical")
		if _, err := repo.CreateAlert(ctx, other); !errors.Is(err, store.ErrIdentityConflict) {
			t.Fatalf("active conflict=%v", err)
		}
		terminal := alert.Clone()
		terminal.Status = domain.AlertStatusRecovered
		terminal.LatestEventID = "event-2"
		terminal.LastOccurredAt = terminal.LastOccurredAt.Add(time.Minute)
		terminal.UpdateAt = terminal.UpdateAt.Add(time.Minute)
		end := terminal.LastOccurredAt
		terminal.EndAt = &end
		terminal.EndType = domain.AlertEndTypeSource
		updated, err := repo.CompareAndSetAlert(ctx, alert.BKTenantID, alert.AlertID, created.Version, terminal)
		if err != nil {
			t.Fatal(err)
		}
		if updated.Version == created.Version {
			t.Fatal("alert version did not change")
		}
		ended, err := repo.FindAlertEndedByEvent(ctx, alert.BKTenantID, "event-2")
		if err != nil || ended.Alert.AlertID != alert.AlertID {
			t.Fatalf("ended=%#v,%v", ended, err)
		}
		if _, err := repo.CompareAndSetAlert(ctx, alert.BKTenantID, alert.AlertID, updated.Version, alert); !errors.Is(err, store.ErrInvalidTransition) {
			t.Fatalf("reopen=%v", err)
		}
	})

	t.Run("logs and query by event", func(t *testing.T) {
		repo := factory(t)
		ctx := context.Background()
		event := Event("tenant-1", "event-1", "fp", "warning")
		storedEvent, _ := repo.CreateEvent(ctx, event)
		alert := Alert("tenant-1", "alert-1", "event-1", "fp", "warning")
		_, _ = repo.CreateAlert(ctx, alert)
		processedAt := event.CreateAt.Add(time.Minute)
		_, _ = repo.CompareAndSetEventResult(ctx, event.BKTenantID, event.EventID, storedEvent.Version, store.EventResult{State: domain.EventProcessStateAccepted, RelatedAlertID: alert.AlertID, Outcome: "alert_created", ProcessedAt: processedAt})
		result, err := waitForAlertByEvent(ctx, repo, event.BKTenantID, event.EventID, alert.AlertID)
		if err != nil || result.Alert == nil || result.Alert.Alert.AlertID != alert.AlertID {
			t.Fatalf("query=%#v,%v", result, err)
		}
		events, err := repo.ListEventsByAlert(ctx, event.BKTenantID, alert.AlertID, store.EventByAlertRequest{})
		if err != nil || len(events.Events) != 1 || events.Events[0].Event.EventID != event.EventID {
			t.Fatalf("events by alert=%#v,%v", events, err)
		}
		log := domain.AlertLog{LogID: "log-1", BKTenantID: event.BKTenantID, AlertID: alert.AlertID, OperatorKind: domain.OperatorKindSource, OperationKind: domain.OperationKindTrigger, Params: domain.JSONObject{}, CreatedTime: processedAt}
		first, err := repo.AppendAlertLog(ctx, log)
		if err != nil || !first.Created {
			t.Fatalf("append=%#v,%v", first, err)
		}
		second, err := repo.AppendAlertLog(ctx, log)
		if err != nil || second.Created {
			t.Fatalf("duplicate log=%#v,%v", second, err)
		}
		page, err := waitForAlertLogs(ctx, repo, event.BKTenantID, alert.AlertID, 1)
		if err != nil || len(page.Logs) != 1 {
			t.Fatalf("logs=%#v,%v", page, err)
		}
	})

	t.Run("alert log batch preserves item results", func(t *testing.T) {
		repo := factory(t)
		ctx := context.Background()
		now := time.Date(2026, 9, 1, 0, 1, 0, 0, time.UTC)
		logs := []domain.AlertLog{
			{LogID: "batch-log-1", BKTenantID: "tenant-1", AlertID: "alert-1", OperatorKind: domain.OperatorKindSource, OperationKind: domain.OperationKindTrigger, Params: domain.JSONObject{}, CreatedTime: now},
			{LogID: "batch-log-2", BKTenantID: "tenant-1", AlertID: "alert-1", OperatorKind: domain.OperatorKindSystem, OperationKind: domain.OperationKindPush, Params: domain.JSONObject{}, CreatedTime: now.Add(time.Nanosecond)},
		}
		items, err := repo.AppendAlertLogs(ctx, logs)
		if err != nil || len(items) != 2 || items[0].Err != nil || items[1].Err != nil ||
			!items[0].Result.Created || !items[1].Result.Created {
			t.Fatalf("AppendAlertLogs()=%#v,%v", items, err)
		}
		items, err = repo.AppendAlertLogs(ctx, logs)
		if err != nil || len(items) != 2 || items[0].Err != nil || items[1].Err != nil ||
			items[0].Result.Created || items[1].Result.Created {
			t.Fatalf("duplicate AppendAlertLogs()=%#v,%v", items, err)
		}
		conflict := logs[1].Clone()
		conflict.Params = domain.JSONObject{"changed": []byte(`true`)}
		items, err = repo.AppendAlertLogs(ctx, []domain.AlertLog{logs[0], conflict})
		if err != nil || len(items) != 2 || items[0].Err != nil || !errors.Is(items[1].Err, store.ErrIdentityConflict) {
			t.Fatalf("conflicting AppendAlertLogs()=%#v,%v", items, err)
		}
		if _, err := repo.AppendAlertLogs(ctx, nil); !errors.Is(err, store.ErrInvalidArgument) {
			t.Fatalf("empty AppendAlertLogs() error=%v", err)
		}
	})
}

func waitForAlertByEvent(
	ctx context.Context,
	repo store.Repository,
	tenantID, eventID, alertID string,
) (store.AlertByEventResult, error) {
	deadline := time.Now().Add(3 * time.Second)
	var last store.AlertByEventResult
	var lastErr error
	for {
		last, lastErr = repo.QueryAlertByEvent(ctx, tenantID, eventID)
		if lastErr == nil && last.Alert != nil && last.Alert.Alert.AlertID == alertID {
			return last, nil
		}
		if time.Now().After(deadline) {
			return last, lastErr
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForAlertLogs(
	ctx context.Context,
	repo store.Repository,
	tenantID, alertID string,
	want int,
) (store.AlertLogPage, error) {
	deadline := time.Now().Add(3 * time.Second)
	var last store.AlertLogPage
	var lastErr error
	for {
		last, lastErr = repo.ListAlertLogs(ctx, tenantID, alertID, store.PageRequest{})
		if lastErr == nil && len(last.Logs) >= want {
			return last, nil
		}
		if time.Now().After(deadline) {
			return last, lastErr
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Event 返回一个有效的新 Event 测试夹具。
func Event(tenantID, eventID, fingerprint, severity string) domain.Event {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	return domain.Event{BKTenantID: tenantID, EventSourceID: "source", EventID: eventID, Fingerprint: fingerprint, Title: "CPU high", Severity: severity, Action: domain.EventActionTriggered, ConditionKey: "cpu", Dimensions: domain.DimensionMap{"host": domain.NewStringScalar("host-1")}, OccurredAt: now, ProducedAt: now, ReceivedAt: now, CreateAt: now, SourceEventID: "source-" + eventID, SourceAlertID: fingerprint, SourceRawData: domain.JSONObject{}, Labels: domain.DimensionMap{}, ExtraData: domain.JSONObject{}}
}

// Alert 返回一个有效的 active Alert 测试夹具。
func Alert(tenantID, alertID, eventID, fingerprint, severity string) domain.Alert {
	event := Event(tenantID, eventID, fingerprint, severity)
	now := event.CreateAt.Add(time.Second)
	return domain.Alert{AlertID: alertID, BKTenantID: tenantID, EventSourceID: event.EventSourceID, Fingerprint: fingerprint, Title: event.Title, Severity: severity, ConditionKey: event.ConditionKey, Dimensions: event.Dimensions.Clone(), SourceEventID: event.SourceEventID, SourceAlertID: event.SourceAlertID, Labels: domain.DimensionMap{}, ExtraData: domain.JSONObject{}, Status: domain.AlertStatusActive, LatestEventID: eventID, LastOccurredAt: event.OccurredAt, UpdateAt: now, TriggerEventID: eventID, BeginAt: event.OccurredAt, CreateAt: event.CreateAt, EnrichStatus: domain.EnrichStatusSucceeded, Enrich: domain.JSONObject{}}
}
