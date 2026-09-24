// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package taskstate 保存当前控制面进程的任务目录和有界执行状态。
// 它不执行任务或触发对账；历史趋势仍由遥测系统负责。
package taskstate

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"sync"
	"time"

	"linkd/internal/taskgroup"
)

// Definition 描述启动时确定的任务职责。Settings 只能显式选择无凭据字段。
type Definition struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Group           string  `json:"group"`
	Description     string  `json:"description"`
	Kind            string  `json:"kind"`
	Enabled         bool    `json:"enabled"`
	DisabledReason  string  `json:"disabledReason,omitempty"`
	IntervalSeconds float64 `json:"intervalSeconds"`
	// DeadlineSeconds 是观测告警预算，不改变任务自身的 I/O 超时；0 表示没有可靠的整轮期限。
	DeadlineSeconds float64        `json:"deadlineSeconds"`
	ConfigSource    string         `json:"configSource"`
	DependsOn       []string       `json:"dependsOn"`
	Settings        map[string]any `json:"settings"`
}

// Execution 是一轮或一个子流程的最近结果及本进程累计次数。
// ErrorCode 只允许装配层固定错误码，不接收原始驱动错误。
type Execution struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	StartedAt       *time.Time `json:"startedAt,omitempty"`
	FinishedAt      *time.Time `json:"finishedAt,omitempty"`
	LastSuccess     *time.Time `json:"lastSuccess,omitempty"`
	Running         bool       `json:"running"`
	Outcome         string     `json:"outcome"`
	ErrorCode       string     `json:"errorCode,omitempty"`
	DurationSeconds float64    `json:"durationSeconds"`
	Succeeded       uint64     `json:"succeeded"`
	Failed          uint64     `json:"failed"`
	Canceled        uint64     `json:"canceled"`
	Work            int        `json:"work"`
	Failures        int        `json:"failures"`
}

// Task 同时保留启用配置、真实生命周期和执行观测，避免将缺指标解释为停用。
type Task struct {
	Definition
	Active           bool        `json:"active"`
	State            string      `json:"state"`
	Execution        Execution   `json:"execution"`
	Steps            []Execution `json:"steps"`
	DetailsTruncated bool        `json:"detailsTruncated"`
}

// Snapshot 只代表 Owner 指定的一个进程，不能解释为集群汇总。
type Snapshot struct {
	Owner      string    `json:"owner"`
	StartedAt  time.Time `json:"startedAt"`
	SnapshotAt time.Time `json:"snapshotAt"`
	Tasks      []Task    `json:"tasks"`
	Services   []Task    `json:"services"`
}

type entry struct {
	task   Task
	active int
	steps  map[string]Execution
}

// Registry 通过互斥锁隔离后台任务与管理 API；每项至多保留 64 个子流程。
// 不保存业务载荷、原始错误、租户凭据或无界历史。
type Registry struct {
	mu      sync.Mutex
	owner   string
	started time.Time
	entries map[string]*entry
	order   []string
}

// New 固定目录，后续未知 ID 的观察会被忽略。定义由控制面装配统一注册。
func New(owner string, definitions []Definition) *Registry {
	r := &Registry{owner: owner, started: time.Now().UTC(), entries: map[string]*entry{}}
	for _, d := range definitions {
		if _, exists := r.entries[d.ID]; exists {
			continue
		}
		d.Settings = maps.Clone(d.Settings)
		d.DependsOn = slices.Clone(d.DependsOn)
		if d.DependsOn == nil {
			d.DependsOn = []string{}
		}
		r.entries[d.ID] = &entry{task: Task{Definition: d, Execution: Execution{ID: d.ID, Name: d.Name, Outcome: "pending", Work: -1}}, steps: map[string]Execution{}}
		r.order = append(r.order, d.ID)
	}
	return r
}

// Wrap 将实际 goroutine 生命周期与目录关联，取消和 panic 均释放 owner。
func (r *Registry) Wrap(id string, task taskgroup.Task) taskgroup.Task {
	r.mu.Lock()
	e := r.entries[id]
	registered := e != nil && e.task.Enabled
	r.mu.Unlock()
	// 装配遗漏不能静默运行成页面上不存在的后台任务。
	if !registered {
		task.Run = func(context.Context) error {
			return fmt.Errorf("control plane task %q is not registered or enabled", id)
		}
		return task
	}
	run := task.Run
	task.Run = func(ctx context.Context) error { r.active(id, 1); defer r.active(id, -1); return run(ctx) }
	return task
}

func (r *Registry) active(id string, delta int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.entries[id]; e != nil {
		e.active = max(0, e.active+delta)
	}
}

// Begin 标记一轮开始；单个 ID/step 必须由所属任务串行调用。
func (r *Registry) Begin(id, step, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, x := r.execution(id, step, name)
	if e == nil {
		return
	}
	now := time.Now().UTC()
	x.StartedAt = &now
	x.Running = true
	r.save(e, step, x)
}

// Finish 记录实际完整轮次；work 为 -1 表示没有可计数工作量，0 表示正常空闲。
// 取消不增加失败次数，不覆盖上次成功时间。
func (r *Registry) Finish(ctx context.Context, id, step, name string, duration time.Duration, work, failures int, code string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, x := r.execution(id, step, name)
	if e == nil {
		return
	}
	now := time.Now().UTC()
	if !x.Running {
		start := now.Add(-duration)
		x.StartedAt = &start
	}
	x.FinishedAt = &now
	x.Running = false
	x.DurationSeconds = duration.Seconds()
	x.Work = work
	x.Failures = failures
	x.ErrorCode = code
	switch {
	case ctx.Err() != nil:
		x.Outcome = "canceled"
		x.Canceled++
		x.ErrorCode = ""
	case code != "" || failures > 0:
		x.Outcome = "failed"
		x.Failed++
	case work == 0:
		x.Outcome = "idle"
		x.Succeeded++
		x.LastSuccess = &now
	default:
		x.Outcome = "succeeded"
		x.Succeeded++
		x.LastSuccess = &now
	}
	r.save(e, step, x)
}

// RetainSteps 移除已经不再属于该任务的动态目标，避免历史失败永久污染当前健康。
func (r *Registry) RetainSteps(id string, ids []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.entries[id]; e != nil {
		for k := range e.steps {
			if !slices.Contains(ids, k) {
				delete(e.steps, k)
			}
		}
		e.task.DetailsTruncated = false
	}
}

func (r *Registry) execution(id, step, name string) (*entry, Execution) {
	e := r.entries[id]
	if e == nil {
		return nil, Execution{}
	}
	if step == "" {
		return e, e.task.Execution
	}
	x, ok := e.steps[step]
	if !ok {
		if len(e.steps) >= 64 {
			e.task.DetailsTruncated = true
			return nil, Execution{}
		}
		x = Execution{ID: step, Name: name, Outcome: "pending", Work: -1}
	}
	return e, x
}

func (r *Registry) save(e *entry, step string, x Execution) {
	if step == "" {
		e.task.Execution = x
	} else {
		e.steps[step] = x
	}
}

// Snapshot 返回独立副本；只对有明确整轮预算的任务推导逾期。
// 连续归档不使用空闲间隔推断失败，服务不要求周期成功事件。
func (r *Registry) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	result := Snapshot{Owner: r.owner, StartedAt: r.started, SnapshotAt: now, Tasks: []Task{}, Services: []Task{}}
	for _, id := range r.order {
		e := r.entries[id]
		t := e.task
		t.Execution = cloneExecution(t.Execution)
		t.Settings = maps.Clone(t.Settings)
		t.DependsOn = slices.Clone(t.DependsOn)
		t.Active = e.active > 0
		t.Steps = []Execution{}
		for _, x := range e.steps {
			t.Steps = append(t.Steps, cloneExecution(x))
		}
		sort.Slice(t.Steps, func(i, j int) bool { return t.Steps[i].ID < t.Steps[j].ID })
		t.State = state(t, now)
		if t.Kind == "service" {
			result.Services = append(result.Services, t)
		} else {
			result.Tasks = append(result.Tasks, t)
		}
	}
	return result
}

func state(t Task, now time.Time) string {
	if !t.Enabled {
		return "disabled"
	}
	if !t.Active {
		return "stopped"
	}
	if t.Kind == "service" {
		return "healthy"
	}
	x := t.Execution
	if x.Outcome == "failed" {
		return "failed"
	}
	for _, s := range t.Steps {
		if s.Outcome == "failed" {
			return "failed"
		}
	}
	if t.DeadlineSeconds > 0 {
		budget := time.Duration(t.DeadlineSeconds * float64(time.Second))
		if x.Running && x.StartedAt != nil && now.Sub(*x.StartedAt) > budget {
			return "overdue"
		}
		if !x.Running && x.FinishedAt != nil && now.Sub(*x.FinishedAt) > budget+time.Duration(t.IntervalSeconds*float64(time.Second)) {
			return "overdue"
		}
	}
	for _, s := range t.Steps {
		if s.Running {
			if t.DeadlineSeconds > 0 && s.StartedAt != nil && now.Sub(*s.StartedAt) > time.Duration(t.DeadlineSeconds*float64(time.Second)) {
				return "overdue"
			}
			return "running"
		}
	}
	if x.Running {
		return "running"
	}
	if x.FinishedAt == nil {
		return "pending"
	}
	if x.Outcome == "idle" {
		return "idle"
	}
	return "healthy"
}

func cloneExecution(x Execution) Execution {
	if x.StartedAt != nil {
		v := *x.StartedAt
		x.StartedAt = &v
	}
	if x.FinishedAt != nil {
		v := *x.FinishedAt
		x.FinishedAt = &v
	}
	if x.LastSuccess != nil {
		v := *x.LastSuccess
		x.LastSuccess = &v
	}
	return x
}

// Workload 补充最近完整轮次的工作量，不重复累计轮次或改变失败结果。
func (r *Registry) Workload(id string, work, failures int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.entries[id]; e != nil {
		e.task.Execution.Work = work
		e.task.Execution.Failures = failures
		if e.task.Execution.Outcome == "succeeded" && work == 0 {
			e.task.Execution.Outcome = "idle"
		}
	}
}
