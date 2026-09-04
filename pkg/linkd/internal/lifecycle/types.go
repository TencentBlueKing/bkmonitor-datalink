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
	"fmt"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

const maxCASAttempts = 3

// ProcessOutcome 是一次 Event 生命周期裁决的可观察结果。
type ProcessOutcome string

const (
	OutcomeAlertCreated    ProcessOutcome = "alert_created"
	OutcomeAlertUpdated    ProcessOutcome = "alert_updated"
	OutcomeAlertRotated    ProcessOutcome = "alert_rotated"
	OutcomeAlertRecovered  ProcessOutcome = "alert_recovered"
	OutcomeAlertClosed     ProcessOutcome = "alert_closed"
	OutcomeAlertSuppressed ProcessOutcome = "alert_suppressed"
	OutcomeEventOrphaned   ProcessOutcome = "event_orphaned"
	OutcomeRejected        ProcessOutcome = "rejected"
	OutcomeAlreadyDone     ProcessOutcome = "already_processed"
)

const (
	ReasonActiveAlertNotFound = "active_alert_not_found"
	ReasonInvalidTransition   = "invalid_event_transition"
	ReasonSeverityUpgrade     = "severity_upgrade"
	ReasonSeveritySuppressed  = "severity_suppressed"
)

type ProcessResult struct {
	EventID    string
	AlertID    string
	EventState domain.EventProcessState
	Outcome    ProcessOutcome
	ReasonCode string
}

type Clock interface{ Now() time.Time }

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

type AlertIDGenerator interface {
	Generate(event domain.Event) (string, error)
}

// SeverityTable 提供当前进程冻结的严重程度排序。
type SeverityTable interface{ Priority(name string) (int, bool) }

type Logger interface {
	WarnContext(ctx context.Context, message string, args ...any)
}

// RecentAlertCache 保存 Elasticsearch 最近写入的 Alert 快照，只用于跨越搜索 refresh 可见性窗口。
// Redis 错误必须返回给 Processor；found=false 只表示对应 key 明确不存在。
type RecentAlertCache interface {
	GetCurrent(ctx context.Context, key store.ActiveAlertKey) (stored store.StoredAlert, found bool, err error)
	GetEndedByEvent(ctx context.Context, bkTenantID, eventID string) (stored store.StoredAlert, found bool, err error)
	PutCurrent(ctx context.Context, stored store.StoredAlert) error
	PutEnded(ctx context.Context, stored store.StoredAlert) error
	PutTerminal(ctx context.Context, stored store.StoredAlert) error
	Repair(ctx context.Context, stored store.StoredAlert) error
}

// NoopRecentAlertCache 供不依赖 Elasticsearch 近实时搜索的存储后端使用。
type NoopRecentAlertCache struct{}

func (NoopRecentAlertCache) GetCurrent(context.Context, store.ActiveAlertKey) (store.StoredAlert, bool, error) {
	return store.StoredAlert{}, false, nil
}

func (NoopRecentAlertCache) GetEndedByEvent(context.Context, string, string) (store.StoredAlert, bool, error) {
	return store.StoredAlert{}, false, nil
}

func (NoopRecentAlertCache) PutCurrent(context.Context, store.StoredAlert) error { return nil }

func (NoopRecentAlertCache) PutEnded(context.Context, store.StoredAlert) error { return nil }

func (NoopRecentAlertCache) PutTerminal(context.Context, store.StoredAlert) error { return nil }

func (NoopRecentAlertCache) Repair(context.Context, store.StoredAlert) error { return nil }

type Processor struct {
	repository   store.Repository
	recentAlerts RecentAlertCache
	idGenerator  AlertIDGenerator
	enricher     AlertEnricher
	finalHook    FinalHook
	severity     SeverityTable
	clock        Clock
	logger       Logger
}

func NewProcessor(
	repository store.Repository,
	recentAlerts RecentAlertCache,
	idGenerator AlertIDGenerator,
	enricher AlertEnricher,
	finalHook FinalHook,
	severity SeverityTable,
	clock Clock,
	logger Logger,
) (*Processor, error) {
	for name, dependency := range map[string]any{
		"repository": repository, "recent_alert_cache": recentAlerts, "id_generator": idGenerator, "enricher": enricher,
		"final_hook": finalHook, "severity": severity, "clock": clock, "logger": logger,
	} {
		if dependency == nil {
			return nil, fmt.Errorf("lifecycle %s must not be nil", name)
		}
	}
	return &Processor{repository: repository, recentAlerts: recentAlerts, idGenerator: idGenerator, enricher: enricher,
		finalHook: finalHook, severity: severity, clock: clock, logger: logger}, nil
}

// CloseAlertCommand 是用户或系统直接关闭 active Alert 的稳定幂等命令。
type CloseAlertCommand struct {
	OperationID  string
	BKTenantID   string
	AlertID      string
	OperatorKind domain.OperatorKind
	OperatorID   string
	Reason       string
	EffectiveAt  time.Time
}

func (c CloseAlertCommand) Validate() error {
	if c.OperationID == "" || c.BKTenantID == "" || c.AlertID == "" {
		return fmt.Errorf("close alert operation, tenant and alert id are required")
	}
	if c.OperatorKind != domain.OperatorKindUser && c.OperatorKind != domain.OperatorKindSystem {
		return fmt.Errorf("close alert operator kind must be user or system")
	}
	if c.OperatorID == "" {
		return fmt.Errorf("close alert operator id is required")
	}
	if c.Reason == "" {
		return fmt.Errorf("close alert reason is required")
	}
	if c.EffectiveAt.IsZero() {
		return fmt.Errorf("close alert effective_at is required")
	}
	if len(c.Reason) > 256 {
		return fmt.Errorf("close alert reason exceeds 256 bytes")
	}
	return nil
}

type CloseAlertResult struct {
	Alert         domain.Alert
	AlreadyClosed bool
}
