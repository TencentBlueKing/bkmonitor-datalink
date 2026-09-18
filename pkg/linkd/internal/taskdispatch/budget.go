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
	"fmt"
	"slices"

	"linkd/internal/config"
)

// TaskBudget 是一个任务实际使用的并发及在途字节额度。
type TaskBudget struct {
	// Concurrency 是任务创建的处理 worker 数量。
	Concurrency int `json:"concurrency"`
	// InflightBytes 是任务接管消息的字节上限。
	InflightBytes int64 `json:"inflight_bytes"`
}

// WorkerRuntime 携带 worker 本机有效配置，不包含连接或凭据。
// Cleaner 需要完整默认配置以合并来源覆盖；Lifecycle 的所有来源共享同一额度。
type WorkerRuntime struct {
	Cleaner   *config.CleanerRuntimeConfig `json:"cleaner,omitempty"`
	Lifecycle *TaskBudget                  `json:"lifecycle,omitempty"`
}

func workerRuntime(cfg config.Config, roles []string) WorkerRuntime {
	runtime := WorkerRuntime{}
	if slices.Contains(roles, "cleaner") {
		cleaner := cfg.Cleaner.WithDefaults()
		runtime.Cleaner = &cleaner
	}
	if slices.Contains(roles, "lifecycle") {
		lc := config.LifecycleConfig{}
		if cfg.Lifecycle != nil {
			lc = *cfg.Lifecycle
		}
		effective := lc.WithDefaults().RuntimeConfig()
		runtime.Lifecycle = &TaskBudget{Concurrency: effective.WorkerCount, InflightBytes: int64(effective.MaxInflightBytes)}
	}
	return runtime
}

func (r WorkerRuntime) validate(roles []string) error {
	for _, role := range roles {
		if _, err := r.budgetFor(role, config.EventSource{}); err != nil {
			return err
		}
	}
	return nil
}

func (r WorkerRuntime) budgetFor(role string, source config.EventSource) (TaskBudget, error) {
	switch role {
	case "cleaner":
		if r.Cleaner == nil {
			return TaskBudget{}, fmt.Errorf("worker cleaner runtime is required")
		}
		effective := source.Cleaner.RuntimeConfig(*r.Cleaner)
		if err := effective.Validate(); err != nil {
			return TaskBudget{}, fmt.Errorf("worker cleaner runtime: %w", err)
		}
		return TaskBudget{Concurrency: effective.WorkerCount, InflightBytes: int64(effective.MaxInflightBytes)}, nil
	case "lifecycle":
		if r.Lifecycle == nil || r.Lifecycle.Concurrency < 1 || r.Lifecycle.Concurrency > 1024 || r.Lifecycle.InflightBytes < 1 || r.Lifecycle.InflightBytes > 256<<20 {
			return TaskBudget{}, fmt.Errorf("worker lifecycle budget is missing or invalid")
		}
		return *r.Lifecycle, nil
	default:
		return TaskBudget{}, fmt.Errorf("unknown worker role")
	}
}

// admitTask 在准备及启动时核对实际额度。停止中的任务仍占额度，
// 防止中心配置错误、延迟响应或排空未完成时超发本进程资源。
// 调用方必须持有 locals 的锁，检查和预留之间不能启动另一任务。
func (a *Agent) admitTask(task Task, source config.EventSource, locals map[string]*localTask) (TaskBudget, error) {
	if !slices.Contains(a.Roles, task.Role) {
		return TaskBudget{}, fmt.Errorf("task role is not registered")
	}
	budget, err := a.Runtime.budgetFor(task.Role, source)
	if err != nil {
		return TaskBudget{}, err
	}
	if task.Concurrency != budget.Concurrency || task.InflightBytes != budget.InflightBytes {
		return TaskBudget{}, fmt.Errorf("assigned budget differs from worker runtime")
	}
	count, concurrency, inflight := 1, budget.Concurrency, budget.InflightBytes
	for id, t := range locals {
		if id == task.ID || t.phase == "stopped" {
			continue
		}
		count++
		concurrency += t.budget.Concurrency
		inflight += t.budget.InflightBytes
	}
	maxConcurrency, maxBytes := a.Worker.Limits()
	if count > a.Config.WithDefaults().MaxTasks || concurrency > maxConcurrency || inflight > maxBytes {
		return TaskBudget{}, fmt.Errorf("worker task capacity exceeded")
	}
	return budget, nil
}
