// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type kacInstruments struct {
	items    metric.Int64Counter
	duration metric.Float64Histogram
	backlog  metric.Int64Gauge
	age      metric.Float64Gauge
}

func newKACInstruments(m *instrumentRegistry) (*kacInstruments, error) {
	k := &kacInstruments{}
	var err error
	k.items, err = m.Int64Counter("linkd.kac.items", describeMetric("KAC 处理条数", "kac", "throughput", "linkd.stage", "linkd.outcome"), metric.WithDescription("各阶段处理条数"))
	if err != nil {
		return nil, err
	}
	k.duration, err = m.Float64Histogram("linkd.kac.duration", describeMetric("KAC 处理耗时", "kac", "latency", "linkd.stage", "linkd.outcome"), metric.WithDescription("KAC 阶段处理耗时"), metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(.001, .01, .05, .1, .5, 1, 5, 10))
	if err != nil {
		return nil, err
	}
	k.backlog, err = m.Int64Gauge("linkd.kac.queue.messages", describeMetric("KAC 队列未完成消息", "kac", "capacity"), metric.WithDescription("Redis Stream 未完成消息总数"))
	if err != nil {
		return nil, err
	}
	k.age, err = m.Float64Gauge("linkd.kac.queue.oldest_age", describeMetric("KAC 队列最早等待时间", "kac", "latency"), metric.WithDescription("Redis Stream 最老消息年龄"), metric.WithUnit("s"))
	return k, err
}

// KACResult 记录低基数阶段指标，失败原因及业务身份由日志承接。
func (r *Runtime) KACResult(ctx context.Context, stage string, err error, duration time.Duration, count int) {
	if r == nil || r.metrics == nil {
		return
	}
	outcome := "succeeded"
	if err != nil {
		outcome = "failed"
	}
	attrs := metric.WithAttributes(attribute.String("linkd.stage", stage), attribute.String("linkd.outcome", outcome))
	r.metrics.kac.items.Add(ctx, int64(count), attrs)
	if duration > 0 {
		r.metrics.kac.duration.Record(ctx, duration.Seconds(), attrs)
	}
}

// KACQueue 记录包含 PEL 的真实积压，不把进程内缓冲解释为全部待办。
func (r *Runtime) KACQueue(ctx context.Context, count int64, age time.Duration) {
	if r == nil || r.metrics == nil {
		return
	}
	r.metrics.kac.backlog.Record(ctx, count)
	r.metrics.kac.age.Record(ctx, max(0, age.Seconds()))
}
