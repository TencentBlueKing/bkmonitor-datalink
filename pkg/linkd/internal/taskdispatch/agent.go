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
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"linkd/internal/config"
	"linkd/internal/consume"
	"linkd/internal/eventsource"
)

// Runner 适配一个任务；返回前必须退出所有后台工作并关闭任务所属资源。
type Runner func(context.Context, Task, config.EventSource) error

// Agent 负责进程全部角色，业务关闭不能阻塞独立的授权 watchdog。
type Agent struct {
	Observer          Observer
	Config            config.DispatchConfig
	Worker            config.WorkerConfig
	Roles             []string
	RunTask           Runner
	OnForcedExit      func()
	ObservePartitions func(string) []string
}

type localTask struct {
	partitions atomic.Value
	admission  atomic.Bool
	task       Task
	spec       config.EventSource
	phase      string
	cancel     context.CancelFunc
	done       chan struct{}
	deadline   time.Time
	stopAt     time.Time
	err        string
}

// Run 运行会话，配置/撤销立即开始停止；失联容错后自停，旧代次不可恢复。
func (a *Agent) Run(ctx context.Context) error {
	observer := observerOrNoop(a.Observer)
	// 停止确认和 watchdog 观测必须在调用方取消后继续，直到协议排空完成。
	observationCtx := context.WithoutCancel(ctx)
	cfg := a.Config.WithDefaults()
	id := uuid.NewString()
	client := Client{URL: cfg.URL, Token: cfg.WorkerToken, WorkerID: id}
	worker := Worker{ID: id, Roles: a.Roles, Labels: a.Worker.Labels, Explicit: a.Worker.RequireExplicitSelector, MaxTasks: cfg.MaxTasks}
	worker.MaxConcurrency, worker.MaxInflightBytes = a.Worker.Limits()
	var mu sync.Mutex
	var forceOnce sync.Once
	locals := map[string]*localTask{}
	lastSuccess := time.Now()
	failures := 0
	stopping := false
	stopTask := func(t *localTask, reason string) {
		if t.phase == "stopped" || t.phase == "stopping" {
			return
		}
		observer.Transition(observationCtx, "worker", t.task.Role, t.phase, "stopping", reason, 0)
		t.phase = "stopping"
		t.admission.Store(false)
		t.stopAt = time.Now()
		if t.cancel != nil {
			t.cancel()
		} else {
			t.phase = "stopped"
		}
	}
	stopAll := func(reason string) {
		for _, t := range locals {
			stopTask(t, reason)
		}
	}
	watchdogCtx, cancelWatch := context.WithCancel(observationCtx)
	watchDone := make(chan struct{})
	defer func() { cancelWatch(); <-watchDone }()
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-watchdogCtx.Done():
				return
			case <-ticker.C:
				mu.Lock()
				now := time.Now()
				if ctx.Err() != nil {
					stopping = true
					stopAll("shutdown")
				}
				if failures >= 3 && now.Sub(lastSuccess) >= 10*time.Second {
					stopAll("disconnected")
				}
				force := false
				for _, t := range locals {
					if (t.phase == "running" || t.phase == "prepared") && !t.deadline.IsZero() && now.Add(DrainTimeout+5*time.Second).After(t.deadline) {
						stopTask(t, "authorization_deadline")
					}
					if t.phase == "stopping" && now.Sub(t.stopAt) > DrainTimeout+5*time.Second {
						force = true
					}
				}
				sample := WorkerObservation{Tasks: map[string]map[string]int64{}, Failures: failures, HeartbeatAge: now.Sub(lastSuccess)}
				first := true
				for _, t := range locals {
					if sample.Tasks[t.task.Role] == nil {
						sample.Tasks[t.task.Role] = map[string]int64{}
					}
					sample.Tasks[t.task.Role][t.phase]++
					if t.phase != "stopped" {
						if !t.admission.Load() {
							sample.Paused++
						}
						if ps := t.partitions.Load(); ps != nil {
							sample.Partitions += int64(len(ps.([]string)))
						}
						remaining := max(0, t.deadline.Sub(now))
						if first || remaining < sample.AuthorizationRemaining {
							sample.AuthorizationRemaining = remaining
							first = false
						}
					}
				}
				mu.Unlock()
				observer.WorkerSnapshot(observationCtx, sample)
				if force && a.OnForcedExit != nil {
					forceOnce.Do(func() {
						observer.Operation(observationCtx, "forced_exit", false, 0)
						a.OnForcedExit()
					})
				}
			}
		}
	}()
	// defer 必须先取消 watchdog 再等待；显式返回路径统一在 finish 中执行。
	finish := func() error { cancelWatch(); return nil }
	for {
		mu.Lock()
		if ctx.Err() != nil {
			stopping = true
			stopAll("shutdown")
		}
		worker.Draining = stopping
		worker.Seq++
		reports := make([]Report, 0, len(locals))
		remaining := 0
		for _, t := range locals {
			r := Report{ID: t.task.ID, Epoch: t.task.Epoch, Phase: t.phase, Error: t.err}
			if a.ObservePartitions != nil {
				r.Partitions = a.ObservePartitions(t.task.ID)
			}
			if ps := t.partitions.Load(); ps != nil {
				r.Partitions = ps.([]string)
			}
			reports = append(reports, r)
			if t.phase != "stopped" {
				remaining++
			}
		}
		mu.Unlock()
		call, cancel := context.WithTimeout(observationCtx, 2*time.Second)
		started := time.Now()
		var tasks []Task
		e := client.Call(call, http.MethodPost, "/internal/heartbeat", Heartbeat{Worker: worker, Reports: reports}, &tasks)
		cancel()
		observer.Operation(observationCtx, "heartbeat_client", e == nil, time.Since(started))
		mu.Lock()
		if e != nil {
			failures++
			for _, t := range locals {
				t.admission.Store(false)
			}
		} else {
			failures = 0
			lastSuccess = time.Now()
			seen := map[string]bool{}
			for _, task := range tasks {
				seen[task.ID] = true
				t := locals[task.ID]
				if t != nil && t.task.Epoch != task.Epoch {
					if t.phase != "stopped" {
						stopTask(t, "revoked")
						continue
					}
					delete(locals, task.ID)
					t = nil
				}
				if t != nil {
					if task.Phase == "stopping" {
						stopTask(t, "revoked")
					} else if t.phase != "stopping" && t.phase != "stopped" {
						t.deadline = started.Add(time.Duration(task.RemainingMillis)*time.Millisecond - SafetyMargin)
					}
					t.task = task
					if t.phase == "running" {
						t.admission.Store(true)
					}
				}
				if t == nil && !stopping { // 只加载固定 Release，不在准备期创建 MQ Session。
					mu.Unlock()
					fetch, done := context.WithTimeout(observationCtx, 2*time.Second)
					var rel eventsource.Release
					err := client.Call(fetch, http.MethodGet, "/internal/releases/"+url.PathEscape(task.Source)+"/"+fmt.Sprint(task.Version)+"?task="+url.QueryEscape(task.ID), nil, &rel)
					done()
					mu.Lock()
					if err != nil {
						continue
					}
					rel.Spec.Version = rel.Version
					t = &localTask{task: task, spec: rel.Spec, phase: "prepared", deadline: started.Add(time.Duration(task.RemainingMillis)*time.Millisecond - SafetyMargin)}
					locals[task.ID] = t
				}
				if t != nil && t.phase == "prepared" && task.Phase == "starting" && !stopping && time.Now().Add(DrainTimeout+5*time.Second).Before(t.deadline) {
					work, cancelTask := context.WithCancel(consume.WithPartitionObserver(consume.WithAdmission(observationCtx, &t.admission), func(p []string) { t.partitions.Store(p) }))
					t.admission.Store(true)
					t.cancel = cancelTask
					t.done = make(chan struct{})
					observer.Transition(observationCtx, "worker", task.Role, t.phase, "running", "authorized", 0)
					t.phase = "running"
					taskCopy, specCopy := t.task, t.spec
					go func(local *localTask) {
						runStarted := time.Now()
						err := a.RunTask(work, taskCopy, specCopy)
						if err == nil && work.Err() == nil {
							err = fmt.Errorf("task exited unexpectedly")
						}
						mu.Lock()
						previousPhase := local.phase
						local.phase = "stopped"
						if errors.Is(err, consume.ErrStopIncomplete) {
							local.phase = "stopping"
							local.admission.Store(false)
							if local.stopAt.IsZero() {
								local.stopAt = time.Now()
							}
						}
						if err != nil {
							local.err = "task failed; inspect worker logs"
						}
						duration := time.Duration(0)
						if !local.stopAt.IsZero() {
							duration = time.Since(local.stopAt)
						}
						observer.Operation(observationCtx, "task_run", err == nil || (errors.Is(err, context.Canceled) && !errors.Is(err, consume.ErrStopIncomplete)), time.Since(runStarted))
						observer.Transition(observationCtx, "worker", local.task.Role, previousPhase, local.phase, "task_returned", duration)
						close(local.done)
						mu.Unlock()
					}(t)
				}
			}
			for key, t := range locals {
				if !seen[key] {
					if t.phase == "stopped" {
						delete(locals, key)
					} else {
						stopTask(t, "revoked")
					}
				}
			}
		}
		isStopping := stopping
		mu.Unlock()
		if isStopping && remaining == 0 {
			return finish()
		}
		timer := time.NewTimer(HeartbeatInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			time.Sleep(100 * time.Millisecond)
		case <-timer.C:
		}
	}
}
