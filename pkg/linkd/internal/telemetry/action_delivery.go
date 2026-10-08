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
	"log/slog"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"linkd/internal/actiondelivery"
)

type actionInstruments struct {
	running      metric.Int64UpDownCounter
	inflight     metric.Int64UpDownCounter
	rounds       metric.Int64Counter
	duration     metric.Float64Histogram
	observations metric.Int64Counter
	pageItems    metric.Int64Gauge
	pageAge      metric.Float64Gauge
	pageAt       metric.Int64Gauge
	unconfirmed  metric.Int64Counter
}

func newActionInstruments(meter *instrumentRegistry) (*actionInstruments, error) {
	result := &actionInstruments{}
	var err error
	if result.running, err = meter.Int64UpDownCounter("linkd.action.runner.active", describeMetric("动作循环实例数", "action_delivery", "state", "linkd.task"), metric.WithUnit("{runner}"), metric.WithDescription("当前进程正在监督的动作意图或发送循环实例数")); err != nil {
		return nil, err
	}
	if result.inflight, err = meter.Int64UpDownCounter("linkd.action.runner.inflight", describeMetric("动作执行中实例数", "action_delivery", "state", "linkd.task"), metric.WithUnit("{runner}"), metric.WithDescription("当前正在扫描或执行动作工作页的循环实例数")); err != nil {
		return nil, err
	}
	if result.rounds, err = meter.Int64Counter("linkd.action.runner.rounds", describeMetric("动作扫描轮次", "action_delivery", "reliability", "linkd.task", "linkd.outcome"), metric.WithUnit("{round}"), metric.WithDescription("动作有界扫描页次数，包含失败和取消")); err != nil {
		return nil, err
	}
	if result.duration, err = meter.Float64Histogram("linkd.action.runner.duration", describeMetric("动作扫描页耗时", "action_delivery", "latency", "linkd.task", "linkd.outcome"), metric.WithUnit("s"), metric.WithDescription("扫描及执行一页动作工作的墙钟耗时，包含有界退出清理"), metric.WithExplicitBucketBoundaries(.01, .1, .5, 1, 2, 5, 10, 20, 30, 60, 90, 120)); err != nil {
		return nil, err
	}
	if result.observations, err = meter.Int64Counter("linkd.action.work.observations", describeMetric("动作工作观察结果", "action_delivery", "throughput", "linkd.task", "linkd.outcome"), metric.WithUnit("{item}"), metric.WithDescription("每页观察项的互斥结果计数，可重复观察同一任务；不是唯一动作数或 HTTP 请求数")); err != nil {
		return nil, err
	}
	if result.pageItems, err = meter.Int64Gauge("linkd.action.last_page.items", describeMetric("最近动作页项目数", "action_delivery", "capacity", "linkd.task"), metric.WithUnit("{item}"), metric.WithDescription("最近合法扫描页的项目数，最多 16；不是全局待办总量")); err != nil {
		return nil, err
	}
	if result.pageAge, err = meter.Float64Gauge("linkd.action.last_page.oldest_age", describeMetric("最近动作页最大等待年龄", "action_delivery", "latency", "linkd.task"), metric.WithUnit("s"), metric.WithDescription("最近合法扫描页在观察时的最大待办年龄，不代表全局最老积压")); err != nil {
		return nil, err
	}
	if result.pageAt, err = meter.Int64Gauge("linkd.action.last_page.observed_at", describeMetric("最近动作页观察时间", "action_delivery", "state", "linkd.task"), metric.WithUnit("s"), metric.WithDescription("最近合法扫描页的观察时间，Unix 秒；扫描失败不覆盖已有观察")); err != nil {
		return nil, err
	}
	if result.unconfirmed, err = meter.Int64Counter("linkd.action.work.unconfirmed", describeMetric("动作此前结果未确认观察数", "action_delivery", "reliability", "linkd.task"), metric.WithUnit("{item}"), metric.WithDescription("返回任务仍有此前结果未确认标记的观察次数；重复读取会重复计数")); err != nil {
		return nil, err
	}
	return result, nil
}

// ActionRunnerObserver 记录固定阶段的本地指标和最多四条失败定位日志，不记录载荷或依赖错误正文。
// 同一个观察器仅绑定一个 Runner；多个实例由调用方分别创建，公共指标按进程和阶段聚合。
type ActionRunnerObserver struct {
	mu       sync.Mutex
	running  [2]bool
	inflight [2]bool
	metrics  *actionInstruments
	logger   *slog.Logger
}

// ActionRunnerObserver 返回运行器可直接使用的观察器；logger 为 nil 时只记录指标。
func (r *Runtime) ActionRunnerObserver(logger *slog.Logger) *ActionRunnerObserver {
	if r == nil || r.metrics == nil {
		return &ActionRunnerObserver{logger: logger}
	}
	return &ActionRunnerObserver{metrics: r.metrics.actionDelivery, logger: logger}
}

// SetRunning 只接受运行器固定阶段，退出时同时清除执行中标记。
func (o *ActionRunnerObserver) SetRunning(ctx context.Context, phase actiondelivery.RunnerPhase, active bool) {
	if o == nil || o.metrics == nil || !phase.Valid() {
		return
	}
	attrs := metric.WithAttributes(attribute.String("linkd.task", string(phase)))
	o.metrics.running.Add(ctx, o.transition(phase, active, false), attrs)
	if !active {
		o.metrics.inflight.Add(ctx, o.transition(phase, false, true), attrs)
	}
}

// RoundStarted 表示正在处理一页，不代表已经取得业务租约或开始 HTTP 请求。
func (o *ActionRunnerObserver) RoundStarted(ctx context.Context, phase actiondelivery.RunnerPhase) {
	if o == nil || o.metrics == nil || !phase.Valid() {
		return
	}
	o.metrics.inflight.Add(ctx, o.transition(phase, true, true), metric.WithAttributes(attribute.String("linkd.task", string(phase))))
}

func (o *ActionRunnerObserver) transition(phase actiondelivery.RunnerPhase, next, inflight bool) int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	i := 0
	if phase == actiondelivery.PhaseDeliver {
		i = 1
	}
	values := &o.running
	if inflight {
		values = &o.inflight
	}
	before := values[i]
	values[i] = next
	return boolInt64(next) - boolInt64(before)
}

// RoundFinished 区分页面/工作结果和扫描观察范围；业务定位信息仅进入有界日志样本。
func (o *ActionRunnerObserver) RoundFinished(ctx context.Context, phase actiondelivery.RunnerPhase, result actiondelivery.RoundResult) {
	if o == nil || !phase.Valid() {
		return
	}
	// 即使依赖返回非法观察值，页面也已结束；拒绝其计数仍须释放执行中水位。
	if o.metrics != nil {
		o.metrics.inflight.Add(ctx, o.transition(phase, false, true), metric.WithAttributes(attribute.String("linkd.task", string(phase))))
	}
	if !validActionRound(result) {
		return
	}
	outcome := "succeeded"
	if result.ErrorCode != "" {
		outcome = "failed"
	}
	if result.ErrorCode == "cancelled" {
		outcome = "cancelled"
	}
	if o.metrics != nil {
		task := metric.WithAttributes(attribute.String("linkd.task", string(phase)))
		attrs := metric.WithAttributes(attribute.String("linkd.task", string(phase)), attribute.String("linkd.outcome", outcome))
		o.metrics.rounds.Add(ctx, 1, attrs)
		o.metrics.duration.Record(ctx, result.Duration.Seconds(), attrs)
		for state, n := range result.Outcomes {
			if n > 0 {
				o.metrics.observations.Add(ctx, int64(n), metric.WithAttributes(attribute.String("linkd.task", string(phase)), attribute.String("linkd.outcome", string(state))))
			}
		}
		if !result.ObservedAt.IsZero() {
			o.metrics.pageItems.Record(ctx, int64(result.Scanned), task)
			o.metrics.pageAge.Record(ctx, result.OldestObservedAge.Seconds(), task)
			o.metrics.pageAt.Record(ctx, result.ObservedAt.Unix(), task)
		}
		if result.Unconfirmed > 0 {
			o.metrics.unconfirmed.Add(ctx, int64(result.Unconfirmed), task)
		}
	}
	if o.logger != nil && result.ErrorCode != "" && result.ErrorCode != "cancelled" {
		o.logger.WarnContext(ctx, "action work page incomplete", "task", phase, "error_code", result.ErrorCode, "scanned", result.Scanned, "visited", result.Visited)
		for _, sample := range result.Failures {
			o.logger.WarnContext(ctx, "action work item failed", "task", phase, "error_code", safeActionFailureCode(sample.Code), "bk_tenant_id", sample.TenantID, "alert_id", sample.AlertID, "action_task_id", sample.TaskID)
		}
	}
}

func validActionRound(r actiondelivery.RoundResult) bool {
	if r.Scanned < 0 || r.Scanned > 16 || r.Visited < 0 || r.Visited > r.Scanned || r.Unconfirmed < 0 || r.Unconfirmed > r.Visited || r.Duration < 0 || r.OldestObservedAge < 0 || len(r.Failures) > 4 {
		return false
	}
	switch r.ErrorCode {
	case "", "scan_failed", "invalid_page", "item_failed", "cancelled", "round_timeout":
	default:
		return false
	}
	sum := 0
	for outcome, n := range r.Outcomes {
		if !outcome.Valid() || n < 0 || n > 16 {
			return false
		}
		sum += n
	}
	return sum == r.Scanned
}

func safeActionFailureCode(v string) string {
	switch v {
	case "invalid_result", "execution_failed", "cancelled", "timeout", "projection_pending", "projection_unavailable", "target_unavailable", "transport_failed", "remote_unauthorized", "identity_conflict", "remote_unavailable", "remote_rejected", "response_too_large", "response_invalid", "visibility_pending", "attempt_interrupted", "superseded_by_terminal":
		return v
	}
	return "execution_failed"
}

var _ actiondelivery.RunnerObserver = (*ActionRunnerObserver)(nil)
