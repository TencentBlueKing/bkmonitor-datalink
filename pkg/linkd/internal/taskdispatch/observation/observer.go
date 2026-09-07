// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package observation 定义调度协议与指标适配器之间的无配置依赖观测端口。
package observation

import (
	"context"
	"time"
)

// Observer 接收协议已提交的状态和本地执行观测；实现必须并发安全、非阻塞且不执行外部 I/O。
// 属性仅使用有限角色/状态/原因，不把会话、slot、版本、连接配置或错误文本作为指标标签。
type Observer interface {
	// Operation 记录有限操作名、成功标志与耗时，不接收错误原文。
	Operation(context.Context, string, bool, time.Duration)
	// Transition 的参数依次为 side、role、from、to、reason 和停止耗时。
	Transition(context.Context, string, string, string, string, string, time.Duration)
	ControllerSnapshot(context.Context, ControllerObservation)
	WorkerSnapshot(context.Context, WorkerObservation)
}

// WorkerObservation 是独立 watchdog 采样的进程状态，不包含任务身份或凭据。
type WorkerObservation struct {
	// Tasks 按角色与阶段计数。
	Tasks map[string]map[string]int64
	// Partitions 为活动 Cleaner 的最近分配数之和。
	Partitions int64
	// Paused 为暂停接收的活动任务数。
	Paused int64
	// Failures 为连续心跳失败次数。
	Failures int
	// HeartbeatAge 为最近成功心跳的年龄。
	HeartbeatAge time.Duration
	// AuthorizationRemaining 为活动任务最早的本地安全截止剩余时间。
	AuthorizationRemaining time.Duration
}

// ControllerObservation 是已提交状态的低基数汇总，不携带配置或业务身份。
type ControllerObservation struct {
	// Tasks 按角色与阶段计数。
	Tasks map[string]map[string]int64
	// Workers 按 healthy/stale/draining/cooldown 计数。
	Workers map[string]int64
	// Replicas 按角色与 matching/target/running/shortage 求和。
	Replicas map[string]map[string]int64
	// Metadata 按 ready/waiting/error 计数。
	Metadata map[string]int64
	// MetadataAge 为最老成功结果的年龄，无成功结果为 -1s。
	MetadataAge time.Duration
}
