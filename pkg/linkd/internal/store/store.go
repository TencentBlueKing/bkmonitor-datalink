// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package store

import (
	"context"

	"linkd/internal/domain"
)

// EventStore 定义 Event 的创建、读取和处理结果单对象 CAS。
type EventStore interface {
	CreateEvent(ctx context.Context, event domain.Event) (CreateEventResult, error)
	CreateEvents(ctx context.Context, events []domain.Event) ([]CreateEventItemResult, error)
	GetEvent(ctx context.Context, bkTenantID, eventID string) (StoredEvent, error)
	GetEvents(ctx context.Context, bkTenantID string, eventIDs []string) (EventBatch, error)
	ListEventsByAlert(
		ctx context.Context,
		bkTenantID, alertID string,
		request EventByAlertRequest,
	) (EventPage, error)
	CompareAndSetEventResult(
		ctx context.Context,
		bkTenantID, eventID string,
		expected VersionToken,
		result EventResult,
	) (StoredEvent, error)
}

// AlertStore 定义逻辑 Alert 的创建、读取、活动关联查询和单对象 CAS。
// 具体存储可以在内部使用多个物理集合，但不得向调用方暴露位置或归档编排状态。
type AlertStore interface {
	CreateAlert(ctx context.Context, alert domain.Alert) (CreateAlertResult, error)
	GetAlert(ctx context.Context, bkTenantID, alertID string) (StoredAlert, error)
	GetAlerts(ctx context.Context, bkTenantID string, alertIDs []string) (AlertBatch, error)
	FindActiveAlert(ctx context.Context, key ActiveAlertKey) (StoredAlert, error)
	FindAlertEndedByEvent(ctx context.Context, bkTenantID, eventID string) (StoredAlert, error)
	CompareAndSetAlert(
		ctx context.Context,
		bkTenantID, alertID string,
		expected VersionToken,
		replacement domain.Alert,
	) (StoredAlert, error)
}

// AlertLogStore 定义不可变 AlertLog 的批量追加和分页读取。
type AlertLogStore interface {
	AppendAlertLog(ctx context.Context, log domain.AlertLog) (AppendAlertLogResult, error)
	AppendAlertLogs(ctx context.Context, logs []domain.AlertLog) ([]AppendAlertLogItemResult, error)
	ListAlertLogs(
		ctx context.Context,
		bkTenantID, alertID string,
		page PageRequest,
	) (AlertLogPage, error)
}

// QueryStore 定义跨对象但不承诺事务快照的一致只读查询。
type QueryStore interface {
	QueryAlertByEvent(ctx context.Context, bkTenantID, eventID string) (AlertByEventResult, error)
}

// Repository 组合 Linkd 当前已确认的逻辑对象存储能力。
type Repository interface {
	EventStore
	AlertStore
	AlertLogStore
	QueryStore
}
