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
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
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

func makeTracesGenerator(n int, valueType string) *generator.TracesGenerator {
	opts := define.TracesOptions{SpanCount: n}
	opts.Resources = map[string]string{
		resourceKey1: "key1",
		resourceKey2: "key2",
		resourceKey3: "key3",
		resourceKey4: "key4",
	}
	opts.DimensionsValueType = valueType
	return generator.NewTracesGenerator(opts)
}

func makeMetricsGenerator(n int, valueType string) *generator.MetricsGenerator {
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
	return generator.NewMetricsGenerator(opts)
}

func makeLogsGenerator(count, length int, valueType string) *generator.LogsGenerator {
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
	return generator.NewLogsGenerator(opts)
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
		g := makeTracesGenerator(1, "string")
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}

		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs := record.Data.(ptrace.Traces).ResourceSpans().At(0).Resource().Attributes()
		testkits.AssertAttrsFoundStringVal(t, attrs, "resource_final", "key1::key2:key3:key4")
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

	assertDropActionAttrs := func(t *testing.T, attrs pcommon.Map) {
		testkits.AssertAttrsNotFound(t, attrs, "resource_key1")
		testkits.AssertAttrsFound(t, attrs, "resource_key2")
		testkits.AssertAttrsFound(t, attrs, "resource_key3")
	}

	t.Run("traces", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		g := makeTracesGenerator(1, "string")
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}

		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs := record.Data.(ptrace.Traces).ResourceSpans().At(0).Resource().Attributes()
		assertDropActionAttrs(t, attrs)
	})

	t.Run("metrics", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		g := makeMetricsGenerator(1, "string")
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordMetrics,
			Data:       data,
		}

		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs := record.Data.(pmetric.Metrics).ResourceMetrics().At(0).Resource().Attributes()
		assertDropActionAttrs(t, attrs)
	})

	t.Run("logs", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		g := makeLogsGenerator(10, 10, "string")
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordLogs,
			Data:       data,
		}

		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs := record.Data.(plog.Logs).ResourceLogs().At(0).Resource().Attributes()
		assertDropActionAttrs(t, attrs)
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

	assertReplaceActionAttrs := func(t *testing.T, attrs pcommon.Map) {
		testkits.AssertAttrsNotFound(t, attrs, resourceKey1)
		testkits.AssertAttrsFound(t, attrs, resourceKey4)
	}

	t.Run("traces", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		g := makeTracesGenerator(1, "string")
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}

		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs := record.Data.(ptrace.Traces).ResourceSpans().At(0).Resource().Attributes()
		assertReplaceActionAttrs(t, attrs)
	})

	t.Run("metrics", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		g := makeMetricsGenerator(1, "float")
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordMetrics,
			Data:       data,
		}

		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs := record.Data.(pmetric.Metrics).ResourceMetrics().At(0).Resource().Attributes()
		assertReplaceActionAttrs(t, attrs)
	})

	t.Run("logs", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		g := makeLogsGenerator(10, 10, "float")
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordLogs,
			Data:       data,
		}

		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs := record.Data.(plog.Logs).ResourceLogs().At(0).Resource().Attributes()
		assertReplaceActionAttrs(t, attrs)
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
	assertAddActionLabels := func(t *testing.T, attrs pcommon.Map) {
		testkits.AssertAttrsFoundStringVal(t, attrs, "label1", "value1")
		testkits.AssertAttrsFoundStringVal(t, attrs, "label2", "value2")
	}

	t.Run("traces", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		g := makeTracesGenerator(1, "bool")
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}

		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs := record.Data.(ptrace.Traces).ResourceSpans().At(0).Resource().Attributes()
		assertAddActionLabels(t, attrs)
	})

	t.Run("metrics", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		g := makeMetricsGenerator(1, "int")
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordMetrics,
			Data:       data,
		}

		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs := record.Data.(pmetric.Metrics).ResourceMetrics().At(0).Resource().Attributes()
		assertAddActionLabels(t, attrs)
	})

	t.Run("logs", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		g := makeLogsGenerator(10, 10, "int")
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordLogs,
			Data:       data,
		}

		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs := record.Data.(plog.Logs).ResourceLogs().At(0).Resource().Attributes()
		assertAddActionLabels(t, attrs)
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

	t.Run("traces", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		g := makeTracesGenerator(1, "bool")
		data := g.Generate()
		record := define.Record{
			RecordType:    define.RecordTraces,
			Data:          data,
			RequestClient: define.RequestClient{IP: "127.1.1.1"},
		}

		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs := record.Data.(ptrace.Traces).ResourceSpans().At(0).Resource().Attributes()
		testkits.AssertAttrsFoundStringVal(t, attrs, "client.ip", "127.1.1.1")
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
			},
		})
		w.Write(b)
	}))
	defer svr.Close()

	content := fmt.Sprintf(`
processor:
    - name: "resource_filter/from_cache"
      config:
        from_cache:
          key: "resource.net.host.ip|resource.client.ip"
          cache:
            url: %s
            interval: "1m"
            timeout: "1m"
`, svr.URL)

	t.Run("traces net.host.ip", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		time.Sleep(time.Second) // wait for syncing
		g := makeTracesGenerator(1, "bool")
		data := g.Generate()
		data.ResourceSpans().At(0).Resource().Attributes().InsertString("net.host.ip", "127.1.0.1")
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}

		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs := record.Data.(ptrace.Traces).ResourceSpans().At(0).Resource().Attributes()

		testkits.AssertAttrsFoundStringVal(t, attrs, "k8s.pod.ip", "127.1.0.1")
		testkits.AssertAttrsFoundStringVal(t, attrs, "k8s.pod.name", "myapp1")
		testkits.AssertAttrsFoundStringVal(t, attrs, "k8s.namespace.name", "my-ns1")
		testkits.AssertAttrsFoundStringVal(t, attrs, "k8s.bcs.cluster.id", "K8S-BCS-00000")
	})

	t.Run("traces client.ip", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		time.Sleep(time.Second) // wait for syncing
		g := makeTracesGenerator(1, "bool")
		data := g.Generate()
		data.ResourceSpans().At(0).Resource().Attributes().InsertString("client.ip", "127.1.0.2")
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}

		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs := record.Data.(ptrace.Traces).ResourceSpans().At(0).Resource().Attributes()

		testkits.AssertAttrsFoundStringVal(t, attrs, "k8s.pod.ip", "127.1.0.2")
		testkits.AssertAttrsFoundStringVal(t, attrs, "k8s.pod.name", "myapp2")
		testkits.AssertAttrsFoundStringVal(t, attrs, "k8s.namespace.name", "my-ns2")
		testkits.AssertAttrsFoundStringVal(t, attrs, "k8s.bcs.cluster.id", "K8S-BCS-90000")
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
		g := makeTracesGenerator(1, "bool")
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
			Metadata:   tokenparser.FromHttpUserMetadata(r),
		}

		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs := record.Data.(ptrace.Traces).ResourceSpans().At(0).Resource().Attributes()

		testkits.AssertAttrsFoundStringVal(t, attrs, "k8s.pod.ip", "127.1.0.2")
		testkits.AssertAttrsFoundStringVal(t, attrs, "k8s.pod.name", "myapp2")
		testkits.AssertAttrsFoundStringVal(t, attrs, "k8s.namespace.name", "my-ns2")
		testkits.AssertAttrsFoundStringVal(t, attrs, "k8s.bcs.cluster.id", "K8S-BCS-90000")
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

	assertFromTokenAction := func(t *testing.T, attrs pcommon.Map) {
		testkits.AssertAttrsFoundStringVal(t, attrs, "app_name", "test_app")
	}

	t.Run("traces from_token", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		g := makeTracesGenerator(1, "string")
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
			Token:      define.Token{AppName: "test_app"},
		}
		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs := record.Data.(ptrace.Traces).ResourceSpans().At(0).Resource().Attributes()
		assertFromTokenAction(t, attrs)

	})

	t.Run("metrics from_token", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		g := makeMetricsGenerator(1, "string")
		data := g.Generate()
		record1 := define.Record{
			RecordType: define.RecordMetrics,
			Data:       data,
			Token:      define.Token{AppName: "test_app"},
		}
		record2 := define.Record{
			RecordType: define.RecordMetricsDerived,
			Data:       data,
			Token:      define.Token{AppName: "test_app"},
		}
		_, err := factory.Process(&record1)
		assert.NoError(t, err)
		_, err = factory.Process(&record2)
		assert.NoError(t, err)

		attrs := record1.Data.(pmetric.Metrics).ResourceMetrics().At(0).Resource().Attributes()
		assertFromTokenAction(t, attrs)
		attrs = record2.Data.(pmetric.Metrics).ResourceMetrics().At(0).Resource().Attributes()
		assertFromTokenAction(t, attrs)
	})

	t.Run("logs from_token", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		g := makeLogsGenerator(1, 10, "string")
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordLogs,
			Data:       data,
			Token:      define.Token{AppName: "test_app"},
		}

		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs := record.Data.(plog.Logs).ResourceLogs().At(0).Resource().Attributes()
		assertFromTokenAction(t, attrs)
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
		g := makeTracesGenerator(1, "bool")
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}

		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs := record.Data.(ptrace.Traces).ResourceSpans().At(0).Resource().Attributes()
		testkits.AssertAttrsFoundStringVal(t, attrs, "service.name", "unknown_service")
	})

	t.Run("traces skipped", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		g := makeTracesGenerator(1, "bool")
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}

		attrs := record.Data.(ptrace.Traces).ResourceSpans().At(0).Resource().Attributes()
		attrs.InsertString("service.name", "app.v1")

		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs = record.Data.(ptrace.Traces).ResourceSpans().At(0).Resource().Attributes()
		testkits.AssertAttrsFoundStringVal(t, attrs, "service.name", "app.v1")
	})

	t.Run("metrics", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		g := makeMetricsGenerator(1, "bool")
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordMetrics,
			Data:       data,
		}

		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs := record.Data.(pmetric.Metrics).ResourceMetrics().At(0).Resource().Attributes()
		testkits.AssertAttrsFoundStringVal(t, attrs, "service.name", "unknown_service")
	})

	t.Run("metrics skipped", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		g := makeMetricsGenerator(1, "bool")
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordMetrics,
			Data:       data,
		}

		attrs := record.Data.(pmetric.Metrics).ResourceMetrics().At(0).Resource().Attributes()
		attrs.InsertString("service.name", "app.v1")

		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs = record.Data.(pmetric.Metrics).ResourceMetrics().At(0).Resource().Attributes()
		testkits.AssertAttrsFoundStringVal(t, attrs, "service.name", "app.v1")
	})

	t.Run("logs", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		g := makeLogsGenerator(1, 10, "bool")
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordLogs,
			Data:       data,
		}

		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs := record.Data.(plog.Logs).ResourceLogs().At(0).Resource().Attributes()
		testkits.AssertAttrsFoundStringVal(t, attrs, "service.name", "unknown_service")
	})

	t.Run("logs skipped", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		g := makeLogsGenerator(1, 10, "bool")
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordLogs,
			Data:       data,
		}

		attrs := record.Data.(plog.Logs).ResourceLogs().At(0).Resource().Attributes()
		attrs.InsertString("service.name", "app.v1")

		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs = record.Data.(plog.Logs).ResourceLogs().At(0).Resource().Attributes()
		testkits.AssertAttrsFoundStringVal(t, attrs, "service.name", "app.v1")
	})
}
