// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consume

import (
	"context"
	"time"
)

// RuntimeLabels 是消费运行时允许进入指标属性的封闭标签。
// Stage 和 Transport 必须由进程装配提供，不能从消息或动态 topic 推导。
type RuntimeLabels struct {
	Stage                  string
	Transport              string
	EventSourceID          string
	RecordPipelineAttempts bool
}

// DeliveryObservation 描述 Runtime 已经接管的一条投递，不包含业务身份或 payload。
type DeliveryObservation struct {
	Lane        string
	Bytes       int
	Redelivered bool
}

// SettlementObservation 描述一次 Broker 确认尝试及其实际消息数量。
type SettlementObservation struct {
	Mode      SettlementMode
	Lane      string
	Messages  int
	Succeeded bool
	Duration  time.Duration
}

// StepObservation 描述一个业务步骤批次的低基数结果。
type StepObservation struct {
	Step     string
	Outcome  string
	Items    int
	Duration time.Duration
}

// LaneSnapshot 是一个当前活跃 lane 的有界运行状态。
type LaneSnapshot struct {
	Lane             string
	InflightMessages int
	InflightBytes    int
	Paused           bool
	Owned            bool
}

// OwnershipObservation 描述 lane 所有权的一次变化。
type OwnershipObservation struct {
	Kind  OwnershipEventKind
	Lanes []string
}

// RuntimeSnapshot 是运行时在单一状态循环中产生的低基数瞬时状态。
type RuntimeSnapshot struct {
	InflightMessages       int
	InflightBytes          int
	RetryItems             int
	RetryOldestAge         time.Duration
	SettlementGap          int
	SettlementGapOldestAge time.Duration
	Lanes                  []LaneSnapshot
}

// Observer 接收消息运行时的可观察状态。实现不得阻塞、返回错误或改变消息确认语义。
type Observer interface {
	DeliveryReceived(ctx context.Context, observation DeliveryObservation)
	HandlerStarted(ctx context.Context, message Message)
	HandlerFinished(ctx context.Context, outcome OutcomeKind, duration time.Duration)
	RetryScheduled(ctx context.Context)
	StepFinished(ctx context.Context, observation StepObservation)
	SettlementFinished(ctx context.Context, observation SettlementObservation)
	FlowTransition(ctx context.Context, action string)
	OwnershipChanged(ctx context.Context, observation OwnershipObservation)
	Snapshot(ctx context.Context, snapshot RuntimeSnapshot)
	ShutdownFinished(ctx context.Context, succeeded bool, duration time.Duration, remaining int)
}

type noopObserver struct{}

func (noopObserver) DeliveryReceived(context.Context, DeliveryObservation) {}

func (noopObserver) HandlerStarted(context.Context, Message) {}

func (noopObserver) HandlerFinished(context.Context, OutcomeKind, time.Duration) {}

func (noopObserver) RetryScheduled(context.Context) {}

func (noopObserver) StepFinished(context.Context, StepObservation) {}

func (noopObserver) SettlementFinished(context.Context, SettlementObservation) {}

func (noopObserver) FlowTransition(context.Context, string) {}

func (noopObserver) OwnershipChanged(context.Context, OwnershipObservation) {}

func (noopObserver) Snapshot(context.Context, RuntimeSnapshot) {}

func (noopObserver) ShutdownFinished(context.Context, bool, time.Duration, int) {}

// RuntimeOption 配置 Runtime 的非业务观察能力。
type RuntimeOption func(*Runtime)

// WithObserver 为 Runtime 注入非阻塞观察器及其封闭标签。
func WithObserver(labels RuntimeLabels, observer Observer) RuntimeOption {
	return func(runtime *Runtime) {
		if observer == nil {
			return
		}
		runtime.labels = labels
		runtime.observer = observer
		runtime.observed = true
	}
}
