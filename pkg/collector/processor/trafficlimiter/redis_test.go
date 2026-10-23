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

// TestRedisLeaseFairShare 覆盖额度紧张时的 max-min 公平封顶：需求不超过水位的实例拿满需求，
// 其余实例平分剩下的额度。授予量受 max(公平份额 × 距上次租约的时长, 桶中富余) 封顶，
// 桶中富余是可用量扣掉其他实例公平份额的预取后剩下的部分。
func TestRedisLeaseFairShare(t *testing.T) {
	server := miniredis.RunT(t)
	base := time.Unix(1700000000, 0)
	server.SetTime(base)
	config := limiterConfig(100, 100)
	a, b, c := newTestLeaser(server, "a"), newTestLeaser(server, "b"), newTestLeaser(server, "c")
	defer a.Close()
	defer b.Close()
	defer c.Close()

	// 只上报需求：总需求 10 + 30 + 80 = 120 > 100。
	leaseOne(t, b, config, 0, 10)
	leaseOne(t, c, config, 0, 30)

	server.SetTime(base.Add(500 * time.Millisecond))
	result := leaseOne(t, a, config, 1000, 80)
	assert.Equal(t, int64(3), result.peers)
	assert.Equal(t, int64(60), result.share, "水位为 60：10 与 30 拿满需求，剩下的 60 给 A")
	assert.Equal(t, int64(70), result.grant, "桶满 100，给 B、C 留 40 × 0.75s = 30 的预取后，A 可拿 70")

	result = leaseOne(t, b, config, 1000, 10)
	assert.Equal(t, int64(10), result.share)
	assert.Equal(t, int64(5), result.grant, "桶里只剩 30，不够给其他实例留预取，B 按 10 × 0.5s 封顶")

	server.SetTime(base.Add(time.Second))
	assert.Equal(t, int64(30), leaseOne(t, c, config, 1000, 30).grant, "C 距上次租约 1s，封顶 30 × 1s")
	assert.Equal(t, int64(30), leaseOne(t, a, config, 1000, 80).grant, "A 距上次租约 0.5s，封顶 60 × 0.5s")
	assert.Zero(t, leaseOne(t, a, config, 1000, 80).grant, "A 刚领过，封顶为 0，拿不走别人的份额")
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
