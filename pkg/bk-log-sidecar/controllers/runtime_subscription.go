// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/define"
)

var errRuntimeSubscriptionClosed = errors.New("runtime event subscription closed")

// subscribeEvent 作为单一 supervisor 管理 Runtime 订阅生命周期。首次配置收敛
// 只执行一次；重连流需要先经过稳定窗口，再做一次差异化全量收敛。containerd
// 的服务端拒绝会异步出现在 error channel，不能因为 Subscribe 返回 channel 就
// 立即把订阅标记为 established，更不能在每次失败重试时重复强制 reload。
func (s *BkLogSidecar) subscribeEvent(ctx context.Context, ready chan<- struct{}) {
	initialConverged := false
	subscriptionAttempt := 1
	subscriptionRecovering := false
	subscriptionStartedAt := time.Now()
	for {
		if ctx.Err() != nil {
			return
		}
		initialAttempt := !initialConverged
		trigger := runtimeSubscriptionTrigger(initialAttempt)

		runtime, err := s.getRuntimeWithContext(ctx)
		if err != nil {
			subscriptionRecovering = true
			s.log.Error(err, "runtime initialization failed, retrying",
				"trigger", string(trigger),
				"result", convergenceResultFailure,
				"stage", "runtime_init",
				"subscriptionAttempt", subscriptionAttempt,
				"duration", time.Since(subscriptionStartedAt).String(),
				"retryAfter", s.runtimeSubscribeRetryInterval().String())
			if !s.waitRuntimeSubscribeRetry(ctx) {
				return
			}
			subscriptionAttempt++
			continue
		}

		subscriptionCtx, cancel := context.WithCancel(ctx)
		events, errs, err := runtime.Subscribe(subscriptionCtx)
		if err != nil {
			cancel()
			subscriptionRecovering = true
			s.log.Error(err, "runtime event subscription start failed, retrying",
				"trigger", string(trigger),
				"result", convergenceResultFailure,
				"stage", "subscribe",
				"runtime", runtime.Type(),
				"subscriptionAttempt", subscriptionAttempt,
				"duration", time.Since(subscriptionStartedAt).String(),
				"retryAfter", s.runtimeSubscribeRetryInterval().String())
			if !initialConverged && s.tryInitialConvergenceWithoutSubscription(ctx) {
				// 订阅接口持续不可用时，不能把启动全量收敛和周期补偿也一起
				// 卡住。首次成功后立即放行周期补偿，后续订阅重试不再重复
				// 启动强制 reload。
				close(ready)
				initialConverged = true
			}
			if !s.waitRuntimeSubscribeRetry(ctx) {
				return
			}
			subscriptionAttempt++
			continue
		}
		subscriptionDone := make(chan error, 1)
		go func() {
			subscriptionDone <- s.consumeRuntimeSubscription(subscriptionCtx, events, errs)
		}()

		// 首次收敛不依赖事件流继续存活。流如果在 Build/Apply 期间异步失败，
		// 配置仍然只强制收敛一次，随后 supervisor 单独重试订阅。
		if !initialConverged {
			err = s.convergeRuntimeSubscription(ctx, nil, true)
			if err != nil {
				cancel()
				return
			}
			close(ready)
			initialConverged = true
		}

		err = s.waitRuntimeSubscriptionStability(subscriptionCtx, subscriptionDone)
		if err != nil {
			cancel()
			if ctx.Err() != nil {
				return
			}
			s.log.Error(err, "runtime event subscription failed before becoming stable, retrying",
				"trigger", string(trigger),
				"result", convergenceResultFailure,
				"stage", "stream",
				"runtime", runtime.Type(),
				"subscriptionAttempt", subscriptionAttempt,
				"duration", time.Since(subscriptionStartedAt).String(),
				"retryAfter", s.runtimeSubscribeRetryInterval().String())
			subscriptionAttempt++
			subscriptionRecovering = true
			if !s.waitRuntimeSubscribeRetry(ctx) {
				return
			}
			continue
		}

		subscriptionEstablishedAt := time.Now()
		s.log.Info("runtime event subscription established",
			"trigger", string(runtimeSubscriptionTrigger(initialAttempt)),
			"result", convergenceResultSuccess,
			"stage", "subscribe",
			"runtime", runtime.Type(),
			"initial", initialAttempt,
			"subscriptionAttempt", subscriptionAttempt,
			"duration", time.Since(subscriptionStartedAt).String(),
			"subscriptionRecovered", subscriptionRecovering,
		)

		// 只有经历过订阅故障并真正稳定下来的流才需要补一次全量差异。
		// 首次配置已经在上方收敛过，健康启动不能再做第二次全量。
		if subscriptionRecovering {
			err = s.convergeRuntimeSubscription(subscriptionCtx, subscriptionDone, false)
			if err != nil {
				cancel()
				if ctx.Err() != nil {
					return
				}
				s.log.Error(err, "runtime event subscription interrupted during recovery convergence, retrying",
					"trigger", string(convergenceTriggerRuntimeReconnect),
					"result", convergenceResultFailure,
					"stage", "stream",
					"runtime", runtime.Type(),
					"subscriptionAttempt", subscriptionAttempt,
					"duration", time.Since(subscriptionEstablishedAt).String(),
					"retryAfter", s.runtimeSubscribeRetryInterval().String())
				subscriptionAttempt++
				if !s.waitRuntimeSubscribeRetry(ctx) {
					return
				}
				continue
			}
			subscriptionRecovering = false
		}

		err = <-subscriptionDone
		cancel()
		if ctx.Err() != nil {
			return
		}
		s.log.Error(err, "runtime event subscription interrupted, retrying",
			"trigger", string(convergenceTriggerRuntimeReconnect),
			"result", convergenceResultFailure,
			"stage", "stream",
			"runtime", runtime.Type(),
			"subscriptionAttempt", subscriptionAttempt,
			"duration", time.Since(subscriptionEstablishedAt).String(),
			"retryAfter", s.runtimeSubscribeRetryInterval().String())
		subscriptionAttempt = 1
		subscriptionRecovering = true
		subscriptionStartedAt = time.Now()
		if !s.waitRuntimeSubscribeRetry(ctx) {
			return
		}
	}
}

// waitRuntimeSubscriptionStability filters asynchronous startup failures from
// containerd. An idle healthy stream has no positive handshake event, so the
// supervisor uses a short stability window: errors arriving before it expires
// are subscription-start failures and must not trigger a full convergence.
func (s *BkLogSidecar) waitRuntimeSubscriptionStability(
	ctx context.Context,
	subscriptionDone <-chan error,
) error {
	timer := time.NewTimer(s.runtimeSubscriptionStableInterval())
	defer timer.Stop()
	select {
	case err := <-subscriptionDone:
		return err
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// convergeRuntimeSubscription 使用带最大间隔的指数退避完成一次全量收敛。
// reconnect 传入 subscriptionDone 时，事件流断开会立即中断等待；首次收敛可传
// nil，此时只受进程 Context 控制，避免异步订阅失败重复触发强制 reload。
func (s *BkLogSidecar) convergeRuntimeSubscription(
	ctx context.Context,
	subscriptionDone <-chan error,
	initial bool,
) error {
	retryDelay := s.convergenceRetryBaseInterval()
	attempt := 1
	for {
		startedAt := time.Now()
		err := s.convergeAfterRuntimeSubscription(ctx, initial)
		duration := time.Since(startedAt)
		if err == nil {
			// 避免在全量收敛期间已经断线时误报首次启动成功。
			select {
			case subscriptionErr := <-subscriptionDone:
				return fmt.Errorf("runtime subscription closed before convergence completed: %w", subscriptionErr)
			default:
				trigger := runtimeSubscriptionTrigger(initial)
				s.log.Info("runtime configuration convergence succeeded",
					"trigger", string(trigger),
					"result", convergenceResultSuccess,
					"stage", "convergence",
					"convergenceAttempt", attempt,
					"duration", duration.String(),
					"convergenceRecovered", attempt > 1,
				)
				return nil
			}
		}

		trigger := runtimeSubscriptionTrigger(initial)
		s.log.Error(err, "runtime configuration convergence failed, retrying",
			"trigger", string(trigger),
			"result", convergenceResultFailure,
			"stage", "convergence",
			"convergenceAttempt", attempt,
			"duration", duration.String(),
			"retryAfter", retryDelay.String(),
		)

		timer := time.NewTimer(retryDelay)
		select {
		case subscriptionErr := <-subscriptionDone:
			timer.Stop()
			return fmt.Errorf("runtime subscription closed while waiting to retry convergence: %w", subscriptionErr)
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}

		retryDelay = nextConvergenceRetryDelay(retryDelay, s.convergenceRetryMaximumInterval())
		attempt++
	}
}

func (s *BkLogSidecar) tryInitialConvergenceWithoutSubscription(ctx context.Context) bool {
	startedAt := time.Now()
	if err := s.convergeAfterRuntimeSubscription(ctx, true); err != nil {
		s.log.Error(err, "initial configuration convergence without runtime subscription failed",
			"trigger", string(convergenceTriggerStartup),
			"result", convergenceResultFailure,
			"stage", "fallback_convergence",
			"duration", time.Since(startedAt).String(),
			"eventSubscriptionAvailable", false,
		)
		return false
	}
	s.log.Info("initial configuration convergence succeeded without runtime subscription",
		"trigger", string(convergenceTriggerStartup),
		"result", convergenceResultSuccess,
		"stage", "fallback_convergence",
		"duration", time.Since(startedAt).String(),
		"eventSubscriptionAvailable", false,
	)
	return true
}

func (s *BkLogSidecar) convergeAfterRuntimeSubscription(ctx context.Context, initial bool) error {
	if initial {
		if err := s.cacheContainerWithContext(ctx); err != nil {
			s.log.Error(err, "initial container cache refresh failed")
		}
		if err := s.generateActualBkLogConfigOnStartup(ctx); err != nil {
			return fmt.Errorf("initial configuration generation: %w", err)
		}
		return nil
	}

	if err := s.generateActualBkLogConfigWithOptions(ctx, configGenerationOptions{}); err != nil {
		return fmt.Errorf("configuration convergence after runtime subscription reconnect: %w", err)
	}
	return nil
}

func (s *BkLogSidecar) consumeRuntimeSubscription(
	ctx context.Context,
	events <-chan *define.ContainerEvent,
	errs <-chan error,
) error {
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return fmt.Errorf("%w: event channel", errRuntimeSubscriptionClosed)
			}
			s.enqueueContainerEvent(event)
		case err, ok := <-errs:
			if !ok {
				return fmt.Errorf("%w: error channel", errRuntimeSubscriptionClosed)
			}
			if err == nil {
				return errors.New("runtime event subscription returned nil error")
			}
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *BkLogSidecar) runtimeSubscribeRetryInterval() time.Duration {
	if s.subscribeRetryInterval > 0 {
		return s.subscribeRetryInterval
	}
	return SubscribeRetryInterval
}

func (s *BkLogSidecar) runtimeSubscriptionStableInterval() time.Duration {
	if s.subscriptionStabilityWindow > 0 {
		return s.subscriptionStabilityWindow
	}
	return SubscriptionStabilityWindow
}

func (s *BkLogSidecar) convergenceRetryBaseInterval() time.Duration {
	if s.convergenceRetryBaseDelay > 0 {
		return s.convergenceRetryBaseDelay
	}
	return ConvergenceRetryBaseDelay
}

func (s *BkLogSidecar) convergenceRetryMaximumInterval() time.Duration {
	if s.convergenceRetryMaxDelay > 0 {
		return s.convergenceRetryMaxDelay
	}
	return ConvergenceRetryMaximumDelay
}

func nextConvergenceRetryDelay(current, maximum time.Duration) time.Duration {
	if current >= maximum || current > maximum/2 {
		return maximum
	}
	return current * 2
}

func (s *BkLogSidecar) waitRuntimeSubscribeRetry(ctx context.Context) bool {
	timer := time.NewTimer(s.runtimeSubscribeRetryInterval())
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
