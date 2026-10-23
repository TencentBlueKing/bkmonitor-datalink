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
)

const (
	// 本地余额和指标活跃表都按 {token, service} 哈希到这么多分片。
	// 只降低不同服务抢同一把锁的概率，不是额度分片，也不改变每个服务一份余额的语义。
	stateShardCount = 64

	// 某个 {token, service} 超过这段时间没有任何请求后，丢掉本地余额和 Prometheus label。
	// 回收的是本进程内存和指标基数，Redis Key 靠 TTL 过期，不看这个值。
	defaultStateIdleTTL = 30 * time.Minute

	// 机会式扫描间隔：只有间隔到达的那个请求会遍历全部分片。
	// 不启动常驻清理 goroutine，避免空闲 Collector 也一直扫表。
	defaultStateSweepInterval = 5 * time.Minute

	// 预取目标：手上保留约这么久的需求量。要大于一个同步周期，定时同步才能在余额用完前补上；
	// 取 1.5 个周期，留出同步抖动的余量。预取量另受共享桶容量的一半封顶，见 quotaBucket.target。
	defaultPrefetchWindow = 3 * defaultSyncInterval / 2

	// 余额低于预取目标的 1/refillWatermark 时，不等定时同步，异步补货。
	refillWatermark = 4

	// 本周期已到达的时长达到它，需求下降就可以用本周期的实时速率判断，上升则任何时候都立即生效。
	// 下降必须及时发现：下降的实例若等到下一次定时同步才退还预取、上报需求，
	// 它压着的额度与过时的需求会让突增的实例在限额之内被拒最多一个周期。
	demandResponseWindow = 100 * time.Millisecond

	// 判断需求下降至少要求窗口内按原需求本应到达这么多个平均大小的请求。
	// 稀疏的流量在短窗口里本来就可能一个请求都没有，按实时速率会被误判成降到 0，
	// 退还额度、上报 0 需求，额度紧张时就会被流量大的实例挤占。
	demandDropSamples = 4

	// 平均请求大小的平滑系数。
	requestSizeSmoothing = 0.1

	// 透支窗口：共享模式下单个服务在单个实例上最多透支这么久的额度，见 quotaBucket.overdraft。
	// 取需求下降被发现的最长滞后（响应窗口 + 扫描间隔）。这段时间里下降的实例还压着额度、
	// 上报着过时的需求，突增的实例从 Redis 领不到足额，靠透支撑过去，不在限额之内丢数据。
	defaultOverdraftWindow = 2 * demandResponseWindow
)

// admitResult 是一次本地判断的结果。
type admitResult int

const (
	admitAccepted admitResult = iota
	// admitRejected 表示额度不足且不必等待：已经被限流，或者处于本地模式。
	admitRejected
	// admitInsufficient 只出现在共享模式：余额不够付这次请求，但最近的租约是足额授予的，
	// 说明共享桶里还有额度，可以等一次补货。
	admitInsufficient
)

// quotaMode 是一次判断使用的额度来源。
type quotaMode int

const (
	// quotaShared：余额来自 Redis 租约，放行量受共享 GCRA 约束，另有有上限的透支（见 quotaBucket.overdraft）。
	quotaShared quotaMode = iota
	// quotaFallback：Redis 不健康，余额按 max(公平份额, L/n) 在本地补充。
	quotaFallback
	// quotaLocalOnly：没有可用的 Redis（未配置或配置非法），每个实例独立使用全额。
	quotaLocalOnly
)

type limitDecision struct {
	// accepted 表示请求中至少有一个服务成功扣减或处于不限流状态。
	accepted bool
	rejected []string
	// oversized 是因单个请求的计费字节超过 burst_bytes 而被拒绝的服务，是 rejected 的子集。
	oversized []string
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

// quotaBucket 是单个 {token, service} 在本实例上的状态。
//
// 被拒的请求不扣余额、不产生欠账，也不向 Redis 领额度。余额只在共享模式下、在透支上限之内为负，
// 欠账从之后的租约中扣回，见 quotaBucket.overdraft。
type quotaBucket struct {
	config Config

	// balance 是本实例当前可用的计费字节。共享模式下来自 Redis 租约，可以透支到 -overdraft()；
	// 本地模式下按速率补充。
	balance int64
	mode    quotaMode
	// refilledAt 是本地模式按速率补充的起点。
	refilledAt time.Time

	// arrived 是 measuredAt 以来到达的计费字节，放行和被拒的都算（超过 burst 的不算，它永远不可能被放行）。
	// 被拒的必须计入：只统计放行量时，被限住的实例上报的需求永远等于它当前拿到的量，份额再也涨不上去。
	arrived    int64
	measuredAt time.Time
	// avgCost 是平滑后的单个请求计费字节，用来判断窗口里的样本是否足以说明需求下降。
	avgCost float64
	// demand 是上一个同步周期的到达速率（计费字节/秒），不超过 bytes_per_second。
	// 不做衰减：下降的实例若按衰减值上报，总需求会被高估成争抢，突增的实例反而被公平封顶卡住；
	// 预取目标也会迟迟降不下来，空闲实例占着的额度退不回共享桶。
	demand float64

	// need 是正在等待补货的请求所需的计费字节之和。
	need int64
	// limitedUntil 之前，余额不够的请求直接拒绝，既不等待也不按需补货：最近一次租约没有足额授予，
	// 立即再去 Redis 也拿不到更多。退避时长见 limitBackoff。
	limitedUntil time.Time
	// queued 表示已经提交了一次异步补货、还没有结果，避免同一服务重复提交。
	queued bool
	// leased 表示进入共享模式以来领到过额度。没领到过就切到本地降级（典型是冷启动时 Redis 恰好卡住），
	// 按新出现的服务处理、从满桶开始，否则第一批请求会因为余额为 0 被拒。
	leased bool

	// share、peers 是 Redis 最近一次返回的本实例公平份额与该服务的存活实例数，本地降级时使用。
	share float64
	peers int64

	lastSeen time.Time
}

func newQuotaBucket(config Config, now time.Time, mode quotaMode) *quotaBucket {
	bucket := &quotaBucket{config: config, mode: mode, refilledAt: now, measuredAt: now}
	if mode != quotaShared {
		// 本地模式下新出现的服务从满桶开始，与单机限流一致；共享模式从 0 开始，第一个请求等一次租约。
		bucket.balance = bucket.localCapacity()
	}
	return bucket
}

// localRate 是本地模式下的补充速率。没有 Redis 时就是全额；Redis 故障时取 max(公平份额, L/n)：
// 冻结的份额保证总量大体不变，L/n 保证故障期间流量转移过来的实例不会被卡在旧份额上，总量至多约 2L。
// 还没从 Redis 拿到过存活实例数时只能按全额。
func (b *quotaBucket) localRate() float64 {
	rate := float64(b.config.BytesPerSecond)
	if b.mode == quotaFallback && b.peers > 0 {
		return min(rate, max(b.share, rate/float64(b.peers)))
	}
	return rate
}

// localCapacity 按本地速率占全额的比例缩放共享桶容量。
func (b *quotaBucket) localCapacity() int64 {
	return int64(float64(b.config.leaseCapacity()) * b.localRate() / float64(b.config.BytesPerSecond))
}

// switchMode 处理额度来源的切换。进入本地模式时保留手上的余额（它们在 Redis 中已经扣减过）；
// 回到共享模式时清零：本地补充出来的令牌在 Redis 中没有对应的扣减，不能带进共享模式。
func (b *quotaBucket) switchMode(now time.Time, mode quotaMode) {
	if b.mode == mode {
		return
	}
	switch {
	case mode == quotaShared:
		b.balance = 0
		b.limitedUntil = time.Time{}
		b.leased = false
	case b.mode == quotaShared && !b.leased:
		b.mode = mode
		b.balance = max(b.balance, b.localCapacity())
	}
	b.mode = mode
	b.refilledAt = now
}

// refill 按本地速率补充余额，上限为本地容量。不足 1 字节的时长保留在起点上，
// 攒到下一次再补，否则高频小请求下每次都被向下取整为 0，余额永远补不回来。
func (b *quotaBucket) refill(now time.Time) {
	elapsed := now.Sub(b.refilledAt)
	if elapsed <= 0 {
		return
	}
	capacity := b.localCapacity()
	if b.balance >= capacity {
		b.refilledAt = now
		return
	}
	rate := b.localRate()
	credit := elapsed.Seconds() * rate
	if credit >= float64(capacity-b.balance) {
		b.balance = capacity
		b.refilledAt = now
		return
	}
	whole := int64(credit)
	if whole <= 0 {
		return
	}
	b.balance += whole
	b.refilledAt = b.refilledAt.Add(time.Duration(math.Ceil(float64(whole) * float64(time.Second) / rate)))
}

// overdraft 是共享模式下的透支上限：min(defaultOverdraftWindow 的额度, 共享桶容量 / 存活实例数)。
// 后者保证所有实例同时透支到上限也不超过一个桶容量，全局峰值上界为「2 × 容量 + 速率 × t」。
//
// 透支只覆盖不超过剩余透支空间的请求：大请求不能把欠账挖深，让后面的请求长时间去填。
// 欠账计入下一次租约的申请量，长期放行速率仍等于 bytes_per_second；持续超限时欠账停在上限，照常限流。
func (b *quotaBucket) overdraft() int64 {
	byWindow := int64(float64(b.config.BytesPerSecond) * defaultOverdraftWindow.Seconds())
	return min(byWindow, b.config.leaseCapacity()/max(b.peers, 1))
}

// admit 在本地余额上判断一次请求。retry 为真表示这是等待补货之后的第二次判断：
// 不再重复计入需求，也不再要求等待。
//
// 共享模式下余额不够时：没被限流的第一次判断返回 admitInsufficient，先等一次补货，用真实额度；
// 被限流或补货后仍不够时，在透支上限之内放行。
func (b *quotaBucket) admit(now time.Time, cost int64, mode quotaMode, retry bool) (admitResult, bool) {
	b.switchMode(now, mode)
	if !retry {
		b.arrived = saturatingAdd(b.arrived, cost)
		if b.avgCost == 0 {
			b.avgCost = float64(cost)
		} else {
			b.avgCost += (float64(cost) - b.avgCost) * requestSizeSmoothing
		}
	}
	if mode != quotaShared {
		b.refill(now)
	}
	limited := now.Before(b.limitedUntil)
	if b.balance >= cost {
		b.balance -= cost
		refill := mode == quotaShared && !limited && !b.queued &&
			b.balance < b.target(now)/refillWatermark
		if refill {
			b.queued = true
		}
		return admitAccepted, refill
	}
	if mode != quotaShared {
		return admitRejected, false
	}
	if !limited && !retry {
		return admitInsufficient, false
	}
	if b.balance-cost >= -b.overdraft() {
		b.balance -= cost
		refill := !limited && !b.queued
		if refill {
			b.queued = true
		}
		return admitAccepted, refill
	}
	return admitRejected, false
}

// demandEstimate 是当前的需求速率：本周期的实时速率高于上一周期时立即采用，突增不被延迟；
// 低于上一周期时，只有窗口足够长、样本足够多（见 demandDropSamples）才采用，否则维持上一周期的值。
func (b *quotaBucket) demandEstimate(now time.Time) float64 {
	window := now.Sub(b.measuredAt)
	current := float64(b.arrived) / max(window, demandResponseWindow).Seconds()
	if current < b.demand {
		expected := b.demand * window.Seconds()
		if window < demandResponseWindow || expected < demandDropSamples*b.avgCost {
			current = b.demand
		}
	}
	return min(current, float64(b.config.BytesPerSecond))
}

// measureDemand 在每次定时同步时结算一个周期的需求。
func (b *quotaBucket) measureDemand(now time.Time) {
	window := max(now.Sub(b.measuredAt), defaultSyncInterval/2)
	b.demand = min(float64(b.arrived)/window.Seconds(), float64(b.config.BytesPerSecond))
	if b.demand < 1 {
		b.demand = 0
	}
	b.arrived = 0
	b.measuredAt = now
}

// target 是预取目标：约 defaultPrefetchWindow 的需求量，不超过共享桶容量的一半。
// 封顶保证所有实例的预取加起来也给共享桶留有余量，突增的实例总能取到额度。
func (b *quotaBucket) target(now time.Time) int64 {
	prefetch := int64(b.demandEstimate(now) * defaultPrefetchWindow.Seconds())
	return min(prefetch, b.config.leaseCapacity()/2)
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

// quotaManager 保存本实例所有 {token, service} 的状态。
// 请求路径按服务哈希到分片锁上做内存判断；后台补货按分片批量取出申请、写回授予，两者都不持锁做网络 I/O。
type quotaManager struct {
	shards        [stateShardCount]quotaShard
	idleTTL       time.Duration
	sweepInterval time.Duration
	lastSweep     atomic.Int64
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

func (m *quotaManager) shard(key serviceKey) *quotaShard {
	return &m.shards[shardIndex(key.token, key.service)]
}

// Admit 在本地状态上判断一次请求，第二个返回值表示需要异步补货。
// 额度变化表示新策略，按新配置重建状态：Redis Key 同样包含额度摘要，共享桶也会随之换成一个新的满桶。
func (m *quotaManager) Admit(
	key serviceKey,
	cost int64,
	config Config,
	now time.Time,
	mode quotaMode,
	retry bool,
) (admitResult, bool) {
	shard := m.shard(key)
	shard.mu.Lock()
	bucket, ok := shard.buckets[key]
	if !ok || bucket.config != config {
		bucket = newQuotaBucket(config, now, mode)
		shard.buckets[key] = bucket
	}
	bucket.lastSeen = now
	result, refill := bucket.admit(now, cost, mode, retry)
	shard.mu.Unlock()

	m.maybeSweep(now)
	return result, refill
}

// AddNeed 登记一个等待补货的请求，补货量至少覆盖所有等待者。
func (m *quotaManager) AddNeed(key serviceKey, config Config, cost int64) {
	shard := m.shard(key)
	shard.mu.Lock()
	if bucket, ok := shard.buckets[key]; ok && bucket.config == config {
		bucket.need = saturatingAdd(bucket.need, cost)
	}
	shard.mu.Unlock()
}

// leaseRequest 是一次租约里对单个服务的申请。want 为负表示退还多余的余额。
type leaseRequest struct {
	key    serviceKey
	config Config
	want   int64
	// need 是这次申请里属于等待者的部分，结果写回后从 bucket.need 中扣掉。
	need int64
	// demand 是上报给 Redis 的需求速率（计费字节/秒），用于公平分配。
	demand int64
}

// leaseResult 是 Redis 对一个服务的答复。
type leaseResult struct {
	grant int64
	peers int64
	share int64
}

// prepare 生成一轮租约申请。scheduled 为真时是定时同步：结算全部服务的需求、补齐预取、
// 退还多余的余额并保持需求表存活；否则只处理 keys 中按需补货的服务。
// shared 为假时 Redis 不健康，只上报需求（同时作为健康探测），不领取额度。
func (m *quotaManager) prepare(
	now time.Time,
	scheduled bool,
	keys map[serviceKey]struct{},
	shared bool,
) []leaseRequest {
	var requests []leaseRequest
	visit := func(key serviceKey, bucket *quotaBucket) {
		if scheduled {
			bucket.measureDemand(now)
		}
		// 每一轮都结算当前登记的等待：余额可能已被其他补货补够（want ≤ 0），等待者也可能已经超时离开，
		// 不结算的话 need 会一直累积，此后每次都多预取，挤占其他实例的额度。
		request := leaseRequest{
			key:    key,
			config: bucket.config,
			need:   bucket.need,
			demand: int64(bucket.demandEstimate(now)),
		}
		if shared && bucket.mode == quotaShared {
			target := bucket.target(now)
			want := max(bucket.need, target) - bucket.balance
			switch {
			case want > 0:
				request.want = min(want, bucket.config.leaseCapacity())
			case bucket.balance > 2*target:
				// 需求下降后把多余的余额还给共享桶，不让空闲的实例占着额度。
				request.want = target - bucket.balance
				bucket.balance = target
			}
		}
		if request.want == 0 && (!scheduled || request.demand == 0) {
			bucket.need = 0
			bucket.queued = false
			return
		}
		requests = append(requests, request)
	}

	if scheduled {
		for i := range m.shards {
			shard := &m.shards[i]
			shard.mu.Lock()
			for key, bucket := range shard.buckets {
				visit(key, bucket)
			}
			shard.mu.Unlock()
		}
		return requests
	}
	for key := range keys {
		shard := m.shard(key)
		shard.mu.Lock()
		if bucket, ok := shard.buckets[key]; ok {
			visit(key, bucket)
		}
		shard.mu.Unlock()
	}
	return requests
}

// surplus 找出需求已经明显下降、手上余额超过预取目标两倍的服务，交给补货泵退还并上报新需求。
// 只扫描内存；没有这类服务时不访问 Redis。
func (m *quotaManager) surplus(now time.Time) []serviceKey {
	var keys []serviceKey
	for i := range m.shards {
		shard := &m.shards[i]
		shard.mu.Lock()
		for key, bucket := range shard.buckets {
			if bucket.mode != quotaShared || bucket.queued || bucket.balance <= 2*bucket.target(now) {
				continue
			}
			bucket.queued = true
			keys = append(keys, key)
		}
		shard.mu.Unlock()
	}
	return keys
}

// apply 把 Redis 的答复写回。申请之后服务被回收或额度已变化时丢弃这笔授予：
// 新额度对应 Redis 中的新桶，旧桶领到的额度不能挪用；损失至多一次申请量。
func (m *quotaManager) apply(now time.Time, requests []leaseRequest, results []leaseResult) {
	for i, request := range requests {
		shard := m.shard(request.key)
		shard.mu.Lock()
		bucket, ok := shard.buckets[request.key]
		if ok && bucket.config == request.config {
			result := results[i]
			bucket.limitedUntil = time.Time{}
			if request.want > 0 {
				bucket.balance = saturatingAdd(bucket.balance, result.grant)
				bucket.leased = bucket.leased || result.grant > 0
				if shortfall := request.want - result.grant; shortfall > 0 {
					bucket.limitedUntil = now.Add(limitBackoff(bucket.config, request, result, shortfall))
				}
			}
			bucket.need = max(bucket.need-request.need, 0)
			bucket.queued = false
			bucket.share = float64(result.share)
			bucket.peers = result.peers
		}
		shard.mu.Unlock()
	}
}

// limitBackoff 是一次不足额租约之后的退避时长，期间该服务不再访问 Redis。
//
// 公平份额低于本实例上报的需求，说明 Redis 看到的总需求超过额度、本实例已用到自己那份：
// 退避一个需求更新粒度（demandResponseWindow）。不能退避一整个同步周期：需求下降的实例
// 至多 100ms 后就会上报并退还额度，那时争抢可能已经消失，锁住一个周期会让限额之内的突增被拒。
// 争抢下的授予按「公平份额 × 距上次租约的时长」封顶，租约更频繁也不会多拿额度。
//
// 否则只是共享桶暂时被取空（例如其他实例刚预取过），按桶以 bytes_per_second 补回缺口所需的时长退避。
// 两种情况都不短于异步补货的最小间隔、不长于一个同步周期。
func limitBackoff(config Config, request leaseRequest, result leaseResult, shortfall int64) time.Duration {
	if result.share < request.demand {
		return demandResponseWindow
	}
	refill := time.Duration(float64(shortfall) / float64(config.BytesPerSecond) * float64(time.Second))
	return min(max(refill, defaultAsyncRefillSpacing), defaultSyncInterval)
}

// abort 撤销一轮失败的申请：退还的余额原样加回，等待登记清掉。
func (m *quotaManager) abort(requests []leaseRequest) {
	for _, request := range requests {
		shard := m.shard(request.key)
		shard.mu.Lock()
		if bucket, ok := shard.buckets[request.key]; ok && bucket.config == request.config {
			if request.want < 0 {
				bucket.balance = saturatingAdd(bucket.balance, -request.want)
			}
			bucket.need = max(bucket.need-request.need, 0)
			bucket.queued = false
		}
		shard.mu.Unlock()
	}
}

func (m *quotaManager) Delete(key serviceKey) {
	shard := m.shard(key)
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
