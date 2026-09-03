// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package eventgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"linkd/internal/cleaner"
	linkdconfig "linkd/internal/config"
	"linkd/internal/consume"
	"linkd/internal/domain"
	"linkd/internal/lifecycle/scheduler"
)

const minuteNanoseconds = uint64(time.Minute)

// Record 是一条已经通过 StandardCleaner 和 EventFactory 校验、可以发送到来源 topic 的消息。
type Record struct {
	Key           []byte
	Body          []byte
	Headers       map[string]string
	Timestamp     time.Time
	Action        domain.EventAction
	SourceEventID string
	SourceAlertID string
	Fingerprint   string
}

// Publisher 把一个周期内已经完整规划的消息写入目标消息队列。
// 返回 nil 表示整个调用均已确认；返回错误后 Engine 不提交本周期的活动池变更。
type Publisher interface {
	Publish(ctx context.Context, records []Record) error
}

// CycleResult 是一个已成功发布周期的汇总，不包含业务 payload。
type CycleResult struct {
	Cycle      int
	Generated  int
	Resolved   int
	Duplicated int
	Active     int
	Published  int
	TotalSent  uint64
	StartedAt  time.Time
	Duration   time.Duration
}

type activeAlert struct {
	template    alertTemplate
	fingerprint string
}

type engineClock interface {
	Now() time.Time
	Wait(ctx context.Context, duration time.Duration) error
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

func (realClock) Wait(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Engine 按周期生成新告警、随机恢复旧告警并维护有界的进程内活动池。
// Engine 不持久化状态；每个实例只属于一个唯一 run ID。
type Engine struct {
	config    Config
	source    linkdconfig.EventSource
	severity  linkdconfig.SeverityConfig
	mapper    *cleaner.Mapper
	publisher Publisher
	logger    *slog.Logger
	random    randomSource
	clock     engineClock

	active        map[string]activeAlert
	alertCounter  uint64
	eventCounter  uint64
	rateRemainder uint64
	totalSent     uint64
}

// New 创建并终检一个 eventgen Engine。
func New(
	cfg Config,
	source linkdconfig.EventSource,
	severity linkdconfig.SeverityConfig,
	publisher Publisher,
	logger *slog.Logger,
) (*Engine, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("create event generator: %w", err)
	}
	if publisher == nil {
		return nil, fmt.Errorf("create event generator: publisher is required")
	}
	if logger == nil {
		return nil, fmt.Errorf("create event generator: logger is required")
	}
	mapper, err := cleaner.NewMapper(source, severity)
	if err != nil {
		return nil, fmt.Errorf("create event generator mapper: %w", err)
	}
	cfg.Scenarios = append([]Scenario(nil), cfg.Scenarios...)
	return &Engine{
		config: cfg, source: source.WithDefaults(), severity: severity.WithDefaults(), mapper: mapper,
		publisher: publisher, logger: logger, random: newRandomSource(cfg.Seed), clock: realClock{},
		active: make(map[string]activeAlert),
	}, nil
}

// Run 立即执行第一个周期，之后按配置间隔串行运行，直到达到有限周期数或 Context 取消。
func (e *Engine) Run(ctx context.Context) error {
	for cycle := 1; e.config.Cycles == 0 || cycle <= e.config.Cycles; cycle++ {
		startedAt := e.clock.Now().Round(0).UTC()
		result, err := e.RunCycle(ctx, cycle, startedAt)
		if err != nil {
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				return nil
			}
			return fmt.Errorf("run event generator cycle %d: %w", cycle, err)
		}
		result.Duration = e.clock.Now().Sub(startedAt)
		e.logger.InfoContext(ctx, "event generator cycle completed",
			"run_id", e.config.RunID,
			"cycle", result.Cycle,
			"generated", result.Generated,
			"resolved", result.Resolved,
			"duplicated", result.Duplicated,
			"active", result.Active,
			"published", result.Published,
			"total_sent", result.TotalSent,
			"duration", result.Duration,
		)
		if e.config.Cycles != 0 && cycle == e.config.Cycles {
			return nil
		}
		wait := e.config.CycleDuration - result.Duration
		if wait < 0 {
			e.logger.WarnContext(ctx, "event generator cycle exceeded configured duration",
				"run_id", e.config.RunID,
				"cycle", cycle,
				"duration", result.Duration,
				"cycle_duration", e.config.CycleDuration,
			)
			wait = 0
		}
		if err := e.clock.Wait(ctx, wait); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			return fmt.Errorf("wait for event generator cycle: %w", err)
		}
	}
	return nil
}

// RunCycle 执行一个确定的周期。它先规划并发布旧告警恢复，再发布本周期新告警。
// 只有整个 Publisher 调用成功后，活动池才一次性应用本周期变更。
func (e *Engine) RunCycle(ctx context.Context, cycle int, startedAt time.Time) (CycleResult, error) {
	if err := ctx.Err(); err != nil {
		return CycleResult{}, err
	}
	if cycle < 1 {
		return CycleResult{}, fmt.Errorf("cycle must be positive")
	}
	startedAt = startedAt.Round(0).UTC()
	if startedAt.IsZero() {
		return CycleResult{}, fmt.Errorf("cycle started_at must not be zero")
	}

	recoveryIDs, recoveryRecords, err := e.planRecoveries(ctx, startedAt)
	if err != nil {
		return CycleResult{}, err
	}
	quota, remainder := e.nextQuota()
	activeAfterRecovery := len(e.active) - len(recoveryIDs)
	if quota > e.config.MaxActiveAlerts-activeAfterRecovery {
		return CycleResult{}, fmt.Errorf(
			"active alert capacity exceeded: active_after_recovery=%d new=%d max=%d",
			activeAfterRecovery,
			quota,
			e.config.MaxActiveAlerts,
		)
	}

	newAlerts := make(map[string]activeAlert, quota)
	newRecords := make([]Record, 0, quota)
	for range quota {
		created, record, createErr := e.planNewAlert(ctx, startedAt)
		if createErr != nil {
			return CycleResult{}, createErr
		}
		if _, exists := e.active[created.template.AlertID]; exists {
			return CycleResult{}, fmt.Errorf("generated active alert identity is duplicated: %q", created.template.AlertID)
		}
		if _, exists := newAlerts[created.template.AlertID]; exists {
			return CycleResult{}, fmt.Errorf("generated cycle alert identity is duplicated: %q", created.template.AlertID)
		}
		newAlerts[created.template.AlertID] = created
		newRecords = append(newRecords, record)
	}

	uniqueRecords := make([]Record, 0, len(recoveryRecords)+len(newRecords))
	uniqueRecords = append(uniqueRecords, recoveryRecords...)
	uniqueRecords = append(uniqueRecords, newRecords...)
	records, duplicated := e.withDuplicates(uniqueRecords)
	if len(records) != 0 {
		if err := e.publisher.Publish(ctx, records); err != nil {
			return CycleResult{}, fmt.Errorf(
				"publish cycle records (resolved=%d generated=%d): %w", len(recoveryRecords), len(newRecords), err,
			)
		}
	}

	e.rateRemainder = remainder
	for _, alertID := range recoveryIDs {
		delete(e.active, alertID)
	}
	for alertID, alert := range newAlerts {
		e.active[alertID] = alert
	}
	e.totalSent += uint64(len(records))
	return CycleResult{
		Cycle: cycle, Generated: len(newRecords), Resolved: len(recoveryRecords), Duplicated: duplicated, Active: len(e.active),
		Published: len(records), TotalSent: e.totalSent, StartedAt: startedAt,
	}, nil
}

// withDuplicates 按概率在原记录后紧邻追加完全相同的 delivery。
// message_id、event_id、body 和时间均保持不变，确保验证 Repository 幂等而非身份冲突。
func (e *Engine) withDuplicates(records []Record) ([]Record, int) {
	if e.config.DuplicatePercent == 0 || len(records) == 0 {
		return records, 0
	}
	result := make([]Record, 0, len(records)*2)
	duplicated := 0
	for _, record := range records {
		result = append(result, record)
		//nolint:gosec // DuplicatePercent 已由 Config.Validate 限制为 0..100。
		if e.random.Uint64N(100) >= uint64(e.config.DuplicatePercent) {
			continue
		}
		result = append(result, record)
		duplicated++
	}
	return result, duplicated
}

func (e *Engine) planRecoveries(ctx context.Context, now time.Time) ([]string, []Record, error) {
	alertIDs := make([]string, 0, len(e.active))
	for alertID := range e.active {
		alertIDs = append(alertIDs, alertID)
	}
	sort.Strings(alertIDs)
	resolvedIDs := make([]string, 0)
	records := make([]Record, 0)
	for _, alertID := range alertIDs {
		// MeanLifetimeCycles 已由 Config.Validate 限制为正 int，转换不会溢出。
		//nolint:gosec // G115: 上述边界保证转换安全。
		lifetimeCycles := uint64(e.config.MeanLifetimeCycles)
		if e.random.Uint64N(lifetimeCycles) != 0 {
			continue
		}
		active := e.active[alertID]
		record, err := e.nextRecord(ctx, active.template, domain.EventActionResolved, now)
		if err != nil {
			return nil, nil, fmt.Errorf("build resolved event for %q: %w", alertID, err)
		}
		if record.Fingerprint != active.fingerprint {
			return nil, nil, fmt.Errorf(
				"resolved event fingerprint changed for %q: triggered=%q resolved=%q",
				alertID,
				active.fingerprint,
				record.Fingerprint,
			)
		}
		resolvedIDs = append(resolvedIDs, alertID)
		records = append(records, record)
	}
	return resolvedIDs, records, nil
}

func (e *Engine) planNewAlert(ctx context.Context, now time.Time) (activeAlert, Record, error) {
	e.alertCounter++
	scenario := e.config.Scenarios[e.random.Uint64N(uint64(len(e.config.Scenarios)))]
	template, err := buildAlertTemplate(scenario, e.alertCounter, e.config.RunID, e.severity, e.random)
	if err != nil {
		return activeAlert{}, Record{}, err
	}
	triggered, err := e.nextRecord(ctx, template, domain.EventActionTriggered, now)
	if err != nil {
		return activeAlert{}, Record{}, fmt.Errorf("build triggered event for %q: %w", template.AlertID, err)
	}
	previewID := fmt.Sprintf("sim-%s-preview-%012d", e.config.RunID, e.alertCounter)
	preview, err := e.buildRecord(ctx, template, domain.EventActionResolved, now, previewID)
	if err != nil {
		return activeAlert{}, Record{}, fmt.Errorf("build resolved preview for %q: %w", template.AlertID, err)
	}
	if triggered.Fingerprint != preview.Fingerprint {
		return activeAlert{}, Record{}, fmt.Errorf(
			"triggered and resolved fingerprints differ for %q: triggered=%q resolved=%q",
			template.AlertID,
			triggered.Fingerprint,
			preview.Fingerprint,
		)
	}
	return activeAlert{template: template, fingerprint: triggered.Fingerprint}, triggered, nil
}

func (e *Engine) nextRecord(
	ctx context.Context,
	template alertTemplate,
	action domain.EventAction,
	now time.Time,
) (Record, error) {
	e.eventCounter++
	sourceEventID := fmt.Sprintf("sim-%s-e%016d", e.config.RunID, e.eventCounter)
	return e.buildRecord(ctx, template, action, now, sourceEventID)
}

func (e *Engine) buildRecord(
	ctx context.Context,
	template alertTemplate,
	action domain.EventAction,
	now time.Time,
	sourceEventID string,
) (Record, error) {
	extra := template.TriggerExtra
	actionReason := "threshold_exceeded"
	content := template.Title + "，模拟告警已触发"
	if action == domain.EventActionResolved {
		extra = template.ResolvedExtra
		actionReason = "condition_recovered"
		content = template.Title + "，模拟告警已恢复"
	}
	payload := standardPayload{
		EventID: sourceEventID, AlertID: template.AlertID,
		Title: template.Title, Content: content, Severity: template.Severity,
		Action: action, ActionReason: actionReason,
		ConditionKey: string(template.Scenario), ConditionName: template.ConditionName,
		Dimensions: cloneAnyMap(template.Dimensions), Subject: template.Subject,
		OccurredAt: now, ProducedAt: now,
		Labels: cloneAnyMap(template.Labels), ExtraData: cloneAnyMap(extra),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Record{}, fmt.Errorf("encode standard payload: %w", err)
	}
	event, err := e.mapper.MapMessage(ctx, consume.Message{
		ID: sourceEventID, TenantID: e.config.TenantID, Body: body, EnqueuedAt: now,
	})
	if err != nil {
		return Record{}, fmt.Errorf("validate standard payload: %w", err)
	}
	orderKey := scheduler.CorrelationKey(event.BKTenantID, event.EventSourceID, event.Fingerprint)
	return Record{
		Key: []byte(event.Fingerprint), Body: body,
		Headers: map[string]string{
			"message_id":   sourceEventID,
			"bk_tenant_id": e.config.TenantID,
			"order_key":    orderKey,
		},
		Timestamp: now, Action: action, SourceEventID: sourceEventID,
		SourceAlertID: template.AlertID, Fingerprint: event.Fingerprint,
	}, nil
}

func (e *Engine) nextQuota() (int, uint64) {
	// Config.Validate 同时限制 rate、duration 并检查乘法溢出。
	//nolint:gosec // G115: NewAlertsPerMinute 是已校验的正 int。
	rate := uint64(e.config.NewAlertsPerMinute)
	//nolint:gosec // G115: CycleDuration 是已校验的正 time.Duration。
	duration := uint64(e.config.CycleDuration)
	total := e.rateRemainder + rate*duration
	quota := total / minuteNanoseconds
	// 最大 rate 和 duration 产生的 quota 远小于当前平台 int 上限。
	//nolint:gosec // G115: Config.Validate 的硬上限保证转换安全。
	return int(quota), total % minuteNanoseconds
}

type standardPayload struct {
	EventID       string             `json:"event_id"`
	AlertID       string             `json:"alert_id"`
	Title         string             `json:"title"`
	Content       string             `json:"content"`
	Severity      string             `json:"severity"`
	Action        domain.EventAction `json:"action"`
	ActionReason  string             `json:"action_reason"`
	ConditionKey  string             `json:"condition_key"`
	ConditionName string             `json:"condition_name"`
	Dimensions    map[string]any     `json:"dimensions"`
	Subject       standardSubject    `json:"subject"`
	OccurredAt    time.Time          `json:"occurred_at"`
	ProducedAt    time.Time          `json:"produced_at"`
	Labels        map[string]any     `json:"labels"`
	ExtraData     map[string]any     `json:"extra_data"`
}

type standardSubject struct {
	System string `json:"system"`
	Type   string `json:"type"`
	ID     string `json:"id"`
	Name   string `json:"name"`
}

func cloneAnyMap(source map[string]any) map[string]any {
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
