// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package taskdispatch

import (
	"context"
	"fmt"
	"sync"

	"linkd/internal/config"
)

type hostKey struct{}

type exitKey struct{}

// Host 显式聚合 all-in-one 两个实际消费者，不使用全局进程注册表。
type Host struct {
	Config  config.DispatchConfig
	Worker  config.WorkerConfig
	Roles   []string
	mu      sync.Mutex
	runners map[string]Runner
	started bool
	done    chan struct{}
	err     error
}

// WithHost 把一个进程的共享 agent 注入两种角色的装配上下文。
func WithHost(ctx context.Context, h *Host) context.Context {
	h.runners = map[string]Runner{}
	h.done = make(chan struct{})
	return context.WithValue(ctx, hostKey{}, h)
}

// WithForcedExit 由进程入口注入强制退出能力，库代码本身不调用 os.Exit。
func WithForcedExit(ctx context.Context, exit func()) context.Context {
	return context.WithValue(ctx, exitKey{}, exit)
}

// Serve 将资源已经准备好的模块加入进程 agent，退出前等待全部任务停止。
func Serve(ctx context.Context, cfg config.Config, role string, runner Runner, observers ...Observer) error {
	if cfg.Dispatch.WorkerToken == "" {
		return fmt.Errorf("dispatch.worker_token is required")
	}
	forced, _ := ctx.Value(exitKey{}).(func())
	h, _ := ctx.Value(hostKey{}).(*Host)
	if h == nil {
		a := Agent{Observer: observerOrNoop(observers...), Config: cfg.Dispatch, Worker: cfg.Worker, Roles: []string{role}, RunTask: runner, OnForcedExit: forced}
		return a.Run(ctx)
	}
	h.mu.Lock()
	if _, ok := h.runners[role]; ok {
		h.mu.Unlock()
		return fmt.Errorf("role already registered in worker")
	}
	h.runners[role] = runner
	if len(h.runners) == len(h.Roles) && !h.started {
		h.started = true
		runners := make(map[string]Runner, len(h.runners))
		for k, v := range h.runners {
			runners[k] = v
		}
		go func() {
			a := Agent{Observer: observerOrNoop(observers...), Config: h.Config, Worker: h.Worker, Roles: h.Roles, RunTask: func(ctx context.Context, t Task, s config.EventSource) error { return runners[t.Role](ctx, t, s) }, OnForcedExit: forced}
			e := a.Run(ctx)
			h.mu.Lock()
			h.err = e
			close(h.done)
			h.mu.Unlock()
		}()
	}
	h.mu.Unlock()
	select {
	case <-h.done:
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.err
	case <-ctx.Done():
		h.mu.Lock()
		started := h.started
		h.mu.Unlock()
		if started {
			<-h.done
		}
		return nil
	}
}
