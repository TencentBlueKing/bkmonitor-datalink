// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain

// EventAction 表示来源事件陈述的动作。
type EventAction string

const (
	EventActionTriggered EventAction = "triggered"
	EventActionResolved  EventAction = "resolved"
	EventActionClosed    EventAction = "closed"
)

func (a EventAction) Valid() bool {
	return a == EventActionTriggered || a == EventActionResolved || a == EventActionClosed
}

// EventProcessState 是 Event JSON 之外的生命周期处理状态。
type EventProcessState string

const (
	EventProcessStateUnprocessed EventProcessState = "unprocessed"
	EventProcessStateAccepted    EventProcessState = "accepted"
	EventProcessStateSuppressed  EventProcessState = "suppressed"
	EventProcessStateOrphaned    EventProcessState = "orphaned"
	EventProcessStateRejected    EventProcessState = "rejected"
)

func (s EventProcessState) Valid() bool {
	switch s {
	case EventProcessStateUnprocessed, EventProcessStateAccepted, EventProcessStateSuppressed,
		EventProcessStateOrphaned, EventProcessStateRejected:
		return true
	default:
		return false
	}
}

func (s EventProcessState) Terminal() bool { return s != EventProcessStateUnprocessed && s.Valid() }

// AlertStatus 表示一次 Alert 生命周期的当前状态。
type AlertStatus string

const (
	AlertStatusActive    AlertStatus = "active"
	AlertStatusRecovered AlertStatus = "recovered"
	AlertStatusClosed    AlertStatus = "closed"
)

func (s AlertStatus) Valid() bool {
	return s == AlertStatusActive || s == AlertStatusRecovered || s == AlertStatusClosed
}

func (s AlertStatus) Terminal() bool { return s == AlertStatusRecovered || s == AlertStatusClosed }

// AlertEndType 区分 Alert 进入终态的推动方。
type AlertEndType string

const (
	AlertEndTypeSource          AlertEndType = "source"
	AlertEndTypeUser            AlertEndType = "user"
	AlertEndTypeSystem          AlertEndType = "system"
	AlertEndTypeSeverityUpgrade AlertEndType = "severity_upgrade"
)

func (t AlertEndType) Valid() bool {
	switch t {
	case AlertEndTypeSource, AlertEndTypeUser, AlertEndTypeSystem, AlertEndTypeSeverityUpgrade:
		return true
	default:
		return false
	}
}

// EnrichStatus 表示 Alert 丰富流程的当前结果。
type EnrichStatus string

const (
	EnrichStatusPending   EnrichStatus = "pending"
	EnrichStatusSucceeded EnrichStatus = "succeeded"
	EnrichStatusPartial   EnrichStatus = "partial"
	EnrichStatusFailed    EnrichStatus = "failed"
)

func (s EnrichStatus) Valid() bool {
	switch s {
	case EnrichStatusPending, EnrichStatusSucceeded, EnrichStatusPartial, EnrichStatusFailed:
		return true
	default:
		return false
	}
}

// OperatorKind 表示 AlertLog 的操作发起方。
type OperatorKind string

const (
	OperatorKindSource OperatorKind = "source"
	OperatorKindUser   OperatorKind = "user"
	OperatorKindSystem OperatorKind = "system"
)

func (k OperatorKind) Valid() bool {
	return k == OperatorKindSource || k == OperatorKindUser || k == OperatorKindSystem
}

// OperationKind 表示 AlertLog 记录的状态操作或最终输出动作。
type OperationKind string

const (
	OperationKindTrigger  OperationKind = "trigger"
	OperationKindRecover  OperationKind = "recover"
	OperationKindClose    OperationKind = "close"
	OperationKindSuppress OperationKind = "suppress"
	OperationKindPush     OperationKind = "push"
)

func (k OperationKind) Valid() bool {
	switch k {
	case OperationKindTrigger, OperationKindRecover, OperationKindClose, OperationKindSuppress, OperationKindPush:
		return true
	default:
		return false
	}
}
