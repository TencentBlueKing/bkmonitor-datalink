// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package telemetry

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"linkd/internal/domain"
	"linkd/internal/store"
)

type observedRepository struct {
	next    store.Repository
	metrics *instruments
}

// ObserveRepository 为逻辑 Repository 增加不改变错误和返回值的指标装饰器。
func (r *Runtime) ObserveRepository(next store.Repository) store.Repository {
	if r == nil || r.metrics == nil || next == nil {
		return next
	}
	return &observedRepository{next: next, metrics: r.metrics}
}

func (r *observedRepository) CreateEvent(
	ctx context.Context,
	event domain.Event,
) (store.CreateEventResult, error) {
	startedAt := time.Now()
	result, err := r.next.CreateEvent(ctx, event)
	r.record(ctx, "event", "create", startedAt, err)
	if err == nil && !result.Created {
		r.replay(ctx, "event", "create")
	}
	return result, err
}

func (r *observedRepository) CreateEvents(
	ctx context.Context,
	events []domain.Event,
) ([]store.CreateEventItemResult, error) {
	startedAt := time.Now()
	results, err := r.next.CreateEvents(ctx, events)
	r.record(ctx, "event", "create_batch", startedAt, err)
	if err == nil {
		for _, item := range results {
			if item.Err == nil && !item.Result.Created {
				r.replay(ctx, "event", "create_batch")
			}
		}
	}
	return results, err
}

func (r *observedRepository) GetEvent(
	ctx context.Context,
	bkTenantID, eventID string,
) (store.StoredEvent, error) {
	startedAt := time.Now()
	result, err := r.next.GetEvent(ctx, bkTenantID, eventID)
	r.record(ctx, "event", "get", startedAt, err)
	return result, err
}

func (r *observedRepository) GetEvents(
	ctx context.Context,
	bkTenantID string,
	eventIDs []string,
) (store.EventBatch, error) {
	startedAt := time.Now()
	result, err := r.next.GetEvents(ctx, bkTenantID, eventIDs)
	r.record(ctx, "event", "get_batch", startedAt, err)
	return result, err
}

func (r *observedRepository) ListEventsByAlert(
	ctx context.Context,
	bkTenantID, alertID string,
	request store.EventByAlertRequest,
) (store.EventPage, error) {
	startedAt := time.Now()
	result, err := r.next.ListEventsByAlert(ctx, bkTenantID, alertID, request)
	r.record(ctx, "event", "list_by_alert", startedAt, err)
	return result, err
}

func (r *observedRepository) CompareAndSetEventResult(
	ctx context.Context,
	bkTenantID, eventID string,
	expected store.VersionToken,
	result store.EventResult,
) (store.StoredEvent, error) {
	startedAt := time.Now()
	stored, err := r.next.CompareAndSetEventResult(ctx, bkTenantID, eventID, expected, result)
	r.record(ctx, "event", "compare_and_set_result", startedAt, err)
	return stored, err
}

func (r *observedRepository) CreateAlert(
	ctx context.Context,
	alert domain.Alert,
) (store.CreateAlertResult, error) {
	startedAt := time.Now()
	result, err := r.next.CreateAlert(ctx, alert)
	r.record(ctx, "alert", "create", startedAt, err)
	if err == nil && !result.Created {
		r.replay(ctx, "alert", "create")
	}
	return result, err
}

func (r *observedRepository) CreateAlertAfterActiveLookup(
	ctx context.Context,
	alert domain.Alert,
) (store.CreateAlertResult, error) {
	startedAt := time.Now()
	var result store.CreateAlertResult
	var err error
	if next, ok := r.next.(store.LifecycleAlertStore); ok {
		result, err = next.CreateAlertAfterActiveLookup(ctx, alert)
	} else {
		result, err = r.next.CreateAlert(ctx, alert)
	}
	r.record(ctx, "alert", "create", startedAt, err)
	if err == nil && !result.Created {
		r.replay(ctx, "alert", "create")
	}
	return result, err
}

func (r *observedRepository) GetAlert(
	ctx context.Context,
	bkTenantID, alertID string,
) (store.StoredAlert, error) {
	startedAt := time.Now()
	result, err := r.next.GetAlert(ctx, bkTenantID, alertID)
	r.record(ctx, "alert", "get", startedAt, err)
	return result, err
}

func (r *observedRepository) GetAlertCurrent(
	ctx context.Context,
	bkTenantID, alertID string,
) (store.StoredAlert, error) {
	startedAt := time.Now()
	var result store.StoredAlert
	var err error
	if next, ok := r.next.(store.LifecycleAlertStore); ok {
		result, err = next.GetAlertCurrent(ctx, bkTenantID, alertID)
	} else {
		result, err = r.next.GetAlert(ctx, bkTenantID, alertID)
	}
	r.record(ctx, "alert", "get_current", startedAt, err)
	return result, err
}

func (r *observedRepository) GetAlerts(
	ctx context.Context,
	bkTenantID string,
	alertIDs []string,
) (store.AlertBatch, error) {
	startedAt := time.Now()
	result, err := r.next.GetAlerts(ctx, bkTenantID, alertIDs)
	r.record(ctx, "alert", "get_batch", startedAt, err)
	return result, err
}

func (r *observedRepository) FindActiveAlert(
	ctx context.Context,
	key store.ActiveAlertKey,
) (store.StoredAlert, error) {
	startedAt := time.Now()
	result, err := r.next.FindActiveAlert(ctx, key)
	r.record(ctx, "alert", "find_active", startedAt, err)
	return result, err
}

func (r *observedRepository) FindAlertEndedByEvent(
	ctx context.Context,
	bkTenantID, eventID string,
) (store.StoredAlert, error) {
	startedAt := time.Now()
	result, err := r.next.FindAlertEndedByEvent(ctx, bkTenantID, eventID)
	r.record(ctx, "alert", "find_terminal_by_event", startedAt, err)
	return result, err
}

func (r *observedRepository) CompareAndSetAlert(
	ctx context.Context,
	bkTenantID, alertID string,
	expected store.VersionToken,
	replacement domain.Alert,
) (store.StoredAlert, error) {
	startedAt := time.Now()
	result, err := r.next.CompareAndSetAlert(ctx, bkTenantID, alertID, expected, replacement)
	r.record(ctx, "alert", "compare_and_set", startedAt, err)
	return result, err
}

func (r *observedRepository) CompareAndSetAlertAfterActiveLookup(
	ctx context.Context,
	bkTenantID, alertID string,
	expected store.VersionToken,
	replacement domain.Alert,
) (store.StoredAlert, error) {
	startedAt := time.Now()
	var result store.StoredAlert
	var err error
	if next, ok := r.next.(store.LifecycleAlertStore); ok {
		result, err = next.CompareAndSetAlertAfterActiveLookup(
			ctx, bkTenantID, alertID, expected, replacement,
		)
	} else {
		result, err = r.next.CompareAndSetAlert(ctx, bkTenantID, alertID, expected, replacement)
	}
	r.record(ctx, "alert", "compare_and_set", startedAt, err)
	return result, err
}

func (r *observedRepository) AppendAlertLog(
	ctx context.Context,
	log domain.AlertLog,
) (store.AppendAlertLogResult, error) {
	startedAt := time.Now()
	result, err := r.next.AppendAlertLog(ctx, log)
	r.record(ctx, "alert_log", "append", startedAt, err)
	if err == nil && !result.Created {
		r.replay(ctx, "alert_log", "append")
	}
	return result, err
}

func (r *observedRepository) AppendAlertLogs(
	ctx context.Context,
	logs []domain.AlertLog,
) ([]store.AppendAlertLogItemResult, error) {
	startedAt := time.Now()
	results, err := r.next.AppendAlertLogs(ctx, logs)
	r.record(ctx, "alert_log", "append_batch", startedAt, err)
	if err == nil {
		for _, item := range results {
			if item.Err == nil && !item.Result.Created {
				r.replay(ctx, "alert_log", "append_batch")
			}
		}
	}
	return results, err
}

func (r *observedRepository) ListAlertLogs(
	ctx context.Context,
	bkTenantID, alertID string,
	page store.PageRequest,
) (store.AlertLogPage, error) {
	startedAt := time.Now()
	result, err := r.next.ListAlertLogs(ctx, bkTenantID, alertID, page)
	r.record(ctx, "alert_log", "list", startedAt, err)
	return result, err
}

func (r *observedRepository) QueryAlertByEvent(
	ctx context.Context,
	bkTenantID, eventID string,
) (store.AlertByEventResult, error) {
	startedAt := time.Now()
	result, err := r.next.QueryAlertByEvent(ctx, bkTenantID, eventID)
	r.record(ctx, "query", "alert_by_event", startedAt, err)
	return result, err
}

func (r *observedRepository) record(
	ctx context.Context,
	objectType, operation string,
	startedAt time.Time,
	err error,
) {
	outcome := storeOutcome(err)
	attributes := metric.WithAttributes(
		attribute.String("linkd.object.type", objectType),
		attribute.String("linkd.operation", operation),
		attribute.String("linkd.outcome", outcome),
	)
	r.metrics.storeOperations.Add(ctx, 1, attributes)
	r.metrics.storeOperationDuration.Record(ctx, time.Since(startedAt).Seconds(), attributes)
	if errors.Is(err, store.ErrIdentityConflict) {
		r.metrics.storeIdentityConflicts.Add(
			ctx,
			1,
			metric.WithAttributes(
				attribute.String("linkd.object.type", objectType),
				attribute.String("linkd.operation", operation),
			),
		)
	}
	if errors.Is(err, store.ErrVersionConflict) {
		r.metrics.storeCASConflicts.Add(
			ctx,
			1,
			metric.WithAttributes(
				attribute.String("linkd.object.type", objectType),
				attribute.String("linkd.operation", operation),
			),
		)
	}
}

func (r *observedRepository) replay(ctx context.Context, objectType, operation string) {
	r.metrics.storeIdempotencyReplay.Add(
		ctx,
		1,
		metric.WithAttributes(
			attribute.String("linkd.object.type", objectType),
			attribute.String("linkd.operation", operation),
		),
	)
}

func storeOutcome(err error) string {
	switch {
	case err == nil:
		return "succeeded"
	case errors.Is(err, store.ErrNotFound):
		return "not_found"
	case errors.Is(err, store.ErrIdentityConflict):
		return "identity_conflict"
	case errors.Is(err, store.ErrVersionConflict):
		return "version_conflict"
	case errors.Is(err, store.ErrInvalidArgument), errors.Is(err, store.ErrInvalidCursor):
		return "invalid_argument"
	case errors.Is(err, store.ErrInvalidTransition):
		return "invalid_transition"
	default:
		return "failed"
	}
}

var _ store.Repository = (*observedRepository)(nil)

var _ store.LifecycleAlertStore = (*observedRepository)(nil)
