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

func testAsStringAction(t *testing.T, valueType string) {
	content := `
processor:
   - name: "attribute_filter/as_string"
     config:
       as_string:
         keys:
           - "attributes.net.peer.ip"

`
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

func TestTracesBoolAsStringAction(t *testing.T) {
	testAsStringAction(t, "bool")
}

func TestTracesIntAsStringAction(t *testing.T) {
	testAsStringAction(t, "int")
}

func TestTracesFloatAsStringAction(t *testing.T) {
	testAsStringAction(t, "float")
}

func TestLogsAsStringAction(t *testing.T) {
	content := `
processor:
  - name: "attribute_filter/logs"
    config:
      as_string:
        keys:
          - "attributes.attr1"
          - "attributes.attr2"
`
	factory := processor.MustCreateFactory(content, NewFactory)
	record := define.Record{
		RecordType: define.RecordLogs,
		Data:       makeLogsGenerator(1, "int").Generate(),
	}
	_, err := factory.Process(&record)
	assert.NoError(t, err)

	rsAttrs := testkits.FirstLogRecord(record.Data.(plog.Logs)).Attributes()
	v, ok := rsAttrs.Get("attr1")
	assert.True(t, ok)
	assert.Equal(t, pcommon.ValueTypeString, v.Type())
}

func TestTracesFromTokenAction(t *testing.T) {
	content := `
processor:
   - name: "attribute_filter/from_token"
     config:
       from_token:
         biz_id: "bk_biz_id"
         app_name: "bk_app_name"
`
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

	span := testkits.FirstSpan(record.Data.(ptrace.Traces))
	testkits.AssertAttrsStringVal(t, span.Attributes(), "bk_biz_id", "10086")
	testkits.AssertAttrsStringVal(t, span.Attributes(), "bk_app_name", "my_app_name")
	testkits.AssertAttrsNotFound(t, span.Attributes(), "traces_dataid")
}

func TestLogsFromTokenAction(t *testing.T) {
	content := `
processor:
   - name: "attribute_filter/from_token"
     config:
       from_token:
         biz_id: "bk_biz_id"
         app_name: "bk_app_name"
`
	factory := processor.MustCreateFactory(content, NewFactory)
	g := makeLogsGenerator(1, "int")
	data := g.Generate()
	record := &define.Record{
		RecordType: define.RecordLogs,
		Data:       data,
		Token: define.Token{
			BizId:   2147483647,
			AppName: "test_app_name",
		},
	}

	_, err := factory.Process(record)
	assert.NoError(t, err)

	logRecord := testkits.FirstLogRecord(record.Data.(plog.Logs))
	testkits.AssertAttrsStringVal(t, logRecord.Attributes(), "bk_biz_id", "2147483647")
	testkits.AssertAttrsStringVal(t, logRecord.Attributes(), "bk_app_name", "test_app_name")
	testkits.AssertAttrsNotFound(t, logRecord.Attributes(), "logs_dataid")
}

func TestMetricsFromTokenAction(t *testing.T) {
	content := `
processor:
   - name: "attribute_filter/from_token"
     config:
       from_token:
         biz_id: "bk_biz_id"
         app_name: "bk_app_name"
`
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

	dp := testkits.FirstGaugeDataPoint(record.Data.(pmetric.Metrics))
	testkits.AssertAttrsStringVal(t, dp.Attributes(), "bk_biz_id", "10086")
	testkits.AssertAttrsStringVal(t, dp.Attributes(), "bk_app_name", "my_app_name")
	testkits.AssertAttrsNotFound(t, dp.Attributes(), "metrics_dataid")
}

func TestTraceAssembleAction(t *testing.T) {
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
}

func TestTraceAssembleWithoutKind(t *testing.T) {
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
}

func TestTraceAssembleWithPlaceholder(t *testing.T) {
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
}

func TestTraceAssembleWithoutPredicate(t *testing.T) {
	t.Run("defaultFrom/null", func(t *testing.T) {
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

	t.Run("defaultFrom/span_name", func(t *testing.T) {
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

	t.Run("defaultFrom/const", func(t *testing.T) {
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

func TestTraceAssembleWithoutDefault(t *testing.T) {
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
}

func TestTraceAssembleWithNullValue(t *testing.T) {
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
}

func TestTraceAsIntAction(t *testing.T) {
	content := `
processor:
  - name: "attribute_filter/common"
    config:
      as_int:
        keys:
          - "attributes.http.status_code"
          - "attributes.http.scheme"
`
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

	span := testkits.FirstSpan(record.Data.(ptrace.Traces))
	attrs := span.Attributes()

	testkits.AssertAttrsFoundIntVal(t, attrs, semconv.AttributeHTTPStatusCode, 200)
	testkits.AssertAttrsFoundStringVal(t, attrs, semconv.AttributeHTTPScheme, "https")
}

func TestLogsAsIntAction(t *testing.T) {
	content := `
processor:
  - name: "attribute_filter/logs"
    config:
      as_int:
        keys:
          - "attributes.uid"
          - "attributes.http.url"
`
	factory := processor.MustCreateFactory(content, NewFactory)
	m := map[string]string{
		"uid":      "8430020787040790664",
		"http.url": "abc",
	}
	g := makeLogsAttributesGenerator(1, m)
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordLogs,
		Data:       data,
	}
	_, err := factory.Process(&record)
	assert.NoError(t, err)

	rsAttrs := testkits.FirstLogRecord(record.Data.(plog.Logs)).Attributes()

	// 如果可以转换，则成功
	testkits.AssertAttrsFoundIntVal(t, rsAttrs, "uid", 8430020787040790664)
	// 如果转换不了，则保留原来的值
	testkits.AssertAttrsFoundStringVal(t, rsAttrs, "http.url", "abc")
}

func TestTraceDropAction(t *testing.T) {
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

	span := testkits.FirstSpan(record.Data.(ptrace.Traces))
	attrs := span.Attributes()
	testkits.AssertAttrsNotFound(t, attrs, semconv.AttributeDBStatement)
	testkits.AssertAttrsNotFound(t, attrs, "db.parameters")
}

func TestLogDropAction(t *testing.T) {
	content := `
processor:
  - name: "attribute_filter/logs"
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

	logRecord := testkits.FirstLogRecord(record.Data.(plog.Logs))
	attrs := logRecord.Attributes()
	testkits.AssertAttrsNotFound(t, attrs, semconv.AttributeDBStatement)
	testkits.AssertAttrsNotFound(t, attrs, "db.parameters")
}

func TestTraceDropActionWithUnmatchedPreKey(t *testing.T) {
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

	span := testkits.FirstSpan(record.Data.(ptrace.Traces))
	attrs := span.Attributes()
	testkits.AssertAttrsFoundStringVal(t, attrs, semconv.AttributeDBStatement, "testDbStatement")
	testkits.AssertAttrsFoundStringVal(t, attrs, "db.parameters", "testDbParameters")
}

func TestTraceDropActionWithoutMatch(t *testing.T) {
	content := `
processor:
  - name: "attribute_filter/common"
    config:
      drop:
        - predicate_key: "attributes.db.system"
          keys:
            - "attributes.db.parameters"
            - "attributes.db.statement"
`
	factory := processor.MustCreateFactory(content, NewFactory)

	m := map[string]string{
		"db.system":     "elasticsearch",
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

	span := testkits.FirstSpan(record.Data.(ptrace.Traces))
	attrs := span.Attributes()
	testkits.AssertAttrsNotFound(t, attrs, semconv.AttributeDBStatement)
	testkits.AssertAttrsNotFound(t, attrs, "db.parameters")
}

func TestTraceCutAction(t *testing.T) {
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

	const maxLen = 10
	span := testkits.FirstSpan(record.Data.(ptrace.Traces))
	attrs := span.Attributes()
	testkits.AssertAttrsFoundStringVal(t, attrs, semconv.AttributeDBStatement, "testDbStatement"[:maxLen])
	testkits.AssertAttrsFoundStringVal(t, attrs, "db.parameters", "testDbParameters"[:maxLen])
}

func TestLogCutAction(t *testing.T) {
	content := `
processor:
  - name: "attribute_filter/logs"
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

	const maxLen = 10
	logRecord := testkits.FirstLogRecord(record.Data.(plog.Logs))
	attrs := logRecord.Attributes()

	testkits.AssertAttrsFoundStringVal(t, attrs, semconv.AttributeDBStatement, "testDbStatement"[:maxLen])
	testkits.AssertAttrsFoundStringVal(t, attrs, "db.parameters", "testDbParameters"[:maxLen])
}

func TestTraceCutActionWithUnmatchedPreKey(t *testing.T) {
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

	span := testkits.FirstSpan(record.Data.(ptrace.Traces))
	attrs := span.Attributes()
	testkits.AssertAttrsFoundStringVal(t, attrs, semconv.AttributeDBStatement, "testDbStatement")
	testkits.AssertAttrsFoundStringVal(t, attrs, "db.parameters", "testDbParameters")
}
