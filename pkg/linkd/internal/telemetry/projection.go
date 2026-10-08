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
	"linkd/internal/projection"
)

type projectionInstruments struct {
	active, inflight     metric.Int64UpDownCounter
	rounds, observations metric.Int64Counter
	duration             metric.Float64Histogram
	pageItems, pageAt    metric.Int64Gauge
	pageAge              metric.Float64Gauge
}

func newProjectionInstruments(meter *instrumentRegistry) (*projectionInstruments, error) {
	out := &projectionInstruments{}
	var err error
	if out.active, err = meter.Int64UpDownCounter("linkd.projection.runner.active", describeMetric("投影循环实例数", "projection", "state", "linkd.task"), metric.WithUnit("{runner}"), metric.WithDescription("当前进程监督的投影生产或发送循环实例数，退出归零")); err != nil {
		return nil, err
	}
	if out.inflight, err = meter.Int64UpDownCounter("linkd.projection.runner.inflight", describeMetric("投影执行中实例数", "projection", "state", "linkd.task"), metric.WithUnit("{runner}"), metric.WithDescription("当前扫描或处理工作页的投影循环数，不是 HTTP 并发数")); err != nil {
		return nil, err
	}
	if out.rounds, err = meter.Int64Counter("linkd.projection.runner.rounds", describeMetric("投影扫描轮次", "projection", "reliability", "linkd.task", "linkd.outcome"), metric.WithUnit("{round}"), metric.WithDescription("投影有界工作页结束次数，包含失败和取消")); err != nil {
		return nil, err
	}
	if out.duration, err = meter.Float64Histogram("linkd.projection.runner.duration", describeMetric("投影扫描页耗时", "projection", "latency", "linkd.task", "linkd.outcome"), metric.WithUnit("s"), metric.WithDescription("扫描和处理一页投影工作的墙钟耗时，包含有界退出清理"), metric.WithExplicitBucketBoundaries(.01, .1, .5, 1, 2, 5, 10, 20, 30, 60, 90)); err != nil {
		return nil, err
	}
	if out.observations, err = meter.Int64Counter("linkd.projection.work.observations", describeMetric("投影工作观察结果", "projection", "throughput", "linkd.task", "linkd.outcome"), metric.WithUnit("{item}"), metric.WithDescription("合法页面条目的互斥结果，可重复观察；advanced 对生产表示建或复用任务，对投递表示 ACK 已完成，非 HTTP 请求数")); err != nil {
		return nil, err
	}
	if out.pageItems, err = meter.Int64Gauge("linkd.projection.last_page.items", describeMetric("最近投影页项目数", "projection", "capacity", "linkd.task"), metric.WithUnit("{item}"), metric.WithDescription("最近合法扫描页的条目数，最多 16，不是全局待办总量")); err != nil {
		return nil, err
	}
	if out.pageAge, err = meter.Float64Gauge("linkd.projection.last_page.oldest_age", describeMetric("最近投影页最大等待年龄", "projection", "latency", "linkd.task"), metric.WithUnit("s"), metric.WithDescription("该页在观察时的最大年龄，生产按当前业务 update_at，投递按首次 created_at；不是全局最老积压")); err != nil {
		return nil, err
	}
	if out.pageAt, err = meter.Int64Gauge("linkd.projection.last_page.observed_at", describeMetric("最近投影页观察时间", "projection", "state", "linkd.task"), metric.WithUnit("s"), metric.WithDescription("最近合法扫描页的 Unix 秒观察时间，扫描失败不覆盖已有值")); err != nil {
		return nil, err
	}
	return out, nil
}

// ProjectionRunnerObserver 为一个投影 Runner 记录固定阶段指标及有界概况日志。
// 多个实例各自创建观察器，共用进程指标时叠加活跃/执行数；不得将业务身份用作标签。
type ProjectionRunnerObserver struct {
	mu               sync.Mutex
	active, inflight [2]bool
	metrics          *projectionInstruments
	logger           *slog.Logger
}

// ProjectionRunnerObserver 不启动运行器；logger 为 nil 时只记录指标。
func (r *Runtime) ProjectionRunnerObserver(logger *slog.Logger) *ProjectionRunnerObserver {
	out := &ProjectionRunnerObserver{logger: logger}
	if r != nil && r.metrics != nil {
		out.metrics = r.metrics.projection
	}
	return out
}

func (o *ProjectionRunnerObserver) transition(phase projection.RunnerPhase, value bool, inflight bool) int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	index := 0
	if phase == projection.PhaseDeliver {
		index = 1
	}
	states := &o.active
	if inflight {
		states = &o.inflight
	}
	before := states[index]
	states[index] = value
	return boolInt64(value) - boolInt64(before)
}

// SetRunning 幂等更新同一阶段的生命周期，退出时同时清除执行中标记。
func (o *ProjectionRunnerObserver) SetRunning(ctx context.Context, phase projection.RunnerPhase, active bool) {
	if o == nil || o.metrics == nil || !phase.Valid() {
		return
	}
	attrs := metric.WithAttributes(attribute.String("linkd.task", string(phase)))
	o.metrics.active.Add(ctx, o.transition(phase, active, false), attrs)
	if !active {
		o.metrics.inflight.Add(ctx, o.transition(phase, false, true), attrs)
	}
}

// RoundStarted 记录一页开始，不表示已经取得目标租约或发出外部请求。
func (o *ProjectionRunnerObserver) RoundStarted(ctx context.Context, phase projection.RunnerPhase) {
	if o == nil || o.metrics == nil || !phase.Valid() {
		return
	}
	o.metrics.inflight.Add(ctx, o.transition(phase, true, true), metric.WithAttributes(attribute.String("linkd.task", string(phase))))
}

// RoundFinished 只接受有界一致的结果；失败扫描不覆盖最近合法页，也不伪造工作项。
func (o *ProjectionRunnerObserver) RoundFinished(ctx context.Context, phase projection.RunnerPhase, r projection.RoundResult) {
	if o == nil || !phase.Valid() {
		return
	}
	attrs := metric.WithAttributes(attribute.String("linkd.task", string(phase)))
	if o.metrics != nil {
		o.metrics.inflight.Add(ctx, o.transition(phase, false, true), attrs)
	}
	if !validProjectionRound(r) {
		return
	}
	outcome := "succeeded"
	if r.ErrorCode != "" {
		outcome = "failed"
	}
	if r.ErrorCode == "cancelled" {
		outcome = "cancelled"
	}
	if o.metrics != nil {
		resultAttrs := metric.WithAttributes(attribute.String("linkd.task", string(phase)), attribute.String("linkd.outcome", outcome))
		o.metrics.rounds.Add(ctx, 1, resultAttrs)
		o.metrics.duration.Record(ctx, r.Duration.Seconds(), resultAttrs)
		if !r.ObservedAt.IsZero() {
			o.metrics.pageItems.Record(ctx, int64(r.Scanned), attrs)
			o.metrics.pageAge.Record(ctx, r.OldestObservedAge.Seconds(), attrs)
			o.metrics.pageAt.Record(ctx, r.ObservedAt.Unix(), attrs)
		}
		// 扫描异常的 Failed=1 表示页面失败，不是一条已读出的业务记录。
		if r.Scanned > 0 {
			for state, n := range map[string]int{"advanced": r.Advanced, "deferred": r.Deferred - r.CapacityDeferred, "capacity": r.CapacityDeferred, "failed": r.Failed} {
				if n > 0 {
					o.metrics.observations.Add(ctx, int64(n), metric.WithAttributes(attribute.String("linkd.task", string(phase)), attribute.String("linkd.outcome", state)))
				}
			}
		}
	}
	if o.logger != nil && r.ErrorCode != "" && r.ErrorCode != "cancelled" {
		o.logger.WarnContext(ctx, "projection work page incomplete", "task", phase, "error_code", r.ErrorCode, "scanned", r.Scanned, "advanced", r.Advanced, "deferred", r.Deferred, "failed", r.Failed)
	}
}

func validProjectionRound(r projection.RoundResult) bool {
	if r.Scanned < 0 || r.Scanned > 16 || r.Advanced < 0 || r.Advanced > 16 || r.Deferred < 0 || r.Deferred > 16 || r.CapacityDeferred < 0 || r.CapacityDeferred > r.Deferred || r.Failed < 0 || r.Failed > 16 || r.Duration < 0 || r.OldestObservedAge < 0 {
		return false
	}
	switch r.ErrorCode {
	case "", "scan_failed", "invalid_page", "item_failed", "cancelled", "round_timeout":
	default:
		return false
	}
	if r.Scanned == 0 && r.ObservedAt.IsZero() && r.ErrorCode != "" {
		return r.Advanced == 0 && r.Deferred == 0 && r.Failed <= 1
	}
	if r.ErrorCode == "scan_failed" || r.ErrorCode == "invalid_page" {
		return false
	}
	return r.Advanced+r.Deferred+r.Failed == r.Scanned
}

var _ projection.RunnerObserver = (*ProjectionRunnerObserver)(nil)
