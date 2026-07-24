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

// subscribeEvent 作为单一 supervisor 管理 Runtime 订阅生命周期。每次订阅建立后
// 都在消费事件的同时执行一次全量收敛，以覆盖首次启动和断线重连期间的事件空窗。
func (s *BkLogSidecar) subscribeEvent(ctx context.Context, ready chan<- struct{}) {
	initial := true
	for {
		if ctx.Err() != nil {
			return
		}

		runtime, err := s.getRuntime()
		if err != nil {
			s.log.Error(err, "runtime initialization failed, retrying",
				"retryAfter", s.runtimeSubscribeRetryInterval().String())
			if !s.waitRuntimeSubscribeRetry(ctx) {
				return
			}
			continue
		}

		subscriptionCtx, cancel := context.WithCancel(ctx)
		events, errs, err := runtime.Subscribe(subscriptionCtx)
		if err != nil {
			cancel()
			s.log.Error(err, "runtime event subscription start failed, retrying",
				"retryAfter", s.runtimeSubscribeRetryInterval().String())
			if !s.waitRuntimeSubscribeRetry(ctx) {
				return
			}
			continue
		}

		subscriptionDone := make(chan error, 1)
		go func() {
			subscriptionDone <- s.consumeRuntimeSubscription(subscriptionCtx, events, errs)
		}()

		err = s.convergeRuntimeSubscription(subscriptionCtx, subscriptionDone, initial)
		if err != nil {
			cancel()
			if ctx.Err() != nil {
				return
			}
			s.log.Error(err, "runtime event subscription interrupted during convergence, retrying",
				"retryAfter", s.runtimeSubscribeRetryInterval().String())
			if !s.waitRuntimeSubscribeRetry(ctx) {
				return
			}
			continue
		}

		if initial {
			close(ready)
			initial = false
		}

		err = <-subscriptionDone
		cancel()
		if ctx.Err() != nil {
			return
		}
		s.log.Error(err, "runtime event subscription interrupted, retrying",
			"retryAfter", s.runtimeSubscribeRetryInterval().String())
		if !s.waitRuntimeSubscribeRetry(ctx) {
			return
		}
	}
}

// convergeRuntimeSubscription 在事件流保持活跃期间完成一次全量收敛。Build/Apply
// 或 reload 临时失败时使用有最大间隔的指数退避重试；只有真正成功后，调用方
// 才能把首次启动标记为 ready。订阅断开或进程退出会立即中断等待。
func (s *BkLogSidecar) convergeRuntimeSubscription(
	ctx context.Context,
	subscriptionDone <-chan error,
	initial bool,
) error {
	retryDelay := s.convergenceRetryBaseInterval()
	attempt := 1
	for {
		err := s.convergeAfterRuntimeSubscription(initial)
		if err == nil {
			// 避免在全量收敛期间已经断线时误报首次启动成功。
			select {
			case subscriptionErr := <-subscriptionDone:
				return fmt.Errorf("runtime subscription closed before convergence completed: %w", subscriptionErr)
			default:
				return nil
			}
		}

		trigger := "runtime_reconnect"
		if initial {
			trigger = "startup"
		}
		s.log.Error(err, "runtime configuration convergence failed, retrying",
			"trigger", trigger,
			"attempt", attempt,
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

func (s *BkLogSidecar) convergeAfterRuntimeSubscription(initial bool) error {
	if initial {
		if err := s.cacheContainer(); err != nil {
			s.log.Error(err, "initial container cache refresh failed")
		}
		if err := s.generateActualBkLogConfigOnStartup(); err != nil {
			return fmt.Errorf("initial configuration generation: %w", err)
		}
		return nil
	}

	if err := s.generateActualBkLogConfig(); err != nil {
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
