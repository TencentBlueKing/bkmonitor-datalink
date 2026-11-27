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
	"sync/atomic"
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
	leaser := newTestLeaser(server, "a")
	defer leaser.Close()
	config := limiterConfig(10, 50)
	lease := func(want int64) leaseResult {
		t.Helper()
		return leaseOne(t, leaser, config, want, 10)
	}

	assert.Equal(t, int64(30), lease(30).grant)
	assert.Equal(t, int64(20), lease(30).grant, "只授予桶里剩下的部分")
	assert.Zero(t, lease(1).grant, "共享桶已空")

	key := leaser.stateKey("token", "checkout", config)
	assert.Equal(t, 5*time.Second, server.TTL(key), "TAT 在额度完全恢复后过期")
	assert.False(t, strings.Contains(key, "token"))
	assert.False(t, strings.Contains(key, "checkout"))
	assert.True(t, server.Exists(key+":demand"), "上报了需求")

	server.SetTime(base.Add(time.Second))
	assert.Equal(t, int64(10), lease(30).grant, "按速率恢复")
}

func TestRedisLeaseSharedByCollectorsAndResetByPolicy(t *testing.T) {
	server := miniredis.RunT(t)
	server.SetTime(time.Unix(1700000000, 0))
	first, second := newTestLeaser(server, "a"), newTestLeaser(server, "b")
	defer first.Close()
	defer second.Close()

	config := limiterConfig(1, 10)
	assert.Equal(t, int64(10), leaseOne(t, first, config, 10, 0).grant)
	assert.Zero(t, leaseOne(t, second, config, 1, 0).grant, "不同 Collector 必须共享同一个服务额度")

	changed := limiterConfig(1, 20)
	assert.Equal(t, int64(20), leaseOne(t, second, changed, 20, 0).grant, "额度变化后按新策略创建满桶")
}

func TestRedisLeaseReturn(t *testing.T) {
	server := miniredis.RunT(t)
	server.SetTime(time.Unix(1700000000, 0))
	leaser := newTestLeaser(server, "a")
	defer leaser.Close()
	config := limiterConfig(10, 50)

	assert.Equal(t, int64(50), leaseOne(t, leaser, config, 50, 10).grant)
	assert.Equal(t, int64(-20), leaseOne(t, leaser, config, -20, 10).grant)
	assert.Equal(t, int64(20), leaseOne(t, leaser, config, 50, 10).grant, "退还的额度可以被再次领取")
	assert.Equal(t, int64(-1000), leaseOne(t, leaser, config, -1000, 10).grant)
	assert.Equal(t, int64(50), leaseOne(t, leaser, config, 100, 10).grant, "退还不会让桶超过容量")
}

// redisLeaseState 直接设置共享桶的可用量与紧张起始时间，用来构造 Lua 的各种分配情况。
func redisLeaseState(t *testing.T, server *miniredis.Miniredis, leaser *redisLeaser, config Config,
	now time.Time, available int64, scarceSince *time.Time,
) {
	t.Helper()
	key := leaser.stateKey("token", "checkout", config)
	tat := now.UnixMicro() + (config.leaseCapacity()-available)*1000000/config.BytesPerSecond
	require.NoError(t, server.Set(key, fmt.Sprintf("%d", tat)))
	if scarceSince != nil {
		server.HSet(key+":demand", "__scarce", fmt.Sprintf("%d", scarceSince.UnixMicro()))
	}
}

// TestRedisLeaseFairShare 覆盖持续紧张时的 max-min 公平分配：桶中可用量持续一个同步周期以上不足一半，
// 授予量受 max(公平额度, 桶中富余) 封顶，需求低于水位的实例拿满需求，先领的实例拿不走别人那份。
func TestRedisLeaseFairShare(t *testing.T) {
	server := miniredis.RunT(t)
	base := time.Unix(1700000000, 0)
	server.SetTime(base)
	config := limiterConfig(100, 100) // 容量 100，一半为 50
	a, b, c := newTestLeaser(server, "a"), newTestLeaser(server, "b"), newTestLeaser(server, "c")
	defer a.Close()
	defer b.Close()
	defer c.Close()

	// 只上报需求：总需求 10 + 30 + 80 = 120 > 100。
	leaseOne(t, a, config, 0, 80)
	leaseOne(t, b, config, 0, 10)
	leaseOne(t, c, config, 0, 30)
	since := base.Add(-time.Second)
	redisLeaseState(t, server, a, config, base, 45, &since)

	results, err := b.Lease(context.Background(), []leaseRequest{{
		key: serviceKey{token: "token", service: "checkout"}, config: config, want: 1000, demand: 10,
	}})
	require.NoError(t, err)
	assert.Equal(t, int64(3), results[0].peers)
	assert.Equal(t, int64(10), results[0].share, "水位为 33：10 拿满需求")
	assert.True(t, results[0].scarce)
	assert.Equal(t, int64(10), results[0].grant, "公平额度为 10 × 2 个周期，桶里虽有 45 也只给 10")
	assert.Zero(t, leaseOne(t, b, config, 1000, 10).grant, "公平额度已用完，拿不走别人的份额")

	result := leaseOne(t, a, config, 1000, 80)
	assert.Equal(t, int64(60), result.share, "10 与 30 拿满需求，剩下的 60 给 A")
	assert.Equal(t, int64(35), result.grant, "公平额度 60，受桶中可用量 35 限制")
}

// TestRedisLeaseBurstNeedFirst 覆盖「上报看起来紧张、桶里却还有存量」：突发让瞬时需求虚高，或桶刚跌破一半
// 还不到一个同步周期。必须满足的部分先到先得，投机性的预取只能用超出半个桶的部分。
func TestRedisLeaseBurstNeedFirst(t *testing.T) {
	server := miniredis.RunT(t)
	base := time.Unix(1700000000, 0)
	server.SetTime(base)
	config := limiterConfig(100, 100)
	a, b := newTestLeaser(server, "a"), newTestLeaser(server, "b")
	defer a.Close()
	defer b.Close()
	request := func(leaser *redisLeaser, want, need int64) leaseResult {
		t.Helper()
		results, err := leaser.Lease(context.Background(), []leaseRequest{{
			key: serviceKey{token: "token", service: "checkout"}, config: config, want: want, demand: 100, need: need,
		}})
		require.NoError(t, err)
		return results[0]
	}

	leaseOne(t, b, config, 0, 100) // 总需求 200 > 100
	result := request(a, 80, 30)
	assert.False(t, result.scarce)
	assert.Equal(t, int64(50), result.grant, "桶满 100：需要的 30 先给，预取只能用超出一半的 20")

	result = request(b, 80, 30)
	assert.Equal(t, int64(30), result.grant, "桶里 50，已到一半：只给需要的 30，不给预取")
	result = request(a, 80, 30)
	assert.False(t, result.scarce, "跌破一半还不到一个同步周期，仍按先到先得")
	assert.Equal(t, int64(20), result.grant, "需要 30，桶里只剩 20")

	since := base.Add(-defaultSyncInterval)
	redisLeaseState(t, server, a, config, base, 20, &since)
	result = request(b, 80, 30)
	assert.True(t, result.scarce, "持续一个同步周期仍不足一半，按公平额度分配")
}

func TestRedisLeaseNoContentionGrantsWant(t *testing.T) {
	server := miniredis.RunT(t)
	server.SetTime(time.Unix(1700000000, 0))
	config := limiterConfig(100, 1000)
	a, b := newTestLeaser(server, "a"), newTestLeaser(server, "b")
	defer a.Close()
	defer b.Close()

	leaseOne(t, b, config, 0, 20)
	result := leaseOne(t, a, config, 500, 30)
	assert.Equal(t, int64(500), result.grant, "总需求不超过额度时不封顶，桶里有多少给多少")
	assert.Equal(t, int64(30+(100-50)/2), result.share, "公平份额为需求加剩余额度均分")
}

func TestRedisLeaseExpiresStaleDemand(t *testing.T) {
	server := miniredis.RunT(t)
	base := time.Unix(1700000000, 0)
	server.SetTime(base)
	config := limiterConfig(100, 1000)
	a, b := newTestLeaser(server, "a"), newTestLeaser(server, "b")
	defer a.Close()
	defer b.Close()

	leaseOne(t, b, config, 0, 90)
	assert.Equal(t, int64(2), leaseOne(t, a, config, 0, 90).peers)

	server.SetTime(base.Add(defaultDemandStaleAfter + time.Second))
	result := leaseOne(t, a, config, 1000, 90)
	assert.Equal(t, int64(1), result.peers, "失联实例的需求被剔除，份额让给存活实例")
	assert.Equal(t, int64(1000), result.grant)

	key := a.stateKey("token", "checkout", config) + ":demand"
	fields, err := server.HKeys(key)
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, fields)
}

func TestRedisLeaseBatchAndProbe(t *testing.T) {
	server := miniredis.RunT(t)
	server.SetTime(time.Unix(1700000000, 0))
	leaser := newTestLeaser(server, "a")
	defer leaser.Close()

	results, err := leaser.Lease(context.Background(), []leaseRequest{
		{key: serviceKey{token: "token", service: "checkout"}, config: limiterConfig(10, 50), want: 60, demand: 10},
		{key: serviceKey{token: "token", service: "payment"}, config: limiterConfig(10, 5), want: 3, demand: 1},
	})
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, int64(50), results[0].grant, "同一批里各服务按各自的桶授予")
	assert.Equal(t, int64(3), results[1].grant)

	results, err = leaser.Lease(context.Background(), nil)
	require.NoError(t, err, "空批次用作健康探测")
	assert.Empty(t, results)
}

// TestSharedQuotaAcrossCollectors 端到端验证两个 Collector 共享同一个服务额度：
// 冷启动等一次租约放行；额度被另一个实例领走后，不足的请求被拒且不扣余额。
func TestSharedQuotaAcrossCollectors(t *testing.T) {
	server := miniredis.RunT(t)
	base := time.Unix(1700000000, 0)
	server.SetTime(base)
	conf := policyMap(100, 100)
	conf["redis"] = map[string]any{
		"mode": redisModeStandalone, "db": 0,
		"key": "shared.traffic_limiter", "addrs": []string{server.Addr()},
	}
	newCollector := func(id string) *trafficLimiter {
		t.Helper()
		clock := newFakeClock()
		factory, err := newFactoryWithLeaser(conf, nil, clock.Now, func(config RedisConfig) quotaLeaser {
			return newRedisLeaserWithID(config, id)
		}, 0)
		require.NoError(t, err)
		t.Cleanup(factory.Clean)
		return factory
	}
	first, second := newCollector("first"), newCollector("second")
	accepted := func(t *testing.T, factory *trafficLimiter, size int) bool {
		t.Helper()
		decision, err := factory.limitSnapshot("token", map[string]int{"checkout": size})
		require.NoError(t, err)
		return decision.accepted
	}

	// 透支上限 min(100 × 0.2s, 100 / 2) = 20。
	assert.True(t, accepted(t, first, 60), "冷启动等一次租约后放行，不丢数据")
	assert.True(t, accepted(t, second, 30), "共享桶还剩 40，全部领走")
	assert.True(t, accepted(t, second, 30), "共享桶已空，余额 10 加透支 20")
	assert.False(t, accepted(t, second, 1), "透支已到上限，被拒")
	assert.True(t, accepted(t, first, 20), "first 也领不到额度，用自己的透支空间")
	assert.False(t, accepted(t, first, 1), "两个实例的透支都到上限：多放行的量不超过各自透支上限之和")

	server.SetTime(base.Add(time.Second))
	second.syncNow()
	assert.True(t, accepted(t, second, 30), "共享桶按速率恢复后，定时同步还清欠账并重新领到额度")
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
	require.NoError(t, err, "首个请求由本地满桶放行")
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
	assert.ErrorContains(t, err, "exceeds burst_bytes", "补上 Redis 配置后额度立即生效")

	factory.Reload(disabled, nil)
	assert.False(t, factory.view.Load().enabled)
	assert.Nil(t, factory.view.Load().redis)
	_, err = factory.Process(newRecord())
	assert.NoError(t, err, "移除 Redis 配置后退回空转")
}

// TestTrafficLimiterRecoversAfterClean 覆盖 Pipeline Manager 的装配路径：
// Reload 会先 Clean 全部新建的 Processor，再把上一代配置中不存在的那些直接装配进来。
// 被 Clean 过的限流器必须重建客户端继续租约，否则会一直停在本地降级。
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
	assert.NotEmpty(t, server.Keys(), "Clean 之后应重建客户端并继续在 Redis 上扣减")
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

// TestRedisHangsFallsBackWithinWaitBudget 覆盖 Redis 只完成握手、从不回包：
// 冷启动的请求最多等 defaultLeaseWaitBudget，随后按本地降级放行；之后的请求不再等待。
func TestRedisHangsFallsBackWithinWaitBudget(t *testing.T) {
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
	factory, err := newFactoryWithLeaser(conf, nil, time.Now, newRedisLeaser, 0)
	require.NoError(t, err)
	defer factory.Clean()

	before := testutil.ToFloat64(leaseWaitTimeout)
	start := time.Now()
	decision, err := factory.limitSnapshot("token", map[string]int{"checkout": 1})
	require.NoError(t, err)
	assert.True(t, decision.accepted, "等待超时后按本地降级放行，冷启动不丢数据")
	assert.Less(t, time.Since(start), defaultLeaseWaitBudget+200*time.Millisecond, "请求最多等一次等待上限")
	assert.Equal(t, before+1, testutil.ToFloat64(leaseWaitTimeout))

	start = time.Now()
	decision, err = factory.limitSnapshot("token", map[string]int{"payment": 1})
	require.NoError(t, err)
	assert.True(t, decision.accepted)
	assert.Less(t, time.Since(start), defaultLeaseWaitBudget/2, "超时一次后不再等待 Redis")
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

// TestDecisionsCounter 验证共享与本地降级分别记在对应的 backend 上：
// 租约失败立即降级，下一次成功的同步立即恢复共享。
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
	beforeTimeout := testutil.ToFloat64(decisionLocalTimeout)
	request()
	assert.Equal(t, beforeTimeout+1, testutil.ToFloat64(decisionLocalTimeout), "同步失败立即降级")

	leaser.setErr(nil)
	factory.syncNow()
	request()
	assert.Equal(t, before+2, testutil.ToFloat64(decisionRedis), "同步恢复后立即回到共享模式")

	clock.Advance(defaultSyncStaleAfter + time.Millisecond)
	request()
	assert.Equal(t, beforeTimeout+2, testutil.ToFloat64(decisionLocalTimeout), "长时间没有同步成功也视为降级")
}

// TestSyncSkipsRedisWhenIdle 覆盖空闲实例：健康时没有需要申请的服务就不访问 Redis；
// 不健康时仍要访问一次，作为恢复的探测。
func TestSyncSkipsRedisWhenIdle(t *testing.T) {
	leaser := &fakeLeaser{}
	clock := newFakeClock()
	factory, err := newFactoryWithLeaser(
		mainConfigMap(1, 1), nil, clock.Now, func(RedisConfig) quotaLeaser { return leaser }, 0,
	)
	require.NoError(t, err)
	defer factory.Clean()

	clock.Advance(2 * defaultSyncStaleAfter)
	factory.syncNow()
	assert.Zero(t, leaser.callCount(), "没有活跃服务时不访问 Redis")
	assert.True(t, factory.snapshot().redis.shared(clock.Now()), "空同步记为成功")

	factory.snapshot().redis.degrade(decisionLocalError)
	factory.syncNow()
	assert.Equal(t, 1, leaser.callCount(), "不健康时空批次作为探测")
	assert.True(t, factory.snapshot().redis.shared(clock.Now()), "探测成功后恢复")
}

// TestLimitedServiceStopsLeasing 覆盖「Redis 没额度了不会一直去取」：一次租约不足额后该服务进入退避，
// 退避期间不足的请求只能使用透支空间，不再等待、不再访问 Redis；退避结束后每次至多再取一次。
func TestLimitedServiceStopsLeasing(t *testing.T) {
	leaser := &fakeLeaser{}
	leaser.setLimit(10)
	clock := newFakeClock()
	factory, err := newFactoryWithLeaser(
		mainConfigMap(100, 1000), nil, clock.Now, func(RedisConfig) quotaLeaser { return leaser }, 0,
	)
	require.NoError(t, err)
	defer factory.Clean()
	accepted := func(size int) bool {
		t.Helper()
		decision, err := factory.limitSnapshot("token", map[string]int{"checkout": size})
		require.NoError(t, err)
		return decision.accepted
	}

	// 透支上限 min(100 × 0.2s, 1000) = 20。
	assert.True(t, accepted(20), "紧急补货只拿到 10，余下的 10 透支：余额 -10")
	calls := leaser.callCount()
	for i := 0; i < 5; i++ {
		assert.False(t, accepted(20), "透支空间不够")
	}
	assert.Equal(t, calls, leaser.callCount(), "退避期间不再访问 Redis")
	assert.True(t, accepted(10), "被拒的请求不扣余额，透支用到上限 -20")
	assert.False(t, accepted(1))

	clock.Advance(defaultSyncInterval)
	assert.False(t, accepted(20), "退避结束后再等一次补货，仍只拿到 10")
	assert.Equal(t, calls+1, leaser.callCount(), "每个退避周期至多访问一次 Redis")

	leaser.setLimit(0)
	factory.syncNow()
	assert.True(t, accepted(20), "定时同步足额授予后还清欠账、解除限流")
}

// TestUrgentLeaseCoalescesServices 覆盖「一个请求只等一次」：多个服务的紧急补货合并成一次 Redis 调用。
func TestUrgentLeaseCoalescesServices(t *testing.T) {
	leaser := &fakeLeaser{}
	clock := newFakeClock()
	factory, err := newFactoryWithLeaser(
		mainConfigMap(1000, 1000), nil, clock.Now, func(RedisConfig) quotaLeaser { return leaser }, 0,
	)
	require.NoError(t, err)
	defer factory.Clean()

	decision, err := factory.limitSnapshot("token", map[string]int{"a": 10, "b": 10, "c": 10})
	require.NoError(t, err)
	assert.True(t, decision.accepted)
	assert.Empty(t, decision.rejected)
	assert.Equal(t, 1, leaser.callCount())
	assert.Len(t, leaser.lastRequests(), 3)
}

// slowLeaser 给每次租约加一段往返时延，模拟跨机 Redis，拉宽补货在途时的并发窗口。
type slowLeaser struct {
	quotaLeaser
	rtt time.Duration
}

func (l slowLeaser) Lease(ctx context.Context, requests []leaseRequest) ([]leaseResult, error) {
	time.Sleep(l.rtt)
	return l.quotaLeaser.Lease(ctx, requests)
}

// TestConcurrentBurstWithinCapacity 是 2026-10-20 真实 OTLP 压测里「满桶边缘并发尾包」的回归：
// 不超过桶容量的一次倒量由大量请求同时到达时，限额之内不能有 429。
//
// 根因是授予写回之后、等待者被唤醒扣款之前的空档：补货泵紧接着开始的下一轮把这笔授予当成空闲余额少申请，
// 后到的请求先把它扣走，等待者只能吃透支。仿真用例在一个 goroutine 里逐个判断，每个等待者独占一轮补货，
// 看不到这种交错；这里用真实 goroutine、真实补货泵和 miniredis。冷启动与已有预取两种起点各跑若干轮。
func TestConcurrentBurstWithinCapacity(t *testing.T) {
	const (
		limit   = int64(10 << 20)
		size    = 66000 // 压测中单包逻辑字节约 66KB
		packets = 154   // 合计约 97% 容量
		spread  = 5 * time.Millisecond
		runs    = 8
	)
	for _, warm := range []bool{false, true} {
		name := "冷启动"
		if warm {
			name = "已有预取"
		}
		t.Run(name, func(t *testing.T) {
			for run := 0; run < runs; run++ {
				server := miniredis.RunT(t)
				conf := policyMap(limit, limit)
				conf["redis"] = map[string]any{
					"mode": redisModeStandalone, "db": 0, "key": "burst.traffic_limiter", "addrs": []string{server.Addr()},
				}
				factory, err := newFactoryWithLeaser(conf, nil, time.Now, func(config RedisConfig) quotaLeaser {
					return slowLeaser{quotaLeaser: newRedisLeaserWithID(config, "burst"), rtt: time.Millisecond}
				}, 0)
				require.NoError(t, err)
				// 只验证额度语义：放宽等待上限与健康阈值，-race 拖慢执行时也不切到本地降级。
				factory.snapshot().redis.waitBudget = time.Minute
				factory.snapshot().redis.healthyLatency = time.Minute

				burst := packets
				if warm {
					// 先放行一个请求：拿到存活实例数之后按需求预取，倒量从「手上已有预取」开始。
					decision, err := factory.limitSnapshot("token", map[string]int{"checkout": size})
					require.NoError(t, err)
					require.True(t, decision.accepted)
					time.Sleep(10 * time.Millisecond)
					burst--
				}

				var wg sync.WaitGroup
				var mu sync.Mutex
				var rejected int
				start := make(chan struct{})
				for i := 0; i < burst; i++ {
					delay := time.Duration(i) * spread / time.Duration(burst)
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-start
						time.Sleep(delay)
						decision, err := factory.limitSnapshot("token", map[string]int{"checkout": size})
						if err != nil || !decision.accepted {
							mu.Lock()
							rejected++
							mu.Unlock()
						}
					}()
				}
				close(start)
				wg.Wait()
				factory.Clean()
				require.Zero(t, rejected, "第 %d 轮：%d 个请求合计 %d 字节，不超过容量 %d，却拒绝了 %d 个",
					run, packets, packets*size, limit, rejected)
			}
		})
	}
}

// stallLeaser 让第 stallAt 次租约卡住 stall，模拟 Redis 抖动（RDB fork、慢命令、网络重传）。
type stallLeaser struct {
	quotaLeaser
	calls   atomic.Int64
	stallAt int64
	stall   time.Duration
}

func (l *stallLeaser) Lease(ctx context.Context, requests []leaseRequest) ([]leaseResult, error) {
	if l.calls.Add(1) == l.stallAt {
		time.Sleep(l.stall)
	}
	return l.quotaLeaser.Lease(ctx, requests)
}

// TestRedisStallDuringBurstWithinCapacity 是 Redis 抖动的回归：不超过桶容量的一次倒量进行到一半时，
// 一轮租约卡了 80ms（超过 50ms 等待上限）。等不到补货的请求按欠账放行，补货回来再还上，限额之内不能有 429；
// 旧实现在这里改走按实例平摊的本地降级，等待者多半被拒。
func TestRedisStallDuringBurstWithinCapacity(t *testing.T) {
	const (
		limit   = int64(10 << 20)
		size    = 66000
		packets = 154 // 合计约 97% 容量
		spread  = 5 * time.Millisecond
		runs    = 5
	)
	for run := 0; run < runs; run++ {
		server := miniredis.RunT(t)
		conf := policyMap(limit, limit)
		conf["redis"] = map[string]any{
			"mode": redisModeStandalone, "db": 0, "key": "stall.traffic_limiter", "addrs": []string{server.Addr()},
		}
		factory, err := newFactoryWithLeaser(conf, nil, time.Now, func(config RedisConfig) quotaLeaser {
			return &stallLeaser{quotaLeaser: newRedisLeaserWithID(config, "stall"), stallAt: 2, stall: 80 * time.Millisecond}
		}, 0)
		require.NoError(t, err)

		timeouts := testutil.ToFloat64(leaseWaitTimeout)
		var wg sync.WaitGroup
		var rejected atomic.Int64
		start := make(chan struct{})
		for i := 0; i < packets; i++ {
			delay := time.Duration(i) * spread / packets
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				time.Sleep(delay)
				decision, err := factory.limitSnapshot("token", map[string]int{"checkout": size})
				if err != nil || !decision.accepted {
					rejected.Add(1)
				}
			}()
		}
		close(start)
		wg.Wait()
		require.Zero(t, rejected.Load(), "第 %d 轮：Redis 卡了 80ms，限额之内的 %d 个请求被拒了 %d 个",
			run, packets, rejected.Load())
		require.Greater(t, testutil.ToFloat64(leaseWaitTimeout), timeouts, "第 %d 轮没有走到等待超时，用例失效", run)

		// 卡住的那一轮回来之后，再同步一次：欠账全部从共享桶里扣回，Redis 也恢复成健康。
		time.Sleep(100 * time.Millisecond)
		factory.syncNow()
		assert.GreaterOrEqual(t, quotaBucketBalance(factory.quota, serviceKey{token: "token", service: "checkout"}), int64(0),
			"第 %d 轮：欠账没有还上", run)
		assert.True(t, factory.snapshot().redis.shared(time.Now()), "第 %d 轮：一轮快的租约之后恢复健康", run)
		factory.Clean()
	}
}

func redisTestConfig(address string) RedisConfig {
	return RedisConfig{
		Mode:  redisModeStandalone,
		DB:    0,
		Key:   "test.traffic_limiter",
		Addrs: []string{address},
	}
}

func newTestLeaser(server *miniredis.Miniredis, id string) *redisLeaser {
	return newRedisLeaserWithID(redisTestConfig(server.Addr()), id).(*redisLeaser)
}

func leaseOne(t *testing.T, leaser quotaLeaser, config Config, want, demand int64) leaseResult {
	t.Helper()
	results, err := leaser.Lease(context.Background(), []leaseRequest{{
		key:    serviceKey{token: "token", service: "checkout"},
		config: config,
		want:   want,
		demand: demand,
	}})
	require.NoError(t, err)
	require.Len(t, results, 1)
	return results[0]
}

// TestCleanReleasesLeasedQuota 覆盖正常退出时的归还：预取的正余额退回共享桶，本实例的需求记录被删除，
// 接手流量的实例立即可以使用这些额度。
func TestCleanReleasesLeasedQuota(t *testing.T) {
	server := miniredis.RunT(t)
	server.SetTime(time.Unix(1700000000, 0))
	conf := policyMap(1000, 10000)
	conf["redis"] = map[string]any{
		"mode": redisModeStandalone, "db": 0,
		"key": "release.traffic_limiter", "addrs": []string{server.Addr()},
	}
	newCollector := func(id string) *trafficLimiter {
		t.Helper()
		factory, err := newFactoryWithLeaser(conf, nil, newFakeClock().Now, func(config RedisConfig) quotaLeaser {
			return newRedisLeaserWithID(config, id)
		}, 0)
		require.NoError(t, err)
		return factory
	}
	leaving, staying := newCollector("leaving"), newCollector("staying")
	defer staying.Clean()

	decision, err := leaving.limitSnapshot("token", map[string]int{"checkout": 9000})
	require.NoError(t, err)
	require.True(t, decision.accepted)
	leaving.syncNow() // 上报需求并补齐预取
	leaser := leaving.snapshot().redis.leaser.(*redisLeaser)
	key := leaser.stateKey("token", "checkout", limiterConfig(1000, 10000))
	fields, err := server.HKeys(key + ":demand")
	require.NoError(t, err)
	require.Contains(t, fields, "leaving")

	leaving.Stop()
	fields, _ = server.HKeys(key + ":demand")
	assert.NotContains(t, fields, "leaving", "退出时删除本实例的需求记录")

	decision, err = staying.limitSnapshot("token", map[string]int{"checkout": 1000})
	require.NoError(t, err)
	assert.True(t, decision.accepted,
		"桶里只剩 10000 − 9000 − 750（预取）= 250，靠退出实例退还的 750 才够放行 1000")
}

// TestStopWithoutRedisIsNoop 覆盖没有 Redis 客户端时的 Stop：不访问 Redis、不报错。
func TestStopWithoutRedisIsNoop(t *testing.T) {
	factory, err := newFactory(policyMap(1, 1), nil, time.Now)
	require.NoError(t, err)
	factory.Stop()
	factory.Stop()
}
