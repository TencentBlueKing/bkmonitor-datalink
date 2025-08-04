// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metricsfilter

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
)

func TestFactory(t *testing.T) {
	content := `
processor:
  - name: "metrics_filter/drop"
    config:
      drop:
        metrics:
          - "runtime.go.mem.live_objects"
          - "none.exist.metric"
`
	mainConf := processor.MustLoadConfigs(content)[0].Config

	customContent := `
processor:
  - name: "metrics_filter/drop"
    config:
      drop:
        metrics:
          - "runtime.go.mem.not_live_objects"
          - "none.exist.metric"
`
	customConf := processor.MustLoadConfigs(customContent)[0].Config

	obj, err := NewFactory(mainConf, []processor.SubConfigProcessor{
		{
			Token: "token1",
			Type:  define.SubConfigFieldDefault,
			Config: processor.Config{
				Config: customConf,
			},
		},
	})
	factory := obj.(*metricsFilter)
	assert.NoError(t, err)
	assert.Equal(t, mainConf, factory.MainConfig())

	var c1 Config
	assert.NoError(t, mapstructure.Decode(mainConf, &c1))
	assert.Equal(t, c1, factory.configs.GetGlobal().(Config))

	var c2 Config
	assert.NoError(t, mapstructure.Decode(customConf, &c2))
	assert.Equal(t, c2, factory.configs.GetByToken("token1").(Config))

	assert.Equal(t, define.ProcessorMetricsFilter, factory.Name())
	assert.False(t, factory.IsDerived())
	assert.False(t, factory.IsPreCheck())

	factory.Reload(mainConf, nil)
	assert.Equal(t, mainConf, factory.MainConfig())
}

func makeMetricsGenerator(n int) *generator.MetricsGenerator {
	opts := define.MetricsOptions{
		MetricName: "my_metrics",
		GaugeCount: n,
	}
	return generator.NewMetricsGenerator(opts)
}

func TestMetricsNoAction(t *testing.T) {
	g := makeMetricsGenerator(1)
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}

	metrics := record.Data.(pmetric.Metrics)
	assert.Equal(t, 1, metrics.ResourceMetrics().Len())

	name := testkits.FirstMetric(metrics).Name()
	assert.Equal(t, "my_metrics", name)
}

func TestMetricsDropAction(t *testing.T) {
	content := `
processor:
   - name: "metrics_filter/drop"
     config:
       drop:
         metrics:
           - "my_metrics"
`
	factory := processor.MustCreateFactory(content, NewFactory)

	g := makeMetricsGenerator(1)
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}

	_, err := factory.Process(&record)
	assert.NoError(t, err)

	metrics := record.Data.(pmetric.Metrics).ResourceMetrics()
	assert.Equal(t, 0, metrics.Len())
}

func TestMetricsReplaceAction(t *testing.T) {
	content := `
processor:
   - name: "metrics_filter/replace"
     config:
       replace:
         - source: my_metrics
           destination: my_metrics_replace
`
	factory := processor.MustCreateFactory(content, NewFactory)

	g := makeMetricsGenerator(1)
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}

	_, err := factory.Process(&record)
	assert.NoError(t, err)

	metrics := record.Data.(pmetric.Metrics)
	assert.Equal(t, 1, metrics.ResourceMetrics().Len())

	name := testkits.FirstMetric(metrics).Name()
	assert.Equal(t, "my_metrics_replace", name)
}

func makeMetricsGeneratorWithAttributes(n int, attrs map[string]string) *generator.MetricsGenerator {
	opts := define.MetricsOptions{
		MetricName: "rpc_client_handled_total",
		GaugeCount: n,
		GeneratorOptions: define.GeneratorOptions{
			Attributes: attrs,
		},
	}
	return generator.NewMetricsGenerator(opts)
}

func TestMetricsRelabelAction(t *testing.T) {
	content := `
processor:
  - name: "metrics_filter/relabel"
    config:
      relabel:
        - metric: "rpc_client_handled_total"
          rules:
            - label: "callee_method"
              op: "in"
              values: ["hello"]
            - label: "callee_service"
              op: "in"
              values: ["example.greeter"]
            - label: "code"
              op: "range"
              values:
              - prefix: "err_"
                min: 10
                max: 19
              - prefix: ""
                min: 100
                max: 200
              - prefix: "trpc_"
                min: 11
                max: 12
              - prefix: "ret_"
                min: 100
                max: 200
              - min: 100
                max: 100
              - min: 200
                max: 200
          destinations:
            - label: "code_type"
              value: "success"
`
	factory := processor.MustCreateFactory(content, NewFactory)

	t.Run("rules hit op in", func(t *testing.T) {
		g := makeMetricsGeneratorWithAttributes(1, map[string]string{
			"callee_method":  "hello",
			"callee_service": "example.greeter",
			"code":           "200",
		})
		data := g.Generate()

		record := define.Record{
			RecordType: define.RecordMetrics,
			Data:       data,
		}
		_, err := factory.Process(&record)
		assert.NoError(t, err)

		metrics := record.Data.(pmetric.Metrics)
		foreach.Metrics(metrics.ResourceMetrics(), func(metric pmetric.Metric) {
			foreach.MetricsDataPointsAttrs(metric, func(attrs pcommon.Map) {
				v, exist := attrs.Get("code_type")
				assert.True(t, exist)
				assert.Equal(t, "success", v.AsString())
			})
		})
	})
	t.Run("rules hit op range", func(t *testing.T) {
		g := makeMetricsGeneratorWithAttributes(1, map[string]string{
			"callee_method":  "hello",
			"callee_service": "example.greeter",
			"code":           "ret_105",
		})
		data := g.Generate()

		record := define.Record{
			RecordType: define.RecordMetrics,
			Data:       data,
		}
		_, err := factory.Process(&record)
		assert.NoError(t, err)

		metrics := record.Data.(pmetric.Metrics)
		foreach.Metrics(metrics.ResourceMetrics(), func(metric pmetric.Metric) {
			foreach.MetricsDataPointsAttrs(metric, func(attrs pcommon.Map) {
				v, exist := attrs.Get("code_type")
				assert.True(t, exist)
				assert.Equal(t, "success", v.AsString())
			})
		})
	})
	t.Run("rules not hit", func(t *testing.T) {
		g := makeMetricsGeneratorWithAttributes(1, map[string]string{
			"callee_method":  "hello_1",
			"callee_service": "example.greeter",
			"code":           "200",
		})
		data := g.Generate()

		record := define.Record{
			RecordType: define.RecordMetrics,
			Data:       data,
		}
		_, err := factory.Process(&record)
		assert.NoError(t, err)

		metrics := record.Data.(pmetric.Metrics)
		foreach.Metrics(metrics.ResourceMetrics(), func(metric pmetric.Metric) {
			foreach.MetricsDataPointsAttrs(metric, func(attrs pcommon.Map) {
				_, exist := attrs.Get("code_type")
				assert.False(t, exist)
			})
		})
	})

}

func BenchmarkMapf(b *testing.B) {

	value := "ret_105"

	v := map[string]interface{}{
		"prefix": "trpc_",
		"min":    11,
		"max":    12,
	}

	for i := 0; i < b.N; i++ {
		mapf := func(v interface{}) {
			val := v.(map[string]interface{})
			prefix, ok := val["prefix"]
			if ok {
				if !strings.HasPrefix(value, prefix.(string)) {
					return
				}
				value = strings.TrimPrefix(value, prefix.(string))
			}
			value, err := strconv.Atoi(value)
			if err != nil {
				return
			}
			minVal, ok := val["min"].(int)
			if !ok {
				return
			}
			maxVal, ok := val["max"].(int)
			if !ok {
				return
			}
			if value >= minVal && value <= maxVal {
				return
			}
		}
		mapf(v)
	}
}

func BenchmarkDecodef(b *testing.B) {

	value := "ret_105"

	v := map[string]interface{}{
		"prefix": "trpc_",
		"min":    11,
		"max":    12,
	}

	for i := 0; i < b.N; i++ {
		decodef := func(v interface{}) {
			rangeValue := RangeValue{}
			err := mapstructure.Decode(v, &rangeValue)
			if err != nil {
				return
			}
			if rangeValue.Prefix != "" {
				if !strings.HasPrefix(value, rangeValue.Prefix) {
					return
				}
				value = strings.TrimPrefix(value, rangeValue.Prefix)
			}
			value, err := strconv.Atoi(value)
			if err != nil {
				return
			}
			if value >= rangeValue.Min && value <= rangeValue.Max {
				return
			}
		}
		decodef(v)
	}
}
