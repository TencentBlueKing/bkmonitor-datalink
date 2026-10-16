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
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

func TestRedisConfigValidate(t *testing.T) {
	valid := RedisConfig{
		Mode:  redisModeStandalone,
		DB:    8,
		Key:   "bkcollector.traffic_limiter",
		Addrs: []string{"127.0.0.1:6379"},
	}
	assert.NoError(t, valid.Validate())

	tests := []struct {
		name   string
		mutate func(*RedisConfig)
	}{
		{name: "missing redis", mutate: func(config *RedisConfig) { *config = RedisConfig{} }},
		{name: "unsupported mode", mutate: func(config *RedisConfig) { config.Mode = "cluster" }},
		{name: "negative db", mutate: func(config *RedisConfig) { config.DB = -1 }},
		{name: "missing key", mutate: func(config *RedisConfig) { config.Key = "" }},
		{name: "missing address", mutate: func(config *RedisConfig) { config.Addrs = nil }},
		{name: "multiple standalone addresses", mutate: func(config *RedisConfig) {
			config.Addrs = []string{"127.0.0.1:6379", "127.0.0.1:6380"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := valid
			tt.mutate(&config)
			assert.Error(t, config.Validate())
		})
	}
}

func TestRedisLeaseGrantsWithinBucket(t *testing.T) {
	server := miniredis.RunT(t)
	base := time.Unix(1700000000, 0)
	server.SetTime(base)
	leaser := newRedisLeaser(redisTestConfig(server.Addr())).(*redisLeaser)
	defer leaser.Close()
	config := limiterConfig(10, 50)
	lease := func(want int64) int64 {
		t.Helper()
		return leaseOne(t, leaser, "token", "checkout", config, want)
	}

	assert.Equal(t, int64(30), lease(30))
	assert.Equal(t, int64(20), lease(30), "只授予桶里剩下的部分")
	assert.Zero(t, lease(1), "共享桶已空")

	key := leaser.stateKey("token", "checkout", config)
	assert.Equal(t, 5*time.Second, server.TTL(key), "Key 在欠账完全恢复后过期")
	assert.False(t, strings.Contains(key, "token"))
	assert.False(t, strings.Contains(key, "checkout"))

	server.SetTime(base.Add(time.Second))
	assert.Equal(t, int64(10), lease(30), "按速率恢复")
}

func TestRedisLeaseSharedByCollectorsAndResetByPolicy(t *testing.T) {
	server := miniredis.RunT(t)
	server.SetTime(time.Unix(1700000000, 0))
	first := newRedisLeaser(redisTestConfig(server.Addr()))
	second := newRedisLeaser(redisTestConfig(server.Addr()))
	defer first.Close()
	defer second.Close()

	config := limiterConfig(1, 10)
	assert.Equal(t, int64(10), leaseOne(t, first, "token", "checkout", config, 10))
	assert.Zero(t, leaseOne(t, second, "token", "checkout", config, 1), "不同 Collector 必须共享同一个服务额度")

	changed := limiterConfig(1, 20)
	assert.Equal(t, int64(20), leaseOne(t, second, "token", "checkout", changed, 20), "额度变化后按新策略创建满桶")
	assert.Len(t, server.Keys(), 2)
}

func TestRedisLeaseBatch(t *testing.T) {
	server := miniredis.RunT(t)
	server.SetTime(time.Unix(1700000000, 0))
	leaser := newRedisLeaser(redisTestConfig(server.Addr()))
	defer leaser.Close()

	grants, err := leaser.Lease(context.Background(), []leaseRequest{
		{key: serviceKey{token: "token", service: "checkout"}, config: limiterConfig(10, 50), want: 60},
		{key: serviceKey{token: "token", service: "payment"}, config: limiterConfig(10, 5), want: 3},
	})
	require.NoError(t, err)
	assert.Equal(t, []int64{50, 3}, grants, "同一批里各服务按各自的桶授予")

	grants, err = leaser.Lease(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, grants)
}

// TestRedisLeaseCapacityCoversSyncInterval 覆盖 burst_bytes 小于一个同步周期额度的配置：
// 租约容量必须放大到至少 defaultSyncStaleAfter 的额度，否则实际速率会被压到「burst / 周期」。
func TestRedisLeaseCapacityCoversSyncInterval(t *testing.T) {
	server := miniredis.RunT(t)
	base := time.Unix(1700000000, 0)
	server.SetTime(base)
	leaser := newRedisLeaser(redisTestConfig(server.Addr()))
	defer leaser.Close()
	config := limiterConfig(1000, 100) // 一个周期 500 字节，远大于 burst

	var granted int64
	for i := 1; i <= 10; i++ {
		server.SetTime(base.Add(time.Duration(i) * defaultSyncInterval))
		granted += leaseOne(t, leaser, "token", "checkout", config, 1000)
	}
	assert.GreaterOrEqual(t, granted, int64(5000), "5 秒内应领到不少于 5000 字节，与 bytes_per_second 一致")
}

func TestSyncSkipsRedisWithoutRequests(t *testing.T) {
	leaser := &fakeLeaser{entered: make(chan struct{}, 1), release: make(chan struct{})}
	close(leaser.release)
	clock := newFakeClock()
	factory, err := newFactoryWithLeaser(
		mainConfigMap(1, 1), nil, clock.Now, func(RedisConfig) quotaLeaser { return leaser }, 0,
	)
	require.NoError(t, err)
	defer factory.Clean()

	clock.Advance(defaultSyncStaleAfter * 2)
	factory.syncNow()
	assert.Empty(t, leaser.entered, "没有活跃服务时不访问 Redis")
	assert.True(t, factory.quota.shared(clock.Now()), "空同步记为成功")
}

// TestSharedQuotaAcrossCollectors 端到端验证两个 Collector 通过后台同步共享同一个服务额度：
// 先同步的实例领走共享桶，后同步的实例领不到，只能在透支线内放行。
func TestSharedQuotaAcrossCollectors(t *testing.T) {
	server := miniredis.RunT(t)
	base := time.Unix(1700000000, 0)
	server.SetTime(base)
	conf := policyMap(100, 100) // 透支线为 50
	conf["redis"] = map[string]any{
		"mode": redisModeStandalone, "db": 0,
		"key": "shared.traffic_limiter", "addrs": []string{server.Addr()},
	}
	newCollector := func() *trafficLimiter {
		t.Helper()
		clock := newFakeClock()
		factory, err := newFactoryWithLeaser(conf, nil, clock.Now, newRedisLeaser, 0)
		require.NoError(t, err)
		t.Cleanup(factory.Clean)
		return factory
	}
	first, second := newCollector(), newCollector()
	accepted := func(t *testing.T, factory *trafficLimiter, size int) bool {
		t.Helper()
		decision, err := factory.limitSnapshot("token", map[string]int{"checkout": size})
		require.NoError(t, err)
		return decision.accepted
	}

	assert.True(t, accepted(t, first, 100), "冷启动靠透支线放行，不等 Redis")
	first.syncNow() // 申请 min(用量 + 欠账, burst) = 100，领走整个共享桶

	assert.True(t, accepted(t, second, 100))
	second.syncNow() // 共享桶已空，领不到额度
	assert.False(t, accepted(t, second, 1), "共享额度被另一个实例领走后只能在透支线内放行")

	assert.True(t, accepted(t, first, 60), "领到额度的实例余额为 0，仍可透支放行")
	assert.False(t, accepted(t, first, 1))

	server.SetTime(base.Add(time.Second))
	first.syncNow()
	assert.True(t, accepted(t, first, 1), "共享桶按速率恢复后，下一次同步重新领到额度")
}

// TestFactoryWithoutRedisConfig 覆盖「主配置没有 redis 段」：处理器必须能正常装载并整体空转，
// 不能在 Factory 阶段报错——那会让整条 Pipeline 建不起来。
func TestFactoryWithoutRedisConfig(t *testing.T) {
	data := ptrace.NewTraces()
	appendTraceResource(data, "checkout", true, 128)
	record := &define.Record{
		Token: define.Token{Original: "no-redis-token"}, RecordType: define.RecordTraces, Data: data,
	}

	// 额度配得再小也不生效，因为 Redis 未配置。
	factory, err := newFactory(policyMap(1, 1), nil, time.Now)
	require.NoError(t, err)
	defer factory.Clean()

	assert.False(t, factory.view.Load().enabled)
	assert.Nil(t, factory.view.Load().redis)
	for i := 0; i < 3; i++ {
		_, err = factory.Process(record)
		require.NoError(t, err, "未配置 Redis 时必须放行")
	}
	assert.Equal(t, 1, data.ResourceSpans().Len(), "空转不得改动数据")
	assert.Zero(t, testutil.ToFloat64(trafficLimiterBytesTotal.WithLabelValues(
		"no-redis-token", "checkout", "traces", resultAccepted,
	)), "空转不产出流量指标")
}

// TestFactoryInvalidRedisConfigDegradesToLocal 覆盖「redis 段存在但非法」。
// 绝不能让 Factory 报错：processor 缺席会导致整条 Pipeline 构建失败，
// 该信号的全部请求随后被判为 400，一次配置笔误就会让在跑的集群丢掉所有数据。
func TestFactoryInvalidRedisConfigDegradesToLocal(t *testing.T) {
	data := ptrace.NewTraces()
	appendTraceResource(data, "checkout", true, 128)
	size := newServiceSizer().tracesSizes(data)["checkout"]
	record := &define.Record{
		Token: define.Token{Original: "bad-redis-token"}, RecordType: define.RecordTraces, Data: data,
	}

	conf := policyMap(1, int64(size))
	conf["redis"] = map[string]any{"mode": redisModeStandalone} // 缺 key 和 addrs

	clock := newFakeClock()
	factory, err := newFactory(conf, nil, clock.Now)
	require.NoError(t, err, "Redis 配置非法不得导致 Processor 装载失败")
	defer factory.Clean()

	view := factory.view.Load()
	assert.True(t, view.enabled, "额度语义保留")
	assert.False(t, view.redisReady, "非法配置不得用于建立客户端")
	assert.Nil(t, view.redis)

	_, err = factory.Process(record)
	require.NoError(t, err, "本地模式从满桶开始")
	_, err = factory.Process(record)
	require.NoError(t, err, "余额为 0 时仍在透支线内")
	_, err = factory.Process(record)
	assert.Error(t, err, "额度仍然生效，只是退化为每实例本地限流")
}

// TestFactoryBrokenMainConfigDisables 覆盖主配置结构本身损坏的情形，同样只降级不报错。
func TestFactoryBrokenMainConfigDisables(t *testing.T) {
	factory, err := newFactory(map[string]any{"bytes_per_second": "not-a-number"}, nil, time.Now)
	require.NoError(t, err)
	defer factory.Clean()
	assert.False(t, factory.view.Load().enabled)
}

// TestFactoryNegativeQuotaIgnored 覆盖额度非法：忽略该额度，不影响 Processor 装载。
func TestFactoryNegativeQuotaIgnored(t *testing.T) {
	factory, err := newFactory(policyMap(-1, 10), nil, time.Now)
	require.NoError(t, err)
	defer factory.Clean()
	assert.Equal(t, Config{Weights: defaultWeights}, factory.view.Load().configs.GetGlobal().(Config))
}

// TestReloadTogglesRedisConfig 覆盖运行期在「未配置」和「已配置」之间来回切换。
func TestReloadTogglesRedisConfig(t *testing.T) {
	server := miniredis.RunT(t)
	data := ptrace.NewTraces()
	appendTraceResource(data, "checkout", true, 128)
	newRecord := func() *define.Record {
		clone := ptrace.NewTraces()
		data.CopyTo(clone)
		return &define.Record{
			Token: define.Token{Original: "toggle-token"}, RecordType: define.RecordTraces, Data: clone,
		}
	}

	disabled := policyMap(1, 1)
	factory, err := newFactory(disabled, nil, time.Now)
	require.NoError(t, err)
	defer factory.Clean()
	_, err = factory.Process(newRecord())
	require.NoError(t, err, "未配置 Redis 时空转")

	enabled := policyMap(1, 1)
	enabled["redis"] = map[string]any{
		"mode": redisModeStandalone, "db": 0,
		"key": "toggle.traffic_limiter", "addrs": []string{server.Addr()},
	}
	factory.Reload(enabled, nil)
	assert.True(t, factory.view.Load().enabled)
	_, err = factory.Process(newRecord())
	require.NoError(t, err, "冷启动在透支线内放行")
	_, err = factory.Process(newRecord())
	assert.Error(t, err, "补上 Redis 配置后额度立即生效")

	factory.Reload(disabled, nil)
	assert.False(t, factory.view.Load().enabled)
	assert.Nil(t, factory.view.Load().redis)
	_, err = factory.Process(newRecord())
	assert.NoError(t, err, "移除 Redis 配置后退回空转")
}

// TestTrafficLimiterRecoversAfterClean 覆盖 Pipeline Manager 的装配路径：
// Reload 会先 Clean 全部新建的 Processor，再把上一代配置中不存在的那些直接装配进来。
// 被 Clean 过的限流器必须重建客户端继续同步，否则会一直停在本地模式。
func TestTrafficLimiterRecoversAfterClean(t *testing.T) {
	server := miniredis.RunT(t)
	conf := policyMap(1024, 4096)
	conf["redis"] = map[string]any{
		"mode":  redisModeStandalone,
		"db":    0,
		"key":   "clean.traffic_limiter",
		"addrs": []string{server.Addr()},
	}

	factory, err := newFactory(conf, nil, time.Now)
	require.NoError(t, err)
	defer factory.Clean()

	factory.Clean()
	require.Nil(t, factory.snapshot().redis, "Clean 需要释放连接池")

	data := ptrace.NewTraces()
	resourceSpans := data.ResourceSpans().AppendEmpty()
	resourceSpans.Resource().Attributes().PutString(serviceNameKey, "checkout")
	resourceSpans.ScopeSpans().AppendEmpty().Spans().AppendEmpty().SetName("span")

	_, err = factory.Process(&define.Record{
		Token:      define.Token{Original: "clean-token"},
		RecordType: define.RecordTraces,
		Data:       data,
	})
	require.NoError(t, err)
	require.NotNil(t, factory.snapshot().redis, "Clean 之后应重建客户端")
	factory.syncNow()
	assert.Eventually(t, func() bool { return len(server.Keys()) > 0 }, 3*time.Second, 10*time.Millisecond,
		"重建后的同步应继续在 Redis 上扣减")
}

func TestFallbackDecisionClassification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want prometheus.Counter
	}{
		{name: "客户端已关闭", err: redis.ErrClosed, want: decisionLocalClosed},
		{name: "ctx 超时", err: context.DeadlineExceeded, want: decisionLocalTimeout},
		{name: "网络读写超时", err: &net.OpError{Err: timeoutError{}}, want: decisionLocalTimeout},
		{name: "其余（含 Lua 脚本报错）", err: errors.New("ERR unknown command"), want: decisionLocalError},
		// 包装过的错误同样要能识别，实际链路上错误都是被 wrap 过的。
		{name: "被包装的 ctx 超时", err: fmt.Errorf("run script: %w", context.DeadlineExceeded), want: decisionLocalTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Same(t, tt.want, fallbackDecision(tt.err))
		})
	}
}

// TestRedisHangsDoesNotBlockRequests 覆盖 Redis 只完成握手、从不回包：
// 请求路径不访问 Redis，不受影响；同步超时后记为失败，超过 defaultSyncStaleAfter 再切到本地模式。
func TestRedisHangsDoesNotBlockRequests(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	var (
		mu    sync.Mutex
		conns []net.Conn
	)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range conns {
			_ = conn.Close()
		}
	}()

	conf := policyMap(1<<20, 1<<20)
	conf["redis"] = map[string]any{
		"mode": redisModeStandalone, "db": 0,
		"key": "hang.traffic_limiter", "addrs": []string{listener.Addr().String()},
	}
	clock := newFakeClock()
	factory, err := newFactoryWithLeaser(conf, nil, clock.Now, newRedisLeaser, 0)
	require.NoError(t, err)
	defer factory.Clean()

	_, err = factory.limitSnapshot("token", map[string]int{"checkout": 1})
	require.NoError(t, err)

	before := testutil.ToFloat64(syncFailed)
	synced := make(chan struct{})
	start := time.Now()
	go func() {
		defer close(synced)
		factory.syncNow()
	}()

	requestStart := time.Now()
	decision, err := factory.limitSnapshot("token", map[string]int{"checkout": 1})
	require.NoError(t, err)
	assert.True(t, decision.accepted)
	assert.Less(t, time.Since(requestStart), 100*time.Millisecond, "请求不得等待 Redis")
	<-synced
	assert.Less(t, time.Since(start), defaultSyncTimeout+time.Second, "同步必须在超时后返回")
	assert.Equal(t, before+1, testutil.ToFloat64(syncFailed))

	clock.Advance(defaultSyncStaleAfter + time.Millisecond)
	beforeTimeout := testutil.ToFloat64(decisionLocalTimeout)
	_, err = factory.limitSnapshot("token", map[string]int{"checkout": 1})
	require.NoError(t, err)
	assert.Equal(t, beforeTimeout+1, testutil.ToFloat64(decisionLocalTimeout), "长时间未同步成功后切到本地模式")
}

type timeoutError struct{}

func (timeoutError) Error() string { return "i/o timeout" }

func (timeoutError) Timeout() bool { return true }

func TestLimiterModeGauge(t *testing.T) {
	modeValue := func(mode string) float64 {
		return testutil.ToFloat64(trafficLimiterMode.WithLabelValues(mode))
	}
	assertMode := func(t *testing.T, want string) {
		t.Helper()
		for _, mode := range limiterModes {
			expected := float64(0)
			if mode == want {
				expected = 1
			}
			assert.Equal(t, expected, modeValue(mode), "mode=%s", mode)
		}
	}

	server := miniredis.RunT(t)
	shared := policyMap(1, 1024)
	shared["redis"] = map[string]any{
		"mode": redisModeStandalone, "db": 0,
		"key": "mode.traffic_limiter", "addrs": []string{server.Addr()},
	}
	invalid := policyMap(1, 1024)
	invalid["redis"] = map[string]any{"mode": redisModeStandalone} // 缺 key 与 addrs

	factory, err := newFactory(shared, nil, time.Now)
	require.NoError(t, err)
	defer factory.Clean()
	assertMode(t, modeShared)

	factory.Reload(invalid, nil)
	assertMode(t, modeLocalOnly)

	factory.Reload(policyMap(1, 1024), nil) // 无 redis 段
	assertMode(t, modeDisabled)

	factory.Reload(shared, nil)
	assertMode(t, modeShared)
}

// TestDecisionsCounter 验证共享与本地模式分别记在对应的 backend 上：
// 单次同步失败仍保持共享，超过 defaultSyncStaleAfter 才记为本地，同步恢复后立即回到共享。
func TestDecisionsCounter(t *testing.T) {
	leaser := &fakeLeaser{}
	clock := newFakeClock()
	factory, err := newFactoryWithLeaser(
		mainConfigMap(1<<20, 1<<20), nil, clock.Now, func(RedisConfig) quotaLeaser { return leaser }, 0,
	)
	require.NoError(t, err)
	defer factory.Clean()
	request := func() {
		t.Helper()
		_, err := factory.limitSnapshot("decisions-token", map[string]int{"checkout": 1})
		require.NoError(t, err)
	}

	before := testutil.ToFloat64(decisionRedis)
	request()
	assert.Equal(t, before+1, testutil.ToFloat64(decisionRedis), "新客户端从共享模式开始")

	leaser.setErr(context.DeadlineExceeded)
	factory.syncNow()
	clock.Advance(defaultSyncInterval)
	request()
	assert.Equal(t, before+2, testutil.ToFloat64(decisionRedis), "单次同步失败不切换模式")

	beforeTimeout := testutil.ToFloat64(decisionLocalTimeout)
	clock.Advance(defaultSyncStaleAfter)
	request()
	assert.Equal(t, beforeTimeout+1, testutil.ToFloat64(decisionLocalTimeout))

	leaser.setErr(nil)
	factory.syncNow()
	request()
	assert.Equal(t, before+3, testutil.ToFloat64(decisionRedis), "同步恢复后立即回到共享模式")
}

func redisTestConfig(address string) RedisConfig {
	return RedisConfig{
		Mode:  redisModeStandalone,
		DB:    0,
		Key:   "test.traffic_limiter",
		Addrs: []string{address},
	}
}

func leaseOne(t *testing.T, leaser quotaLeaser, token, service string, config Config, want int64) int64 {
	t.Helper()
	grants, err := leaser.Lease(context.Background(), []leaseRequest{
		{key: serviceKey{token: token, service: service}, config: config, want: want},
	})
	require.NoError(t, err)
	require.Len(t, grants, 1)
	return grants[0]
}
