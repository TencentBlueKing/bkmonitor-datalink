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

func TestRedisGCRABurstRefillAndTTL(t *testing.T) {
	server := miniredis.RunT(t)
	base := time.Unix(1700000000, 0)
	server.SetTime(base)
	limiter := newRedisGCRA(redisTestConfig(server.Addr())).(*redisGCRA)
	defer limiter.Close()
	config := limiterConfig(10, 50)

	allowed, err := limiter.Allow("token", "checkout", 20, config)
	require.NoError(t, err)
	assert.True(t, allowed)
	allowed, err = limiter.Allow("token", "checkout", 30, config)
	require.NoError(t, err)
	assert.True(t, allowed)
	allowed, err = limiter.Allow("token", "checkout", 1, config)
	require.NoError(t, err)
	assert.False(t, allowed)

	key := limiter.stateKey("token", "checkout", config)
	assert.Equal(t, 5*time.Second, server.TTL(key))
	assert.False(t, strings.Contains(key, "token"))
	assert.False(t, strings.Contains(key, "checkout"))

	server.SetTime(base.Add(time.Second))
	allowed, err = limiter.Allow("token", "checkout", 10, config)
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestRedisGCRASharedByCollectorsAndResetByPolicy(t *testing.T) {
	server := miniredis.RunT(t)
	server.SetTime(time.Unix(1700000000, 0))
	first := newRedisGCRA(redisTestConfig(server.Addr())).(*redisGCRA)
	second := newRedisGCRA(redisTestConfig(server.Addr())).(*redisGCRA)
	defer first.Close()
	defer second.Close()

	config := limiterConfig(1, 10)
	allowed, err := first.Allow("token", "checkout", 10, config)
	require.NoError(t, err)
	assert.True(t, allowed)
	allowed, err = second.Allow("token", "checkout", 1, config)
	require.NoError(t, err)
	assert.False(t, allowed, "不同 Collector 必须共享同一个服务额度")

	changed := limiterConfig(1, 20)
	allowed, err = second.Allow("token", "checkout", 20, changed)
	require.NoError(t, err)
	assert.True(t, allowed, "额度变化后按新策略创建满桶")
	assert.Len(t, server.Keys(), 2)
}

func TestRedisFailureUsesOneLocalBucketPerOutage(t *testing.T) {
	remote := &sequenceDistributedGCRA{steps: []sequenceStep{
		{err: errors.New("redis unavailable")},
		{err: errors.New("redis unavailable")},
		{allowed: false},
		{err: errors.New("redis unavailable again")},
	}}
	clock := newFakeClock()
	factory, err := newFactoryWithRedisFactory(
		mainConfigMap(1, 10),
		nil,
		clock.Now,
		func(RedisConfig) distributedGCRA { return remote },
	)
	require.NoError(t, err)
	defer factory.Clean()

	decision, err := factory.limitSnapshot("token", map[string]int{"checkout": 10})
	require.NoError(t, err)
	assert.True(t, decision.accepted)
	decision, err = factory.limitSnapshot("token", map[string]int{"checkout": 1})
	require.NoError(t, err)
	assert.False(t, decision.accepted, "同一次故障不能为每个请求重新创建满桶")
	decision, err = factory.limitSnapshot("token", map[string]int{"checkout": 1})
	require.NoError(t, err)
	assert.False(t, decision.accepted, "Redis 明确拒绝时不能绕过到本地桶")
	decision, err = factory.limitSnapshot("token", map[string]int{"checkout": 10})
	require.NoError(t, err)
	assert.True(t, decision.accepted, "Redis 拒绝也表示连接已恢复，下一次独立故障应重新从满桶开始")
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
	assert.Error(t, err, "额度仍然生效，只是退化为每实例本地 GCRA")
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
	assert.Error(t, err, "补上 Redis 配置后额度立即生效")

	factory.Reload(disabled, nil)
	assert.False(t, factory.view.Load().enabled)
	assert.Nil(t, factory.view.Load().redis)
	_, err = factory.Process(newRecord())
	assert.NoError(t, err, "移除 Redis 配置后退回空转")
}

// TestTrafficLimiterRecoversAfterClean 覆盖 Pipeline Manager 的装配路径：
// Reload 会先 Clean 全部新建的 Processor，再把上一代配置中不存在的那些直接装配进来。
// 被 Clean 过的限流器必须重建客户端继续访问 Redis，否则新启用限流的 Collector
// 会把该 Token 的全部请求判为超限。
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
		{name: "客户端缺失", err: errNoRedisClient, want: decisionLocalNoClient},
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

// TestDecisionsCounter 验证成功与降级分别记在对应的 backend 上。
func TestDecisionsCounter(t *testing.T) {
	data := ptrace.NewTraces()
	appendTraceResource(data, "checkout", true, 128)
	record := func() *define.Record {
		clone := ptrace.NewTraces()
		data.CopyTo(clone)
		return &define.Record{
			Token: define.Token{Original: "decisions-token"}, RecordType: define.RecordTraces, Data: clone,
		}
	}

	server := miniredis.RunT(t)
	conf := policyMap(1, 1<<20)
	conf["redis"] = map[string]any{
		"mode": redisModeStandalone, "db": 0,
		"key": "decisions.traffic_limiter", "addrs": []string{server.Addr()},
	}

	factory, err := newFactory(conf, nil, time.Now)
	require.NoError(t, err)
	defer factory.Clean()

	before := testutil.ToFloat64(decisionRedis)
	_, err = factory.Process(record())
	require.NoError(t, err)
	assert.Equal(t, before+1, testutil.ToFloat64(decisionRedis), "走通 Redis 应记在 backend=redis")

	// 关掉客户端制造运行时故障，此时应记 closed 而不是 redis。
	beforeClosed := testutil.ToFloat64(decisionLocalClosed)
	require.NoError(t, factory.view.Load().redis.Close())
	_, err = factory.Process(record())
	require.NoError(t, err, "降级后仍应放行")
	assert.Equal(t, beforeClosed+1, testutil.ToFloat64(decisionLocalClosed))
}

func redisTestConfig(address string) RedisConfig {
	return RedisConfig{
		Mode:  redisModeStandalone,
		DB:    0,
		Key:   "test.traffic_limiter",
		Addrs: []string{address},
	}
}

type sequenceStep struct {
	allowed bool
	err     error
}

type sequenceDistributedGCRA struct {
	mu    sync.Mutex
	steps []sequenceStep
}

func (g *sequenceDistributedGCRA) Allow(string, string, int64, Config) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.steps) == 0 {
		return false, errors.New("no scripted redis result")
	}
	step := g.steps[0]
	g.steps = g.steps[1:]
	return step.allowed, step.err
}

func (g *sequenceDistributedGCRA) Close() error { return nil }
