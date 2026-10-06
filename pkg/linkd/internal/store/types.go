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
	"fmt"
	"time"

	"linkd/internal/domain"
)

const (
	DefaultPageSize = 100
	MaxPageSize     = 500
	MaxBatchSize    = 500
)

// VersionToken 是由具体存储产生、只供单对象 CAS 原样回传的不透明令牌。
type VersionToken struct{ value string }

func NewVersionToken(value string) VersionToken { return VersionToken{value: value} }

func (t VersionToken) IsZero() bool { return t.value == "" }

func (t VersionToken) String() string { return t.value }

// EventProcessing 是 Event JSON 之外的生命周期处理元数据。
type EventProcessing struct {
	State       domain.EventProcessState `json:"state"`
	Outcome     string                   `json:"outcome,omitempty"`
	ReasonCode  string                   `json:"reason_code,omitempty"`
	ProcessedAt *time.Time               `json:"processed_at,omitempty"`
}

func NewUnprocessedEventProcessing() EventProcessing {
	return EventProcessing{State: domain.EventProcessStateUnprocessed}
}

func (p EventProcessing) Normalize() (EventProcessing, error) {
	if !p.State.Valid() {
		return EventProcessing{}, fmt.Errorf("event processing state is invalid: %q", p.State)
	}
	if p.ProcessedAt != nil {
		value := p.ProcessedAt.Round(0).UTC()
		p.ProcessedAt = &value
	}
	if p.State == domain.EventProcessStateUnprocessed {
		if p.Outcome != "" || p.ReasonCode != "" || p.ProcessedAt != nil {
			return EventProcessing{}, fmt.Errorf("unprocessed event must not contain process result")
		}
		return p, nil
	}
	if p.Outcome == "" || p.ProcessedAt == nil || p.ProcessedAt.IsZero() {
		return EventProcessing{}, fmt.Errorf("terminal event processing requires outcome and processed_at")
	}
	return p, nil
}

func (p EventProcessing) Clone() EventProcessing {
	if p.ProcessedAt != nil {
		value := *p.ProcessedAt
		p.ProcessedAt = &value
	}
	return p
}

// StoredEvent 是领域 Event、处理元数据和 CAS 版本组成的快照。
type StoredEvent struct {
	Event      domain.Event
	Processing EventProcessing
	Version    VersionToken
}

// Validate 校验领域字段与处理元数据之间的关联约束。
func (s StoredEvent) Validate() error {
	if err := s.Event.Validate(); err != nil {
		return err
	}
	processing, err := s.Processing.Normalize()
	if err != nil {
		return err
	}
	if s.Version.IsZero() {
		return fmt.Errorf("stored event version must not be empty")
	}
	if processing.State == domain.EventProcessStateAccepted || processing.State == domain.EventProcessStateSuppressed {
		if s.Event.RelatedAlertID == "" {
			return fmt.Errorf("associated event requires related_alert_id")
		}
	} else if s.Event.RelatedAlertID != "" {
		return fmt.Errorf("only accepted or suppressed event may contain related_alert_id")
	}
	return nil
}

// StoredAlert 是领域 Alert 和存储专属 CAS 版本组成的快照。
type StoredAlert struct {
	Alert   domain.Alert
	Version VersionToken
}

type CreateEventResult struct {
	StoredEvent
	Created bool
}

// CreateEventItemResult 是批量创建中与输入位置一一对应的逐项结果。
// Err 只描述当前 Event；批量请求本身无法产生可信逐项结果时由 CreateEvents 返回顶层 error。
type CreateEventItemResult struct {
	Result CreateEventResult
	Err    error
}

type CreateAlertResult struct {
	StoredAlert
	Created bool
}

type AppendAlertLogResult struct {
	Log     domain.AlertLog
	Created bool
}

// AppendAlertLogItemResult 是批量追加中与输入位置一一对应的逐项结果。
// Err 只描述当前 AlertLog；批量请求本身无法产生可信逐项结果时由 AppendAlertLogs 返回顶层 error。
type AppendAlertLogItemResult struct {
	Result AppendAlertLogResult
	Err    error
}

type EventBatch struct {
	Events   []StoredEvent
	NotFound []string
}

type AlertBatch struct {
	Alerts   []StoredAlert
	NotFound []string
}

type PageRequest struct {
	Cursor string
	Limit  int
}

type EventPage struct {
	Events     []StoredEvent
	NextCursor string
}

// EventByAlertRequest 定义按关联 Alert 查询 Event 的可选接收时间范围和分页。
// 零时间由 Repository 根据 Alert 生命周期边界补齐。
type EventByAlertRequest struct {
	ReceivedFrom time.Time
	ReceivedTo   time.Time
	Page         PageRequest
}

// ResolveEventByAlertRange 根据 Alert 生命周期和调用方约束得到最终接收时间范围与分页参数。
func ResolveEventByAlertRange(
	alert domain.Alert,
	request EventByAlertRequest,
	now time.Time,
) (time.Time, time.Time, PageRequest, error) {
	page, err := request.Page.Normalize()
	if err != nil {
		return time.Time{}, time.Time{}, PageRequest{}, err
	}
	from := alert.CreateAt.Round(0).UTC()
	if !request.ReceivedFrom.IsZero() && request.ReceivedFrom.After(from) {
		from = request.ReceivedFrom.Round(0).UTC()
	}
	to := now.Round(0).UTC()
	if alert.Status.Terminal() {
		parsed, parseErr := domain.ParseEventID(alert.LatestEventID)
		if parseErr == nil {
			to = parsed.Timestamp.Add(time.Second - time.Nanosecond)
		} else {
			to = alert.UpdateAt.Round(0).UTC()
		}
	}
	if !request.ReceivedTo.IsZero() && request.ReceivedTo.Before(to) {
		to = request.ReceivedTo.Round(0).UTC()
	}
	return from, to, page, nil
}

type AlertLogPage struct {
	Logs       []domain.AlertLog
	NextCursor string
}

// EventResult 是一次生命周期裁决写回 Event 的完整结果。
type EventResult struct {
	State          domain.EventProcessState
	RelatedAlertID string
	Outcome        string
	ReasonCode     string
	ProcessedAt    time.Time
}

func (r EventResult) Normalize() (EventResult, error) {
	processing, err := (EventProcessing{State: r.State, Outcome: r.Outcome, ReasonCode: r.ReasonCode, ProcessedAt: &r.ProcessedAt}).Normalize()
	if err != nil {
		return EventResult{}, err
	}
	r.ProcessedAt = *processing.ProcessedAt
	if r.State == domain.EventProcessStateAccepted || r.State == domain.EventProcessStateSuppressed {
		if r.RelatedAlertID == "" {
			return EventResult{}, fmt.Errorf("associated event result requires related_alert_id")
		}
	} else if r.RelatedAlertID != "" {
		return EventResult{}, fmt.Errorf("only accepted or suppressed event result may contain related_alert_id")
	}
	return r, nil
}

type ActiveAlertKey struct{ BKTenantID, EventSourceID, Fingerprint string }

type AlertByEventResult struct {
	Event StoredEvent
	Alert *StoredAlert
}

func (request PageRequest) Normalize() (PageRequest, error) {
	if request.Limit < 0 || request.Limit > MaxPageSize {
		return PageRequest{}, fmt.Errorf("%w: page limit must be between 0 and %d", ErrInvalidArgument, MaxPageSize)
	}
	if request.Limit == 0 {
		request.Limit = DefaultPageSize
	}
	return request, nil
}
