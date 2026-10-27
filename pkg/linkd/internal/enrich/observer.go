// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package enrich

import (
	"context"
	"time"

	"linkd/internal/domain"
)

const (
	ProcessorOutcomeCompleted     = "completed"
	ProcessorOutcomeReused        = "reused"
	ProcessorOutcomeMatchError    = "match_error"
	ProcessorOutcomeProcessError  = "process_error"
	ProcessorOutcomePanic         = "panic"
	ProcessorOutcomeInvalidResult = "invalid_result"
)

// ProcessorObservation 是一次 Processor 执行完成后的低基数观测事实。
type ProcessorObservation struct {
	Processor   string
	Status      domain.EnrichStatus
	Outcome     string
	Duration    time.Duration
	Diagnostics []Diagnostic
}

// Observer 观察 Chain 中的 Processor 执行结果。
type Observer interface {
	ProcessorFinished(ctx context.Context, observation ProcessorObservation)
}

type noopObserver struct{}

func (noopObserver) ProcessorFinished(context.Context, ProcessorObservation) {}

// NoopObserver 返回可安全复用的空观察器。
func NoopObserver() Observer { return noopObserver{} }

// ChainOption 配置 Chain 的可选能力。
type ChainOption func(*Chain)

// WithObserver 注入 Processor 观察器。
func WithObserver(observer Observer) ChainOption {
	return func(chain *Chain) {
		if observer != nil {
			chain.observer = observer
		}
	}
}

// WithProcessorTiming 为本次串行 Enrich 调用注入 processor 的 Match/Process 计时边界。
// 不修改共享 Chain；回调与返回的结束函数都在原 goroutine 同步调用。
func WithProcessorTiming(ctx context.Context, start func(string) func()) context.Context {
	return context.WithValue(ctx, processorTimingKey{}, start)
}

type processorTimingKey struct{}

func startProcessorTiming(ctx context.Context, name string) func() {
	if start, ok := ctx.Value(processorTimingKey{}).(func(string) func()); ok && start != nil {
		return start(name)
	}
	return func() {}
}

// WithCMDBSetModuleEnabled 控制监控场景的集群、模块拓扑补充；业务归属与实例属性不受影响。
func WithCMDBSetModuleEnabled(enabled bool) ChainOption {
	return func(chain *Chain) { chain.cmdbSetModuleEnabled = enabled }
}
