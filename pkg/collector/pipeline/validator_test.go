// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/trafficlimiter"
)

type noneValidator struct{}

func (noneValidator) GetProcessor(name string) processor.Instance {
	return nil
}

type trafficLimiterGetter struct {
	instance processor.Instance
	pipeline Pipeline
}

func (g trafficLimiterGetter) GetProcessor(string) processor.Instance {
	return g.instance
}

func (g trafficLimiterGetter) GetPipeline(define.RecordType) Pipeline {
	return g.pipeline
}

func TestValidateTrafficLimiter(t *testing.T) {
	redisServer := miniredis.RunT(t)
	data := ptrace.NewTraces()
	resourceSpans := data.ResourceSpans().AppendEmpty()
	resourceSpans.Resource().Attributes().PutString("service.name", "checkout")
	resourceSpans.ScopeSpans().AppendEmpty().Spans().AppendEmpty().SetName("large-span")
	record := &define.Record{
		Token:      define.Token{Original: "token"},
		RecordType: define.RecordTraces,
		Data:       data,
	}

	newGetter := func(burst int64) (trafficLimiterGetter, processor.Processor) {
		p, err := trafficlimiter.NewFactory(map[string]any{
			"bytes_per_second": int64(1),
			"burst_bytes":      burst,
			"redis": map[string]any{
				"mode":  "standalone",
				"db":    0,
				"key":   "test.traffic_limiter",
				"addrs": []string{redisServer.Addr()},
			},
		}, nil)
		require.NoError(t, err)
		instance := processor.NewInstance("traffic_limiter/gcra", p)
		return trafficLimiterGetter{
			instance: instance,
			pipeline: NewPipeline("traces", define.RecordTraces, instance),
		}, p
	}

	t.Run("too many requests", func(t *testing.T) {
		getter, p := newGetter(1)
		defer p.Clean()
		code, name, err := validatePreCheckProcessors(record, getter)
		assert.Equal(t, define.StatusCodeTooManyRequests, code)
		assert.Equal(t, define.ProcessorTrafficLimiter, name)
		assert.Error(t, err)
	})

	t.Run("accepted", func(t *testing.T) {
		getter, p := newGetter(1024)
		defer p.Clean()
		code, name, err := validatePreCheckProcessors(record, getter)
		assert.Equal(t, define.StatusCodeOK, code)
		assert.Empty(t, name)
		assert.NoError(t, err)
	})

	t.Run("partially accepted", func(t *testing.T) {
		redisServer.FlushAll()
		mixed := ptrace.NewTraces()
		checkout := mixed.ResourceSpans().AppendEmpty()
		checkout.Resource().Attributes().PutString("service.name", "checkout")
		checkout.ScopeSpans().AppendEmpty().Spans().AppendEmpty().SetName("checkout-span")
		payment := mixed.ResourceSpans().AppendEmpty()
		payment.Resource().Attributes().PutString("service.name", "payment")
		payment.ScopeSpans().AppendEmpty().Spans().AppendEmpty().SetName("payment-span")
		mixedRecord := &define.Record{
			Token:      define.Token{Original: "token"},
			RecordType: define.RecordTraces,
			Data:       mixed,
		}

		p, err := trafficlimiter.NewFactory(map[string]any{
			"bytes_per_second": int64(1),
			"burst_bytes":      int64(1024),
			"redis": map[string]any{
				"mode":  "standalone",
				"db":    0,
				"key":   "test.traffic_limiter",
				"addrs": []string{redisServer.Addr()},
			},
		}, []processor.SubConfigProcessor{{
			Token: "token",
			Type:  define.SubConfigFieldService,
			ID:    "payment",
			Config: processor.Config{Config: map[string]any{
				"bytes_per_second": int64(1),
				"burst_bytes":      int64(1),
			}},
		}})
		require.NoError(t, err)
		defer p.Clean()
		instance := processor.NewInstance("traffic_limiter/gcra", p)
		getter := trafficLimiterGetter{
			instance: instance,
			pipeline: NewPipeline("traces", define.RecordTraces, instance),
		}

		code, name, err := validatePreCheckProcessors(mixedRecord, getter)
		assert.Equal(t, define.StatusCodeOK, code)
		assert.Empty(t, name)
		assert.NoError(t, err)
		require.Equal(t, 1, mixed.ResourceSpans().Len())
		service, ok := mixed.ResourceSpans().At(0).Resource().Attributes().Get("service.name")
		require.True(t, ok)
		assert.Equal(t, "checkout", service.AsString())
	})
}

func (noneValidator) GetPipeline(rtype define.RecordType) Pipeline {
	return nil
}

// TestValidateTrafficLimiterEnabledByReload 覆盖限流器被 Reload 新增启用的路径。
// Manager.Reload 会先 Clean 全部新建的 Processor，再把上一代配置中不存在的那些直接装配进来，
// 因此新启用的限流器拿到的是已经 Clean 过的实例，它必须能继续正常放行流量。
func TestValidateTrafficLimiterEnabledByReload(t *testing.T) {
	redisServer := miniredis.RunT(t)

	config := func(withLimiter bool) *confengine.Config {
		limiterProcessor := `
    - name: "traffic_limiter/gcra"
      config:
        bytes_per_second: 1048576
        burst_bytes: 1048576
        redis:
          mode: standalone
          db: 0
          key: reload.traffic_limiter
          addrs: ["` + redisServer.Addr() + `"]
`
		limiterPipeline := `        - "traffic_limiter/gcra"` + "\n"
		if !withLimiter {
			limiterProcessor = ""
			limiterPipeline = ""
		}

		content := `
bk-collector:
  apm:
    patterns:
      - "../example/fixtures/traffic-limiter-absent-*.yml"
  processor:
    - name: "token_checker/fixed"
      config:
        type: "fixed"
        fixed_token: "token1"
        resource_key: "bk.data.token"
        traces_dataid: 1000
` + limiterProcessor + `
  pipeline:
    - name: "traces_pipeline/common"
      type: "traces"
      processors:
        - "token_checker/fixed"
` + limiterPipeline

		conf, err := confengine.LoadConfigContent(content)
		require.NoError(t, err)
		child, err := conf.Child("bk-collector")
		require.NoError(t, err)
		return child
	}

	manager, err := New(config(false))
	require.NoError(t, err)
	require.NoError(t, manager.Reload(config(true)))

	data := ptrace.NewTraces()
	resourceSpans := data.ResourceSpans().AppendEmpty()
	resourceSpans.Resource().Attributes().PutString("service.name", "checkout")
	resourceSpans.ScopeSpans().AppendEmpty().Spans().AppendEmpty().SetName("span")

	code, name, err := validatePreCheckProcessors(&define.Record{
		Token:      define.Token{Original: "token1"},
		RecordType: define.RecordTraces,
		Data:       data,
	}, manager)
	assert.NoError(t, err)
	assert.Empty(t, name)
	assert.Equal(t, define.StatusCodeOK, code)
	assert.NotEmpty(t, redisServer.Keys(), "新启用的限流器应该真正在共享 Redis 上扣减额度")
}

// TestPipelineSurvivesInvalidTrafficLimiterRedis 守住限流器配置问题的爆炸半径。
//
// parseProcessors 遇到 Factory 报错只会 continue，processor 随之缺席；
// parsePipelines 又要求 len(instances) == len(plc.Processors)，于是整条流水线构建失败，
// 该信号的全部请求被 validatePreCheckProcessors 判为 400。也就是说，限流器的一处配置笔误
// 足以让在跑的集群丢掉全部数据——限流是保护性功能，绝不能有这种升级路径。
func TestPipelineSurvivesInvalidTrafficLimiterRedis(t *testing.T) {
	// redis 段存在但缺 key 和 addrs，是最典型的下发笔误。
	content := `
bk-collector:
  apm:
    patterns:
      - "../example/fixtures/traffic-limiter-absent-*.yml"
  processor:
    - name: "token_checker/fixed"
      config:
        type: "fixed"
        fixed_token: "token1"
        resource_key: "bk.data.token"
        traces_dataid: 1000
    - name: "traffic_limiter/gcra"
      config:
        bytes_per_second: 1048576
        burst_bytes: 1048576
        redis:
          mode: standalone
  pipeline:
    - name: "traces_pipeline/common"
      type: "traces"
      processors:
        - "token_checker/fixed"
        - "traffic_limiter/gcra"
`
	conf, err := confengine.LoadConfigContent(content)
	require.NoError(t, err)
	child, err := conf.Child("bk-collector")
	require.NoError(t, err)

	manager, err := New(child)
	require.NoError(t, err)
	require.NotNil(t, manager.GetPipeline(define.RecordTraces), "流水线必须照常构建")

	data := ptrace.NewTraces()
	resourceSpans := data.ResourceSpans().AppendEmpty()
	resourceSpans.Resource().Attributes().PutString("service.name", "checkout")
	resourceSpans.ScopeSpans().AppendEmpty().Spans().AppendEmpty().SetName("span")

	code, name, err := validatePreCheckProcessors(&define.Record{
		Token:      define.Token{Original: "token1"},
		RecordType: define.RecordTraces,
		Data:       data,
	}, manager)
	assert.NoError(t, err)
	assert.Empty(t, name)
	assert.Equal(t, define.StatusCodeOK, code, "配置非法只降级为本地 GCRA，不得丢数据")
}

func TestValidatePreCheckProcessors(t *testing.T) {
	t.Run("nil pipeline getter", func(t *testing.T) {
		code, p, err := validatePreCheckProcessors(nil, nil)
		assert.Equal(t, define.StatusCodeOK, code)
		assert.Equal(t, "", p)
		assert.NoError(t, err)
	})

	t.Run("none pipeline getter", func(t *testing.T) {
		code, p, err := validatePreCheckProcessors(&define.Record{RequestType: "unknown"}, noneValidator{})
		assert.Equal(t, define.StatusBadRequest, code)
		assert.Equal(t, "", p)
		assert.Error(t, err)
	})

	t.Run("default", func(t *testing.T) {
		v := Validator{
			Func: func(record *define.Record) (define.StatusCode, string, error) {
				return define.StatusCodeOK, "", nil
			},
		}
		code, p, err := v.Validate(&define.Record{RequestType: "unknown"})
		assert.Equal(t, define.StatusCodeOK, code)
		assert.Equal(t, "", p)
		assert.NoError(t, err)
	})
}
