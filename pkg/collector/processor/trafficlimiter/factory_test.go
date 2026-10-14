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

	// 本地模式从满桶开始；满桶刚好够两个原始请求，traces 权重为 2 时只够一个。
	// 透支线为 1 字节（速率 1 字节/秒），取空之后的请求即被拒绝。
	conf := policyMap(1, int64(size*2))
	conf["weights"] = map[string]any{"traces": 2.0}

	clock := newFakeClock()
	factory, err := newLocalFactory(conf, nil, clock.Now)
	require.NoError(t, err)
	defer factory.Clean()

	_, err = factory.Process(newRecord())
	require.NoError(t, err, "首个请求扣减 2 倍字节后刚好取空桶")
	_, err = factory.Process(newRecord())
	require.NoError(t, err, "余额为 0 仍在透支线内")
	_, err = factory.Process(newRecord())
	assert.Error(t, err, "权重放大后第三个请求就应超限，不加权时要第五个")

	// 指标记录的仍然是原始逻辑字节，不是计费字节。
	assert.Equal(
		t, float64(2*size),
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
	newRecord := func() (*define.Record, ptrace.Traces) {
		clone := ptrace.NewTraces()
		data.CopyTo(clone)
		return &define.Record{Token: define.Token{Original: token}, RecordType: define.RecordTraces, Data: clone}, clone
	}

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

	// 第一次两个服务都在透支线内放行；同步后 checkout 领到「用量 + 欠账」，
	// payment 的申请被 burst_bytes 封顶，还清不了欠账，下一次被拒。
	record, _ := newRecord()
	_, err = factory.Process(record)
	require.NoError(t, err)
	factory.syncNow()
	record, traces := newRecord()
	_, err = factory.Process(record)
	require.NoError(t, err)

	assert.Equal(
		t, float64(sizes["payment"]),
		testutil.ToFloat64(trafficLimiterBytesTotal.WithLabelValues(token, "payment", "traces", resultRejected)),
	)
	assert.Equal(
		t, float64(2*sizes["checkout"]),
		testutil.ToFloat64(trafficLimiterBytesTotal.WithLabelValues(token, "checkout", "traces", resultAccepted)),
	)
	require.Equal(t, 1, traces.ResourceSpans().Len())
	service, ok := traces.ResourceSpans().At(0).Resource().Attributes().Get(serviceNameKey)
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

		// 满桶只够 traces，metrics 在透支线内放行，logs 到来时同一服务的余额已低于透支线。
		clock := newFakeClock()
		factory, err := newLocalFactory(
			policyMap(1, int64(traceSize+metricSize-1)), nil, clock.Now,
		)
		require.NoError(t, err)
		defer factory.Clean()
		process := func(recordType define.RecordType, data any) error {
			_, err := factory.Process(&define.Record{
				Token: define.Token{Original: token}, RecordType: recordType, Data: data,
			})
			return err
		}
		require.NoError(t, process(define.RecordTraces, traces))
		require.NoError(t, process(define.RecordMetrics, metrics), "metrics 透支放行")
		assert.Error(t, process(define.RecordLogs, logs), "traces 与 metrics 已经用光同一个服务的余额")
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
	conf := policyMap(1, int64(size))
	factory, err := newLocalFactory(conf, nil, clock.Now)
	require.NoError(t, err)
	defer factory.Clean()

	_, err = factory.Process(record)
	require.NoError(t, err, "首个请求刚好取空满桶")
	_, err = factory.Process(record)
	require.NoError(t, err, "余额为 0 仍在透支线内")
	_, err = factory.Process(record)
	require.Error(t, err)

	factory.Reload(conf, nil)
	_, err = factory.Process(record)
	assert.Error(t, err, "重载相同配置不能重新发放满桶")

	factory.Reload(policyMap(0, 0), nil)
	_, err = factory.Process(record)
	assert.NoError(t, err, "配置放开后才允许通过")
}

// TestRequestsAndReloadNotBlockedBySync 守住「请求路径与 Reload 都不等 Redis」这条约束：
// 同步卡在 Redis 上时，请求只在本地余额上判断，Reload 也只替换快照、不等在途同步返回。
func TestRequestsAndReloadNotBlockedBySync(t *testing.T) {
	leaser := &fakeLeaser{entered: make(chan struct{}, 1), release: make(chan struct{})}
	conf := mainConfigMap(1024, 4096)
	factory, err := newFactoryWithLeaser(
		conf, nil, time.Now, func(RedisConfig) quotaLeaser { return leaser }, 0,
	)
	require.NoError(t, err)
	defer factory.Clean()

	_, err = factory.limitSnapshot("token", map[string]int{"checkout": 1})
	require.NoError(t, err)
	synced := make(chan struct{})
	go func() {
		defer close(synced)
		factory.syncNow()
	}()
	<-leaser.entered

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = factory.limitSnapshot("token", map[string]int{"checkout": 1})
		factory.Reload(conf, nil)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		// 用 Error 而不是 Fatal，让在途同步先退出，避免失败时把 Clean 一起拖进死锁。
		t.Error("请求或 Reload 被在途同步阻塞")
	}

	close(leaser.release)
	<-synced
	<-done
}

func TestTrafficLimiterUnsupportedRecord(t *testing.T) {
	factory, err := newTestFactory(mainConfigMap(1, 1), nil, time.Now)
	require.NoError(t, err)
	defer factory.Clean()

	_, err = factory.Process(&define.Record{RecordType: define.RecordProfiles})
	assert.NoError(t, err)
}

func TestQuotaBucketServiceIsolation(t *testing.T) {
	clock := newFakeClock()
	manager := newQuotaManager(clock.Now)
	allow := func(token, service string, cost int64, config Config) bool {
		return manager.Allow(token, service, cost, config, clock.Now(), false)
	}
	small := limiterConfig(2, 5) // 透支线 1

	assert.True(t, allow("token", "checkout", 20, limiterConfig(2, 20)))
	assert.True(t, allow("token", "payment", 6, small), "超过 burst 的单个请求在不欠账时放行")
	assert.False(t, allow("token", "payment", 1, small), "欠账超过透支线后拒绝")
	assert.True(t, allow("token", "checkout", 1, limiterConfig(2, 20)), "不同服务互不影响")
	assert.True(t, allow("another-token", "payment", 5, small), "不同 Token 互不影响")
}

func TestQuotaBucketLocalRefillAndConfigReset(t *testing.T) {
	clock := newFakeClock()
	manager := newQuotaManager(clock.Now)
	config := limiterConfig(10, 20) // 透支线 5
	allow := func(cost int64, config Config) bool {
		return manager.Allow("token", "checkout", cost, config, clock.Now(), false)
	}

	assert.True(t, allow(20, config), "本地模式从满桶开始")
	assert.True(t, allow(5, config), "余额 0 仍在透支线内，扣到 -5")
	assert.False(t, allow(1, config), "余额到达透支线")
	clock.Advance(time.Second)
	assert.True(t, allow(10, config), "补充 10 后余额回到 5，放行后扣到 -5")
	assert.False(t, allow(1, config))

	// 额度变化表示新策略，按约定重新创建满桶。
	enlarged := limiterConfig(10, 100)
	assert.True(t, allow(100, enlarged))
	assert.True(t, allow(5, enlarged))
	assert.False(t, allow(1, enlarged))
}

// TestQuotaBucketRefillKeepsFraction 覆盖高频小间隔的补充：不足 1 字节的时长必须累积，
// 否则每次都被向下取整为 0，余额永远补不回来。
func TestQuotaBucketRefillKeepsFraction(t *testing.T) {
	clock := newFakeClock()
	manager := newQuotaManager(clock.Now)
	config := limiterConfig(1, 2) // 透支线 1

	assert.True(t, manager.Allow("token", "checkout", 3, config, clock.Now(), false))
	assert.False(t, manager.Allow("token", "checkout", 1, config, clock.Now(), false))
	for i := 0; i < 10; i++ {
		clock.Advance(100 * time.Millisecond)
		manager.Allow("token", "checkout", 0, config, clock.Now(), false)
	}
	assert.True(t, manager.Allow("token", "checkout", 1, config, clock.Now(), false), "10 次 0.1s 应累积补充 1 字节")
}

// TestQuotaBucketSharedStartsEmpty 覆盖共享模式：余额只来自同步授予，冷启动靠透支线放行，
// 且共享期间的时长不会在切到本地模式后被折算成本地额度。
func TestQuotaBucketSharedStartsEmpty(t *testing.T) {
	clock := newFakeClock()
	manager := newQuotaManager(clock.Now)
	config := limiterConfig(10, 100) // 透支线 5

	assert.True(t, manager.Allow("token", "checkout", 5, config, clock.Now(), true))
	assert.False(t, manager.Allow("token", "checkout", 1, config, clock.Now(), true), "共享模式从 0 开始")

	clock.Advance(10 * time.Second)
	assert.False(t, manager.Allow("token", "checkout", 1, config, clock.Now(), true), "共享模式不在本地补充")

	// 切到本地模式只补充切换之后的 1 秒（10 字节，余额 5）；若把共享期间的 10 秒也算上，余额会到 100。
	clock.Advance(time.Second)
	assert.True(t, manager.Allow("token", "checkout", 10, config, clock.Now(), false))
	assert.False(t, manager.Allow("token", "checkout", 1, config, clock.Now(), false))
}

func TestQuotaManagerCollectAndApply(t *testing.T) {
	clock := newFakeClock()
	manager := newQuotaManager(clock.Now)
	config := limiterConfig(10, 100) // 透支线 5
	allow := func(service string, cost int64) bool {
		return manager.Allow("token", service, cost, config, clock.Now(), true)
	}

	assert.True(t, allow("checkout", 3))
	assert.True(t, allow("checkout", 4), "余额 -3 仍在透支线内")
	assert.False(t, allow("checkout", 1), "余额 -7 已越过透支线")
	allow("payment", 0) // 没有计费字节

	requests := manager.collect()
	require.Len(t, requests, 1, "只为有用量或欠账的服务申请")
	assert.Equal(t, serviceKey{token: "token", service: "checkout"}, requests[0].key)
	assert.Equal(t, int64(7+1+7), requests[0].want, "用量 + 被拒需求 + 欠账")

	manager.apply(requests, []int64{15})
	assert.True(t, allow("checkout", 5), "授予写回后余额为 8")
	assert.Equal(t, int64(5-3), manager.collect()[0].want, "预取一个周期的用量，扣掉手上的余额")
	assert.Empty(t, manager.collect(), "用量已清零、余额非负时不再申请")

	manager.apply([]leaseRequest{{key: requests[0].key, config: limiterConfig(1, 1), want: 1}}, []int64{1000})
	assert.True(t, allow("checkout", 8), "余额 3，放行后到 -5")
	assert.False(t, allow("checkout", 1), "额度已变化的授予必须丢弃")
}

func TestQuotaBucketWantCappedByBurst(t *testing.T) {
	clock := newFakeClock()
	manager := newQuotaManager(clock.Now)
	config := limiterConfig(10, 20)
	manager.Allow("token", "checkout", 1000, config, clock.Now(), true)
	assert.Equal(t, int64(20), manager.collect()[0].want, "单次申请不超过 burst_bytes")
}

func TestQuotaBucketSweep(t *testing.T) {
	clock := newFakeClock()
	manager := newQuotaManager(clock.Now)
	manager.idleTTL = time.Minute
	manager.sweepInterval = time.Minute

	assert.True(t, manager.Allow("token", "checkout", 1, limiterConfig(1, 10), clock.Now(), false))
	clock.Advance(2 * time.Minute)
	assert.True(t, manager.Allow("other", "checkout", 1, limiterConfig(1, 10), clock.Now(), false))
	assert.False(t, quotaBucketExists(manager, "token", "checkout"))
}

func TestQuotaBucketOverflow(t *testing.T) {
	clock := newFakeClock()
	config := limiterConfig(math.MaxInt64, math.MaxInt64)

	shared := newQuotaManager(clock.Now)
	assert.True(t, shared.Allow("token", "checkout", math.MaxInt64, config, clock.Now(), true))
	assert.False(t, shared.Allow("token", "checkout", math.MaxInt64, config, clock.Now(), true),
		"欠账封底在下限，下限不高于透支线")
	assert.Equal(t, int64(math.MaxInt64), shared.collect()[0].want, "饱和计算不得溢出成负数")

	local := newQuotaManager(clock.Now)
	assert.True(t, local.Allow("token", "checkout", math.MaxInt64, config, clock.Now(), false))
	assert.True(t, local.Allow("token", "checkout", math.MaxInt64, config, clock.Now(), false))
	clock.Advance(time.Second)
	assert.True(t, local.Allow("token", "checkout", math.MaxInt64, config, clock.Now(), false),
		"欠账在下限时补充不得溢出")
}

func TestQuotaBucketConcurrentLimit(t *testing.T) {
	clock := newFakeClock()
	manager := newQuotaManager(clock.Now)
	config := limiterConfig(2, 1000) // 透支线 1：满桶 1000，最多放行到余额 -9

	var accepted atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if manager.Allow("token", "checkout", 10, config, clock.Now(), false) {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int64(101), accepted.Load())
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

// fakeLeaser 默认授予全部申请量；entered/release 非空时在 Lease 中阻塞，用来模拟卡住的 Redis。
type fakeLeaser struct {
	mu      sync.Mutex
	err     error
	entered chan struct{}
	release chan struct{}
}

func (l *fakeLeaser) setErr(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.err = err
}

func (l *fakeLeaser) Lease(_ context.Context, requests []leaseRequest) ([]int64, error) {
	if l.entered != nil {
		l.entered <- struct{}{}
		<-l.release
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return nil, l.err
	}
	grants := make([]int64, len(requests))
	for i, request := range requests {
		grants[i] = request.want
	}
	return grants, nil
}

func (l *fakeLeaser) Close() error { return nil }

// newTestFactory 使用授予全部申请量的假 Redis，且不启动后台同步：共享模式下余额只在 syncNow 时补充。
func newTestFactory(
	conf map[string]any,
	customized []processor.SubConfigProcessor,
	now func() time.Time,
) (*trafficLimiter, error) {
	return newFactoryWithLeaser(
		conf, customized, now, func(RedisConfig) quotaLeaser { return &fakeLeaser{} }, 0,
	)
}

// newLocalFactory 构造本地模式的限流器：redis 段非法，额度保留但不建立客户端，余额按速率在本地补充。
// 用于直接验证满桶、补充与透支线的语义，结果与时间以外的因素无关。
func newLocalFactory(
	conf map[string]any,
	customized []processor.SubConfigProcessor,
	now func() time.Time,
) (*trafficLimiter, error) {
	conf["redis"] = map[string]any{"mode": redisModeStandalone}
	return newTestFactory(conf, customized, now)
}

// limitSnapshot 按请求路径的方式取快照后判断额度，供直接验证限流语义的用例使用。
func (p *trafficLimiter) limitSnapshot(token string, sizes map[string]int) (limitDecision, error) {
	view := p.snapshot()
	return p.limit(view.configs, view.redis, token, define.RecordTraces, sizes)
}

// syncNow 立即执行一次后台同步，供不启动同步 goroutine 的用例手动驱动。
func (p *trafficLimiter) syncNow() {
	if syncer := p.snapshot().redis; syncer != nil {
		syncer.sync()
	}
}

func quotaBucketExists(manager *quotaManager, token, service string) bool {
	key := serviceKey{token: token, service: service}
	shard := &manager.shards[shardIndex(token, service)]
	shard.mu.Lock()
	defer shard.mu.Unlock()
	_, ok := shard.buckets[key]
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
