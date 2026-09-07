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
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/eventsource"
)

const commitScript = `if redis.call('GET',KEYS[1])~=ARGV[1] then return -1 end
if redis.call('GET',KEYS[2])~=ARGV[2] then return 0 end
redis.call('SET',KEYS[2],ARGV[3]); return 1`

// Controller 串行规划与协议转移，Redis CAS 同时检查中心身份和完整旧快照。
type Controller struct {
	CleanerDefaults    config.CleanerRuntimeConfig
	LifecycleDefaults  config.LifecycleConfig
	client             *redis.Client
	sources            *eventsource.Service
	key, leader, token string
	mu                 sync.Mutex
	observer           Observer
}

// NewController 取得单活动中心资格；已有协调状态不被覆盖。
func NewController(ctx context.Context, c *redis.Client, s *eventsource.Service, namespace string, observers ...Observer) (*Controller, error) {
	x := &Controller{observer: observerOrNoop(observers...), client: c, sources: s, key: "linkd:dispatch:{" + namespace + "}:state", leader: "linkd:dispatch:{" + namespace + "}:leader", token: uuid.NewString()}
	ok, e := c.SetNX(ctx, x.leader, x.token, LeaseTTL).Result()
	if e != nil {
		return nil, e
	}
	if !ok {
		return nil, fmt.Errorf("another scheduler is active; wait for its authorization to expire")
	}
	ready := false
	defer func() {
		if !ready {
			cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = c.Eval(cleanup, `if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) else return 0 end`, []string{x.leader}, x.token).Err()
		}
	}()
	exists, e := c.Exists(ctx, x.key).Result()
	if e != nil {
		return nil, e
	}
	if exists == 0 {
		records, e := s.List(ctx, "", 1)
		if e != nil {
			return nil, e
		}
		if len(records) > 0 {
			x.observer.Operation(ctx, "recovery_required", false, 0)
			return nil, fmt.Errorf("coordination history missing: stop old workers and explicitly initialize scheduling")
		}
		b, _ := json.Marshal(newState())
		if e = c.SetNX(ctx, x.key, b, 0).Err(); e != nil {
			return nil, e
		}
	}
	ready = true
	return x, nil
}

func (c *Controller) update(ctx context.Context, fn func(*State) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, e := c.client.Get(ctx, c.key).Bytes()
	if e != nil {
		return fmt.Errorf("read coordination state: %w", e)
	}
	var st State
	if e = json.Unmarshal(b, &st); e != nil {
		return e
	}
	if st.Workers == nil || st.Tasks == nil || st.Metadata == nil {
		return fmt.Errorf("coordination state incomplete")
	}
	previous := make(map[string]Task, len(st.Tasks))
	for id, task := range st.Tasks {
		previous[id] = task
	}
	if e = fn(&st); e != nil {
		return e
	}
	now := time.Now()
	for id, task := range st.Tasks {
		if task.Phase == "stopping" && previous[id].Phase != "stopping" {
			task.StoppingAt = now
			st.Tasks[id] = task
		}
	}
	next, e := json.Marshal(st)
	if e != nil {
		return e
	}
	if len(next) > 8<<20 {
		return fmt.Errorf("coordination state exceeds 8 MiB")
	}
	n, e := c.client.Eval(ctx, commitScript, []string{c.leader, c.key}, c.token, string(b), string(next)).Int()
	if e != nil {
		return e
	}
	if n != 1 {
		return fmt.Errorf("scheduler authority or state changed")
	}
	// 只在 Redis CAS 成功后计数；失败写入不能制造已经交接的观测。
	for id, task := range st.Tasks {
		old := previous[id]
		// 同一次对账可能先超时终结旧代次，再复用 slot。最终快照只留下新代次，
		// 必须在提交成功后补记旧代次终态，不能让强制接管消失在 assigned 指标中。
		if old.Epoch > 0 && old.Epoch != task.Epoch && old.Phase != "stopped" && !now.Before(old.Expires.Add(SafetyMargin)) {
			duration := time.Duration(0)
			if !old.StoppingAt.IsZero() {
				duration = now.Sub(old.StoppingAt)
			}
			c.observer.Transition(ctx, "controller", old.Role, old.Phase, "stopped", "authorization_expired", duration)
			old.Phase = "stopped"
		}
		if old.Phase != task.Phase || old.Epoch != task.Epoch {
			reason := "reported"
			if task.Phase == "preparing" {
				reason = "assigned"
			}
			if task.Phase == "stopping" {
				reason = "revoked"
			}
			if task.Error == "authorization expired" {
				reason = "authorization_expired"
			}
			duration := time.Duration(0)
			if task.Phase == "stopped" && !old.StoppingAt.IsZero() {
				duration = now.Sub(old.StoppingAt)
			}
			c.observer.Transition(ctx, "controller", task.Role, old.Phase, task.Phase, reason, duration)
		}
	}
	c.observer.ControllerSnapshot(ctx, controllerObservation(st, now))
	return nil
}

// Snapshot 返回运行诊断，不包含 Kafka 凭据。
func (c *Controller) Snapshot(ctx context.Context) (State, error) {
	var s State
	b, e := c.client.Get(ctx, c.key).Bytes()
	if e == nil {
		e = json.Unmarshal(b, &s)
	}
	return s, e
}

// Report 描述本次会话观察的确切执行代次。
type Report struct {
	ID         string   `json:"id"`
	Epoch      int64    `json:"epoch"`
	Phase      string   `json:"phase"`
	Error      string   `json:"error,omitempty"`
	Partitions []string `json:"partitions,omitempty"`
}

// Heartbeat 携带原会话及各任务报告；新会话不得冒认旧会话任务。
type Heartbeat struct {
	Worker  Worker   `json:"worker"`
	Reports []Report `json:"reports"`
}

// Assignment 是当前会话获得的命令；配置单独按 Release 加载。
type Assignment struct {
	Task        Task  `json:"task"`
	SpecVersion int64 `json:"spec_version"`
}

// Beat 原子确认停止，再发送当前会话的任务快照；停止代次不可续租复活。
func (c *Controller) Beat(ctx context.Context, h Heartbeat) (_ []Task, runErr error) {
	started := time.Now()
	defer func() { c.observer.Operation(ctx, "heartbeat_server", runErr == nil, time.Since(started)) }()
	var tasks []Task
	if h.Worker.ID == "" || len(h.Worker.ID) > 128 || h.Worker.MaxTasks < 1 || h.Worker.MaxTasks > 256 || len(h.Reports) > 256 {
		return nil, fmt.Errorf("invalid worker registration")
	}
	if len(h.Worker.Roles) < 1 || len(h.Worker.Roles) > 2 {
		return nil, fmt.Errorf("invalid worker roles")
	}
	for _, r := range h.Worker.Roles {
		if r != "cleaner" && r != "lifecycle" {
			return nil, fmt.Errorf("unknown worker role")
		}
	}
	now := time.Now()
	e := c.update(ctx, func(s *State) error {
		if len(s.Workers) >= 256 {
			if _, exists := s.Workers[h.Worker.ID]; !exists {
				return fmt.Errorf("worker capacity exceeded")
			}
		}
		w := h.Worker
		if old, ok := s.Workers[w.ID]; ok && w.Seq <= old.Seq {
			return fmt.Errorf("stale heartbeat sequence")
		}
		if old, ok := s.Workers[w.ID]; ok {
			w.StableAfter = old.StableAfter
			w.CooldownUntil = old.CooldownUntil
			if now.Sub(old.Seen) > 10*time.Second {
				w.StableAfter = now.Add(15 * time.Second)
			}
		}
		w.Seen = now
		s.Workers[w.ID] = w
		for _, r := range h.Reports {
			t, ok := s.Tasks[r.ID]
			if !ok || t.Worker != w.ID || t.Epoch != r.Epoch {
				continue
			}
			if r.Phase == "stopped" && t.Phase != "stopped" {
				t.Phase = "stopped"
				t.Retired = append(t.Retired, ConsumerName(t))
				t.Error = r.Error
				if r.Error != "" {
					t.Failures++
					t.RetryAfter = now.Add(time.Duration(min(60, 5*t.Failures)) * time.Second)
				}
				s.Tasks[t.ID] = t
				continue
			}
			if t.Phase == "stopped" {
				continue
			}
			if r.Phase == "stopping" || w.Draining {
				t.Phase = "stopping"
			}
			if !now.Before(t.Expires) {
				t.Phase = "stopping"
			}
			if t.Phase != "stopping" {
				if r.Phase == "prepared" && t.Phase == "preparing" {
					t.Phase = "starting"
				}
				if r.Phase == "running" && t.Phase == "starting" {
					t.Phase = "running"
				}
				t.Expires = now.Add(LeaseTTL)
			}
			t.Partitions = r.Partitions
			s.Tasks[t.ID] = t
		}
		for _, t := range s.Tasks {
			if t.Worker == w.ID && t.Phase != "stopped" {
				t.RemainingMillis = max(0, t.Expires.Sub(now).Milliseconds())
				tasks = append(tasks, t)
			}
		}
		return nil
	})
	return tasks, e
}

func (c *Controller) releases(ctx context.Context) ([]eventsource.Release, error) {
	var all []eventsource.Release
	after := ""
	for {
		rs, e := c.sources.List(ctx, after, 100)
		if e != nil {
			return nil, e
		}
		for _, r := range rs {
			if r.Pending != nil {
				r, e = c.sources.Recover(ctx, r.ID)
				if e != nil {
					return nil, e
				}
			}
			if r.Published > 0 {
				rel, e := c.sources.GetRelease(ctx, r.ID, r.Published)
				if e != nil {
					return nil, e
				}
				all = append(all, rel)
			}
			after = r.ID
		}
		if len(rs) < 100 {
			break
		}
		if len(all) >= 10000 {
			return nil, fmt.Errorf("source capacity exceeds 10000")
		}
	}
	return all, nil
}

// Run 周期刷新 Kafka 元数据并重新规划，不因单来源探测错误停止整个控制面。
func (c *Controller) Run(ctx context.Context) error {
	// 正常发布释放中心资格，但保留完整任务/授权历史；新中心可恢复现有会话。
	// 崩溃没有机会释放时仍由 TTL 兜底，比较 token 避免删除继任者。
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = c.client.Eval(cleanup, `if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) else return 0 end`, []string{c.leader}, c.token).Err()
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if e := c.tick(ctx); e != nil {
			return e
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (c *Controller) tick(ctx context.Context) (runErr error) {
	started := time.Now()
	defer func() { c.observer.Operation(ctx, "reconcile", runErr == nil, time.Since(started)) }()
	call, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	n, e := c.client.Eval(call, `if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('PEXPIRE',KEYS[1],ARGV[2]) else return 0 end`, []string{c.leader}, c.token, LeaseTTL.Milliseconds()).Int()
	if e != nil || n != 1 {
		return fmt.Errorf("scheduler lease lost: %w", errors.Join(e, context.Canceled))
	}
	releases, e := c.releases(call)
	if e != nil {
		return e
	}
	snapshot, e := c.Snapshot(call)
	if e != nil {
		return e
	}
	type result struct {
		id string
		m  Metadata
	}
	results := make(chan result, 4)
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	now := time.Now()
	// 限制每轮至四个探测，避免大量来源阻塞续租。优先检查从未探测或最久未探测的来源，避免大清单头部饿死尾部。
	count := 0
	probes := append([]eventsource.Release(nil), releases...)
	sort.SliceStable(probes, func(i, j int) bool {
		a, b := snapshot.Metadata[probes[i].ID], snapshot.Metadata[probes[j].ID]
		if a.Digest != digest(probes[i].Spec.Storage) {
			a.Attempt = time.Time{}
		}
		if b.Digest != digest(probes[j].Spec.Storage) {
			b.Attempt = time.Time{}
		}
		return a.Attempt.Before(b.Attempt)
	})
	for _, rel := range probes {
		previous := snapshot.Metadata[rel.ID]
		if previous.Digest == digest(rel.Spec.Storage) && now.Sub(previous.Attempt) < 30*time.Second {
			continue
		}
		if count == 4 {
			break
		}
		count++
		slots <- struct{}{}
		wg.Add(1)
		go func(r eventsource.Release, p Metadata) {
			defer wg.Done()
			defer func() { <-slots }()
			q, stop := context.WithTimeout(ctx, 5*time.Second)
			defer stop()
			started := time.Now()
			id, num, err := Probe(q, r.Spec)
			m := UpdateMetadata(p, r.Spec, id, num, err, now)
			c.observer.Operation(ctx, "kafka_probe", m.Error == "", time.Since(started))
			results <- result{r.ID, m}
		}(rel, previous)
	}
	wg.Wait()
	close(results)
	updates := map[string]Metadata{}
	for r := range results {
		updates[r.id] = r.m
	}
	write, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	return c.update(write, func(s *State) error {
		for id, m := range updates {
			s.Metadata[id] = m
		}
		reconcileBudgets(s, releases, time.Now(), c.CleanerDefaults.WithDefaults(), c.LifecycleDefaults.WithDefaults())
		return nil
	})
}

// InitializeMissingState 仅用于部署方确认旧 worker/中心均已停止后的缺失状态初始化。
// 已存在的协调记录不覆盖；本函数不删除来源配置或业务数据。
func InitializeMissingState(ctx context.Context, client *redis.Client, namespace string) error {
	base := "linkd:dispatch:{" + namespace + "}:"
	data, err := json.Marshal(newState())
	if err != nil {
		return err
	}
	n, err := client.Eval(ctx, `if redis.call('EXISTS',KEYS[1])==1 or redis.call('EXISTS',KEYS[2])==1 then return 0 end redis.call('SET',KEYS[2],ARGV[1]); return 1`, []string{base + "leader", base + "state"}, string(data)).Int()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("scheduler is active or coordination state already exists")
	}
	return nil
}
