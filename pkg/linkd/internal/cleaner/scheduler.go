// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cleaner

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"linkd/internal/config"
)

// Flow 是一个 EventSource 对应的长生命周期处理流程。
//
// Run 必须持续运行到 ctx 取消或流程发生不可恢复错误。ctx 仍有效时返回 nil
// 会被 Scheduler 视为异常退出，避免事件源在无提示的情况下停止处理。
type Flow interface {
	Run(ctx context.Context) error
}

// FlowFunc 让普通函数实现 Flow。
type FlowFunc func(ctx context.Context) error

// Run 调用 f。
func (f FlowFunc) Run(ctx context.Context) error {
	if f == nil {
		return fmt.Errorf("flow function is nil")
	}
	return f(ctx)
}

// FlowFactory 为一个 EventSource 创建一条独立处理流程。
//
// Scheduler 会并发调用同一个 Factory；实现必须保证并发安全，且不能在不同
// EventSource 之间共享可变的消费 Session、确认状态或重试状态。
type FlowFactory interface {
	NewFlow(ctx context.Context, source config.EventSource) (Flow, error)
}

// FlowFactoryFunc 让普通函数实现 FlowFactory。
type FlowFactoryFunc func(ctx context.Context, source config.EventSource) (Flow, error)

// NewFlow 调用 f。
func (f FlowFactoryFunc) NewFlow(ctx context.Context, source config.EventSource) (Flow, error) {
	if f == nil {
		return nil, fmt.Errorf("flow factory function is nil")
	}
	return f(ctx, source)
}

// Scheduler 根据静态 EventSource 配置监督进程内处理流程。
// Scheduler 实例只能调用一次 Run。
type Scheduler struct {
	sources []config.EventSource
	factory FlowFactory
	running atomic.Bool
}

// NewScheduler 创建调度器。当前固定为每个 enabled EventSource 创建一条 Flow；
// disabled 来源保留配置但不创建 Flow。
func NewScheduler(sources []config.EventSource, severity config.SeverityConfig, factory FlowFactory) (*Scheduler, error) {
	normalized := make([]config.EventSource, len(sources))
	hasEnabledSource := false
	for index, source := range sources {
		normalized[index] = cloneEventSource(source)
		hasEnabledSource = hasEnabledSource || normalized[index].Enabled
	}
	if err := config.ValidateEventSources(normalized, severity); err != nil {
		return nil, fmt.Errorf("create event source scheduler: %w", err)
	}
	if hasEnabledSource && factory == nil {
		return nil, fmt.Errorf("create event source scheduler: flow factory is nil")
	}
	return &Scheduler{sources: normalized, factory: factory}, nil
}

type flowResult struct {
	eventSourceID string
	err           error
}

// Run 启动全部 enabled EventSource 的处理流程并等待退出。
//
// 当前尚未建立流程级健康状态、退避重建和部分可用状态面，因此采用 fail-fast：
// 任一流程创建失败、异常退出或运行失败都会取消其他流程；Run 会等待所有已启动流程
// 完成清理后返回聚合错误。调用方取消 ctx 属于正常停止，不返回取消错误。
func (s *Scheduler) Run(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("run event source scheduler: context is nil")
	}
	if !s.running.CompareAndSwap(false, true) {
		return fmt.Errorf("run event source scheduler: scheduler can only run once")
	}

	enabled := make([]config.EventSource, 0, len(s.sources))
	for _, source := range s.sources {
		if source.Enabled {
			enabled = append(enabled, cloneEventSource(source))
		}
	}
	if len(enabled) == 0 {
		<-ctx.Done()
		return nil
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan flowResult, len(enabled))
	for _, source := range enabled {
		go func(source config.EventSource) {
			results <- flowResult{
				eventSourceID: source.EventSourceID,
				err:           s.runFlow(runCtx, source),
			}
		}(source)
	}

	var runErrors []error
	for range enabled {
		result := <-results
		flowErr := result.err
		if flowErr == nil && runCtx.Err() == nil {
			flowErr = fmt.Errorf("flow stopped unexpectedly")
		}
		if flowErr == nil || expectedFlowCancellation(flowErr, runCtx) {
			continue
		}

		runErrors = append(runErrors, fmt.Errorf("event source %q: %w", result.eventSourceID, flowErr))
		cancel()
	}
	return errors.Join(runErrors...)
}

func (s *Scheduler) runFlow(ctx context.Context, source config.EventSource) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("flow panic: %v", recovered)
		}
	}()

	flow, err := s.factory.NewFlow(ctx, cloneEventSource(source))
	if err != nil {
		return fmt.Errorf("create flow: %w", err)
	}
	if flow == nil {
		return fmt.Errorf("create flow: factory returned nil flow")
	}
	if err := flow.Run(ctx); err != nil {
		return fmt.Errorf("run flow: %w", err)
	}
	return nil
}

func expectedFlowCancellation(err error, ctx context.Context) bool {
	return ctx.Err() != nil && errors.Is(err, ctx.Err())
}

func cloneEventSource(source config.EventSource) config.EventSource {
	return source.WithDefaults()
}
