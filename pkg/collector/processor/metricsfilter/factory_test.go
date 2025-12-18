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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/common/expfmt"
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

func makeMetricsRecord(n int) pmetric.Metrics {
	opts := define.MetricsOptions{
		MetricName: "my_metrics",
		GaugeCount: n,
	}
	return generator.NewMetricsGenerator(opts).Generate()
}

func makeMetricsRecordWith(name string, n int, rs, attrs map[string]string) pmetric.Metrics {
	opts := define.MetricsOptions{
		MetricName: name,
		GaugeCount: n,
		GeneratorOptions: define.GeneratorOptions{
			Attributes: attrs,
			Resources:  rs,
		},
	}
	return generator.NewMetricsGenerator(opts).Generate()
}

func makePushGatewayData(t *testing.T, name string, rs, attrs map[string]string) define.PushGatewayData {
	sb := new(strings.Builder)
	sb.WriteString(fmt.Sprintf("# HELP %s help\n", name))
	sb.WriteString(fmt.Sprintf("# TYPE %s counter\n", name))
	sb.WriteString(name)
	sb.WriteString("{")

	var pairs []string
	for k, v := range attrs {
		pairs = append(pairs, fmt.Sprintf(`%s="%s"`, k, v))
	}
	sb.WriteString(strings.Join(pairs, ","))
	sb.WriteString("} 1\n")

	parser := expfmt.TextParser{}
	mfs, err := parser.TextToMetricFamilies(strings.NewReader(sb.String()))
	if err != nil {
		t.Fatalf("failed to parse metrics: %v", err)
	}

	return define.PushGatewayData{MetricFamilies: mfs[name], Labels: rs}
}

func TestMetricsNoAction(t *testing.T) {
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       makeMetricsRecord(1),
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

	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       makeMetricsRecord(1),
	}

	testkits.MustProcess(t, factory, record)
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

	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       makeMetricsRecord(1),
	}

	testkits.MustProcess(t, factory, record)
	metrics := record.Data.(pmetric.Metrics)
	assert.Equal(t, 1, metrics.ResourceMetrics().Len())

	name := testkits.FirstMetric(metrics).Name()
	assert.Equal(t, "my_metrics_replace", name)
}

func makeRemoteWriteData(name string, rs, attrs map[string]string) define.RemoteWriteData {
	labels := []prompb.Label{{Name: "__name__", Value: name}}
	for k, v := range rs {
		labels = append(labels, prompb.Label{
			Name:  k,
			Value: v,
		})
	}
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

func makeLabelMap(labels []prompb.Label) map[string]*prompb.Label {
	m := make(map[string]*prompb.Label, len(labels))
	for i := 0; i < len(labels); i++ {
		if labels[i].GetName() == "__name__" {
			continue
		}
		m[labels[i].GetName()] = &labels[i]
	}
	return m
}

type relabelBasedArgs struct {
	metric string
	rs     map[string]string
	attrs  map[string]string
}

type relabelBasedCase struct {
	name      string
	args      relabelBasedArgs
	wantValue string
}

func testRelabelBasedFactory(t *testing.T, content string, tests []relabelBasedCase) {
	factory := processor.MustCreateFactory(content, NewFactory)
	for _, tt := range tests {
		t.Run("Map_"+tt.name, func(t *testing.T) {
			record := define.Record{
				RecordType: define.RecordMetrics,
				Data:       makeMetricsRecordWith(tt.args.metric, 1, tt.args.rs, tt.args.attrs),
			}

			testkits.MustProcess(t, factory, record)
			metrics := record.Data.(pmetric.Metrics)
			foreach.MetricsDataPointWithResource(metrics, func(metric pmetric.Metric, _, attrs pcommon.Map) {
				testkits.AssertAttrsStringKeyVal(t, attrs, "code_type", tt.wantValue)
			})
		})

		t.Run("Labels_"+tt.name, func(t *testing.T) {
			rwData := makeRemoteWriteData(tt.args.metric, tt.args.rs, tt.args.attrs)
			record := define.Record{
				RecordType: define.RecordRemoteWrite,
				Data:       &rwData,
			}

			testkits.MustProcess(t, factory, record)
			tss := record.Data.(*define.RemoteWriteData).Timeseries
			for _, ts := range tss {
				labels := makeLabelMap(ts.GetLabels())
				v := labels["code_type"]
				if len(tt.wantValue) > 0 {
					assert.Equal(t, tt.wantValue, v.GetValue())
				}
			}
		})

		t.Run("PushGateway_"+tt.name, func(t *testing.T) {
			pd := makePushGatewayData(t, tt.args.metric, tt.args.rs, tt.args.attrs)
			record := define.Record{
				RecordType: define.RecordPushGateway,
				Data:       &pd,
			}
			testkits.MustProcess(t, factory, record)
			mf := record.Data.(*define.PushGatewayData).MetricFamilies
			for _, metric := range mf.Metric {
				var found bool
				for _, label := range metric.Label {
					if label.GetName() == "code_type" {
						assert.Equal(t, tt.wantValue, label.GetValue())
						found = true
					}
				}
				if len(tt.wantValue) > 0 {
					assert.True(t, found)
				}
			}
		})
	}
}

func TestRelabelAction(t *testing.T) {
	const (
		content = `
processor:
  - name: "metrics_filter/relabel"
    config:
      relabel:
        - metrics: ["rpc_client_handled_total","rpc_client_dropped_total"]
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
                - prefix: ""
                  min: 2000
                  max: 3000
          target:
            action: "upsert"
            label: "code_type"
            value: "success"
`
	)

	tests := []relabelBasedCase{
		{
			name: "hit op in",
			args: relabelBasedArgs{
				metric: "rpc_client_handled_total",
				attrs: map[string]string{
					"callee_method":  "hello",
					"callee_service": "example.greeter",
					"code":           "200",
				},
			},
			wantValue: "success",
		},
		{
			name: "hit op in but metric not match",
			args: relabelBasedArgs{
				metric: "test_metric",
				attrs: map[string]string{
					"callee_method":  "hello",
					"callee_service": "example.greeter",
					"code":           "200",
				},
			},
		},
		{
			name: "hit op range",
			args: relabelBasedArgs{
				metric: "rpc_client_handled_total",
				attrs: map[string]string{
					"callee_method":  "hello",
					"callee_service": "example.greeter",
					"code":           "ret_105",
				},
			},
			wantValue: "success",
		},
		{
			name: "miss",
			args: relabelBasedArgs{
				metric: "rpc_client_handled_total",
				attrs: map[string]string{
					"callee_method":  "hello_1",
					"callee_service": "example.greeter",
					"code":           "200",
				},
			},
		},
		{
			name: "hit replace attrs",
			args: relabelBasedArgs{
				metric: "rpc_client_handled_total",
				attrs: map[string]string{
					"callee_method":  "hello",
					"callee_service": "example.greeter",
					"code":           "200",
					"code_type":      "test_type",
				},
			},
			wantValue: "success",
		},
		{
			name: "hit noprefix",
			args: relabelBasedArgs{
				metric: "rpc_client_handled_total",
				attrs: map[string]string{
					"callee_method":  "hello",
					"callee_service": "example.greeter",
					"code":           "2009",
				},
			},
			wantValue: "success",
		},
		{
			name: "miss noprefix",
			args: relabelBasedArgs{
				metric: "rpc_client_handled_total",
				attrs: map[string]string{
					"callee_method":  "hello",
					"callee_service": "example.greeter",
					"code":           "3001",
				},
			},
		},
	}

	testRelabelBasedFactory(t, content, tests)
}

func TestCodeRelabelAction(t *testing.T) {
	const (
		content = `
processor:
  - name: "metrics_filter/code_relabel"
    config:
      code_relabel:
        - metrics: ["rpc_client_handled_total","rpc_client_dropped_total"]
          source: "my.service.name"
          services:
          - name: "my.server;my.service;my.method"
            codes: 
            - rule: "err_200~300"
              target:
                 action: "upsert"
                 label: "code_type"
                 value: "success"
            - rule: "err_400~500"
              target:
                 action: "upsert"
                 label: "code_type"
                 value: "error"
            - rule: "600"
              target:
                 action: "upsert"
                 label: "code_type"
                 value: "normal"
          - name: "my.server;*;my.method1"
            codes: 
            - rule: "err_200~300"
              target:
                 action: "upsert"
                 label: "code_type"
                 value: "success"
          - name: "my.server;*;my.method3"
            codes: 
            - rule: "2000~3000"
              target:
                 action: "upsert"
                 label: "code_type"
                 value: "noprefix"
          - name: "my.server;my.service4;my.method4"
            codes: 
            - rule: "err_5003,50004"
              target:
                 action: "upsert"
                 label: "code_type"
                 value: "fatal"
          - name: "*;my.service5;*"
            codes: 
            - rule: "4836"
              target:
                 action: "upsert"
                 label: "code_type"
                 value: "math_all_callee_server"
        - metrics: ["rpc_server_handled_total"]
          source: "my.service.name"
          services:
          - name: "my.server;my.service;my.method"
            codes: 
            - rule: "err_200~300"
              target:
                 action: "upsert"
                 label: "code_type"
                 value: "success"
`
	)

	tests := []relabelBasedCase{
		{
			name: "rule err_200~300",
			args: relabelBasedArgs{
				metric: "rpc_client_handled_total",
				rs: map[string]string{
					"service_name": "my.service.name",
				},
				attrs: map[string]string{
					"callee_server":  "my.server",
					"callee_service": "my.service",
					"callee_method":  "my.method",
					"code":           "err_200",
				},
			},
			wantValue: "success",
		},
		{
			name: "rule err_400~500",
			args: relabelBasedArgs{
				metric: "rpc_client_handled_total",
				rs: map[string]string{
					"service_name": "my.service.name",
				},
				attrs: map[string]string{
					"callee_server":  "my.server",
					"callee_service": "my.service",
					"callee_method":  "my.method",
					"code":           "err_500",
				},
			},
			wantValue: "error",
		},
		{
			name: "rule 600",
			args: relabelBasedArgs{
				metric: "rpc_client_handled_total",
				rs: map[string]string{
					"service_name": "my.service.name",
				},
				attrs: map[string]string{
					"callee_server":  "my.server",
					"callee_service": "my.service",
					"callee_method":  "my.method",
					"code":           "600",
				},
			},
			wantValue: "normal",
		},
		{
			name: "rule err_5003",
			args: relabelBasedArgs{
				metric: "rpc_client_handled_total",
				rs: map[string]string{
					"service_name": "my.service.name",
				},
				attrs: map[string]string{
					"callee_server":  "my.server",
					"callee_service": "my.service4",
					"callee_method":  "my.method4",
					"code":           "err_5003",
				},
			},
			wantValue: "fatal",
		},
		{
			name: "missing callee_service",
			args: relabelBasedArgs{
				metric: "rpc_client_handled_total",
				rs: map[string]string{
					"service_name": "my.service.name",
				},
				attrs: map[string]string{
					"callee_server": "my.server",
					"callee_method": "my.method",
					"code":          "600",
				},
			},
		},
		{
			name: "missing service_name",
			args: relabelBasedArgs{
				metric: "rpc_client_handled_total",
				attrs: map[string]string{
					"callee_server":  "my.server",
					"callee_method":  "my.method",
					"callee_service": "my.service",
					"code":           "600",
				},
			},
		},
		{
			name: "rule skip",
			args: relabelBasedArgs{
				metric: "rpc_client_handled_total",
				rs: map[string]string{
					"service_name": "my.service.name",
				},
				attrs: map[string]string{
					"callee_server":  "my.server",
					"callee_service": "anything?",
					"callee_method":  "my.method1",
					"code":           "err_200",
				},
			},
			wantValue: "success",
		},
		{
			name: "rule skip but not method",
			args: relabelBasedArgs{
				metric: "rpc_client_handled_total",
				rs: map[string]string{
					"service_name": "my.service.name",
				},
				attrs: map[string]string{
					"callee_server":  "my.server",
					"callee_service": "anything?",
					"callee_method":  "my.method2",
					"code":           "err_200",
				},
			},
		},
		{
			name: "rule noprofix",
			args: relabelBasedArgs{
				metric: "rpc_client_handled_total",
				rs: map[string]string{
					"service_name": "my.service.name",
				},
				attrs: map[string]string{
					"callee_server":  "my.server",
					"callee_service": "anything?",
					"callee_method":  "my.method3",
					"code":           "2000",
				},
			},
			wantValue: "noprefix",
		},
		{
			name: "missing callee_server",
			args: relabelBasedArgs{
				metric: "rpc_client_handled_total",
				rs: map[string]string{
					"service_name": "my.service.name",
				},
				attrs: map[string]string{
					"callee_service": "my.service5",
					"callee_method":  "my.method5",
					"code":           "4836",
				},
			},
			wantValue: "math_all_callee_server",
		},
		{
			name: "math all callee_server",
			args: relabelBasedArgs{
				metric: "rpc_client_handled_total",
				rs: map[string]string{
					"service_name": "my.service.name",
				},
				attrs: map[string]string{
					"callee_server":  "any.server",
					"callee_service": "my.service5",
					"callee_method":  "my.method5",
					"code":           "4836",
				},
			},
			wantValue: "math_all_callee_server",
		},
		{
			name: "missing callee_server but callee_service mismatch",
			args: relabelBasedArgs{
				metric: "rpc_client_handled_total",
				rs: map[string]string{
					"service_name": "my.service.name",
				},
				attrs: map[string]string{
					"callee_service": "my.service5x",
					"callee_method":  "my.method5",
					"code":           "4836",
					"code_type":      "success",
				},
			},
			wantValue: "success",
		},
		{
			name: "missing callee_server and callee_service",
			args: relabelBasedArgs{
				metric: "rpc_client_handled_total",
				rs: map[string]string{
					"service_name": "my.service.name",
				},
				attrs: map[string]string{
					"callee_method": "my.method5",
					"code":          "4836",
					"code_type":     "success",
				},
			},
			wantValue: "success",
		},
		{
			name: "callee rule err_200~300",
			args: relabelBasedArgs{
				metric: "rpc_server_handled_total",
				rs: map[string]string{
					"service_name": "my.service.name",
				},
				attrs: map[string]string{
					"callee_server":  "my.server",
					"callee_service": "my.service",
					"callee_method":  "my.method",
					"code":           "err_200",
				},
			},
			wantValue: "success",
		},
	}

	testRelabelBasedFactory(t, content, tests)
}

func makePushGatewayDataFromText(t *testing.T) []define.Record {
	content := `
# HELP rpc_client_handled_seconds Histogram of response latency (seconds) of the RPC until it is finished by the application.
# TYPE rpc_client_handled_seconds histogram
rpc_client_handled_seconds_bucket{callee_method="/200",callee_service="trpc.example.greeter.http",caller_method="",caller_service="trpc.example.greeter.Greeter",code="0",code_desc="code=0",code_type="success",system_name="trpc",le="0.005"} 15
rpc_client_handled_seconds_bucket{callee_method="/200",callee_service="trpc.example.greeter.http",caller_method="",caller_service="trpc.example.greeter.Greeter",code="0",code_desc="code=0",code_type="success",system_name="trpc",le="+Inf"} 15
rpc_client_handled_seconds_sum{callee_method="/200",callee_service="trpc.example.greeter.http",caller_method="",caller_service="trpc.example.greeter.Greeter",code="0",code_desc="code=0",code_type="success",system_name="trpc"} 0.022185792000000003
# HELP rpc_client_handled_total Total number of RPCs completed by the client, regardless of success or failure.
# TYPE rpc_client_handled_total counter
rpc_client_handled_total{callee_method="/200",callee_service="trpc.example.greeter.http",caller_method="",caller_service="trpc.example.greeter.Greeter",code="0",code_desc="code=0",code_type="success",system_name="trpc"} 15
# HELP rpc_server_handled_seconds Histogram of response latency (seconds) of RPC that had been application-level handled by the server.
# TYPE rpc_server_handled_seconds histogram
rpc_server_handled_seconds_bucket{callee_method="SayHello",callee_service="trpc.example.greeter.Greeter",caller_method="",caller_service="trpc.example.greeter.Greeter",code="0",code_desc="code=0",code_type="success",system_name="trpc",le="0.005"} 25
rpc_server_handled_seconds_bucket{callee_method="SayHello",callee_service="trpc.example.greeter.Greeter",caller_method="",caller_service="trpc.example.greeter.Greeter",code="0",code_desc="code=0",code_type="success",system_name="trpc",le="+Inf"} 25
rpc_server_handled_seconds_sum{callee_method="SayHello",callee_service="trpc.example.greeter.Greeter",caller_method="",caller_service="trpc.example.greeter.Greeter",code="0",code_desc="code=0",code_type="success",system_name="trpc"} 0.027845586
rpc_server_handled_seconds_count{callee_method="SayHello",callee_service="trpc.example.greeter.Greeter",caller_method="",caller_service="trpc.example.greeter.Greeter",code="0",code_desc="code=0",code_type="success",system_name="trpc"} 25
# HELP rpc_server_handled_total Total number of RPCs completed on the server, regardless of success or failure.
# TYPE rpc_server_handled_total counter
rpc_server_handled_total{callee_method="SayHello",callee_service="trpc.example.greeter.Greeter",caller_method="",caller_service="trpc.example.greeter.Greeter",code="0",code_desc="code=0",code_type="success",system_name="trpc"} 25
`

	parser := expfmt.TextParser{}
	mfs, err := parser.TextToMetricFamilies(strings.NewReader(content))
	if err != nil {
		t.Fatalf("failed to parse metrics: %v", err)
	}

	var records []define.Record
	for _, mf := range mfs {
		records = append(records, define.Record{
			RecordType: define.RecordPushGateway,
			Data: &define.PushGatewayData{
				MetricFamilies: mf,
				Labels: map[string]string{
					"service_name": "example.greeter",
				},
			},
		})
	}
	return records
}

func TestRelabelActionPushGatewayData(t *testing.T) {
	content := `
processor:
  - name: "metrics_filter/code_relabel"
    config:
      code_relabel:
        - metrics:
          - "rpc_server_handled_total"
          - "rpc_server_handled_seconds_bucket" 
          - "rpc_server_handled_seconds_count" 
          - "rpc_server_handled_seconds_sum"
          source: "example.greeter"
          services:
          - name: "*;*;SayHello"
            codes: 
            - rule: "0"
              target:
                 action: "upsert"
                 label: "code_type"
                 value: "success_rpc"
        - metrics:
          - "rpc_client_handled_total"
          - "rpc_client_handled_seconds_bucket" 
          - "rpc_client_handled_seconds_count" 
          - "rpc_client_handled_seconds_sum"
          source: "example.greeter"
          services:
          - name: "*;*;/200"
            codes: 
            - rule: "0"
              target:
                 action: "upsert"
                 label: "code_type"
                 value: "success_rpc"
`
	factory := processor.MustCreateFactory(content, NewFactory)

	for _, record := range makePushGatewayDataFromText(t) {
		testkits.MustProcess(t, factory, record)
		pd := record.Data.(*define.PushGatewayData)
		for _, m := range pd.MetricFamilies.Metric {
			for _, label := range m.Label {
				if label.GetName() == "code_type" {
					assert.Equal(t, "success_rpc", label.GetValue())
				}
			}
		}
	}
}
