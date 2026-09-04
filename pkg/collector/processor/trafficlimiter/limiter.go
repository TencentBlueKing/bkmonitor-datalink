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
	// 本地 GCRA 状态和指标活跃表都按 {token, service} 哈希到这么多分片。
	// 只降低不同服务抢同一把锁的概率，不是额度分片，也不改变每个服务一份桶的语义。
	stateShardCount = 64

	// 某个 {token, service} 超过这段时间没有任何请求后，丢掉本地降级桶和 Prometheus label。
	// 回收的是本进程内存和指标基数，Redis Key 靠 Lua 的动态 PX 过期，不看这个值。
	defaultStateIdleTTL = 30 * time.Minute

	// 机会式扫描间隔：只有间隔到达的那个请求会遍历全部分片。
	// 不启动常驻清理 goroutine，避免空闲 Collector 也一直扫表。
	defaultStateSweepInterval = 5 * time.Minute

	// GCRA 把「字节 / 每秒字节」换成微秒占用。Lua 用 1e6，Go 侧用这个常量保持同一时间单位。
	microsecondsPerSecond = int64(time.Second / time.Microsecond)
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

type localGCRAState struct {
	config   Config
	tat      int64
	lastSeen time.Time
}

type localGCRAShard struct {
	mu     sync.Mutex
	states map[serviceKey]*localGCRAState
}

// localGCRAManager 只保存 Redis 故障期间使用的降级状态。
// 不同服务被分散到多个分片锁，避免大量 Token 和服务竞争同一把全局锁。
type localGCRAManager struct {
	shards        [stateShardCount]localGCRAShard
	idleTTL       time.Duration
	sweepInterval time.Duration
	lastSweep     atomic.Int64
}

func newLocalGCRAManager(now func() time.Time) *localGCRAManager {
	manager := &localGCRAManager{
		idleTTL:       defaultStateIdleTTL,
		sweepInterval: defaultStateSweepInterval,
	}
	for i := range manager.shards {
		manager.shards[i].states = make(map[serviceKey]*localGCRAState)
	}
	manager.lastSweep.Store(now().UnixNano())
	return manager
}

// Allow 在 Redis 失败时按服务执行本地 GCRA。首次进入一次故障周期时从满桶开始；
// 同一故障周期的后续请求复用当前状态，配置变化则按新配置重新创建满桶。
func (m *localGCRAManager) Allow(
	token, service string,
	cost int64,
	config Config,
	now time.Time,
) bool {
	key := serviceKey{token: token, service: service}
	shard := &m.shards[shardIndex(token, service)]
	shard.mu.Lock()
	state, ok := shard.states[key]
	if !ok || state.config != config {
		state = &localGCRAState{config: config, tat: now.UnixMicro()}
		shard.states[key] = state
	}
	state.lastSeen = now
	allowed := state.allow(now, cost)
	shard.mu.Unlock()

	m.maybeSweep(now)
	return allowed
}

func (s *localGCRAState) allow(now time.Time, cost int64) bool {
	if s.config.BytesPerSecond == 0 || cost <= 0 {
		return true
	}
	if cost > s.config.BurstBytes {
		return false
	}

	nowMicros := now.UnixMicro()
	if s.tat < nowMicros {
		s.tat = nowMicros
	}
	increment := bytesToMicroseconds(cost, s.config.BytesPerSecond)
	burstInterval := bytesToMicroseconds(s.config.BurstBytes, s.config.BytesPerSecond)
	if s.tat-nowMicros > burstInterval-increment {
		return false
	}
	s.tat = saturatingAdd(s.tat, increment)
	return true
}

// bytesToMicroseconds 向上取整到微秒，与 Redis Lua 的时间精度保持一致。
// 先做整除再计算余数，避免 amount*1e6 发生 int64 溢出。
func bytesToMicroseconds(amount, rate int64) int64 {
	seconds := amount / rate
	if seconds > (int64(^uint64(0)>>1) / microsecondsPerSecond) {
		return int64(^uint64(0) >> 1)
	}
	micros := seconds * microsecondsPerSecond
	remainder := amount % rate
	if remainder == 0 {
		return micros
	}
	fraction := int64(math.Ceil(
		float64(remainder) * float64(microsecondsPerSecond) / float64(rate),
	))
	return saturatingAdd(micros, fraction)
}

func saturatingAdd(left, right int64) int64 {
	const maxInt64 = int64(^uint64(0) >> 1)
	if right > maxInt64-left {
		return maxInt64
	}
	return left + right
}

func (m *localGCRAManager) Delete(token, service string) {
	key := serviceKey{token: token, service: service}
	shard := &m.shards[shardIndex(token, service)]
	shard.mu.Lock()
	delete(shard.states, key)
	shard.mu.Unlock()
}

func (m *localGCRAManager) maybeSweep(now time.Time) {
	previous := m.lastSweep.Load()
	if now.UnixNano()-previous < m.sweepInterval.Nanoseconds() ||
		!m.lastSweep.CompareAndSwap(previous, now.UnixNano()) {
		return
	}
	for i := range m.shards {
		shard := &m.shards[i]
		shard.mu.Lock()
		for key, state := range shard.states {
			if now.Sub(state.lastSeen) >= m.idleTTL {
				delete(shard.states, key)
			}
		}
		shard.mu.Unlock()
	}
}

func (m *localGCRAManager) Clean() {
	for i := range m.shards {
		shard := &m.shards[i]
		shard.mu.Lock()
		clear(shard.states)
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
