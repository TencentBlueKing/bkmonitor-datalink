// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consume

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

var (
	// ErrSessionClosed 表示当前 Broker Session 已关闭，Receipt 也随之失效。
	ErrSessionClosed = errors.New("message consumption session is closed")
	// ErrReceiveLimitExceeded 表示适配器返回的数据超过运行时本次允许接管的上限。
	ErrReceiveLimitExceeded = errors.New("message receive limit exceeded")
	// ErrInvalidDelivery 表示适配器返回的投递缺少稳定身份、租户或传输位置。
	ErrInvalidDelivery = errors.New("invalid message delivery")
)

// SettlementMode 描述 Broker 的确认进度是逐条独立还是按 lane 累计推进。
type SettlementMode uint8

const (
	// SettlementIndividual 表示每个 Receipt 可以彼此独立确认。
	SettlementIndividual SettlementMode = iota
	// SettlementCumulative 表示只能确认同一 lane 中连续完成的前缀。
	SettlementCumulative
)

// Capabilities 描述一个 Session 对核心运行时暴露的可靠性能力。
type Capabilities struct {
	Settlement   SettlementMode
	CanPauseLane bool
}

// ReceiveLimits 是运行时本次允许 Session 接管的消息与载荷上限。
type ReceiveLimits struct {
	MaxMessages int
	MaxBytes    int
}

// Message 是 Handler 可见的、与 Broker 协议无关的业务消息。
type Message struct {
	ID         string
	TenantID   string
	OrderKey   string
	Body       []byte
	Headers    map[string][]byte
	EnqueuedAt time.Time
}

// DeliveryMeta 保存只用于调度、确认与诊断的传输元数据。
type DeliveryMeta struct {
	Transport string
	Lane      string
	Position  string
	Attempt   int
	Redeliver bool
}

// Delivery 表示 Broker 在当前 Session 中交付的一次投递。
type Delivery struct {
	Message Message
	Receipt Receipt
	Meta    DeliveryMeta
}

// Receipt 是 Session 作用域内的不透明确认凭据。其字段不可序列化，也不提供
// 原生 delivery tag、offset 或 Stream ID 的读取能力。
type Receipt struct {
	issuer *ReceiptIssuer
	token  uint64
}

// Valid 报告 Receipt 是否由一个活跃的 issuer 创建。
func (r Receipt) Valid() bool {
	return r.issuer != nil && r.token != 0
}

// ReceiptIssuer 为一个适配器 Session 发行并校验 Receipt。
// 每个 Session 必须创建独立实例，关闭后不得复用。
type ReceiptIssuer struct {
	next atomic.Uint64
}

// NewReceiptIssuer 创建 Session 作用域的 Receipt 发行器。
func NewReceiptIssuer() *ReceiptIssuer {
	return &ReceiptIssuer{}
}

// Issue 创建一个 Receipt，并返回仅供适配器本地索引原生凭据的 token。
func (i *ReceiptIssuer) Issue() (Receipt, uint64) {
	token := i.next.Add(1)
	return Receipt{issuer: i, token: token}, token
}

// Resolve 校验 Receipt 是否属于当前 Session，并返回本地 token。
func (i *ReceiptIssuer) Resolve(receipt Receipt) (uint64, bool) {
	if i == nil || receipt.issuer != i || receipt.token == 0 {
		return 0, false
	}
	return receipt.token, true
}

// Session 是消息队列适配器向核心运行时提供的最小端口。
type Session interface {
	Capabilities() Capabilities
	Receive(ctx context.Context, limits ReceiveLimits) ([]Delivery, error)
	Confirm(ctx context.Context, receipts []Receipt) error
	Close(ctx context.Context) error
}

// LanePauser 是支持 lane 级暂停的 Session 可选实现。
//
// Runtime 在暂停后会返回错误并关闭 Session，不会在同一所有权代际内恢复 lane。这样重试
// 耗尽或 Handler panic 不会把进程留在不可观测的永久阻塞状态，未确认消息仍由 Broker 重投。
type LanePauser interface {
	Pause(ctx context.Context, lane string) error
}

// LaneController 允许支持分片背压的 Session 暂停并恢复单个 lane。
type LaneController interface {
	Pause(ctx context.Context, lane string) error
	Resume(ctx context.Context, lane string) error
}

// FlowController 允许支持消费端背压的 Session 暂停和恢复整个 Flow 的数据 fetch。
// 暂停后 Runtime 仍会调用 Receive，使 Consumer Group 可以维持 poll 和 rebalance 协议。
type FlowController interface {
	PauseFlow(ctx context.Context) error
	ResumeFlow(ctx context.Context) error
}

type OwnershipEventKind uint8

const (
	OwnershipAssigned OwnershipEventKind = iota
	OwnershipRevoked
	OwnershipLost
)

// OwnershipEvent 描述一次 lane 所有权变化。处理方完成必要的接管或排空后必须调用 Complete。
type OwnershipEvent struct {
	Kind     OwnershipEventKind
	Lanes    []string
	Complete func()
}

// OwnershipSession 允许 Runtime 在接管 poll 结果后放行所有权变化，并响应 revoke/lost。
type OwnershipSession interface {
	OwnershipEvents() <-chan OwnershipEvent
	AllowOwnershipChanges()
}

// RuntimeValidator 允许适配器校验其 Broker 租约与运行时处理预算等跨边界约束。
type RuntimeValidator interface {
	ValidateRuntime(config Config) error
}

// Handler 处理单条业务消息并返回结构化结果。它不接收 Delivery 或 Receipt，
// 因而不能越过运行时直接确认消息。
type Handler interface {
	Handle(ctx context.Context, message Message) Outcome
}

// HandlerFunc 让普通函数实现 Handler。
type HandlerFunc func(ctx context.Context, message Message) Outcome

// Handle 调用 f。
func (f HandlerFunc) Handle(ctx context.Context, message Message) Outcome {
	return f(ctx, message)
}

// OutcomeKind 描述一次 Handler 调用的处理结果。
type OutcomeKind uint8

const (
	// OutcomeComplete 表示业务结果已经可靠落地，可以确认消息。
	OutcomeComplete OutcomeKind = iota
	// OutcomeRetry 表示发生暂时错误，应在当前进程内延迟重试。
	OutcomeRetry
	// OutcomeDiscard 表示确定性错误已形成诊断且产品规则允许跳过，可以确认消息。
	OutcomeDiscard
	// OutcomeBlock 表示不能确认当前消息；Runtime 会暂停可暂停的 lane 并以错误退出。
	OutcomeBlock
	// OutcomeDefer 表示当前没有失败，但应释放 worker 后延迟再次调度；不消耗普通重试预算。
	OutcomeDefer
)

// Outcome 是 Handler 返回给运行时的结构化处理结果。
type Outcome struct {
	Kind       OutcomeKind
	RetryAfter time.Duration
	Err        error
}

// Complete 创建可确认的成功结果。
func Complete() Outcome {
	return Outcome{Kind: OutcomeComplete}
}

// Retry 创建暂时失败结果；after 为零时由运行时计算退避。
func Retry(err error, after time.Duration) Outcome {
	return Outcome{Kind: OutcomeRetry, RetryAfter: after, Err: err}
}

// Discard 创建已经可靠记录且允许跳过的确定性失败结果。
func Discard(err error) Outcome {
	return Outcome{Kind: OutcomeDiscard, Err: err}
}

// Block 创建必须保留未确认状态并终止当前 Runtime 所有权代际的结果。
func Block(err error) Outcome {
	return Outcome{Kind: OutcomeBlock, Err: err}
}

// Defer 创建不消耗 Retry 次数和累计时间预算的让出结果。
func Defer(after time.Duration) Outcome {
	return Outcome{Kind: OutcomeDefer, RetryAfter: after}
}

func (o Outcome) validate() error {
	if o.RetryAfter < 0 {
		return fmt.Errorf("retry_after must not be negative: %s", o.RetryAfter)
	}
	switch o.Kind {
	case OutcomeComplete, OutcomeRetry, OutcomeDiscard, OutcomeBlock, OutcomeDefer:
		return nil
	default:
		return fmt.Errorf("unknown outcome kind: %d", o.Kind)
	}
}
