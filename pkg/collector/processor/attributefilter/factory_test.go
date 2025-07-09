// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package attributefilter

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	semconv "go.opentelemetry.io/collector/semconv/v1.8.0"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
)

func TestFactory(t *testing.T) {
	content := `
processor:
  - name: "attribute_filter/as_string"
    config:
      as_string:
        keys:
          - "attributes.http.host"
`
	mainConf := processor.MustLoadConfigs(content)[0].Config

	customContent := `
processor:
  - name: "attribute_filter/as_string"
    config:
      as_string:
        keys:
          - "attributes.http.port"
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
	factory := obj.(*attributeFilter)
	assert.NoError(t, err)
	assert.Equal(t, mainConf, factory.MainConfig())

	mainConfig := factory.configs.GetGlobal().(Config)
	assert.Equal(t, "http.host", mainConfig.AsString.Keys[0])

	customConfig := factory.configs.GetByToken("token1").(Config)
	assert.Equal(t, "http.port", customConfig.AsString.Keys[0])

	assert.Equal(t, define.ProcessorAttributeFilter, factory.Name())
	assert.False(t, factory.IsDerived())
	assert.False(t, factory.IsPreCheck())

	factory.Reload(mainConf, nil)
	assert.Equal(t, mainConf, factory.MainConfig())
}

const (
	resourceKeyPerIp   = "net.peer.ip"
	resourceKeyPerPort = "net.peer.port"
)

func makeTracesGenerator(n int, valueType string) *generator.TracesGenerator {
	opts := define.TracesOptions{SpanCount: n}
	opts.RandomAttributeKeys = []string{
		resourceKeyPerIp,
		resourceKeyPerPort,
	}
	opts.DimensionsValueType = valueType
	return generator.NewTracesGenerator(opts)
}

func makeTracesAttributesGenerator(n int, attrs map[string]string) *generator.TracesGenerator {
	opts := define.TracesOptions{SpanKind: n}
	opts.SpanCount = 1
	opts.Attributes = attrs
	opts.Resources = map[string]string{
		"http.status_code": "200",
	}
	return generator.NewTracesGenerator(opts)
}

func makeLogsGenerator(n int, valueType string) *generator.LogsGenerator {
	opts := define.LogsOptions{LogName: "testlog", LogCount: n, LogLength: 10}
	opts.RandomAttributeKeys = []string{"attr1", "attr2"}
	opts.DimensionsValueType = valueType
	opts.RandomAttributeKeys = []string{
		resourceKeyPerIp,
		resourceKeyPerPort,
	}
	return generator.NewLogsGenerator(opts)
}

func makeLogsAttributesGenerator(n int, attrs map[string]string) *generator.LogsGenerator {
	return generator.NewLogsGenerator(define.LogsOptions{
		GeneratorOptions: define.GeneratorOptions{
			Resources:  map[string]string{"foo": "bar"},
			Attributes: attrs,
		},
		LogName:   "testlog",
		LogCount:  n,
		LogLength: 10,
	})
}

func makeMetricsGenerator(n int, valueType string) *generator.MetricsGenerator {
	opts := define.MetricsOptions{GaugeCount: n}
	opts.RandomResourceKeys = []string{
		resourceKeyPerIp,
		resourceKeyPerPort,
	}
	opts.DimensionsValueType = valueType
	return generator.NewMetricsGenerator(opts)
}

func TestAsStringAction(t *testing.T) {
	content := `
processor:
   - name: "attribute_filter/as_string"
     config:
       as_string:
         keys:
           - "attributes.net.peer.ip"
`
	t.Run("traces", func(t *testing.T) {
		testAsStringAction := func(t *testing.T, valueType string) {
			factory := processor.MustCreateFactory(content, NewFactory)

			g := makeTracesGenerator(1, valueType)
			data := g.Generate()
			record := define.Record{
				RecordType: define.RecordTraces,
				Data:       data,
			}

			_, err := factory.Process(&record)
			assert.NoError(t, err)

			span := testkits.FirstSpan(record.Data.(ptrace.Traces))
			attrs := span.Attributes()
			v, ok := attrs.Get(resourceKeyPerIp)
			assert.True(t, ok)
			assert.Equal(t, pcommon.ValueTypeString, v.Type())
		}

		tests := []string{"bool", "int", "float"}
		for _, tt := range tests {
			t.Run(fmt.Sprintf("traces %s as string", tt), func(t *testing.T) {
				testAsStringAction(t, tt)
			})
		}
	})

	t.Run("logs", func(t *testing.T) {
		testAsStringAction := func(t *testing.T, valueType string) {
			factory := processor.MustCreateFactory(content, NewFactory)
			record := define.Record{
				RecordType: define.RecordLogs,
				Data:       makeLogsGenerator(1, valueType).Generate(),
			}
			_, err := factory.Process(&record)
			assert.NoError(t, err)

			rsAttrs := testkits.FirstLogRecord(record.Data.(plog.Logs)).Attributes()
			v, ok := rsAttrs.Get(resourceKeyPerIp)
			assert.True(t, ok)
			assert.Equal(t, pcommon.ValueTypeString, v.Type())
		}

		tests := []string{"bool", "int", "float"}
		for _, tt := range tests {
			t.Run(fmt.Sprintf("logs %s as string", tt), func(t *testing.T) {
				testAsStringAction(t, tt)
			})
		}
	})

}

func TestAsIntAction(t *testing.T) {
	content := `
processor:
  - name: "attribute_filter/common"
    config:
      as_int:
        keys:
          - "attributes.http.status_code"
          - "attributes.http.scheme"
`
	assertFunc := func(attrs pcommon.Map) {
		testkits.AssertAttrsFoundIntVal(t, attrs, semconv.AttributeHTTPStatusCode, 200)
		testkits.AssertAttrsFoundStringVal(t, attrs, semconv.AttributeHTTPScheme, "https")
	}

	t.Run("traces", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		m := map[string]string{
			"http.status_code": "200",
			"http.scheme":      "https",
		}
		g := makeTracesAttributesGenerator(int(ptrace.SpanKindUnspecified), m)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}
		_, err := factory.Process(&record)
		assert.NoError(t, err)
		assertFunc(testkits.FirstSpan(record.Data.(ptrace.Traces)).Attributes())
	})

	t.Run("logs", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		m := map[string]string{
			"http.status_code": "200",
			"http.scheme":      "https",
		}
		g := makeLogsAttributesGenerator(1, m)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordLogs,
			Data:       data,
		}
		_, err := factory.Process(&record)
		assert.NoError(t, err)
		assertFunc(testkits.FirstLogRecord(record.Data.(plog.Logs)).Attributes())
	})
}

func TestFromTokenAction(t *testing.T) {
	content := `
processor:
   - name: "attribute_filter/from_token"
     config:
       from_token:
         biz_id: "bk_biz_id"
         app_name: "bk_app_name"
`

	assertFunc := func(attrs pcommon.Map) {
		testkits.AssertAttrsStringVal(t, attrs, "bk_biz_id", "10086")
		testkits.AssertAttrsStringVal(t, attrs, "bk_app_name", "my_app_name")
	}

	t.Run("traces", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		g := makeTracesGenerator(1, "float")
		data := g.Generate()
		record := &define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
			Token: define.Token{
				BizId:   10086,
				AppName: "my_app_name",
			},
		}

		_, err := factory.Process(record)
		assert.NoError(t, err)
		assertFunc(testkits.FirstSpan(record.Data.(ptrace.Traces)).Attributes())
	})

	t.Run("logs", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		g := makeLogsGenerator(1, "int")
		data := g.Generate()
		record := &define.Record{
			RecordType: define.RecordLogs,
			Data:       data,
			Token: define.Token{
				BizId:   10086,
				AppName: "my_app_name",
			},
		}

		_, err := factory.Process(record)
		assert.NoError(t, err)
		assertFunc(testkits.FirstLogRecord(record.Data.(plog.Logs)).Attributes())
	})

	t.Run("metrics", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)

		g := makeMetricsGenerator(1, "float")
		data := g.Generate()
		record := &define.Record{
			RecordType: define.RecordMetrics,
			Data:       data,
			Token: define.Token{
				BizId:   10086,
				AppName: "my_app_name",
			},
		}

		_, err := factory.Process(record)
		assert.NoError(t, err)
		assertFunc(testkits.FirstGaugeDataPoint(record.Data.(pmetric.Metrics)).Attributes())
	})
}

func TestAssembleAction(t *testing.T) {
	content := `
processor:
  - name: "attribute_filter/common"
    config:
      assemble:
        - destination: "api_name"
          predicate_key: "attributes.http.scheme"
          default_from: "span_name"
          rules:
            - kind: "SPAN_KIND_SERVER"
              first_upper:
                - "attributes.http.method"
              keys:
                - "attributes.http.method"
                - "attributes.http.route"
                - "unmatchedKey"
              separator: ":"
              placeholder: ""
`
	t.Run("traces", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		m := map[string]string{
			"http.scheme": "HTTP",
			"http.method": "gET",
			"http.route":  "testRoute",
		}
		g := makeTracesAttributesGenerator(int(ptrace.SpanKindServer), m)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}
		_, err := factory.Process(&record)
		assert.NoError(t, err)

		span := testkits.FirstSpan(record.Data.(ptrace.Traces))
		testkits.AssertAttrsFoundStringVal(t, span.Attributes(), "api_name", "Get:testRoute:")
	})
}

func TestAssembleActionWithoutKind(t *testing.T) {
	content := `
processor:
  - name: "attribute_filter/common"
    config:
      assemble:
        - destination: "api_name"
          predicate_key: "attributes.rpc.system"
          default_from: "span_name"
          rules:
            - kind: ""
              first_upper:
                - "attributes.rpc.method"
              keys:
                - "attributes.rpc.method"
                - "const.TestConstCondition"
                - "unmatchedKey"
              separator: ":"
              placeholder: "placeholder"
`
	t.Run("traces", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		m := map[string]string{
			"rpc.system": "PRC",
			"rpc.method": "rpcMethod",
		}
		g := makeTracesAttributesGenerator(int(ptrace.SpanKindUnspecified), m)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}
		_, err := factory.Process(&record)
		assert.NoError(t, err)

		span := testkits.FirstSpan(record.Data.(ptrace.Traces))
		testkits.AssertAttrsFoundStringVal(t, span.Attributes(), "api_name", "Rpcmethod:TestConstCondition:placeholder")
	})
}

func TestAssembleActionWithPlaceholder(t *testing.T) {
	content := `
processor:
  - name: "attribute_filter/common"
    config:
      assemble:
        - destination: "api_name"
          predicate_key: "attributes.rpc.system"
          default_from: "span_name"
          rules:
            - kind: ""
              first_upper:
                - "attributes.rpc.method"
              keys:
                - "attributes.rpc.method"
                - "const.TestConstCondition"
              separator: ":"
              placeholder: "Unknown"
`
	t.Run("traces", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		m := map[string]string{
			"rpc.system": "PRC",
		}
		g := makeTracesAttributesGenerator(int(ptrace.SpanKindUnspecified), m)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}
		_, err := factory.Process(&record)
		assert.NoError(t, err)

		span := testkits.FirstSpan(record.Data.(ptrace.Traces))
		testkits.AssertAttrsFoundStringVal(t, span.Attributes(), "api_name", "Unknown:TestConstCondition")
	})
}

func TestAssembleActionWithoutPredicate(t *testing.T) {
	t.Run("traces defaultFrom/null", func(t *testing.T) {
		content := `
processor:
  - name: "attribute_filter/common"
    config:
      assemble:
        - destination: "api_name"
          predicate_key: "attributes.rpc.system"
          default_from: ""
          rules:
            - kind: ""
              first_upper:
                - "attributes.rpc.method"
              keys:
                - "attributes.rpc.method"
                - "const.TestConstCondition"
              separator: ":"
              placeholder: "Unknown"
`
		factory := processor.MustCreateFactory(content, NewFactory)
		m := map[string]string{
			"http.scheme": "HTTP",
		}
		g := makeTracesAttributesGenerator(int(ptrace.SpanKindUnspecified), m)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}
		_, err := factory.Process(&record)
		assert.NoError(t, err)

		span := testkits.FirstSpan(record.Data.(ptrace.Traces))
		testkits.AssertAttrsNotFound(t, span.Attributes(), "api_name")
	})

	t.Run("traces defaultFrom/span_name", func(t *testing.T) {
		content := `
processor:
  - name: "attribute_filter/common"
    config:
      assemble:
        - destination: "api_name"
          predicate_key: "attributes.rpc.system"
          default_from: "span_name"
          rules:
            - kind: ""
              first_upper:
                - "attributes.rpc.method"
              keys:
                - "attributes.rpc.method"
                - "const.TestConstCondition"
              separator: ":"
              placeholder: "Unknown"
`
		factory := processor.MustCreateFactory(content, NewFactory)

		m := map[string]string{
			"http.scheme": "HTTP",
		}
		g := makeTracesAttributesGenerator(int(ptrace.SpanKindUnspecified), m)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}
		_, err := factory.Process(&record)
		assert.NoError(t, err)

		span := testkits.FirstSpan(record.Data.(ptrace.Traces))
		testkits.AssertAttrsFoundStringVal(t, span.Attributes(), "api_name", span.Name())
	})

	t.Run("traces defaultFrom/const", func(t *testing.T) {
		content := `
processor:
  - name: "attribute_filter/common"
    config:
      assemble:
        - destination: "api_name"
          predicate_key: "attributes.rpc.system"
          default_from: "const.TestDefaultFrom"
          rules:
            - kind: ""
              first_upper:
                - "attributes.rpc.method"
              keys:
                - "attributes.rpc.method"
                - "const.TestConstCondition"
              separator: ":"
              placeholder: "Unknown"
`
		factory := processor.MustCreateFactory(content, NewFactory)

		m := map[string]string{
			"http.scheme": "HTTP",
		}
		g := makeTracesAttributesGenerator(int(ptrace.SpanKindUnspecified), m)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}
		_, err := factory.Process(&record)
		assert.NoError(t, err)

		span := testkits.FirstSpan(record.Data.(ptrace.Traces))
		testkits.AssertAttrsFoundStringVal(t, span.Attributes(), "api_name", "TestDefaultFrom")
	})
}

func TestAssembleActionWithoutDefault(t *testing.T) {
	content := `
processor:
  - name: "attribute_filter/common"
    config:
      assemble:
        - destination: "api_name"
          predicate_key: "attributes.rpc.system"
          default_from: ""
          rules:
            - kind: ""
              first_upper:
                - "attributes.rpc.method"
              keys:
                - "attributes.rpc.method"
                - "const.TestConstCondition"
              separator: ":"
              placeholder: ""
`
	t.Run("traces", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		m := map[string]string{
			"http.scheme": "HTTP",
		}
		g := makeTracesAttributesGenerator(int(ptrace.SpanKindUnspecified), m)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}
		_, err := factory.Process(&record)
		assert.NoError(t, err)

		span := testkits.FirstSpan(record.Data.(ptrace.Traces))
		testkits.AssertAttrsNotFound(t, span.Attributes(), "api_name")
	})
}

func TestAssembleActionWithNullValue(t *testing.T) {
	content := `
processor:
  - name: "attribute_filter/common"
    config:
      assemble:
        - destination: "api_name"
          predicate_key: "attributes.rpc.system"
          default_from: "span_name"
          rules:
            - kind: ""
              first_upper:
                - "attributes.rpc.method"
              keys:
                - "attributes.rpc.method"
                - "const.TestConstCondition"
                - "attributes.rpc.target"
              separator: ":"
              placeholder: ""
`
	t.Run("traces", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		m := map[string]string{
			"rpc.system": "rpc",
			"rpc.method": "rpcMethod",
			"rpc.target": "",
		}
		g := makeTracesAttributesGenerator(int(ptrace.SpanKindUnspecified), m)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}
		_, err := factory.Process(&record)
		assert.NoError(t, err)

		span := testkits.FirstSpan(record.Data.(ptrace.Traces))
		testkits.AssertAttrsFoundStringVal(t, span.Attributes(), "api_name", "Rpcmethod:TestConstCondition:")
	})
}

func TestDropAction(t *testing.T) {
	content := `
processor:
  - name: "attribute_filter/common"
    config:
      drop:
        - predicate_key: "attributes.db.system"
          match:
            - "mysql"
            - "postgresql"
            - "elasticsearch"
          keys:
            - "attributes.db.parameters"
            - "attributes.db.statement"
`
	assertFunc := func(attrs pcommon.Map) {
		testkits.AssertAttrsNotFound(t, attrs, semconv.AttributeDBStatement)
		testkits.AssertAttrsNotFound(t, attrs, "db.parameters")
	}
	t.Run("traces", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		m := map[string]string{
			"db.system":     "mysql",
			"db.parameters": "testDbParameters",
			"db.statement":  "testDbStatement",
		}
		g := makeTracesAttributesGenerator(int(ptrace.SpanKindUnspecified), m)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}
		_, err := factory.Process(&record)
		assert.NoError(t, err)
		assertFunc(testkits.FirstSpan(record.Data.(ptrace.Traces)).Attributes())
	})

	t.Run("logs", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		m := map[string]string{
			"db.system":     "mysql",
			"db.parameters": "testDbParameters",
			"db.statement":  "testDbStatement",
		}
		g := makeLogsAttributesGenerator(1, m)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordLogs,
			Data:       data,
		}
		_, err := factory.Process(&record)
		assert.NoError(t, err)
		assertFunc(testkits.FirstLogRecord(record.Data.(plog.Logs)).Attributes())
	})

	t.Run("traces unmatched predicate key", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		m := map[string]string{
			"db.system":     "",
			"db.parameters": "testDbParameters",
			"db.statement":  "testDbStatement",
		}
		g := makeTracesAttributesGenerator(int(ptrace.SpanKindUnspecified), m)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}
		_, err := factory.Process(&record)
		assert.NoError(t, err)

		attrs := testkits.FirstSpan(record.Data.(ptrace.Traces)).Attributes()
		testkits.AssertAttrsFound(t, attrs, semconv.AttributeDBStatement)
		testkits.AssertAttrsFound(t, attrs, "db.parameters")
	})
}

func TestCutAction(t *testing.T) {
	content := `
processor:
  - name: "attribute_filter/common"
    config:
      cut:
        - predicate_key: "attributes.db.system"
          match:
            - "mysql"
            - "postgresql"
          max_length: 10
          keys:
            - "attributes.db.parameters"
            - "attributes.db.statement"
`
	const maxLen = 10
	assertFunc := func(attrs pcommon.Map) {
		testkits.AssertAttrsFoundStringVal(t, attrs, semconv.AttributeDBStatement, "testDbStatement"[:maxLen])
		testkits.AssertAttrsFoundStringVal(t, attrs, "db.parameters", "testDbParameters"[:maxLen])
	}

	t.Run("traces", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		m := map[string]string{
			"db.system":     "postgresql",
			"db.parameters": "testDbParameters",
			"db.statement":  "testDbStatement",
		}

		g := makeTracesAttributesGenerator(int(ptrace.SpanKindUnspecified), m)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}
		_, err := factory.Process(&record)
		assert.NoError(t, err)
		assertFunc(testkits.FirstSpan(record.Data.(ptrace.Traces)).Attributes())
	})

	t.Run("logs", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		m := map[string]string{
			"db.system":     "postgresql",
			"db.parameters": "testDbParameters",
			"db.statement":  "testDbStatement",
		}

		g := makeLogsAttributesGenerator(1, m)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordLogs,
			Data:       data,
		}
		_, err := factory.Process(&record)
		assert.NoError(t, err)
		assertFunc(testkits.FirstLogRecord(record.Data.(plog.Logs)).Attributes())
	})

	t.Run("traces unmatched predicate key", func(t *testing.T) {
		factory := processor.MustCreateFactory(content, NewFactory)
		m := map[string]string{
			"db.system":     "",
			"db.parameters": "testDbParameters",
			"db.statement":  "testDbStatement",
		}

		g := makeTracesAttributesGenerator(int(ptrace.SpanKindUnspecified), m)
		data := g.Generate()
		record := define.Record{
			RecordType: define.RecordTraces,
			Data:       data,
		}
		_, err := factory.Process(&record)
		assert.NoError(t, err)
		attrs := testkits.FirstSpan(record.Data.(ptrace.Traces)).Attributes()
		testkits.AssertAttrsFoundStringVal(t, attrs, semconv.AttributeDBStatement, "testDbStatement")
		testkits.AssertAttrsFoundStringVal(t, attrs, "db.parameters", "testDbParameters")
	})
}
