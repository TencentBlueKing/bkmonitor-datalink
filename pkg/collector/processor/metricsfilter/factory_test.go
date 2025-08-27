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
	"testing"
	"time"

	"github.com/prometheus/prometheus/prompb"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
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

func makeMetricsGeneratorWithAttrs(name string, n int, attrs map[string]string) *generator.MetricsGenerator {
	opts := define.MetricsOptions{
		MetricName: name,
		GaugeCount: n,
		GeneratorOptions: define.GeneratorOptions{
			Attributes: attrs,
		},
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

func makeRemoteWriteData(name string, attrs map[string]string) define.RemoteWriteData {
	labels := []prompb.Label{{Name: "__name__", Value: name}}
	for k, v := range attrs {
		labels = append(labels, prompb.Label{
			Name:  k,
			Value: v,
		})
	}

	var rwData define.RemoteWriteData
	rwData.Timeseries = append(rwData.Timeseries, prompb.TimeSeries{
		Labels: labels,
		Samples: []prompb.Sample{{
			Value:     1,
			Timestamp: time.Now().Unix(),
		}},
	})
	return rwData
}

const (
	relabelActionContent = `
processor:
  - name: "metrics_filter/relabel"
    config:
      relabel:
        - metrics: ["rpc_client_handled_total"]
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
                - prefix: "trpc_"
                  min: 11
                  max: 12
                - prefix: "ret_"
                  min: 100
                  max: 200
                - min: 200
                  max: 200
          destinations:
            - action: "upsert"
              label: "code_type"
              value: "success"
`
)

func TestRelabelActionMetrics(t *testing.T) {
	factory := processor.MustCreateFactory(relabelActionContent, NewFactory)
	type args struct {
		metric     string
		attributes map[string]string
	}
	tests := []struct {
		name      string
		args      args
		wantValue string
	}{
		{
			name: "rules hit op in",
			args: args{
				metric: "rpc_client_handled_total",
				attributes: map[string]string{
					"callee_method":  "hello",
					"callee_service": "example.greeter",
					"code":           "200",
				},
			},
			wantValue: "success",
		},
		{
			name: "rules hit op in but name not match",
			args: args{
				metric: "test_metric",
				attributes: map[string]string{
					"callee_method":  "hello",
					"callee_service": "example.greeter",
					"code":           "200",
				},
			},
		},
		{
			name: "rules hit op range",
			args: args{
				metric: "rpc_client_handled_total",
				attributes: map[string]string{
					"callee_method":  "hello",
					"callee_service": "example.greeter",
					"code":           "ret_105",
				},
			},
			wantValue: "success",
		},
		{
			name: "rules not hit",
			args: args{
				metric: "rpc_client_handled_total",
				attributes: map[string]string{
					"callee_method":  "hello_1",
					"callee_service": "example.greeter",
					"code":           "200",
				},
			},
		},
		{
			name: "rules hit replace attr",
			args: args{
				metric: "rpc_client_handled_total",
				attributes: map[string]string{
					"callee_method":  "hello",
					"callee_service": "example.greeter",
					"code":           "200",
					"code_type":      "test_type",
				},
			},
			wantValue: "success",
		},
	}

	for _, tt := range tests {
		t.Run("metrics_"+tt.name, func(t *testing.T) {
			g := makeMetricsGeneratorWithAttrs(tt.args.metric, 1, tt.args.attributes)
			record := define.Record{
				RecordType: define.RecordMetrics,
				Data:       g.Generate(),
			}

			_, err := factory.Process(&record)
			assert.NoError(t, err)

			metrics := record.Data.(pmetric.Metrics)
			foreach.MetricsSliceDataPointsAttrs(metrics.ResourceMetrics(), func(name string, attrs pcommon.Map) {
				testkits.AssertAttrsStringVal(t, attrs, "code_type", tt.wantValue)
			})
		})

		t.Run("remotewrite_"+tt.name, func(t *testing.T) {
			rwData := makeRemoteWriteData(tt.args.metric, tt.args.attributes)
			record := define.Record{
				RecordType: define.RecordRemoteWrite,
				Data:       &rwData,
			}

			_, err := factory.Process(&record)
			assert.NoError(t, err)

			tss := record.Data.(*define.RemoteWriteData).Timeseries
			for _, ts := range tss {
				labels := makeLabelMap(ts.GetLabels())
				v := labels["code_type"]
				if len(tt.wantValue) > 0 {
					assert.Equal(t, tt.wantValue, v.GetValue())
				}
			}
		})
	}
}
