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

// TestRequestsAndReloadNotBlockedByRedis 守住「Redis 卡住不能拖住请求与 Reload」这条约束：
// 请求为补货最多等 defaultLeaseWaitBudget，超时后按本地降级判断；Reload 只替换快照，不等在途租约返回。
func TestRequestsAndReloadNotBlockedByRedis(t *testing.T) {
	leaser := &fakeLeaser{entered: make(chan struct{}, 1), release: make(chan struct{})}
	conf := mainConfigMap(1024, 4096)
	factory, err := newFactoryWithLeaser(
		conf, nil, time.Now, func(RedisConfig) quotaLeaser { return leaser }, 0,
	)
	require.NoError(t, err)
	defer factory.Clean()

	done := make(chan limitDecision)
	go func() {
		decision, _ := factory.limitSnapshot("token", map[string]int{"checkout": 1})
		done <- decision
	}()
	<-leaser.entered

	reloaded := make(chan struct{})
	go func() {
		defer close(reloaded)
		factory.Reload(conf, nil)
	}()
	select {
	case decision := <-done:
		assert.True(t, decision.accepted, "等待超时后按欠账放行")
	case <-time.After(10 * time.Second):
		t.Error("请求被卡住的 Redis 阻塞")
	}
	select {
	case <-reloaded:
	case <-time.After(10 * time.Second):
		// 用 Error 而不是 Fatal，让在途租约先退出，避免失败时把 Clean 一起拖进死锁。
		t.Error("Reload 被在途租约阻塞")
	}
	close(leaser.release)
}

func TestTrafficLimiterUnsupportedRecord(t *testing.T) {
	factory, err := newTestFactory(mainConfigMap(1, 1), nil, time.Now)
	require.NoError(t, err)
	defer factory.Clean()

	_, err = factory.Process(&define.Record{RecordType: define.RecordProfiles})
	assert.NoError(t, err)
}

func TestQuotaBucketLocalOnly(t *testing.T) {
	clock := newFakeClock()
	manager := newQuotaManager(clock.Now)
	allow := func(service string, cost int64, config Config) bool {
		result, _ := manager.Admit(testKey(service), cost, config, clock.Now(), quotaLocalOnly, false)
		return result == admitAccepted
	}

	config := limiterConfig(5, 10)
	assert.True(t, allow("checkout", 10, config), "本地模式从满桶开始")
	assert.False(t, allow("checkout", 1, config))
	assert.True(t, allow("payment", 10, config), "不同服务互不影响")
	clock.Advance(time.Second)
	assert.True(t, allow("checkout", 5, config), "按速率补充")
	assert.False(t, allow("checkout", 1, config))

	// 额度变化表示新策略，按约定重新创建满桶。
	enlarged := limiterConfig(5, 100)
	assert.True(t, allow("checkout", 100, enlarged))
	assert.False(t, allow("checkout", 1, enlarged))
}

// TestQuotaBucketRefillKeepsFraction 覆盖高频小间隔的补充：不足 1 字节的时长必须累积，
// 否则每次都被向下取整为 0，余额永远补不回来。
func TestQuotaBucketRefillKeepsFraction(t *testing.T) {
	clock := newFakeClock()
	manager := newQuotaManager(clock.Now)
	config := limiterConfig(1, 2)
	admit := func(cost int64) admitResult {
		result, _ := manager.Admit(testKey("checkout"), cost, config, clock.Now(), quotaLocalOnly, false)
		return result
	}

	assert.Equal(t, admitAccepted, admit(2))
	assert.Equal(t, admitRejected, admit(1))
	for i := 0; i < 10; i++ {
		clock.Advance(100 * time.Millisecond)
		admit(0)
	}
	assert.Equal(t, admitAccepted, admit(1), "10 次 0.1s 应累积补充 1 字节")
}

// TestQuotaBucketSharedOverdraft 覆盖共享模式：余额来自租约，不足时先等一次补货；
// 被限流或补货后仍不足时在透支上限之内放行，超出上限的请求被拒且不扣余额。
func TestQuotaBucketSharedOverdraft(t *testing.T) {
	clock := newFakeClock()
	manager := newQuotaManager(clock.Now)
	config := limiterConfig(100, 1000) // 透支上限 min(100 × 0.2s, 1000) = 20
	key := testKey("checkout")
	admit := func(cost int64, retry bool) admitResult {
		result, _ := manager.Admit(key, cost, config, clock.Now(), quotaShared, retry)
		return result
	}
	lease := func(request leaseRequest, result leaseResult) {
		request.key, request.config = key, config
		result.peers = 1
		manager.apply(clock.Now(), []leaseRequest{request}, []leaseResult{result})
	}

	assert.Equal(t, admitInsufficient, admit(10, false), "共享模式从 0 开始，先等一次租约")
	lease(leaseRequest{want: 30, need: 10}, leaseResult{grant: 30})
	assert.Equal(t, admitAccepted, admit(25, false))
	assert.Equal(t, admitInsufficient, admit(10, false), "没被限流时先等补货，不急于透支")
	assert.Equal(t, admitAccepted, admit(25, true), "补货后仍不足，在透支上限之内放行：余额 5 → -20")
	assert.Equal(t, admitRejected, admit(1, true), "透支已到上限")

	// 额度真正紧张：退避一个需求更新粒度，期间不足的请求只能用透支空间。
	lease(leaseRequest{want: 50, demand: 100}, leaseResult{grant: 30, share: 50, scarce: true})
	assert.Equal(t, admitAccepted, admit(5, false), "欠账从授予中扣回：-20 + 30 = 10 → 5")
	assert.Equal(t, admitRejected, admit(26, false), "被限流时只能用透支空间，超出上限的请求被拒")
	assert.Equal(t, admitAccepted, admit(25, false), "被拒的请求不扣余额，5 → -20")
	clock.Advance(demandResponseWindow - time.Millisecond)
	assert.Equal(t, admitRejected, admit(1, false))
	clock.Advance(time.Millisecond)
	assert.Equal(t, admitInsufficient, admit(1, false), "退避结束后可以再等一次补货")

	// 只是投机性的预取没领满：真实需要不受影响，仍可以等待补货，只暂停异步预取。
	lease(leaseRequest{want: 60, debt: 20}, leaseResult{grant: 40, share: 50})
	assert.Equal(t, admitInsufficient, admit(21, false), "预取没领满不挡住真实请求的等待")

	// 连真实需要都没满足：之后的请求不再等待（缺口 30 字节 / 100 B/s = 300ms）。
	lease(leaseRequest{want: 60, need: 40}, leaseResult{grant: 10, share: 50})
	assert.Equal(t, admitAccepted, admit(30, false), "余额 20 + 10 = 30")
	assert.Equal(t, admitRejected, admit(21, false), "被限流，超出透支空间")
	clock.Advance(300 * time.Millisecond)
	assert.Equal(t, admitInsufficient, admit(21, false))

	lease(leaseRequest{want: 30}, leaseResult{grant: 30})
	assert.Equal(t, admitAccepted, admit(30, false), "足额授予后解除限流")
}

func TestQuotaBucketAsyncRefillWatermark(t *testing.T) {
	clock := newFakeClock()
	manager := newQuotaManager(clock.Now)
	config := limiterConfig(1000, 10000)
	key := testKey("checkout")

	manager.Admit(key, 100, config, clock.Now(), quotaShared, false)
	manager.apply(clock.Now(), []leaseRequest{{key: key, config: config, want: 300}}, []leaseResult{{grant: 300, peers: 1}})
	// 本周期已到达 200 字节，需求估计 400 B/s，预取目标 300，水位 75。
	_, refill := manager.Admit(key, 100, config, clock.Now(), quotaShared, false)
	assert.False(t, refill, "余额 200 仍高于水位")
	// 已到达 350 字节，预取目标 525，水位 131。
	_, refill = manager.Admit(key, 150, config, clock.Now(), quotaShared, false)
	assert.True(t, refill, "余额 50 低于水位时触发异步补货")
	_, refill = manager.Admit(key, 1, config, clock.Now(), quotaShared, false)
	assert.False(t, refill, "已提交的补货返回前不重复提交")
}

func TestQuotaManagerPrepare(t *testing.T) {
	clock := newFakeClock()
	manager := newQuotaManager(clock.Now)
	config := limiterConfig(10000, 100000)
	key := testKey("checkout")
	keys := map[serviceKey]struct{}{key: {}}
	admit := func(cost int64) {
		manager.Admit(key, cost, config, clock.Now(), quotaShared, false)
	}

	admit(200)
	ticket, granted := manager.Enqueue(key, config, 200)
	require.False(t, granted)
	clock.Advance(200 * time.Millisecond)
	requests := manager.prepare(clock.Now(), false, keys, true)
	require.Len(t, requests, 1)
	assert.Equal(t, int64(1000), requests[0].demand, "本周期 200ms 到达 200 字节")
	assert.Equal(t, int64(200), requests[0].want, "还不知道实例数，只领真实需要，不预取")
	assert.Equal(t, int64(200), requests[0].need)
	manager.apply(clock.Now(), requests, []leaseResult{{grant: 200, peers: 1}})

	requests = manager.prepare(clock.Now(), false, keys, true)
	require.Len(t, requests, 1)
	assert.Equal(t, int64(750), requests[0].want,
		"知道实例数后按 1000 B/s × 0.75s 预取；授予已经兑现给等待者，不算在余额里")
	assert.Zero(t, requests[0].need)
	manager.apply(clock.Now(), requests, []leaseResult{{grant: 750, peers: 1}})
	result, _ := manager.Settle(key, config, ticket, 200, clock.Now(), quotaShared)
	assert.Equal(t, admitAccepted, result, "等待者醒来时额度已经兑现")
	assert.Equal(t, int64(750), quotaBucketBalance(manager, key), "兑现过的等待者不再扣第二次")

	clock.Advance(300 * time.Millisecond)
	requests = manager.prepare(clock.Now(), true, nil, true)
	require.Len(t, requests, 1)
	assert.Equal(t, int64(400), requests[0].demand, "定时同步按整个周期结算：0.5s 到达 200 字节")
	assert.Equal(t, int64(300-750), requests[0].want, "余额超过预取目标两倍时退还多余的部分")
	manager.abort(requests)

	admit(100)
	assert.Empty(t, manager.prepare(clock.Now(), false, keys, false), "不健康时按需补货不领额度")
	requests = manager.prepare(clock.Now(), true, nil, false)
	require.Len(t, requests, 1, "不健康时定时同步仍上报需求")
	assert.Zero(t, requests[0].want)

	clock.Advance(defaultSyncInterval)
	requests = manager.prepare(clock.Now(), true, nil, true)
	require.Len(t, requests, 1)
	assert.Zero(t, requests[0].demand, "没有流量的周期需求降为 0")
	assert.Equal(t, int64(-650), requests[0].want, "需求消失后退还全部余额")
}

func TestLeaseRequestRequired(t *testing.T) {
	assert.Equal(t, int64(30), leaseRequest{want: 100, need: 10, debt: 20}.required(), "等待中的请求加透支欠账")
	assert.Equal(t, int64(50), leaseRequest{want: 50, need: 40, debt: 20}.required(), "不超过申请量")
	assert.Zero(t, leaseRequest{want: -10, need: 5}.required(), "退还时没有必须满足的部分")
}

// TestQuotaManagerWaitersSettled 覆盖等待者的兑现与撤销：任何一轮写回都按到达顺序兑现等待者，
// 余额已够时登记即兑现；没被兑现的等待者离开时撤下票据，need 不会累积，之后也不会因此多预取。
func TestQuotaManagerWaitersSettled(t *testing.T) {
	clock := newFakeClock()
	manager := newQuotaManager(clock.Now)
	config := limiterConfig(10000, 100000) // 透支上限 min(10000 × 0.2s, 100000) = 2000
	key := testKey("checkout")
	keys := map[serviceKey]struct{}{key: {}}

	manager.Admit(key, 100, config, clock.Now(), quotaShared, false)
	ticket, granted := manager.Enqueue(key, config, 100)
	require.False(t, granted)
	assert.Equal(t, int64(100), quotaBucketNeed(manager, key))
	// 登记等待之前生成的另一轮写回了 750：直接兑现给等待者。
	manager.apply(clock.Now(), []leaseRequest{{key: key, config: config, want: 750}}, []leaseResult{{grant: 750, peers: 1}})
	assert.Zero(t, quotaBucketNeed(manager, key))
	assert.Equal(t, int64(650), quotaBucketBalance(manager, key))
	result, _ := manager.Settle(key, config, ticket, 100, clock.Now(), quotaShared)
	assert.Equal(t, admitAccepted, result)
	assert.Equal(t, int64(650), quotaBucketBalance(manager, key), "兑现过的等待者不再扣第二次")

	_, granted = manager.Enqueue(key, config, 100)
	assert.True(t, granted, "余额已够时登记即兑现，不必再等")
	assert.Equal(t, int64(550), quotaBucketBalance(manager, key))

	// 付不起的等待者：这一轮的申请必须覆盖它，它离开时撤下票据。
	big, granted := manager.Enqueue(key, config, 1000)
	require.False(t, granted)
	requests := manager.prepare(clock.Now(), false, keys, true)
	require.Len(t, requests, 1)
	assert.Equal(t, int64(1000), requests[0].need)
	manager.abort(requests)
	result, _ = manager.Settle(key, config, big, 1000, clock.Now(), quotaShared)
	assert.Equal(t, admitAccepted, result, "没被兑现时在透支上限之内放行：550 → -450")
	assert.Zero(t, quotaBucketNeed(manager, key), "离开的等待者撤下票据，need 不累积")
	assert.Equal(t, int64(-450), quotaBucketBalance(manager, key))
}

// TestQuotaManagerGrantedWaitersAreReserved 覆盖并发压测里限额之内 429 的根因：授予写回之后、
// 等待者被唤醒扣款之前，这笔额度不能被下一轮当成空闲余额、不能被当成富余退回、也不能被后到的请求拿走。
func TestQuotaManagerGrantedWaitersAreReserved(t *testing.T) {
	config := limiterConfig(1000, 10000)
	key := testKey("checkout")
	keys := map[serviceKey]struct{}{key: {}}
	enqueue := func(t *testing.T, manager *quotaManager, now time.Time, cost int64) *waitTicket {
		t.Helper()
		result, _ := manager.Admit(key, cost, config, now, quotaShared, false)
		require.Equal(t, admitInsufficient, result)
		ticket, granted := manager.Enqueue(key, config, cost)
		require.False(t, granted)
		return ticket
	}
	settle := func(t *testing.T, manager *quotaManager, now time.Time, tickets ...*waitTicket) {
		t.Helper()
		for _, ticket := range tickets {
			result, _ := manager.Settle(key, config, ticket, ticket.cost, now, quotaShared)
			assert.Equal(t, admitAccepted, result)
		}
		assert.GreaterOrEqual(t, quotaBucketBalance(manager, key), int64(0), "限额之内不应动用透支")
	}

	t.Run("下一轮按还没兑现的等待者申请", func(t *testing.T) {
		clock := newFakeClock()
		manager := newQuotaManager(clock.Now)
		first := enqueue(t, manager, clock.Now(), 100)
		round1 := manager.prepare(clock.Now(), false, keys, true)
		require.Len(t, round1, 1)
		second := enqueue(t, manager, clock.Now(), 100) // 第一轮在途时到达
		// peers 很大让预取目标（C / 2n = 50）小于一个请求，申请量只由等待者决定。
		manager.apply(clock.Now(), round1, []leaseResult{{grant: round1[0].want, peers: 100}})

		// 补货泵紧接着开始第二轮，first 还没被唤醒扣款。
		round2 := manager.prepare(clock.Now(), false, keys, true)
		require.Len(t, round2, 1, "已许给 first 的额度不能抵掉 second 的需要")
		assert.Equal(t, int64(100), round2[0].want)
		manager.apply(clock.Now(), round2, []leaseResult{{grant: round2[0].want, peers: 100}})
		settle(t, manager, clock.Now(), first, second)
	})

	t.Run("需求下降的退还不拿走等待者的额度", func(t *testing.T) {
		clock := newFakeClock()
		manager := newQuotaManager(clock.Now)
		var tickets []*waitTicket
		for i := 0; i < 10; i++ {
			tickets = append(tickets, enqueue(t, manager, clock.Now(), 100))
		}
		round1 := manager.prepare(clock.Now(), false, keys, true)
		require.Len(t, round1, 1)
		// 20 个存活实例：预取目标 C / 2n = 250，旧实现会把还没扣走的 1000 当成超过两倍目标的富余退掉 750。
		manager.apply(clock.Now(), round1, []leaseResult{{grant: 1000, peers: 20}})

		assert.Empty(t, manager.surplus(clock.Now()), "余额里没有富余")
		for _, request := range manager.prepare(clock.Now(), false, keys, true) {
			assert.Positive(t, request.want, "只补预取，不退还")
		}
		settle(t, manager, clock.Now(), tickets...)
	})

	t.Run("后到的请求不插到等待者前面", func(t *testing.T) {
		clock := newFakeClock()
		manager := newQuotaManager(clock.Now)
		waiter := enqueue(t, manager, clock.Now(), 100)
		round := manager.prepare(clock.Now(), false, keys, true)
		manager.apply(clock.Now(), round, []leaseResult{{grant: 100, peers: 100}})

		result, _ := manager.Admit(key, 100, config, clock.Now(), quotaShared, false)
		assert.Equal(t, admitInsufficient, result, "这笔额度已经属于等待者，后到的请求要自己等补货")
		settle(t, manager, clock.Now(), waiter)
	})
}

// TestQuotaBucketStalledAdmitsOnDebt 覆盖 Redis 变慢时的判断：不再等待，余额不够时按共享桶最近一次的可用量
// 加上之后补回的量先欠着放行；额度真正紧张（被限流）时仍只有透支空间；从没拿到过可用量时按满桶计。
func TestQuotaBucketStalledAdmitsOnDebt(t *testing.T) {
	clock := newFakeClock()
	manager := newQuotaManager(clock.Now)
	config := limiterConfig(1000, 1000) // 透支上限 min(1000 × 0.2s, 1000 / 1) = 200
	key := testKey("checkout")
	admit := func(cost int64, mode quotaMode) admitResult {
		result, _ := manager.Admit(key, cost, config, clock.Now(), mode, false)
		return result
	}

	assert.Equal(t, admitAccepted, admit(900, quotaStalled), "还没拿到过可用量，按满桶计：余额 0 → -900")
	assert.Equal(t, admitRejected, admit(200, quotaStalled), "欠账不超过桶容量")

	// 一轮租约还清欠账并告诉本实例：共享桶此后还剩 300。
	manager.apply(clock.Now(), []leaseRequest{{key: key, config: config, want: 900}},
		[]leaseResult{{grant: 900, peers: 1, available: 300}})
	assert.Equal(t, int64(0), quotaBucketBalance(manager, key))
	assert.Equal(t, admitInsufficient, admit(100, quotaShared), "健康时照常等一次补货")
	assert.Equal(t, admitAccepted, admit(300, quotaStalled), "变慢时不等待，按可用量 300 先欠着")
	assert.Equal(t, admitRejected, admit(100, quotaStalled), "超出可用量与透支上限")
	clock.Advance(200 * time.Millisecond)
	assert.Equal(t, admitAccepted, admit(200, quotaStalled), "共享桶按速率补回：300 + 1000 × 0.2s = 500")

	// 额度真正紧张：只剩透支空间，不按可用量放大。
	manager.apply(clock.Now(), []leaseRequest{{key: key, config: config, want: 600, demand: 1000}},
		[]leaseResult{{grant: 500, peers: 1, share: 500, scarce: true, available: 1000}})
	assert.Equal(t, int64(0), quotaBucketBalance(manager, key), "-500 + 500")
	assert.Equal(t, admitAccepted, admit(200, quotaStalled), "透支到上限 200")
	assert.Equal(t, admitRejected, admit(1, quotaStalled), "被限流时不按可用量欠账")
}

// TestSyncerStates 覆盖补货泵对 Redis 的三档判断：等待超时记为变慢（请求按欠账放行），一轮快的租约恢复；
// 租约出错记为故障（本地降级）；故障之后成功但不够快的一轮记为变慢，而不是一直停在本地降级。
func TestSyncerStates(t *testing.T) {
	clock := newFakeClock()
	leaser := &fakeLeaser{}
	slow := &delayLeaser{quotaLeaser: leaser}
	quota := newQuotaManager(clock.Now)
	syncer := newQuotaSyncer(slow, quota, clock.Now, 0)
	defer syncer.Close()
	config := limiterConfig(1<<20, 1<<20)
	key := testKey("checkout")

	result, _ := quota.Admit(key, 100, config, clock.Now(), quotaShared, false)
	require.Equal(t, admitInsufficient, result)
	ticket, _ := quota.Enqueue(key, config, 100)
	slow.set(2 * syncer.waitBudget)
	assert.Equal(t, quotaStalled, syncer.await([]serviceKey{key}), "等不到补货：按欠账放行")
	assert.Equal(t, redisSlow, syncer.state(clock.Now()))
	result, _ = quota.Settle(key, config, ticket, 100, clock.Now(), quotaStalled)
	assert.Equal(t, admitAccepted, result)

	slow.set(0)
	time.Sleep(3 * syncer.waitBudget) // 等那一轮慢租约写回
	syncer.syncNow()
	assert.Equal(t, redisHealthy, syncer.state(clock.Now()), "一轮快的租约之后恢复等待")
	assert.GreaterOrEqual(t, quotaBucketBalance(quota, key), int64(0), "慢租约回来后欠账已经还上")

	leaser.setErr(context.DeadlineExceeded)
	syncer.syncNow()
	assert.Equal(t, redisFailed, syncer.state(clock.Now()), "租约出错：本地降级")

	leaser.setErr(nil)
	slow.set(syncer.healthyLatency + 10*time.Millisecond)
	syncer.syncNow()
	assert.Equal(t, redisSlow, syncer.state(clock.Now()), "Redis 恢复应答但不够快：按共享额度记账，不再停在本地降级")

	slow.set(0)
	clock.Advance(defaultSyncStaleAfter + time.Millisecond)
	assert.Equal(t, redisFailed, syncer.state(clock.Now()), "长时间没有同步成功按故障处理")
}

// delayLeaser 给每次租约加一段可调的时延。
type delayLeaser struct {
	quotaLeaser
	mu    sync.Mutex
	delay time.Duration
}

func (l *delayLeaser) set(delay time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.delay = delay
}

func (l *delayLeaser) Lease(ctx context.Context, requests []leaseRequest) ([]leaseResult, error) {
	l.mu.Lock()
	delay := l.delay
	l.mu.Unlock()
	time.Sleep(delay)
	return l.quotaLeaser.Lease(ctx, requests)
}

func TestQuotaBucketFallback(t *testing.T) {
	clock := newFakeClock()
	manager := newQuotaManager(clock.Now)
	config := limiterConfig(100, 100)
	admit := func(service string, cost int64, mode quotaMode) admitResult {
		result, _ := manager.Admit(testKey(service), cost, config, clock.Now(), mode, false)
		return result
	}

	// 没领到过额度的服务切到本地降级时从满桶开始，冷启动遇到 Redis 卡住也不丢数据。
	assert.Equal(t, admitInsufficient, admit("cold", 50, quotaShared))
	assert.Equal(t, admitAccepted, admit("cold", 50, quotaFallback))

	// 领到过额度的服务保留余额，按 max(公平份额, L/n) 补充。
	key := testKey("warm")
	admit("warm", 1, quotaShared)
	manager.apply(clock.Now(), []leaseRequest{{key: key, config: config, want: 10}}, []leaseResult{{grant: 10, peers: 4, share: 10}})
	assert.Equal(t, admitAccepted, admit("warm", 10, quotaFallback))
	assert.Equal(t, admitRejected, admit("warm", 1, quotaFallback))
	clock.Advance(time.Second)
	assert.Equal(t, admitAccepted, admit("warm", 25, quotaFallback), "份额 10 < L/n = 25，按 25 补充")
	assert.Equal(t, admitRejected, admit("warm", 1, quotaFallback))

	assert.Equal(t, admitInsufficient, admit("warm", 1, quotaShared), "回到共享模式时清零本地补充的余额")
}

func TestQuotaBucketSweep(t *testing.T) {
	clock := newFakeClock()
	manager := newQuotaManager(clock.Now)
	manager.idleTTL = time.Minute
	manager.sweepInterval = time.Minute

	manager.Admit(testKey("checkout"), 1, limiterConfig(1, 10), clock.Now(), quotaLocalOnly, false)
	clock.Advance(2 * time.Minute)
	manager.Admit(serviceKey{token: "other", service: "checkout"}, 1, limiterConfig(1, 10), clock.Now(), quotaLocalOnly, false)
	assert.False(t, quotaBucketExists(manager, testKey("checkout")))
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

func TestQuotaBucketConcurrentLimit(t *testing.T) {
	clock := newFakeClock()
	manager := newQuotaManager(clock.Now)
	config := limiterConfig(1, 1000)

	var accepted atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, _ := manager.Admit(testKey("checkout"), 10, config, clock.Now(), quotaLocalOnly, false)
			if result == admitAccepted {
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

// memoryLeaser 是单实例的内存共享桶，按假时钟实现与 Lua 相同的 GCRA 授予与退还，
// 用来在不依赖 Redis 的情况下验证共享模式的限流语义。
type memoryLeaser struct {
	mu   sync.Mutex
	now  func() time.Time
	tats map[serviceKey]int64
}

func (l *memoryLeaser) Lease(_ context.Context, requests []leaseRequest) ([]leaseResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now().UnixMicro()
	results := make([]leaseResult, len(requests))
	for i, request := range requests {
		rate := float64(request.config.BytesPerSecond)
		tat := max(l.tats[request.key], now)
		var grant int64
		switch {
		case request.want < 0:
			tat = max(now, tat-int64(float64(-request.want)*1e6/rate))
			grant = request.want
		case request.want > 0:
			capacity := float64(request.config.leaseCapacity())
			available := int64((float64(now) + math.Ceil(capacity*1e6/rate) - float64(tat)) * rate / 1e6)
			grant = max(min(request.want, available), 0)
			tat += int64(math.Ceil(float64(grant) * 1e6 / rate))
		}
		l.tats[request.key] = tat
		results[i] = leaseResult{grant: grant, peers: 1, share: request.demand}
	}
	return results, nil
}

func (l *memoryLeaser) Close() error { return nil }

// fakeLeaser 默认授予全部申请量，用于验证补货泵的调用行为：
// limit 大于 0 时每次最多授予 limit；entered/release 非空时在 Lease 中阻塞，用来模拟卡住的 Redis。
type fakeLeaser struct {
	mu       sync.Mutex
	err      error
	limit    int64
	calls    int
	requests []leaseRequest
	entered  chan struct{}
	release  chan struct{}
}

func (l *fakeLeaser) setErr(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.err = err
}

func (l *fakeLeaser) setLimit(limit int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.limit = limit
}

func (l *fakeLeaser) callCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

func (l *fakeLeaser) lastRequests() []leaseRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.requests
}

func (l *fakeLeaser) Lease(_ context.Context, requests []leaseRequest) ([]leaseResult, error) {
	if l.entered != nil {
		// 只需要第一次进入的信号；之后的调用（补货、退出时的归还）不能卡在信号上。
		select {
		case l.entered <- struct{}{}:
		default:
		}
		<-l.release
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	l.requests = requests
	if l.err != nil {
		return nil, l.err
	}
	results := make([]leaseResult, len(requests))
	for i, request := range requests {
		grant := request.want
		if l.limit > 0 && grant > l.limit {
			grant = l.limit
		}
		results[i] = leaseResult{grant: grant, peers: 1, share: request.demand}
	}
	return results, nil
}

func (l *fakeLeaser) Close() error { return nil }

// newTestFactory 使用内存共享桶，且不启动定时同步：余额只来自紧急补货与手动的 syncNow。
// 同一个工厂内重建客户端（Reload、Clean 后自愈）共用同一个内存桶，与共用同一个 Redis 一致。
func newTestFactory(
	conf map[string]any,
	customized []processor.SubConfigProcessor,
	now func() time.Time,
) (*trafficLimiter, error) {
	leaser := &memoryLeaser{now: now, tats: make(map[serviceKey]int64)}
	return newFactoryWithLeaser(conf, customized, now, func(RedisConfig) quotaLeaser { return leaser }, 0)
}

// limitSnapshot 按请求路径的方式取快照后判断额度，供直接验证限流语义的用例使用。
func (p *trafficLimiter) limitSnapshot(token string, sizes map[string]int) (limitDecision, error) {
	view := p.snapshot()
	return p.limit(view.configs, view.redis, token, define.RecordTraces, sizes)
}

// syncNow 立即执行一次定时同步，供不启动定时同步的用例手动驱动。
func (p *trafficLimiter) syncNow() {
	if syncer := p.snapshot().redis; syncer != nil {
		syncer.syncNow()
	}
}

// checkSurplusNow 立即扫描一次需求下降并等退还完成，供不启动后台扫描的用例手动驱动。
func (p *trafficLimiter) checkSurplusNow() {
	syncer := p.snapshot().redis
	if syncer == nil || !syncer.shared(syncer.now()) {
		return
	}
	if keys := p.quota.surplus(syncer.now()); len(keys) > 0 {
		<-syncer.request(keys, true).done
	}
}

func testKey(service string) serviceKey {
	return serviceKey{token: "token", service: service}
}

func quotaBucketNeed(manager *quotaManager, key serviceKey) int64 {
	shard := manager.shard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	return shard.buckets[key].need
}

func quotaBucketBalance(manager *quotaManager, key serviceKey) int64 {
	shard := manager.shard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	return shard.buckets[key].balance
}

func quotaBucketExists(manager *quotaManager, key serviceKey) bool {
	shard := manager.shard(key)
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
