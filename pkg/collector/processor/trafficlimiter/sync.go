// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trafficlimiter

import (
	"context"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	// 定时同步间隔。每次实际等待在 ±defaultSyncJitter 内随机，让各实例同步的先后顺序每轮都不同。
	defaultSyncInterval = 500 * time.Millisecond
	defaultSyncJitter   = 0.25

	// 单轮租约的 ctx 上限。等待补货的请求另有 defaultLeaseWaitBudget，不受它约束；
	// 放宽它是为了让「Lua 已在 Redis 执行但客户端超时」导致的授予丢失只在真正断网时发生。
	defaultSyncTimeout = time.Second

	// 距最近一次同步成功超过这么久，就不再信任共享额度，切到本地降级。
	defaultSyncStaleAfter = time.Second

	// 单次 EVAL 携带的服务数上限，服务很多时分批提交，避免一次脚本执行太久占住 Redis。
	defaultLeaseBatchSize = 256

	// 两次异步补货之间的最小间隔。等待中的请求触发的补货不受它约束。
	defaultAsyncRefillSpacing = 20 * time.Millisecond

	// 扫描需求下降的间隔，见 quotaManager.surplus。
	defaultSurplusCheckInterval = demandResponseWindow

	// 请求为一次补货最多等这么久，超时按 Redis 变慢处理：关闭等待、本次请求按本地降级判断。
	defaultLeaseWaitBudget = 50 * time.Millisecond

	// 关闭等待后，一轮成功的租约耗时不超过它才重新开启，与 defaultLeaseWaitBudget 之间留出迟滞，避免来回切换。
	defaultHealthyLatency = 25 * time.Millisecond

	// Redis 需求表中某个实例超过这么久没有上报，就视为已经离开，它的份额让给其他实例。
	defaultDemandStaleAfter = 2 * time.Second
)

// quotaLeaser 是共享额度的来源。
type quotaLeaser interface {
	// Lease 为一批服务申请或退还计费字节并上报需求，返回值与 requests 一一对应。
	// requests 为空时也要访问一次后端，用作健康探测。
	Lease(ctx context.Context, requests []leaseRequest) ([]leaseResult, error)
	Close() error
}

type quotaLeaserFactory func(RedisConfig) quotaLeaser

// leaseRound 是一轮租约，done 在这一轮结束后关闭；ok 表示这一轮全部成功，在 close 之前写入。
type leaseRound struct {
	done chan struct{}
	ok   bool
}

func newLeaseRound() *leaseRound {
	return &leaseRound{done: make(chan struct{})}
}

// quotaSyncer 是补货泵，生命周期与一个 Redis 客户端绑定：Reload 换客户端、Clean 释放客户端时一并停止。
//
// 它有三个来源：定时同步、余额低于水位时的异步补货、等待中的请求触发的紧急补货。
// 全部由同一个 goroutine 串行执行，同一时刻至多一个 Redis 调用在途；调用进行中到达的申请自动并入下一轮。
// 因此对 Redis 的调用次数以 1/RTT 为上限，与请求量和服务数无关。
type quotaSyncer struct {
	leaser   quotaLeaser
	quota    *quotaManager
	now      func() time.Time
	interval time.Duration

	mu        sync.Mutex
	scheduled bool
	urgent    bool
	pending   map[serviceKey]struct{}
	next      *leaseRound
	// lastRun 只由补货泵 goroutine 读写，用于异步补货的最小间隔。
	lastRun time.Time

	kick      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error

	// healthy 为假时请求不再等待、也不再异步补货，额度改由本地降级承担；定时同步继续作为探测。
	healthy atomic.Bool
	// lastSynced 是最近一次同步成功的时间（UnixNano，按 now 计）。
	// 同步卡在一次慢调用里时它会变旧，请求路径据此在 defaultSyncStaleAfter 后自行切到本地降级。
	lastSynced atomic.Int64
	// lastFailure 是最近一次失败的降级原因，本地降级下按它记录 decisions 指标。
	lastFailure atomic.Pointer[prometheus.Counter]
}

// newQuotaSyncer 创建并启动补货泵。interval 为 0 时不做定时同步，由 syncNow 手动驱动（测试使用）。
//
// 新客户端按健康处理：若从本地降级开始，每次 Reload 重建客户端都会让每个实例按本地满桶放行一次。
// Redis 若确实不可用，第一次调用失败就会切到本地降级。
func newQuotaSyncer(
	leaser quotaLeaser,
	quota *quotaManager,
	now func() time.Time,
	interval time.Duration,
) *quotaSyncer {
	s := &quotaSyncer{
		leaser:   leaser,
		quota:    quota,
		now:      now,
		interval: interval,
		pending:  make(map[serviceKey]struct{}),
		next:     newLeaseRound(),
		kick:     make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
	s.healthy.Store(true)
	s.lastSynced.Store(now().UnixNano())
	go s.run()
	return s
}

// shared 判断当前是否可以使用共享额度。
func (s *quotaSyncer) shared(now time.Time) bool {
	return s.healthy.Load() && now.UnixNano()-s.lastSynced.Load() <= defaultSyncStaleAfter.Nanoseconds()
}

// failureDecision 返回本地降级下应记的 decisions 计数。还没有失败记录却已经超时未同步，
// 说明同步卡在一次尚未返回的慢调用里，按超时记。
func (s *quotaSyncer) failureDecision() prometheus.Counter {
	if decision := s.lastFailure.Load(); decision != nil {
		return *decision
	}
	return decisionLocalTimeout
}

func (s *quotaSyncer) degrade(decision prometheus.Counter) {
	s.lastFailure.Store(&decision)
	s.healthy.Store(false)
}

// request 登记一批需要补货的服务并唤醒补货泵，返回会处理它们的那一轮。
func (s *quotaSyncer) request(keys []serviceKey, urgent bool) *leaseRound {
	s.mu.Lock()
	for _, key := range keys {
		s.pending[key] = struct{}{}
	}
	s.urgent = s.urgent || urgent
	round := s.next
	s.mu.Unlock()
	s.wake()
	return round
}

// await 为等待中的请求发起一次紧急补货，最多等 defaultLeaseWaitBudget。
// 返回 true 表示补货已经完成、可以按共享额度重新判断；false 表示应按本地降级判断。
func (s *quotaSyncer) await(keys []serviceKey) bool {
	round := s.request(keys, true)
	timer := time.NewTimer(defaultLeaseWaitBudget)
	defer timer.Stop()
	select {
	case <-round.done:
		return round.ok
	case <-timer.C:
		// 不等这一轮返回：后续请求立即改走本地降级，直到一轮足够快的租约成功。
		s.degrade(decisionLocalTimeout)
		leaseWaitTimeout.Inc()
		return false
	case <-s.done:
		return false
	}
}

// syncNow 立即执行一次定时同步并等它结束，供不启动定时同步的测试手动驱动。
func (s *quotaSyncer) syncNow() {
	s.mu.Lock()
	s.scheduled = true
	round := s.next
	s.mu.Unlock()
	s.wake()
	select {
	case <-round.done:
	case <-s.done:
	}
}

func (s *quotaSyncer) wake() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

func (s *quotaSyncer) run() {
	var schedule, surplus <-chan time.Time
	var timer *time.Timer
	if s.interval > 0 {
		timer = time.NewTimer(jitter(s.interval))
		defer timer.Stop()
		schedule = timer.C
		ticker := time.NewTicker(defaultSurplusCheckInterval)
		defer ticker.Stop()
		surplus = ticker.C
	}
	for {
		select {
		case <-s.done:
			return
		case <-schedule:
			s.mu.Lock()
			s.scheduled = true
			s.mu.Unlock()
			timer.Reset(jitter(s.interval))
		case <-surplus:
			s.checkSurplus()
		case <-s.kick:
		}
		s.drain()
	}
}

// checkSurplus 把需求已经下降的服务登记为异步补货，由 prepare 生成退还申请。
// 只在共享模式下进行：本地降级时余额不来自 Redis，没有可退还的额度。
func (s *quotaSyncer) checkSurplus() {
	now := s.now()
	if !s.shared(now) {
		return
	}
	if keys := s.quota.surplus(now); len(keys) > 0 {
		s.mu.Lock()
		for _, key := range keys {
			s.pending[key] = struct{}{}
		}
		s.mu.Unlock()
	}
}

// jitter 让每次定时同步的间隔在 ±defaultSyncJitter 内随机。只在首次加偏移的话，
// 各实例之后的相位就固定了，额度紧张时同一个实例会一直排在最后。
func jitter(interval time.Duration) time.Duration {
	spread := float64(interval) * defaultSyncJitter
	return interval + time.Duration((rand.Float64()*2-1)*spread)
}

// drain 连续执行租约直到没有待处理的申请。只有异步补货的一轮遵守最小间隔，紧急补货与定时同步立即执行。
func (s *quotaSyncer) drain() {
	for {
		s.mu.Lock()
		if !s.scheduled && len(s.pending) == 0 {
			s.mu.Unlock()
			return
		}
		if !s.scheduled && !s.urgent {
			if wait := defaultAsyncRefillSpacing - time.Since(s.lastRun); wait > 0 {
				s.mu.Unlock()
				timer := time.NewTimer(wait)
				select {
				case <-s.done:
					timer.Stop()
					return
				case <-s.kick:
					timer.Stop()
				case <-timer.C:
				}
				continue
			}
		}
		scheduled, keys, round := s.scheduled, s.pending, s.next
		s.scheduled, s.urgent = false, false
		s.pending, s.next = make(map[serviceKey]struct{}), newLeaseRound()
		s.mu.Unlock()

		s.lastRun = time.Now()
		round.ok = s.runRound(scheduled, keys)
		close(round.done)
	}
}

// runRound 执行一轮租约：生成申请、分批提交、写回结果，并按结果与耗时更新健康状态。
func (s *quotaSyncer) runRound(scheduled bool, keys map[serviceKey]struct{}) bool {
	now := s.now()
	healthy := s.healthy.Load()
	requests := s.quota.prepare(now, scheduled, keys, healthy)
	if len(requests) == 0 && healthy {
		// 没有需要申请的服务时不访问 Redis：平台可能给所有 Collector 下发 Redis 而额度为 0，
		// 空闲实例每个周期一次空调用，乘上实例数就是对 Redis 的无谓压力。
		// 不健康时仍要访问一次，作为恢复的探测。
		if scheduled {
			s.lastSynced.Store(now.UnixNano())
		}
		return true
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultSyncTimeout)
	defer cancel()
	start := time.Now()
	for offset := 0; offset == 0 || offset < len(requests); offset += defaultLeaseBatchSize {
		batch := requests[offset:min(offset+defaultLeaseBatchSize, len(requests))]
		results, err := s.leaser.Lease(ctx, batch)
		if err == nil && len(results) != len(batch) {
			err = errors.Errorf("traffic limiter lease returned %d results for %d requests", len(results), len(batch))
		}
		if err != nil {
			// 之前批次的结果已经写回，只撤销尚未成功的部分。
			s.quota.abort(requests[offset:])
			s.degrade(fallbackDecision(err))
			syncFailed.Inc()
			warnRedisFailure(err)
			return false
		}
		s.quota.apply(s.now(), batch, results)
	}

	latency := time.Since(start)
	s.lastSynced.Store(now.UnixNano())
	syncSucceeded.Inc()
	switch {
	case latency > defaultLeaseWaitBudget:
		// Redis 能用但太慢：请求等不起，改由本地降级承担，定时同步继续上报需求并探测。
		s.degrade(decisionLocalTimeout)
	case latency <= defaultHealthyLatency:
		s.healthy.Store(true)
	}
	return true
}

// Close 可能被重复调用（Reload 关闭旧客户端后 Clean 仍持有同一快照），只执行一次。
// 不等待在途的租约返回：关闭客户端会让它尽快失败，迟到写回的授予本身也是合法额度。
func (s *quotaSyncer) Close() error {
	s.closeOnce.Do(func() {
		close(s.done)
		s.closeErr = s.leaser.Close()
	})
	return s.closeErr
}
