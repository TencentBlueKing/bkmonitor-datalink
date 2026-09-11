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
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
)

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{name: "unlimited", config: limiterConfig(0, 0)},
		{name: "limited", config: limiterConfig(100, 50)},
		{name: "negative rate", config: limiterConfig(-1, 10), wantErr: true},
		{name: "negative burst", config: limiterConfig(1, -1), wantErr: true},
		{name: "missing burst", config: limiterConfig(1, 0), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestWeightsNormalizeAndValidate(t *testing.T) {
	t.Run("未设置补成 1", func(t *testing.T) {
		assert.Equal(t, defaultWeights, Weights{}.normalized())
	})

	// 解码后无法区分「没填」和「填了 0」，因此 0 按未设置处理而不是「免费」，
	// 否则一份没写 weights 的服务级覆盖会让该服务的限流直接失效。
	t.Run("显式 0 按未设置处理", func(t *testing.T) {
		assert.Equal(t, defaultWeights, Weights{Traces: 0, Metrics: 0, Logs: 0}.normalized())
	})

	t.Run("只补未设置的项", func(t *testing.T) {
		assert.Equal(
			t, Weights{Traces: 2.5, Metrics: 1, Logs: 0.5},
			Weights{Traces: 2.5, Logs: 0.5}.normalized(),
		)
	})

	t.Run("拒绝非法值", func(t *testing.T) {
		assert.Error(t, Weights{Traces: -1}.Validate())
		assert.Error(t, Weights{Metrics: math.NaN()}.Validate())
		assert.Error(t, Weights{Logs: math.Inf(1)}.Validate())
		assert.NoError(t, defaultWeights.Validate())
	})
}

func TestConfigChargedBytes(t *testing.T) {
	config := func(traces, metrics, logs float64) Config {
		return Config{Weights: Weights{Traces: traces, Metrics: metrics, Logs: logs}}
	}

	t.Run("权重为 1 时等于原始字节", func(t *testing.T) {
		assert.Equal(t, int64(1000), config(1, 1, 1).chargedBytes(1000, define.RecordTraces))
	})

	t.Run("按信号类型分别折算", func(t *testing.T) {
		c := config(2, 1, 0.5)
		assert.Equal(t, int64(2000), c.chargedBytes(1000, define.RecordTraces))
		assert.Equal(t, int64(1000), c.chargedBytes(1000, define.RecordMetrics))
		assert.Equal(t, int64(500), c.chargedBytes(1000, define.RecordLogs))
	})

	t.Run("向上取整", func(t *testing.T) {
		assert.Equal(t, int64(334), config(1, 1, 0.3334).chargedBytes(1000, define.RecordLogs))
	})

	// 极小的权重不能把请求折算成 0，否则等于该信号完全不限流。
	t.Run("非空请求至少计 1 字节", func(t *testing.T) {
		assert.Equal(t, int64(1), config(1, 1, 0.0000001).chargedBytes(1, define.RecordLogs))
	})

	t.Run("超大权重饱和而不溢出", func(t *testing.T) {
		assert.Equal(t, int64(math.MaxInt64), config(1e18, 1, 1).chargedBytes(1<<20, define.RecordTraces))
	})

	t.Run("不支持的信号类型按 1 计费", func(t *testing.T) {
		assert.Equal(t, int64(1000), config(2, 2, 2).chargedBytes(1000, define.RecordProfiles))
	})
}

// TestTrafficLimiterWeightedCost 端到端验证权重生效：同样的字节数，
// 权重更高的信号消耗更多额度，因而更早触顶。
func TestTrafficLimiterWeightedCost(t *testing.T) {
	traces := ptrace.NewTraces()
	appendTraceResource(traces, "checkout", true, 128)
	size := newServiceSizer().tracesSizes(traces)["checkout"]

	newRecord := func() *define.Record {
		clone := ptrace.NewTraces()
		traces.CopyTo(clone)
		return &define.Record{
			Token: define.Token{Original: "weighted-token"}, RecordType: define.RecordTraces, Data: clone,
		}
	}

	// 桶容量刚好够两个原始请求；traces 权重为 2 时只够一个。
	conf := mainConfigMap(1, int64(size*2))
	conf["weights"] = map[string]any{"traces": 2.0}

	clock := newFakeClock()
	factory, err := newTestFactory(conf, nil, clock.Now)
	require.NoError(t, err)
	defer factory.Clean()

	_, err = factory.Process(newRecord())
	require.NoError(t, err, "首个请求扣减 2 倍字节后刚好取空桶")
	_, err = factory.Process(newRecord())
	assert.Error(t, err, "权重放大后第二个请求就应超限")

	// 指标记录的仍然是原始逻辑字节，不是计费字节。
	assert.Equal(
		t, float64(size),
		testutil.ToFloat64(trafficLimiterBytesTotal.WithLabelValues(
			"weighted-token", "checkout", "traces", resultAccepted,
		)),
	)
}

func TestFactoryAndTierConfig(t *testing.T) {
	mainConf := mainConfigMap(100, 200)
	customized := []processor.SubConfigProcessor{
		{
			Token:  "token-1",
			Type:   define.SubConfigFieldDefault,
			Config: processor.Config{Config: policyMap(200, 300)},
		},
		{
			Token:  "token-1",
			Type:   define.SubConfigFieldService,
			ID:     "checkout",
			Config: processor.Config{Config: policyMap(300, 400)},
		},
	}

	factory, err := newTestFactory(mainConf, customized, time.Now)
	require.NoError(t, err)
	defer factory.Clean()

	assert.Equal(t, mainConf, factory.MainConfig())
	assert.Equal(t, define.ProcessorTrafficLimiter, factory.Name())
	assert.False(t, factory.IsDerived())
	assert.True(t, factory.IsPreCheck())
	assert.Equal(t, limiterConfig(100, 200), factory.snapshot().configs.GetGlobal().(Config))
	assert.Equal(t, limiterConfig(200, 300), factory.snapshot().configs.Get("token-1", "payment", "").(Config))
	assert.Equal(t, limiterConfig(300, 400), factory.snapshot().configs.Get("token-1", "checkout", "").(Config))
}

func TestFactoryFromYAML(t *testing.T) {
	content := `
processor:
  - name: "traffic_limiter/gcra"
    config:
      bytes_per_second: 10485760
      burst_bytes: 20971520
      redis:
        mode: standalone
        db: 0
        key: test.traffic_limiter
        addrs: ["127.0.0.1:6379"]
        password: ""
`
	factory := processor.MustCreateFactory(content, func(
		conf map[string]any,
		customized []processor.SubConfigProcessor,
	) (processor.Processor, error) {
		return newTestFactory(conf, customized, time.Now)
	})
	defer factory.Clean()

	trafficFactory := factory.(*trafficLimiter)
	assert.Equal(t, limiterConfig(10485760, 20971520), trafficFactory.snapshot().configs.GetGlobal().(Config))
}

// TestLimitIndex 覆盖额度开关的折叠规则：服务级覆盖优先，其次 Token 默认配置，最后回落主配置。
func TestLimitIndex(t *testing.T) {
	subConfig := func(typ, id string, bytesPerSecond, burstBytes int64) processor.SubConfigProcessor {
		return processor.SubConfigProcessor{
			Token:  "token-1",
			Type:   typ,
			ID:     id,
			Config: processor.Config{Config: policyMap(bytesPerSecond, burstBytes)},
		}
	}

	tests := []struct {
		name       string
		main       map[string]any
		customized []processor.SubConfigProcessor
		want       map[string]bool
	}{
		{
			name: "global disabled",
			main: mainConfigMap(0, 0),
			want: map[string]bool{"token-1": false, "token-2": false},
		},
		{
			name: "global enabled applies to every token",
			main: mainConfigMap(100, 200),
			want: map[string]bool{"token-1": true, "token-2": true},
		},
		{
			name:       "token default enables one token only",
			main:       mainConfigMap(0, 0),
			customized: []processor.SubConfigProcessor{subConfig(define.SubConfigFieldDefault, "", 100, 200)},
			want:       map[string]bool{"token-1": true, "token-2": false},
		},
		{
			name: "service override enables token with disabled default",
			main: mainConfigMap(0, 0),
			customized: []processor.SubConfigProcessor{
				subConfig(define.SubConfigFieldDefault, "", 0, 0),
				subConfig(define.SubConfigFieldService, "checkout", 100, 200),
			},
			want: map[string]bool{"token-1": true, "token-2": false},
		},
		{
			name:       "token default disables despite enabled global",
			main:       mainConfigMap(100, 200),
			customized: []processor.SubConfigProcessor{subConfig(define.SubConfigFieldDefault, "", 0, 0)},
			want:       map[string]bool{"token-1": false, "token-2": true},
		},
		{
			name:       "service override alone falls back to global",
			main:       mainConfigMap(100, 200),
			customized: []processor.SubConfigProcessor{subConfig(define.SubConfigFieldService, "checkout", 0, 0)},
			want:       map[string]bool{"token-1": true, "token-2": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loaded, err := newTierConfigs(tt.main, tt.customized)
			require.NoError(t, err)
			for token, want := range tt.want {
				assert.Equal(t, want, loaded.index.limited(token), "token=%s", token)
			}
		})
	}
}

func TestTrafficLimiterMetrics(t *testing.T) {
	const token = "metrics-token"
	data := ptrace.NewTraces()
	appendTraceResource(data, "checkout", true, 128)
	appendTraceResource(data, "payment", true, 256)
	sizes := newServiceSizer().tracesSizes(data)

	mainBurst := int64(sumSizes(sizes) * 2)
	customized := []processor.SubConfigProcessor{
		{
			Token:  token,
			Type:   define.SubConfigFieldDefault,
			Config: processor.Config{Config: policyMap(1, mainBurst)},
		},
		{
			Token:  token,
			Type:   define.SubConfigFieldService,
			ID:     "payment",
			Config: processor.Config{Config: policyMap(1, int64(sizes["payment"]-1))},
		},
	}
	factory, err := newTestFactory(mainConfigMap(0, 0), customized, time.Now)
	require.NoError(t, err)
	defer factory.Clean()

	_, err = factory.Process(&define.Record{
		Token:      define.Token{Original: token},
		RecordType: define.RecordTraces,
		Data:       data,
	})
	require.NoError(t, err)

	assert.Equal(
		t, float64(sizes["payment"]),
		testutil.ToFloat64(trafficLimiterBytesTotal.WithLabelValues(token, "payment", "traces", resultRejected)),
	)
	assert.Equal(
		t, float64(sizes["checkout"]),
		testutil.ToFloat64(trafficLimiterBytesTotal.WithLabelValues(token, "checkout", "traces", resultAccepted)),
	)
	require.Equal(t, 1, data.ResourceSpans().Len())
	service, ok := data.ResourceSpans().At(0).Resource().Attributes().Get(serviceNameKey)
	require.True(t, ok)
	assert.Equal(t, "checkout", service.AsString())
}

func TestTrafficLimiterUnlimitedAndSharedSignals(t *testing.T) {
	t.Run("unlimited short circuits", func(t *testing.T) {
		const token = "unlimited-token"
		data := ptrace.NewTraces()
		appendTraceResource(data, "checkout", true, 128)

		factory, err := newTestFactory(mainConfigMap(0, 0), nil, time.Now)
		require.NoError(t, err)
		defer factory.Clean()
		_, err = factory.Process(&define.Record{
			Token:      define.Token{Original: token},
			RecordType: define.RecordTraces,
			Data:       data,
		})
		require.NoError(t, err)
		assert.Zero(
			t,
			testutil.ToFloat64(trafficLimiterBytesTotal.WithLabelValues(token, "checkout", "traces", resultAccepted)),
			"未配置额度的 Token 不应付出 Sizer 开销，也不产生指标",
		)
		assert.Equal(t, 1, data.ResourceSpans().Len())
	})

	t.Run("signals share one service bucket", func(t *testing.T) {
		const token = "shared-signals-token"
		traces := ptrace.NewTraces()
		appendTraceResource(traces, "checkout", true, 128)
		metrics := pmetric.NewMetrics()
		appendMetricResource(metrics, "checkout", true, 128)
		logs := plog.NewLogs()
		appendLogResource(logs, "checkout", true, 128)
		sizer := newServiceSizer()
		traceSize := sizer.tracesSizes(traces)["checkout"]
		metricSize := sizer.metricsSizes(metrics)["checkout"]
		logSize := sizer.logsSizes(logs)["checkout"]

		clock := newFakeClock()
		factory, err := newTestFactory(
			mainConfigMap(1, int64(traceSize+metricSize+logSize-1)), nil, clock.Now,
		)
		require.NoError(t, err)
		defer factory.Clean()
		_, err = factory.Process(&define.Record{
			Token: define.Token{Original: token}, RecordType: define.RecordTraces, Data: traces,
		})
		require.NoError(t, err)
		_, err = factory.Process(&define.Record{
			Token: define.Token{Original: token}, RecordType: define.RecordMetrics, Data: metrics,
		})
		require.NoError(t, err)
		_, err = factory.Process(&define.Record{
			Token: define.Token{Original: token}, RecordType: define.RecordLogs, Data: logs,
		})
		assert.Error(t, err)
	})
}

func TestTrafficLimiterReload(t *testing.T) {
	data := ptrace.NewTraces()
	appendTraceResource(data, "checkout", true, 128)
	size := newServiceSizer().tracesSizes(data)["checkout"]
	record := &define.Record{
		Token: define.Token{Original: "reload-token"}, RecordType: define.RecordTraces, Data: data,
	}

	clock := newFakeClock()
	conf := mainConfigMap(1, int64(size))
	factory, err := newTestFactory(conf, nil, clock.Now)
	require.NoError(t, err)
	defer factory.Clean()

	_, err = factory.Process(record)
	require.NoError(t, err, "首个请求刚好取空令牌桶")
	_, err = factory.Process(record)
	require.Error(t, err)

	factory.Reload(conf, nil)
	_, err = factory.Process(record)
	assert.Error(t, err, "重载相同配置不能重新发放满桶")

	factory.Reload(mainConfigMap(0, 0), nil)
	_, err = factory.Process(record)
	assert.NoError(t, err, "配置放开后才允许通过")
}

// TestTrafficLimiterReloadNotBlockedByRedis 守住「请求路径不持锁做 Redis I/O」这条约束。
// sync.RWMutex 的写者防饥饿策略意味着 Reload 一旦阻塞在写锁上，后续所有请求都会排到它后面，
// 一次 Redis 超时会因此被放大成全部 Receiver goroutine 的停顿，而 Reload 是周期性的常规操作。
func TestTrafficLimiterReloadNotBlockedByRedis(t *testing.T) {
	remote := &blockingDistributedGCRA{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	conf := mainConfigMap(1024, 4096)
	factory, err := newFactoryWithRedisFactory(
		conf, nil, time.Now, func(RedisConfig) distributedGCRA { return remote },
	)
	require.NoError(t, err)
	defer factory.Clean()

	inflight := make(chan struct{})
	go func() {
		defer close(inflight)
		_, _ = factory.limitSnapshot("token", map[string]int{"checkout": 1})
	}()
	<-remote.entered

	reloaded := make(chan struct{})
	go func() {
		defer close(reloaded)
		factory.Reload(conf, nil)
	}()
	select {
	case <-reloaded:
	case <-time.After(10 * time.Second):
		// 用 Error 而不是 Fatal，让在途请求先退出，避免失败时把 Clean 一起拖进死锁。
		t.Error("Reload 被在途 Redis 调用阻塞，请求路径不能持有 p.mu 做网络 I/O")
	}

	close(remote.release)
	<-inflight
	<-reloaded
}

type blockingDistributedGCRA struct {
	entered chan struct{}
	release chan struct{}
}

func (g *blockingDistributedGCRA) Allow(string, string, int64, Config) (bool, error) {
	g.entered <- struct{}{}
	<-g.release
	return true, nil
}

func (g *blockingDistributedGCRA) Close() error { return nil }

func TestTrafficLimiterUnsupportedRecord(t *testing.T) {
	factory, err := newTestFactory(mainConfigMap(1, 1), nil, time.Now)
	require.NoError(t, err)
	defer factory.Clean()

	_, err = factory.Process(&define.Record{RecordType: define.RecordProfiles})
	assert.NoError(t, err)
}

func TestLocalGCRAServiceIsolation(t *testing.T) {
	clock := newFakeClock()
	manager := newLocalGCRAManager(clock.Now)

	assert.True(t, manager.Allow("token", "checkout", 12, limiterConfig(1, 20), clock.Now()))
	assert.False(t, manager.Allow("token", "payment", 8, limiterConfig(1, 5), clock.Now()))
	assert.True(t, manager.Allow("token", "checkout", 8, limiterConfig(1, 20), clock.Now()))
	assert.False(t, manager.Allow("token", "checkout", 1, limiterConfig(1, 20), clock.Now()))
	assert.True(t, manager.Allow("another-token", "checkout", 20, limiterConfig(1, 20), clock.Now()))
}

func TestLocalGCRARefillAndConfigReset(t *testing.T) {
	clock := newFakeClock()
	manager := newLocalGCRAManager(clock.Now)
	config := limiterConfig(5, 10)

	assert.True(t, manager.Allow("token", "checkout", 10, config, clock.Now()))
	assert.False(t, manager.Allow("token", "checkout", 1, config, clock.Now()))
	clock.Advance(time.Second)
	assert.True(t, manager.Allow("token", "checkout", 5, config, clock.Now()))
	assert.False(t, manager.Allow("token", "checkout", 1, config, clock.Now()))

	// 额度变化表示新策略，按约定重新创建满桶。
	enlarged := limiterConfig(5, 100)
	assert.True(t, manager.Allow("token", "checkout", 100, enlarged, clock.Now()))
	assert.False(t, manager.Allow("token", "checkout", 1, enlarged, clock.Now()))
}

func TestLocalGCRASweep(t *testing.T) {
	clock := newFakeClock()
	manager := newLocalGCRAManager(clock.Now)
	manager.idleTTL = time.Minute
	manager.sweepInterval = time.Minute

	assert.True(t, manager.Allow("token", "checkout", 1, limiterConfig(1, 10), clock.Now()))
	clock.Advance(2 * time.Minute)
	assert.True(t, manager.Allow("other", "checkout", 1, limiterConfig(1, 10), clock.Now()))
	assert.False(t, localStateExists(manager, "token", "checkout"))
}

func TestServiceActivityTrackerSweep(t *testing.T) {
	clock := newFakeClock()
	var evicted []string
	tracker := newServiceActivityTracker(clock.Now, func(token, service string) {
		evicted = append(evicted, token+"/"+service)
	})
	tracker.idleTTL = time.Minute
	tracker.sweepInterval = time.Minute

	tracker.Touch("token", "checkout", clock.Now())
	clock.Advance(2 * time.Minute)
	tracker.Touch("other", "checkout", clock.Now())
	assert.Equal(t, []string{"token/checkout"}, evicted)
}

func TestLocalGCRAConcurrentLimit(t *testing.T) {
	clock := newFakeClock()
	manager := newLocalGCRAManager(clock.Now)
	config := limiterConfig(1, 1000)

	var accepted atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if manager.Allow("token", "checkout", 10, config, clock.Now()) {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int64(100), accepted.Load())
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1700000000, 0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

// limiterConfig 产出的权重与解码路径归一化后的结果一致，便于直接和装载结果比较。
func limiterConfig(bytesPerSecond, burstBytes int64) Config {
	return Config{
		BytesPerSecond: bytesPerSecond,
		BurstBytes:     burstBytes,
		Weights:        defaultWeights,
	}
}

var defaultWeights = Weights{Traces: 1, Metrics: 1, Logs: 1}

func policyMap(bytesPerSecond, burstBytes int64) map[string]any {
	return map[string]any{
		"bytes_per_second": bytesPerSecond,
		"burst_bytes":      burstBytes,
	}
}

func mainConfigMap(bytesPerSecond, burstBytes int64) map[string]any {
	config := policyMap(bytesPerSecond, burstBytes)
	config["redis"] = map[string]any{
		"mode":     redisModeStandalone,
		"db":       0,
		"key":      "test.traffic_limiter",
		"addrs":    []string{"127.0.0.1:6379"},
		"password": "",
	}
	return config
}

type testDistributedGCRA struct {
	manager *localGCRAManager
	now     func() time.Time
}

func (g *testDistributedGCRA) Allow(
	token, service string,
	cost int64,
	config Config,
) (bool, error) {
	return g.manager.Allow(token, service, cost, config, g.now()), nil
}

func (g *testDistributedGCRA) Close() error { return nil }

func newTestFactory(
	conf map[string]any,
	customized []processor.SubConfigProcessor,
	now func() time.Time,
) (*trafficLimiter, error) {
	return newFactoryWithRedisFactory(
		conf,
		customized,
		now,
		func(RedisConfig) distributedGCRA {
			return &testDistributedGCRA{manager: newLocalGCRAManager(now), now: now}
		},
	)
}

// limitSnapshot 按请求路径的方式取快照后判断额度，供直接验证限流语义的用例使用。
func (p *trafficLimiter) limitSnapshot(token string, sizes map[string]int) (limitDecision, error) {
	view := p.snapshot()
	return p.limit(view.configs, view.redis, token, define.RecordTraces, sizes)
}

func localStateExists(manager *localGCRAManager, token, service string) bool {
	key := serviceKey{token: token, service: service}
	shard := &manager.shards[shardIndex(token, service)]
	shard.mu.Lock()
	defer shard.mu.Unlock()
	_, ok := shard.states[key]
	return ok
}

// BenchmarkTrafficLimiterProcess 对比两种额度状态下的单请求开销，并衡量同 Token/服务分片的竞争程度。
// disabled 量的是灰度未开启该 Token 时的短路成本，此时不计算逻辑字节；
// enabled 使用大到不会拒绝的 Burst，量的是计量、分片状态维护和指标写入的成本。
//
// Sizer 会短暂借出 Resource，因此每个并行 goroutine 必须持有独立的 Record，
// 否则它们会互相搬空数据，量到的是给空结构算大小的耗时。
// 构造 Record 的开销记在每个 goroutine 的首次迭代上，需使用默认的时间模式 benchtime 才能摊薄。
func BenchmarkTrafficLimiterProcess(b *testing.B) {
	const neverRejects = int64(1) << 40

	// 灰度期间订阅配置里通常已经有大量 Token，额度索引不会是空 Map，
	// 因此 disabled 用例要带上这批子配置，量到的才是真实的一次 Map 查找。
	customized := make([]processor.SubConfigProcessor, 0, 512)
	for i := 0; i < 512; i++ {
		customized = append(customized, processor.SubConfigProcessor{
			Token:  fmt.Sprintf("benchmark-idle-token-%d", i),
			Type:   define.SubConfigFieldDefault,
			Config: processor.Config{Config: policyMap(0, 0)},
		})
	}

	for _, bm := range []struct {
		name string
		conf map[string]any
	}{
		{name: "disabled", conf: mainConfigMap(0, 0)},
		{name: "enabled", conf: mainConfigMap(int64(1)<<30, neverRejects)},
	} {
		factory, err := newTestFactory(bm.conf, customized, time.Now)
		if err != nil {
			b.Fatal(err)
		}

		b.Run(bm.name+"/same_token", func(b *testing.B) {
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				record := benchmarkRecord(define.RecordTraces, 1, 500, benchmarkAttrsPerItem)
				record.Token = define.Token{Original: "benchmark-same-token-" + bm.name}
				for pb.Next() {
					_, _ = factory.Process(record)
				}
			})
		})

		b.Run(bm.name+"/different_tokens", func(b *testing.B) {
			var sequence atomic.Uint64
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				record := benchmarkRecord(define.RecordTraces, 1, 500, benchmarkAttrsPerItem)
				token := fmt.Sprintf("benchmark-token-%s-%d", bm.name, sequence.Add(1))
				record.Token = define.Token{Original: token}
				for pb.Next() {
					_, _ = factory.Process(record)
				}
			})
		})

		factory.Clean()
	}
}
