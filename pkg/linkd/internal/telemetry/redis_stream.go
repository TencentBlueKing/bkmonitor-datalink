// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	controlplaneredisstream "linkd/internal/controlplane/redisstream"
)

type redisStreamObserver struct {
	metrics *instruments
	task    *ControlPlaneTaskObserver
}

// RedisStreamObserver 创建控制面 Redis Stream 管理任务的低基数观察器。
// Stream 和 Consumer Group 名称来自配置，禁止作为指标属性输出。
func (r *Runtime) RedisStreamObserver() controlplaneredisstream.Observer {
	if r == nil || r.metrics == nil {
		return nil
	}
	return &redisStreamObserver{
		metrics: r.metrics,
		task:    r.ControlPlaneTaskObserver(ControlPlaneTaskRedisStreamManager),
	}
}

func (o *redisStreamObserver) ObserveSnapshot(ctx context.Context, snapshot controlplaneredisstream.Snapshot) {
	o.metrics.redisStreamExists.Record(ctx, boolInt64(snapshot.Exists))
	o.metrics.redisStreamExpectedGroupPresent.Record(ctx, boolInt64(snapshot.ExpectedGroupPresent))
	o.metrics.redisStreamLength.Record(ctx, snapshot.Length)
	o.metrics.redisStreamEntriesAdded.Record(ctx, snapshot.EntriesAdded)
	o.metrics.redisStreamMemory.Record(ctx, snapshot.MemoryBytes)
	o.metrics.redisStreamGroups.Record(ctx, snapshot.Groups)
	o.metrics.redisStreamConsumers.Record(ctx, snapshot.Consumers)
	o.metrics.redisStreamPending.Record(ctx, snapshot.Pending)
	o.metrics.redisStreamMaxLag.Record(ctx, snapshot.MaxLag)
	o.metrics.redisStreamOldestEntryAge.Record(ctx, snapshot.OldestEntryAgeSeconds)
	o.metrics.redisStreamOldestPendingAge.Record(ctx, snapshot.OldestPendingAgeSeconds)
	o.metrics.redisStreamMaxEntries.Record(ctx, snapshot.MaxEntries)
	o.metrics.redisStreamEntriesAboveMax.Record(ctx, snapshot.EntriesAboveConfiguredMax)
	o.metrics.redisStreamTrimRequired.Record(ctx, boolInt64(snapshot.TrimRequired))
	o.metrics.redisStreamTrimSafe.Record(ctx, boolInt64(snapshot.TrimSafe))
}

func (o *redisStreamObserver) ReconcileFinished(
	ctx context.Context,
	outcome string,
	duration time.Duration,
	trimmed int64,
) {
	attributes := metric.WithAttributes(attribute.String("linkd.outcome", outcome))
	o.metrics.redisStreamReconcileOperations.Add(ctx, 1, attributes)
	o.metrics.redisStreamReconcileDuration.Record(ctx, duration.Seconds(), attributes)
	o.metrics.redisStreamTrimLastEntries.Record(ctx, trimmed)
	if trimmed > 0 {
		o.metrics.redisStreamTrimmedEntries.Add(ctx, trimmed)
	}
	o.task.RunFinished(ctx, duration, outcome == "succeeded")
}

var _ controlplaneredisstream.Observer = (*redisStreamObserver)(nil)
