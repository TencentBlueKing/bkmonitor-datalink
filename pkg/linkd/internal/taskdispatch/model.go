// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package taskdispatch 实现跨角色共享的单中心调度、执行授权与有界失联自停。
package taskdispatch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"linkd/internal/config"
)

const (
	// HeartbeatInterval 也限制配置变化丢通知时的发现延迟。
	HeartbeatInterval = 3 * time.Second
	// LeaseTTL 是服务端确认的执行授权窗口。
	LeaseTTL = 60 * time.Second
	// SafetyMargin 避免网络延迟造成提前接管。
	SafetyMargin = 10 * time.Second
	// DrainTimeout 限制正常停止排空。
	DrainTimeout = 20 * time.Second
)

// Worker 是进程会话；all-in-one 注册两种角色而不是两个相同进程。
type Worker struct {
	MaxConcurrency   int               `json:"max_concurrency"`
	MaxInflightBytes int64             `json:"max_inflight_bytes"`
	StableAfter      time.Time         `json:"stable_after"`
	CooldownUntil    time.Time         `json:"cooldown_until"`
	Seq              int64             `json:"seq"`
	ID               string            `json:"id"`
	Roles            []string          `json:"roles"`
	Labels           map[string]string `json:"labels"`
	Explicit         bool              `json:"require_explicit_selector"`
	MaxTasks         int               `json:"max_tasks"`
	Seen             time.Time         `json:"seen"`
	Draining         bool              `json:"draining"`
}

// Task 的稳定 slot 可迁移，代次不能复用；Stopping 仍占用 worker/source/role。
type Task struct {
	// StoppingAt 是中心提交停止请求的时间，用于跨中心重启测量停止确认耗时。
	StoppingAt      time.Time `json:"stopping_at,omitempty"`
	Concurrency     int       `json:"concurrency"`
	InflightBytes   int64     `json:"inflight_bytes"`
	RetryAfter      time.Time `json:"retry_after"`
	Failures        int       `json:"failures"`
	RemainingMillis int64     `json:"remaining_millis"`
	ID              string    `json:"id"`
	Source          string    `json:"source"`
	Role            string    `json:"role"`
	Slot            int       `json:"slot"`
	Epoch           int64     `json:"epoch"`
	Worker          string    `json:"worker"`
	Version         int64     `json:"version"`
	Digest          string    `json:"digest"`
	Phase           string    `json:"phase"`
	Expires         time.Time `json:"expires"`
	Retired         []string  `json:"retired,omitempty"`
	Error           string    `json:"error,omitempty"`
	Partitions      []string  `json:"partitions,omitempty"`
}

// Metadata 保存完整 topic 身份和最后已知分片数；错误禁止扩容但不清空旧任务。
type Metadata struct {
	Digest     string    `json:"digest"`
	TopicID    string    `json:"topic_id"`
	Partitions int       `json:"partitions"`
	Success    time.Time `json:"success"`
	Attempt    time.Time `json:"attempt"`
	Error      string    `json:"error"`
}

// Status 区分匹配数量、元数据上限、目标与实际启动数量。
type Status struct {
	Source   string    `json:"source"`
	Role     string    `json:"role"`
	Matching int       `json:"matching"`
	Target   int       `json:"target"`
	Running  int       `json:"running"`
	Reason   string    `json:"reason,omitempty"`
	Metadata *Metadata `json:"metadata,omitempty"`
}

// State 是 Redis 的有界协调快照，丢失后必须显式恢复。
type State struct {
	Workers   map[string]Worker   `json:"workers"`
	Tasks     map[string]Task     `json:"tasks"`
	Metadata  map[string]Metadata `json:"metadata"`
	Statuses  []Status            `json:"statuses"`
	NextEpoch int64               `json:"next_epoch"`
}

func newState() State {
	return State{Workers: map[string]Worker{}, Tasks: map[string]Task{}, Metadata: map[string]Metadata{}}
}

// ConsumerName 绑定任务和执行代次，用于仅接管已经退休的 consumer。
func ConsumerName(t Task) string { return fmt.Sprintf("linkd-%s-%d", t.ID, t.Epoch) }

func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func executionDigest(s config.EventSource) string {
	s.Scheduling = config.SourceScheduling{}
	s.Enabled = true
	return digest(s)
}

func taskID(source, role string, slot int) string { return fmt.Sprintf("%s:%s:%d", source, role, slot) }

func placement(s config.EventSource, role string) config.Placement {
	if role == "cleaner" {
		return s.Scheduling.Cleaner
	}
	return s.Scheduling.Lifecycle
}

func matches(w Worker, s config.EventSource, role string) bool {
	return slices.Contains(w.Roles, role) && placement(s, role).Matches(w.Labels, w.Explicit)
}
