// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package runtime

import (
	"context"
	"sync"
	"time"

	"linkd/internal/policy"
)

// PolicyStatisticsWriter 只接受固定结果名称；租户和策略身份仅用于Redis分桶，不进入指标标签。
type PolicyStatisticsWriter interface {
	RecordPolicyObservation(context.Context, policy.Scope, string, string, time.Time) error
}

// PolicyMetrics 的全部参数均为有限分类，避免策略身份造成指标基数膨胀。
type PolicyMetrics interface {
	ObservePolicyMatch(context.Context, string, string, time.Duration)
	ObservePolicySample(context.Context, string)
	ObservePolicyDelay(context.Context, string, time.Duration)
}

// Observations 异步尽力采样，最多四个50ms写入，满额立即丢弃，没有等待队列。
// Close 必须在关闭Redis连接前调用；停止接受新观察并等待有界在途写入。
// 丢弃/失败有独立指标，不能通过采样结果推断精确业务执行次数。
type Observations struct {
	statistics PolicyStatisticsWriter
	metrics    PolicyMetrics
	slots      chan struct{}
	mu         sync.Mutex
	closed     bool
	pending    sync.WaitGroup
}

// NewObservations 为多个来源/控制任务共享同一个有界采样器。
func NewObservations(statistics PolicyStatisticsWriter, metrics PolicyMetrics) *Observations {
	return &Observations{statistics: statistics, metrics: metrics, slots: make(chan struct{}, 4)}
}

// Close 幂等停止采样，等待所有已接受的有界写入退出。
func (o *Observations) Close() {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.closed = true
	o.mu.Unlock()
	o.pending.Wait()
}

func (o *Observations) record(ctx context.Context, scope policy.Scope, id, outcome string) {
	if o == nil || o.statistics == nil || ctx.Err() != nil {
		return
	}
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return
	}
	select {
	case o.slots <- struct{}{}:
		o.pending.Add(1)
	default:
		o.mu.Unlock()
		if o.metrics != nil {
			o.metrics.ObservePolicySample(ctx, "dropped")
		}
		return
	}
	o.mu.Unlock()
	at := time.Now()
	// 业务请求结束不撤销已接受采样；独立50ms截止时间保证退出有界，且不消耗策略10秒预算。
	call, cancel := context.WithTimeout(context.WithoutCancel(ctx), 50*time.Millisecond)
	go func() {
		defer o.pending.Done()
		defer func() { <-o.slots }()
		defer cancel()
		err := o.statistics.RecordPolicyObservation(call, scope, id, outcome, at)
		if o.metrics != nil {
			result := "recorded"
			if err != nil {
				result = "failed"
			}
			o.metrics.ObservePolicySample(call, result)
		}
	}()
}

func (s *Suppressor) evaluate(ctx context.Context, release policy.Release, compiled *policy.Compiled, view *policy.FactView, targets policy.TargetReader, at time.Time, rely, group bool) (policy.Verdict, error) {
	started := time.Now()
	var verdict policy.Verdict
	var err error
	if group {
		verdict, err = policy.Evaluate(ctx, release, compiled, view, targets, at, rely)
	} else {
		verdict, err = policy.EvaluateConditions(ctx, release, compiled, view, targets, at, rely)
	}
	outcome := "not_matched"
	if err != nil || !verdict.Evaluated {
		outcome = "unavailable"
	} else if verdict.Matched {
		outcome = "matched"
	}
	if s.Observations != nil {
		if s.Observations.metrics != nil {
			s.Observations.metrics.ObservePolicyMatch(ctx, string(release.Kind), outcome, time.Since(started))
		}
		s.Observations.record(ctx, release.Scope, release.ID, outcome)
	}
	return verdict, err
}

func (s *Suppressor) fault(ctx context.Context, tenant, kind, id string) {
	if s != nil {
		s.Observations.record(ctx, policy.Scope{TenantID: tenant, Kind: policy.Kind(kind)}, id, "execution_skipped")
	}
}
