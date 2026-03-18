// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package resourcefilter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/cache/k8scache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/random"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/tokenparser"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
)

func TestFactory(t *testing.T) {
	content := `
processor:
  - name: "resource_filter/drop"
    config:
      drop:
        keys:
          - "resource.service.name"
`
	mainConf := processor.MustLoadConfigs(content)[0].Config

	customContent := `
processor:
  - name: "resource_filter/drop"
    config:
      drop:
        keys:
          - "resource.service.name1"
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
	factory := obj.(*resourceFilter)
	assert.NoError(t, err)
	assert.Equal(t, mainConf, factory.MainConfig())

	mainConfig := factory.configs.GetGlobal().(Config)
	assert.Equal(t, "service.name", mainConfig.Drop.Keys[0])

	customConfig := factory.configs.GetByToken("token1").(Config)
	assert.Equal(t, "service.name1", customConfig.Drop.Keys[0])

	assert.Equal(t, define.ProcessorResourceFilter, factory.Name())
	assert.False(t, factory.IsDerived())
	assert.False(t, factory.IsPreCheck())

	factory.Reload(mainConf, nil)
	assert.Equal(t, mainConf, factory.MainConfig())
}

const (
	resourceKey1 = "resource_key1"
	resourceKey2 = "resource_key2"
	resourceKey3 = "resource_key3"
	resourceKey4 = "resource_key4"
)

func makeTracesRecord(n int, valueType string) ptrace.Traces {
	opts := define.TracesOptions{SpanCount: n}
	opts.Resources = map[string]string{
		resourceKey1: "key1",
		resourceKey2: "key2",
		resourceKey3: "key3",
		resourceKey4: "key4",
	}
	opts.DimensionsValueType = valueType
	return generator.NewTracesGenerator(opts).Generate()
}

func makeMetricsRecord(n int, valueType string) pmetric.Metrics {
	opts := define.MetricsOptions{
		GaugeCount:     n,
		CounterCount:   n,
		HistogramCount: n,
	}
	opts.RandomResourceKeys = []string{
		resourceKey1,
		resourceKey2,
		resourceKey3,
		resourceKey4,
	}
	opts.DimensionsValueType = valueType
	return generator.NewMetricsGenerator(opts).Generate()
}

func makeLogsRecord(count, length int, valueType string) plog.Logs {
	opts := define.LogsOptions{
		LogCount:  count,
		LogLength: length,
	}
	opts.RandomResourceKeys = []string{
		resourceKey1,
		resourceKey2,
		resourceKey3,
		resourceKey4,
	}
	opts.DimensionsValueType = valueType
	return generator.NewLogsGenerator(opts).Generate()
}

func createTracesWithResourceSpan(resourceAttrs map[string]string) (ptrace.Traces, ptrace.ResourceSpans) {
	traces := ptrace.NewTraces()
	rs := traces.ResourceSpans().AppendEmpty()
	for k, v := range resourceAttrs {
		rs.Resource().Attributes().InsertString(k, v)
	}
	return traces, rs
}

func addScopeSpanWithSpans(rs ptrace.ResourceSpans, scopeName string, traceID pcommon.TraceID, spanCount int) {
	ss := rs.ScopeSpans().AppendEmpty()
	ss.Scope().SetName(scopeName)
	for i := 0; i < spanCount; i++ {
		span := ss.Spans().AppendEmpty()
		span.SetTraceID(traceID)
		span.SetSpanID(random.SpanID())
	}
}

func createSpanInScopeSpan(ss ptrace.ScopeSpans, traceID pcommon.TraceID) ptrace.Span {
	span := ss.Spans().AppendEmpty()
	span.SetTraceID(traceID)
	span.SetSpanID(random.SpanID())
	return span
}

// makeOpenTelemetryTraces creates traces data with OpenTelemetry SDK attributes
func makeOpenTelemetryTraces(spanCount int) ptrace.Traces {
	data := generator.NewTracesGenerator(define.TracesOptions{SpanCount: spanCount}).Generate()
	// Add SDK name to Resource attributes for all resource spans
	foreach.SpansSliceResource(data, func(rs pcommon.Resource) {
		rs.Attributes().InsertString(keySdkName, sdkOpenTelemetry)
	})
	return data
}

// makeSkyWalkingTraces creates traces data with SkyWalking SDK attributes
func makeSkyWalkingTraces(spanCount int, traceID string) ptrace.Traces {
	data := generator.NewTracesGenerator(define.TracesOptions{SpanCount: spanCount}).Generate()
	// Add SDK name and sw8.trace_id to Resource attributes for all resource spans
	foreach.SpansSliceResource(data, func(rs pcommon.Resource) {
		rs.Attributes().InsertString(keySw8TraceID, traceID)
		rs.Attributes().InsertString(keySdkName, sdkSkyWalking)
	})
	return data
}

// addOpenTelemetryResourceSpan adds an OpenTelemetry resource span to traces
func addOpenTelemetryResourceSpan(traces ptrace.Traces, traceID pcommon.TraceID, spanCount int) {
	rs := traces.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().InsertString(keySdkName, sdkOpenTelemetry)
	addScopeSpanWithSpans(rs, "", traceID, spanCount)
}

// addSkyWalkingResourceSpan adds a SkyWalking resource span to traces
func addSkyWalkingResourceSpan(traces ptrace.Traces, traceID pcommon.TraceID, spanCount int) {
	rs := traces.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().InsertString(keySdkName, sdkSkyWalking)
	rs.Resource().Attributes().InsertString(keySw8TraceID, traceID.HexString())
	addScopeSpanWithSpans(rs, "", traceID, spanCount)
}

func TestAssembleAction(t *testing.T) {
	content := `
processor:
    - name: "resource_filter/assemble"
      config:
        assemble:
          - destination: "resource_final"
            separator: ":"
            keys:
              - "resource.resource_key1"
              - "resource.not_exist"
              - "resource.resource_key2"
              - "resource.resource_key3"
              - "resource.resource_key4"
`
	t.Run("traces", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       makeTracesRecord(1, "string"),
		}

		testkits.MustProcess(t, factory, record)
		attrs := testkits.FirstSpanAttrs(record.Data)
		testkits.AssertAttrsStringKeyVal(t, attrs, "resource_final", "key1::key2:key3:key4")
	})
}

func TestDropAction(t *testing.T) {
	content := `
processor:
    - name: "resource_filter/drop"
      config:
        drop:
          keys:
            - "resource.resource_key1"
`

	assertFunc := func(t *testing.T, attrs pcommon.Map) {
		testkits.AssertAttrsNotFound(t, attrs, "resource_key1")
		testkits.AssertAttrsFound(t, attrs, "resource_key2")
		testkits.AssertAttrsFound(t, attrs, "resource_key3")
	}

	t.Run("traces", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       makeTracesRecord(1, "string"),
		}

		testkits.MustProcess(t, factory, record)
		assertFunc(t, testkits.FirstSpanAttrs(record.Data))
	})

	t.Run("metrics", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		record := define.Record{
			RecordType: define.RecordMetrics,
			Data:       makeMetricsRecord(1, "string"),
		}

		testkits.MustProcess(t, factory, record)
		assertFunc(t, testkits.FirstMetricAttrs(record.Data))
	})

	t.Run("logs", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		record := define.Record{
			RecordType: define.RecordLogs,
			Data:       makeLogsRecord(10, 10, "string"),
		}

		testkits.MustProcess(t, factory, record)
		assertFunc(t, testkits.FirstLogRecordAttrs(record.Data))
	})
}

func TestReplaceAction(t *testing.T) {
	content := `
processor:
    - name: "resource_filter/replace"
      config:
        replace:
          - source: resource_key1
            destination: resource_key4
`

	assertFunc := func(t *testing.T, attrs pcommon.Map) {
		testkits.AssertAttrsNotFound(t, attrs, resourceKey1)
		testkits.AssertAttrsFound(t, attrs, resourceKey4)
	}

	t.Run("traces", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       makeTracesRecord(1, "string"),
		}

		testkits.MustProcess(t, factory, record)
		assertFunc(t, testkits.FirstSpanAttrs(record.Data))
	})

	t.Run("metrics", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		record := define.Record{
			RecordType: define.RecordMetrics,
			Data:       makeMetricsRecord(1, "float"),
		}

		testkits.MustProcess(t, factory, record)
		assertFunc(t, testkits.FirstMetricAttrs(record.Data))
	})

	t.Run("logs", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		record := define.Record{
			RecordType: define.RecordLogs,
			Data:       makeLogsRecord(10, 10, "float"),
		}

		testkits.MustProcess(t, factory, record)
		assertFunc(t, testkits.FirstLogRecordAttrs(record.Data))
	})
}

func TestAddAction(t *testing.T) {
	content := `
processor:
    - name: "resource_filter/replace"
      config:
        add:
          - label: label1
            value: value1
          - label: label2
            value: value2
`
	assertFunc := func(t *testing.T, attrs pcommon.Map) {
		testkits.AssertAttrsStringKeyVal(t, attrs, "label1", "value1", "label2", "value2")
	}

	t.Run("traces", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       makeTracesRecord(1, "bool"),
		}

		testkits.MustProcess(t, factory, record)
		assertFunc(t, testkits.FirstSpanAttrs(record.Data))
	})

	t.Run("metrics", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		record := define.Record{
			RecordType: define.RecordMetrics,
			Data:       makeMetricsRecord(1, "int"),
		}

		testkits.MustProcess(t, factory, record)
		assertFunc(t, testkits.FirstMetricAttrs(record.Data))
	})

	t.Run("logs", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		record := define.Record{
			RecordType: define.RecordLogs,
			Data:       makeLogsRecord(10, 10, "int"),
		}

		testkits.MustProcess(t, factory, record)
		assertFunc(t, testkits.FirstLogRecordAttrs(record.Data))
	})
}

func TestFromRecordAction(t *testing.T) {
	content := `
processor:
    - name: "resource_filter/from_record"
      config:
        from_record:
          - source: "request.client.ip"
            destination: "resource.client.ip"
`

	assertFunc := func(t *testing.T, attrs pcommon.Map) {
		testkits.AssertAttrsStringKeyVal(t, attrs, "client.ip", "127.1.1.1")
	}

	t.Run("traces", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		record := define.Record{
			RecordType:    define.RecordTraces,
			Data:          makeTracesRecord(1, "bool"),
			RequestClient: define.RequestClient{IP: "127.1.1.1"},
		}

		testkits.MustProcess(t, factory, record)
		assertFunc(t, testkits.FirstSpanAttrs(record.Data))
	})

	t.Run("metrics", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		record := define.Record{
			RecordType:    define.RecordMetrics,
			Data:          makeMetricsRecord(1, "int"),
			RequestClient: define.RequestClient{IP: "127.1.1.1"},
		}

		testkits.MustProcess(t, factory, record)
		assertFunc(t, testkits.FirstMetricAttrs(record.Data))
	})

	t.Run("logs", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		record := define.Record{
			RecordType:    define.RecordLogs,
			Data:          makeLogsRecord(1, 10, "int"),
			RequestClient: define.RequestClient{IP: "127.1.1.1"},
		}

		testkits.MustProcess(t, factory, record)
		assertFunc(t, testkits.FirstLogRecordAttrs(record.Data))
	})
}

func TestFromCacheAction(t *testing.T) {
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := json.Marshal(map[string][]map[string]string{
			"pods": {
				{
					"action":    "CreateOrUpdate",
					"ip":        "127.1.0.1",
					"name":      "myapp1",
					"namespace": "my-ns1",
					"cluster":   "K8S-BCS-00000",
				},
				{
					"action":    "CreateOrUpdate",
					"ip":        "127.1.0.2",
					"name":      "myapp2",
					"namespace": "my-ns2",
					"cluster":   "K8S-BCS-90000",
				},
				{
					"action":    "CreateOrUpdate",
					"ip":        "127.1.0.3",
					"name":      "myapp3",
					"namespace": "my-ns3",
					"cluster":   "K8S-BCS-90000",
				},
			},
		})
		w.Write(b)
	}))
	defer svr.Close()

	err := k8scache.Install(&k8scache.Config{
		URL:      svr.URL,
		Timeout:  10 * time.Second,
		Interval: 10 * time.Second,
	})
	assert.NoError(t, err)
	defer k8scache.Uninstall()

	content := `
processor:
    - name: "resource_filter/from_cache"
      config:
        from_cache:
          key: "resource.net.host.ip|resource.client.ip"
          cache_name: "k8s_cache"
`

	t.Run("traces net.host.ip", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		time.Sleep(time.Second) // wait for syncing
		data := makeTracesRecord(1, "bool")
		testkits.FirstSpanAttrs(data).InsertString("net.host.ip", "127.1.0.1")

		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}

		testkits.MustProcess(t, factory, record)
		attrs := testkits.FirstSpanAttrs(record.Data)
		testkits.AssertAttrsStringKeyVal(t, attrs,
			"k8s.pod.ip", "127.1.0.1",
			"k8s.pod.name", "myapp1",
			"k8s.namespace.name", "my-ns1",
			"k8s.bcs.cluster.id", "K8S-BCS-00000",
		)
	})

	t.Run("traces client.ip", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		time.Sleep(time.Second) // wait for syncing
		data := makeTracesRecord(1, "bool")
		testkits.FirstSpanAttrs(data).InsertString("client.ip", "127.1.0.2")

		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}

		testkits.MustProcess(t, factory, record)
		attrs := testkits.FirstSpanAttrs(record.Data)
		testkits.AssertAttrsStringKeyVal(t, attrs,
			"k8s.pod.ip", "127.1.0.2",
			"k8s.pod.name", "myapp2",
			"k8s.namespace.name", "my-ns2",
			"k8s.bcs.cluster.id", "K8S-BCS-90000",
		)
	})

	t.Run("metrics net.host.ip", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		time.Sleep(time.Second) // wait for syncing
		data := makeMetricsRecord(1, "bool")
		testkits.FirstMetricAttrs(data).InsertString("net.host.ip", "127.1.0.3")

		record := define.Record{
			RecordType: define.RecordMetrics,
			Data:       data,
		}

		testkits.MustProcess(t, factory, record)
		attrs := testkits.FirstMetricAttrs(record.Data)
		testkits.AssertAttrsStringKeyVal(t, attrs,
			"k8s.pod.ip", "127.1.0.3",
			"k8s.pod.name", "myapp3",
			"k8s.namespace.name", "my-ns3",
			"k8s.bcs.cluster.id", "K8S-BCS-90000",
		)
	})

	t.Run("logs net.host.ip", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		time.Sleep(time.Second) // wait for syncing
		data := makeLogsRecord(1, 10, "bool")
		testkits.FirstLogRecordAttrs(data).InsertString("net.host.ip", "127.1.0.3")

		record := define.Record{
			RecordType: define.RecordLogs,
			Data:       data,
		}

		testkits.MustProcess(t, factory, record)
		attrs := testkits.FirstLogRecordAttrs(record.Data)
		testkits.AssertAttrsStringKeyVal(t, attrs,
			"k8s.pod.ip", "127.1.0.3",
			"k8s.pod.name", "myapp3",
			"k8s.namespace.name", "my-ns3",
			"k8s.bcs.cluster.id", "K8S-BCS-90000",
		)
	})
}

func TestFromMetadataAction(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set(define.KeyUserMetadata, "k8s.pod.ip=127.1.0.2,k8s.pod.name=myapp2,k8s.namespace.name=my-ns2,k8s.bcs.cluster.id=K8S-BCS-90000")

	const content = `
processor:
    - name: "resource_filter/from_metadata"
      config:
        from_metadata:
          keys: ["*"]
`

	t.Run("traces from_metadata", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       makeTracesRecord(1, "bool"),
			Metadata:   tokenparser.FromHttpUserMetadata(r),
		}

		testkits.MustProcess(t, factory, record)
		attrs := testkits.FirstSpanAttrs(record.Data)
		testkits.AssertAttrsStringKeyVal(t, attrs,
			"k8s.pod.ip", "127.1.0.2",
			"k8s.pod.name", "myapp2",
			"k8s.namespace.name", "my-ns2",
			"k8s.bcs.cluster.id", "K8S-BCS-90000",
		)
	})
}

func TestFromTokenAction(t *testing.T) {
	const content = `
processor:
    - name: "resource_filter/from_token"
      config:
        from_token:
          keys:
            - "app_name"
`

	assertFunc := func(t *testing.T, attrs pcommon.Map) {
		testkits.AssertAttrsStringKeyVal(t, attrs, "app_name", "test_app")
	}

	t.Run("traces from_token", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       makeTracesRecord(1, "string"),
			Token:      define.Token{AppName: "test_app"},
		}

		testkits.MustProcess(t, factory, record)
		assertFunc(t, testkits.FirstSpanAttrs(record.Data))
	})

	t.Run("metrics.derived from_token", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		data := makeMetricsRecord(1, "string")

		record := define.Record{
			RecordType: define.RecordMetricsDerived,
			Data:       data,
			Token:      define.Token{AppName: "test_app"},
		}
		testkits.MustProcess(t, factory, record)
		assertFunc(t, testkits.FirstMetricAttrs(record.Data))
	})

	t.Run("metrics from_token", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		record := define.Record{
			RecordType: define.RecordMetrics,
			Data:       makeMetricsRecord(1, "string"),
			Token:      define.Token{AppName: "test_app"},
		}
		testkits.MustProcess(t, factory, record)
		assertFunc(t, testkits.FirstMetricAttrs(record.Data))
	})

	t.Run("logs from_token", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		record := define.Record{
			RecordType: define.RecordLogs,
			Data:       makeLogsRecord(1, 10, "string"),
			Token:      define.Token{AppName: "test_app"},
		}

		testkits.MustProcess(t, factory, record)
		assertFunc(t, testkits.FirstLogRecordAttrs(record.Data))
	})
}

func TestDefaultValueAction(t *testing.T) {
	const content = `
processor:
    - name: "resource_filter/default_value"
      config:
        default_value:
          - type: string
            key: resource.service.name
            value: "unknown_service"
`
	t.Run("traces", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       makeTracesRecord(1, "bool"),
		}

		testkits.MustProcess(t, factory, record)
		attrs := testkits.FirstSpanAttrs(record.Data)
		testkits.AssertAttrsStringKeyVal(t, attrs, "service.name", "unknown_service")
	})

	t.Run("traces skipped", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       makeTracesRecord(1, "bool"),
		}

		testkits.FirstSpanAttrs(record.Data).InsertString("service.name", "app.v1")

		testkits.MustProcess(t, factory, record)
		attrs := testkits.FirstSpanAttrs(record.Data)
		testkits.AssertAttrsStringKeyVal(t, attrs, "service.name", "app.v1")
	})

	t.Run("metrics", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		record := define.Record{
			RecordType: define.RecordMetrics,
			Data:       makeMetricsRecord(1, "bool"),
		}

		testkits.MustProcess(t, factory, record)
		attrs := testkits.FirstMetricAttrs(record.Data)
		testkits.AssertAttrsStringKeyVal(t, attrs, "service.name", "unknown_service")
	})

	t.Run("metrics skipped", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		record := define.Record{
			RecordType: define.RecordMetrics,
			Data:       makeMetricsRecord(1, "bool"),
		}

		testkits.FirstMetricAttrs(record.Data).InsertString("service.name", "app.v1")

		testkits.MustProcess(t, factory, record)
		attrs := testkits.FirstMetricAttrs(record.Data)
		testkits.AssertAttrsStringKeyVal(t, attrs, "service.name", "app.v1")
	})

	t.Run("logs", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		record := define.Record{
			RecordType: define.RecordLogs,
			Data:       makeLogsRecord(1, 10, "bool"),
		}

		testkits.MustProcess(t, factory, record)
		attrs := testkits.FirstLogRecordAttrs(record.Data)
		testkits.AssertAttrsStringKeyVal(t, attrs, "service.name", "unknown_service")
	})

	t.Run("logs skipped", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		record := define.Record{
			RecordType: define.RecordLogs,
			Data:       makeLogsRecord(1, 10, "bool"),
		}

		testkits.FirstLogRecordAttrs(record.Data).InsertString("service.name", "app.v1")

		testkits.MustProcess(t, factory, record)
		attrs := testkits.FirstLogRecordAttrs(record.Data)
		testkits.AssertAttrsStringKeyVal(t, attrs, "service.name", "app.v1")
	})
}

func TestKeepOriginTraceIdAction(t *testing.T) {
	const enabledContent = `
processor:
  - name: "resource_filter/keep_origin_traceid"
    config:
      keep_origin_traceid:
        enabled: true
`
	const disabledContent = `
processor:
  - name: "resource_filter/keep_origin_traceid"
    config:
      keep_origin_traceid:
        enabled: false
`

	newFactory := func(conf string) processor.Processor {
		return processor.MustCreateFactory(conf, NewFactory)
	}

	t.Run("opentelemetry enabled", func(t *testing.T) {
		factory := newFactory(enabledContent)
		data := makeOpenTelemetryTraces(3)
		orig := testkits.FirstSpan(data).TraceID().HexString()

		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}
		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs := testkits.FirstSpanAttrs(record.Data)
		testkits.AssertAttrsStringKeyVal(t, attrs, keyOriginTraceID, orig)
	})

	t.Run("skywalking enabled", func(t *testing.T) {
		factory := newFactory(enabledContent)
		orig := random.TraceID().HexString()
		data := makeSkyWalkingTraces(2, orig)

		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}
		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs := testkits.FirstSpanAttrs(record.Data)
		testkits.AssertAttrsStringKeyVal(t, attrs, keyOriginTraceID, orig)
		testkits.AssertAttrsNotFound(t, attrs, keySw8TraceID)
	})

	t.Run("opentelemetry disabled", func(t *testing.T) {
		factory := newFactory(disabledContent)
		data := makeOpenTelemetryTraces(1)

		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}
		testkits.MustProcess(t, factory, record)

		attrs := testkits.FirstSpanAttrs(record.Data)
		testkits.AssertAttrsNotFound(t, attrs, keyOriginTraceID)
	})

	t.Run("skywalking disabled", func(t *testing.T) {
		factory := newFactory(disabledContent)
		orig := random.TraceID().HexString()
		data := makeSkyWalkingTraces(1, orig)

		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}
		testkits.MustProcess(t, factory, record)

		attrs := testkits.FirstSpanAttrs(record.Data)
		testkits.AssertAttrsStringKeyVal(t, attrs, keySw8TraceID, orig)
		testkits.AssertAttrsNotFound(t, attrs, keyOriginTraceID)
	})

	t.Run("mixed opentelemetry and skywalking traces enabled", func(t *testing.T) {
		factory := processor.MustCreateFactory(enabledContent, NewFactory)

		traces := ptrace.NewTraces()
		traceIDOT := random.TraceID()
		traceIDSW := random.TraceID()

		// Use helper functions to add resource spans
		addOpenTelemetryResourceSpan(traces, traceIDOT, 2)
		addSkyWalkingResourceSpan(traces, traceIDSW, 1)

		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       traces,
		}

		_, err := factory.Process(&record)
		assert.NoError(t, err)

		pd := record.Data.(ptrace.Traces)
		foundOT := false
		foundSW := false

		for i := 0; i < pd.ResourceSpans().Len(); i++ {
			rs := pd.ResourceSpans().At(i)
			attrs := rs.Resource().Attributes()

			sdk, hasSdk := attrs.Get(keySdkName)
			if !hasSdk {
				continue
			}

			switch sdk.AsString() {
			case sdkOpenTelemetry:
				testkits.AssertAttrsFound(t, attrs, keyOriginTraceID)
				testkits.AssertAttrsStringKeyVal(t, attrs, keyOriginTraceID, traceIDOT.HexString())
				foundOT = true

			case sdkSkyWalking:
				// sw8.trace_id should be removed
				testkits.AssertAttrsNotFound(t, attrs, keySw8TraceID)
				// origin.trace_id should be set
				testkits.AssertAttrsFound(t, attrs, keyOriginTraceID)
				testkits.AssertAttrsStringKeyVal(t, attrs, keyOriginTraceID, traceIDSW.HexString())
				foundSW = true
			}
		}

		assert.True(t, foundOT, "Should process OpenTelemetry traces")
		assert.True(t, foundSW, "Should process SkyWalking traces")
	})

	t.Run("mixed sdk types disabled", func(t *testing.T) {
		factory := processor.MustCreateFactory(disabledContent, NewFactory)

		traces := ptrace.NewTraces()
		traceID := random.TraceID()

		// Use helper functions to add resource spans
		addOpenTelemetryResourceSpan(traces, traceID, 1)
		addSkyWalkingResourceSpan(traces, traceID, 1)

		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       traces,
		}

		_, err := factory.Process(&record)
		assert.NoError(t, err)

		pd := record.Data.(ptrace.Traces)
		for i := 0; i < pd.ResourceSpans().Len(); i++ {
			attrs := pd.ResourceSpans().At(i).Resource().Attributes()

			// origin.trace_id should not be added when disabled
			testkits.AssertAttrsNotFound(t, attrs, keyOriginTraceID)

			// sw8.trace_id should remain when disabled
			if sdk, ok := attrs.Get(keySdkName); ok && sdk.AsString() == sdkSkyWalking {
				testkits.AssertAttrsFound(t, attrs, keySw8TraceID)
			}
		}
	})
}

func TestReplaceActionWithExtraction(t *testing.T) {
	// 测试正则表达式提取
	t.Run("regex extraction", func(t *testing.T) {
		content := `
processor:
    - name: "resource_filter/replace"
      config:
        replace:
          - source: telemetry.target
            destination: service.name
            extract_pattern: '.*\.(.*\..*)'
`
		factory := processor.MustCreateFactory(content, NewFactory)
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       makeTracesRecord(1, "string"),
		}

		// 修改原始值以包含前缀和后缀
		attrs := testkits.FirstSpanAttrs(record.Data)
		attrs.UpsertString("telemetry.target", "BCS.test.helloworld")

		testkits.MustProcess(t, factory, record)

		processedAttrs := testkits.FirstSpanAttrs(record.Data)
		testkits.AssertAttrsNotFound(t, processedAttrs, "telemetry.target")
		testkits.AssertAttrsStringKeyVal(t, processedAttrs, "service.name", "test.helloworld")
	})

	// 测试所有数据类型
	t.Run("all data types", func(t *testing.T) {
		content := `
processor:
    - name: "resource_filter/replace"
      config:
        replace:
          - source: telemetry.target
            destination: service.name
            extract_pattern: '.*\.(.*\..*)'
`
		assertFunc := func(t *testing.T, attrs pcommon.Map) {
			testkits.AssertAttrsNotFound(t, attrs, "telemetry.target")
			testkits.AssertAttrsStringKeyVal(t, attrs, "service.name", "test.helloworld")
		}

		t.Run("traces", func(t *testing.T) {
			factory := processor.MustCreateFactory(content, NewFactory)
			record := define.Record{
				RecordType: define.RecordTraces,
				Data:       makeTracesRecord(1, "string"),
			}
			// 修改原始值以包含前缀和后缀
			attrs := testkits.FirstSpanAttrs(record.Data)
			attrs.UpsertString("telemetry.target", "BCS.test.helloworld")

			testkits.MustProcess(t, factory, record)

			assertFunc(t, testkits.FirstSpanAttrs(record.Data))
		})

		t.Run("metrics", func(t *testing.T) {
			factory := processor.MustCreateFactory(content, NewFactory)
			record := define.Record{
				RecordType: define.RecordMetrics,
				Data:       makeMetricsRecord(1, "string"),
			}
			// 修改原始值以包含前缀和后缀
			attrs := testkits.FirstMetricAttrs(record.Data)
			attrs.UpsertString("telemetry.target", "BCS.test.helloworld")

			testkits.MustProcess(t, factory, record)

			assertFunc(t, testkits.FirstMetricAttrs(record.Data))
		})

		t.Run("logs", func(t *testing.T) {
			factory := processor.MustCreateFactory(content, NewFactory)
			record := define.Record{
				RecordType: define.RecordLogs,
				Data:       makeLogsRecord(10, 10, "string"),
			}
			// 修改原始值以包含前缀和后缀
			attrs := testkits.FirstLogRecordAttrs(record.Data)
			attrs.UpsertString("telemetry.target", "BCS.test.helloworld")

			testkits.MustProcess(t, factory, record)

			assertFunc(t, testkits.FirstLogRecordAttrs(record.Data))
		})
	})
}

func TestRegroupResourceSpansByTraceID(t *testing.T) {
	t.Run("empty traces", func(t *testing.T) {
		empty := ptrace.NewTraces()
		regrouped := regroupResourceSpansByTraceID(empty)
		assert.Equal(t, 0, regrouped.ResourceSpans().Len())
	})

	t.Run("single trace id", func(t *testing.T) {
		traceID := random.TraceID()
		orig, rs := createTracesWithResourceSpan(map[string]string{"service.name": "test-service"})
		addScopeSpanWithSpans(rs, "test-scope", traceID, 2)

		regrouped := regroupResourceSpansByTraceID(orig)
		assert.Equal(t, 1, regrouped.ResourceSpans().Len())

		firstRS := regrouped.ResourceSpans().At(0)
		attrs := firstRS.Resource().Attributes()
		testkits.AssertAttrsFound(t, attrs, "service.name")
		testkits.AssertAttrsStringKeyVal(t, attrs, "service.name", "test-service")
	})

	t.Run("multiple trace ids with overlapping resources", func(t *testing.T) {
		orig := ptrace.NewTraces()
		traceIDA := random.TraceID()
		traceIDB := random.TraceID()

		// Resource span 1: contains 2 spans with traceID A
		rs1 := orig.ResourceSpans().AppendEmpty()
		rs1.Resource().Attributes().InsertString("service.name", "svc1")
		addScopeSpanWithSpans(rs1, "scope1", traceIDA, 2)

		// Resource span 2: contains 1 span with traceID A and 1 span with traceID B
		rs2 := orig.ResourceSpans().AppendEmpty()
		rs2.Resource().Attributes().InsertString("service.name", "svc2")
		ss2 := rs2.ScopeSpans().AppendEmpty()
		ss2.Scope().SetName("scope2")
		createSpanInScopeSpan(ss2, traceIDA)
		createSpanInScopeSpan(ss2, traceIDB)

		// Original has 2 resource spans
		assert.Equal(t, 2, orig.ResourceSpans().Len())

		regrouped := regroupResourceSpansByTraceID(orig)

		// After regrouping, should have 2 resource spans: one for traceID A, one for traceID B
		assert.Equal(t, 2, regrouped.ResourceSpans().Len())

		// Verify each trace ID is grouped correctly
		traceToService := make(map[string]string)
		for i := 0; i < regrouped.ResourceSpans().Len(); i++ {
			rs := regrouped.ResourceSpans().At(i)
			serviceName, _ := rs.Resource().Attributes().Get("service.name")

			for j := 0; j < rs.ScopeSpans().Len(); j++ {
				ss := rs.ScopeSpans().At(j)
				for k := 0; k < ss.Spans().Len(); k++ {
					span := ss.Spans().At(k)
					tid := span.TraceID().HexString()

					if existing, ok := traceToService[tid]; ok {
						// All spans with same trace ID should be in same resource
						assert.Equal(t, existing, serviceName.AsString())
					} else {
						traceToService[tid] = serviceName.AsString()
					}
				}
			}
		}

		// Verify trace A uses first occurrence resource (svc1)
		assert.Equal(t, "svc1", traceToService[traceIDA.HexString()])
		// Verify trace B uses its original resource (svc2)
		assert.Equal(t, "svc2", traceToService[traceIDB.HexString()])
	})
}
