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
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	// 本地余额和指标活跃表都按 {token, service} 哈希到这么多分片。
	// 只降低不同服务抢同一把锁的概率，不是额度分片，也不改变每个服务一份余额的语义。
	stateShardCount = 64

	// 某个 {token, service} 超过这段时间没有任何请求后，丢掉本地余额和 Prometheus label。
	// 回收的是本进程内存和指标基数，Redis Key 靠 Lua 的动态 PX 过期，不看这个值。
	defaultStateIdleTTL = 30 * time.Minute

	// 机会式扫描间隔：只有间隔到达的那个请求会遍历全部分片。
	// 不启动常驻清理 goroutine，避免空闲 Collector 也一直扫表。
	defaultStateSweepInterval = 5 * time.Minute

	// minQuotaBalance 是透支的下限。单个请求的计费字节可能接近 MaxInt64，
	// 限住下限后取反不会溢出；欠账再大也只是按速率慢慢还清，语义不变。
	minQuotaBalance = math.MinInt64 / 2
)

type limitDecision struct {
	// accepted 表示请求中至少有一个服务成功扣减或处于不限流状态。
	accepted bool
	rejected []string
}

type serviceKey struct {
	token   string
	service string
}

func shardIndex(token, service string) uint64 {
	const (
		offset64 = uint64(14695981039346656037)
		prime64  = uint64(1099511628211)
	)
	hash := offset64
	for i := 0; i < len(token); i++ {
		hash ^= uint64(token[i])
		hash *= prime64
	}
	hash ^= 0xff
	hash *= prime64
	for i := 0; i < len(service); i++ {
		hash ^= uint64(service[i])
		hash *= prime64
	}
	return hash % stateShardCount
}

// quotaBucket 是单个 {token, service} 在本实例上的计费字节余额。
//
// 共享模式下余额只来自后台同步从 Redis 领到的额度，请求路径只做内存扣减、不等 Redis；
// 本地模式（未配置可用的 Redis，或同步持续失败）下余额按速率在本地补充，等价于每实例独立限流。
type quotaBucket struct {
	config Config

	// balance 是本实例当前可用的计费字节，允许为负：负数是透支，下次同步时一并向 Redis 补扣。
	balance int64

	// used、denied 是自上次同步以来放行与拒绝的计费字节，决定下次向 Redis 申请多少。
	used   int64
	denied int64

	// refilledAt 是本地模式按速率补充的起点。共享模式下每次判断都把它推到当前时间，
	// 切到本地模式时只补充切换之后的时长，不会把共享期间的时间也折算成本地额度。
	refilledAt time.Time
	lastSeen   time.Time
}

func newQuotaBucket(config Config, now time.Time, shared bool) *quotaBucket {
	bucket := &quotaBucket{config: config, refilledAt: now}
	if !shared {
		// 本地模式从满桶开始，与原先的本地 GCRA 一致；共享模式从 0 开始，冷启动靠透支线放行。
		bucket.balance = config.BurstBytes
	}
	return bucket
}

// allow 只要余额没有低于透支线就放行并扣减，扣减后可以低于透支线。
// 因此单个超过 burst_bytes 的请求在不欠账时也能通过，随后按速率还清欠账，长期平均速率不变，
// 不会出现某个服务的单个请求永远过不去的情况。
func (b *quotaBucket) allow(now time.Time, cost int64, shared bool) bool {
	if !b.config.limited() || cost <= 0 {
		return true
	}
	if shared {
		b.refilledAt = now
	} else {
		b.refill(now)
	}
	if b.balance <= -b.config.overdraftBytes() {
		b.denied = saturatingAdd(b.denied, cost)
		return false
	}
	b.balance = max(saturatingAdd(b.balance, -cost), minQuotaBalance)
	b.used = saturatingAdd(b.used, cost)
	return true
}

// refill 按速率补充本地余额，上限为 burst_bytes。不足 1 字节的时长保留在起点上，
// 攒到下一次再补，否则高频小请求下每次都被向下取整为 0，余额永远补不回来。
func (b *quotaBucket) refill(now time.Time) {
	elapsed := now.Sub(b.refilledAt)
	if elapsed <= 0 {
		return
	}
	headroom := saturatingAdd(b.config.BurstBytes, -b.balance)
	if headroom <= 0 {
		b.refilledAt = now
		return
	}
	credit := elapsed.Seconds() * float64(b.config.BytesPerSecond)
	if credit >= float64(headroom) {
		b.balance = b.config.BurstBytes
		b.refilledAt = now
		return
	}
	whole := int64(credit)
	if whole <= 0 {
		return
	}
	b.balance += whole
	b.refilledAt = b.refilledAt.Add(time.Duration(math.Ceil(
		float64(whole) * float64(time.Second) / float64(b.config.BytesPerSecond),
	)))
}

// want 是下次同步要申请的计费字节：补上本周期的用量与被拒需求，再扣掉手上还没用完的余额，
// 也就是预取下一个周期的额度并还清欠账。单次不超过共享桶容量，避免一个实例一次取空共享桶。
func (b *quotaBucket) want() int64 {
	return min(saturatingAdd(saturatingAdd(b.used, b.denied), -b.balance), b.config.leaseCapacity())
}

func saturatingAdd(left, right int64) int64 {
	switch {
	case right > 0 && left > math.MaxInt64-right:
		return math.MaxInt64
	case right < 0 && left < math.MinInt64-right:
		return math.MinInt64
	}
	return left + right
}

type quotaShard struct {
	mu      sync.Mutex
	buckets map[serviceKey]*quotaBucket
}

// quotaManager 保存本实例所有 {token, service} 的余额。
// 请求路径按服务哈希到分片锁上做内存扣减；后台同步按分片批量取出申请量、写回授予量，两者都不持锁做网络 I/O。
type quotaManager struct {
	shards        [stateShardCount]quotaShard
	idleTTL       time.Duration
	sweepInterval time.Duration
	lastSweep     atomic.Int64

	// lastSynced 是最近一次同步成功的时间（UnixNano），请求路径据此判断当前处于共享还是本地模式。
	// 判断放在请求路径而不是由同步 goroutine 切换：同步卡在一次慢调用里时，请求同样会在超时后自行切到本地。
	lastSynced atomic.Int64
	// lastFailure 是最近一次同步失败的降级原因，本地模式下按它记录 decisions 指标。
	lastFailure atomic.Pointer[prometheus.Counter]
}

func newQuotaManager(now func() time.Time) *quotaManager {
	manager := &quotaManager{
		idleTTL:       defaultStateIdleTTL,
		sweepInterval: defaultStateSweepInterval,
	}
	for i := range manager.shards {
		manager.shards[i].buckets = make(map[serviceKey]*quotaBucket)
	}
	manager.lastSweep.Store(now().UnixNano())
	return manager
}

// Allow 在本地余额上判断一次请求。额度变化表示新策略，按新配置重建余额：
// Redis Key 同样包含额度摘要，共享桶也会随之换成一个新的满桶。
func (m *quotaManager) Allow(
	token, service string,
	cost int64,
	config Config,
	now time.Time,
	shared bool,
) bool {
	key := serviceKey{token: token, service: service}
	shard := &m.shards[shardIndex(token, service)]
	shard.mu.Lock()
	bucket, ok := shard.buckets[key]
	if !ok || bucket.config != config {
		bucket = newQuotaBucket(config, now, shared)
		shard.buckets[key] = bucket
	}
	bucket.lastSeen = now
	allowed := bucket.allow(now, cost, shared)
	shard.mu.Unlock()

	m.maybeSweep(now)
	return allowed
}

// shared 判断当前是否处于共享模式：距最近一次同步成功不超过 defaultSyncStaleAfter。
func (m *quotaManager) shared(now time.Time) bool {
	last := m.lastSynced.Load()
	return last != 0 && now.UnixNano()-last <= defaultSyncStaleAfter.Nanoseconds()
}

func (m *quotaManager) markSynced(now time.Time) {
	m.lastSynced.Store(now.UnixNano())
}

func (m *quotaManager) markFailed(err error) {
	decision := fallbackDecision(err)
	m.lastFailure.Store(&decision)
}

// failureDecision 返回本地模式下应记的 decisions 计数。还没有失败记录却已经超时未同步，
// 说明同步卡在一次尚未返回的慢调用里，按超时记。
func (m *quotaManager) failureDecision() prometheus.Counter {
	if decision := m.lastFailure.Load(); decision != nil {
		return *decision
	}
	return decisionLocalTimeout
}

// leaseRequest 是一次同步里对单个服务的额度申请。
type leaseRequest struct {
	key    serviceKey
	config Config
	want   int64
}

// collect 取出需要向 Redis 申请额度的服务，并清零各服务本周期的用量与拒绝计数。
// 只有本周期出现过流量或仍有欠账的服务才会申请，批量大小随活跃服务数变化，与配置了多少服务无关。
func (m *quotaManager) collect() []leaseRequest {
	var requests []leaseRequest
	for i := range m.shards {
		shard := &m.shards[i]
		shard.mu.Lock()
		for key, bucket := range shard.buckets {
			want := bucket.want()
			bucket.used, bucket.denied = 0, 0
			if want > 0 {
				requests = append(requests, leaseRequest{key: key, config: bucket.config, want: want})
			}
		}
		shard.mu.Unlock()
	}
	return requests
}

// apply 把 Redis 授予的额度加到对应服务的余额上。申请之后服务被回收或额度已变化时丢弃这笔授予：
// 新额度对应 Redis 中的新桶，旧桶领到的额度不能挪用；损失至多一个周期的申请量。
func (m *quotaManager) apply(requests []leaseRequest, grants []int64) {
	for i, request := range requests {
		if grants[i] <= 0 {
			continue
		}
		shard := &m.shards[shardIndex(request.key.token, request.key.service)]
		shard.mu.Lock()
		if bucket, ok := shard.buckets[request.key]; ok && bucket.config == request.config {
			bucket.balance = saturatingAdd(bucket.balance, grants[i])
		}
		shard.mu.Unlock()
	}
}

func (m *quotaManager) Delete(token, service string) {
	key := serviceKey{token: token, service: service}
	shard := &m.shards[shardIndex(token, service)]
	shard.mu.Lock()
	delete(shard.buckets, key)
	shard.mu.Unlock()
}

func (m *quotaManager) maybeSweep(now time.Time) {
	previous := m.lastSweep.Load()
	if now.UnixNano()-previous < m.sweepInterval.Nanoseconds() ||
		!m.lastSweep.CompareAndSwap(previous, now.UnixNano()) {
		return
	}
	for i := range m.shards {
		shard := &m.shards[i]
		shard.mu.Lock()
		for key, bucket := range shard.buckets {
			if now.Sub(bucket.lastSeen) >= m.idleTTL {
				delete(shard.buckets, key)
			}
		}
		shard.mu.Unlock()
	}
}

// Clean 只清空余额，不重置同步状态：被 Clean 的实例仍可能继续处理请求（见 ensureRedis），
// 共享还是本地模式应当继续由真实的同步结果决定。
func (m *quotaManager) Clean() {
	for i := range m.shards {
		shard := &m.shards[i]
		shard.mu.Lock()
		clear(shard.buckets)
		shard.mu.Unlock()
	}
}

type serviceActivityShard struct {
	mu       sync.Mutex
	lastSeen map[serviceKey]time.Time
}

// serviceActivityTracker 只跟踪指标 Label 的活跃时间，不保存或同步任何限流状态。
// Redis 正常时也需要该索引，否则已经消失的动态服务会永久占用 Prometheus Label。
type serviceActivityTracker struct {
	shards        [stateShardCount]serviceActivityShard
	idleTTL       time.Duration
	sweepInterval time.Duration
	lastSweep     atomic.Int64
	onEvict       func(token, service string)
}

func newServiceActivityTracker(
	now func() time.Time,
	onEvict func(token, service string),
) *serviceActivityTracker {
	tracker := &serviceActivityTracker{
		idleTTL:       defaultStateIdleTTL,
		sweepInterval: defaultStateSweepInterval,
		onEvict:       onEvict,
	}
	for i := range tracker.shards {
		tracker.shards[i].lastSeen = make(map[serviceKey]time.Time)
	}
	tracker.lastSweep.Store(now().UnixNano())
	return tracker
}

func (t *serviceActivityTracker) Touch(token, service string, now time.Time) {
	key := serviceKey{token: token, service: service}
	shard := &t.shards[shardIndex(token, service)]
	shard.mu.Lock()
	shard.lastSeen[key] = now
	shard.mu.Unlock()
	t.maybeSweep(now)
}

func (t *serviceActivityTracker) maybeSweep(now time.Time) {
	previous := t.lastSweep.Load()
	if now.UnixNano()-previous < t.sweepInterval.Nanoseconds() ||
		!t.lastSweep.CompareAndSwap(previous, now.UnixNano()) {
		return
	}
	for i := range t.shards {
		shard := &t.shards[i]
		shard.mu.Lock()
		for key, lastSeen := range shard.lastSeen {
			if now.Sub(lastSeen) < t.idleTTL {
				continue
			}
			delete(shard.lastSeen, key)
			if t.onEvict != nil {
				t.onEvict(key.token, key.service)
			}
		}
		shard.mu.Unlock()
	}
}

func (t *serviceActivityTracker) Clean() {
	for i := range t.shards {
		shard := &t.shards[i]
		shard.mu.Lock()
		for key := range shard.lastSeen {
			if t.onEvict != nil {
				t.onEvict(key.token, key.service)
			}
		}
		clear(shard.lastSeen)
		shard.mu.Unlock()
	}
}
