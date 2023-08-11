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

	"github.com/mitchellh/mapstructure"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	conventions "go.opentelemetry.io/collector/semconv/v1.8.0"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
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
	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*attributeFilter)
	assert.NoError(t, err)
	assert.Equal(t, psc[0].Config, factory.MainConfig())

	var c Config
	err = mapstructure.Decode(psc[0].Config, &c)
	assert.NoError(t, err)

	c.AsString.Keys[0] = "http.host"
	assert.Equal(t, c, factory.configs.Get("", "", "").(Config))

	assert.Equal(t, define.ProcessorAttributeFilter, factory.Name())
	assert.False(t, factory.IsDerived())
	assert.False(t, factory.IsPreCheck())
}

const (
	resourceKeyPerIp   = "net.peer.ip"
	resourceKeyPerPort = "net.peer.port"
)

func makeTracesGenerator(n int, valueType string) *generator.TracesGenerator {
	opts := define.TracesOptions{SpanCount: n}
	opts.RandomResourceKeys = []string{
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

func makeMetricsGenerator(n int, valueType string) *generator.MetricsGenerator {
	opts := define.MetricsOptions{GaugeCount: n}
	opts.RandomResourceKeys = []string{
		resourceKeyPerIp,
		resourceKeyPerPort,
	}
	opts.DimensionsValueType = valueType
	return generator.NewMetricsGenerator(opts)
}

func TestTracesBoolAsStringAction(t *testing.T) {
	g := makeTracesGenerator(1, "bool")
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	}

	configs := confengine.NewTierConfig()
	configs.SetGlobal(Config{
		AsString: AsStringAction{
			Keys: []string{resourceKeyPerIp},
		},
	})

	filter := &attributeFilter{configs: configs}
	_, err := filter.Process(&record)
	assert.NoError(t, err)

	attr := record.Data.(ptrace.Traces).ResourceSpans().At(0).Resource().Attributes()
	v, ok := attr.Get(resourceKeyPerIp)
	assert.True(t, ok)
	assert.Equal(t, pcommon.ValueTypeString, v.Type())
}

func TestTracesIntAsStringAction(t *testing.T) {
	g := makeTracesGenerator(1, "int")
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	}

	configs := confengine.NewTierConfig()
	configs.SetGlobal(Config{
		AsString: AsStringAction{
			Keys: []string{resourceKeyPerIp},
		},
	})

	filter := &attributeFilter{configs: configs}
	_, err := filter.Process(&record)
	assert.NoError(t, err)

	attr := record.Data.(ptrace.Traces).ResourceSpans().At(0).Resource().Attributes()
	v, ok := attr.Get(resourceKeyPerIp)
	assert.True(t, ok)
	assert.Equal(t, pcommon.ValueTypeString, v.Type())
}

func TestTracesFloatAsStringAction(t *testing.T) {
	g := makeTracesGenerator(1, "float")
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	}

	configs := confengine.NewTierConfig()
	configs.SetGlobal(Config{
		AsString: AsStringAction{
			Keys: []string{resourceKeyPerIp, resourceKeyPerPort},
		},
	})

	filter := &attributeFilter{configs: configs}
	_, err := filter.Process(&record)
	assert.NoError(t, err)

	attr := record.Data.(ptrace.Traces).ResourceSpans().At(0).Resource().Attributes()
	v, ok := attr.Get(resourceKeyPerPort)
	assert.True(t, ok)
	assert.Equal(t, pcommon.ValueTypeString, v.Type())

	v, ok = attr.Get(resourceKeyPerIp)
	assert.True(t, ok)
	assert.Equal(t, pcommon.ValueTypeString, v.Type())
}

func TestTracesFromTokenAction(t *testing.T) {
	g := makeTracesGenerator(1, "float")
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
		Token: define.Token{
			BizId:   10086,
			AppName: "my_app_name",
		},
	}

	configs := confengine.NewTierConfig()
	configs.SetGlobal(Config{
		FromToken: FromTokenAction{
			BizId:   "bk_biz_id",
			AppName: "bk_app_name",
		},
	})

	filter := &attributeFilter{configs: configs}
	filter.fromTokenAction(&record, configs.GetByToken("").(Config))

	traces := record.Data.(ptrace.Traces)
	attrs := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).Attributes()
	val, ok := attrs.Get("bk_biz_id")
	assert.True(t, ok)
	assert.Equal(t, val.AsString(), "10086")
}

func TestMetricsFromTokenAction(t *testing.T) {
	g := makeMetricsGenerator(1, "float")
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
		Token: define.Token{
			BizId:   10086,
			AppName: "my_app_name",
		},
	}

	configs := confengine.NewTierConfig()
	configs.SetGlobal(Config{
		FromToken: FromTokenAction{
			BizId:   "bk_biz_id",
			AppName: "bk_app_name",
		},
	})

	filter := &attributeFilter{configs: configs}
	filter.fromTokenAction(&record, configs.GetByToken("").(Config))

	metrics := record.Data.(pmetric.Metrics)
	point := metrics.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0)
	val, ok := point.Attributes().Get("bk_biz_id")
	assert.True(t, ok)
	assert.Equal(t, val.AsString(), "10086")
}

func TestTraceAssembleAction(t *testing.T) {
	content := `
processor:
  - name: "attribute_filter/common"
    config:
      assemble:
        - destination: "api_name"
          predicate_key: "attributes.http.scheme"
          rules:
            - kind: "SPAN_KIND_CLIENT"
              keys:
                - "attributes.http.method"
                - "attributes.http.host"
                - "attributes.http.target"
              separator: ":"
            - kind: "SPAN_KIND_SERVER"
              first_upper:
                - "attributes.http.method"
              keys:
                - "attributes.http.method"
                - "attributes.http.route"
              separator: ":"
`
	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*attributeFilter)
	assert.NoError(t, err)

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
	_, err = factory.Process(&record)
	assert.NoError(t, err)

	span := record.Data.(ptrace.Traces).ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	attrs := span.Attributes()
	v, ok := attrs.Get("api_name")
	assert.True(t, ok)
	assert.Equal(t, "GET:testRoute", v.AsString())
}

func TestTraceAssembleWithoutKind(t *testing.T) {
	content := `
processor:
  - name: "attribute_filter/common"
    config:
      assemble:
        - destination: "api_name"
          predicate_key: "attributes.rpc.system"
          rules:
            - kind: ""
              first_upper:
                - "attributes.rpc.method"
              keys:
                - "attributes.rpc.method"
                - "const.TestConstCondition"
              separator: ":"
`
	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*attributeFilter)
	assert.NoError(t, err)

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
	_, err = factory.Process(&record)
	assert.NoError(t, err)

	span := record.Data.(ptrace.Traces).ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	attrs := span.Attributes()
	v, ok := attrs.Get("api_name")
	assert.True(t, ok)
	assert.Equal(t, "RpcMethod:TestConstCondition", v.AsString())
}

func TestTraceAssembleWithUnknown(t *testing.T) {
	content := `
processor:
  - name: "attribute_filter/common"
    config:
      assemble:
        - destination: "api_name"
          predicate_key: "attributes.rpc.system"
          rules:
            - kind: ""
              first_upper:
                - "attributes.rpc.method"
              keys:
                - "attributes.rpc.method"
                - "const.TestConstCondition"
              separator: ":"
`
	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*attributeFilter)
	assert.NoError(t, err)

	m := map[string]string{
		"rpc.system": "PRC",
	}
	g := makeTracesAttributesGenerator(int(ptrace.SpanKindUnspecified), m)
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	}
	_, err = factory.Process(&record)
	assert.NoError(t, err)

	span := record.Data.(ptrace.Traces).ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	attrs := span.Attributes()
	v, ok := attrs.Get("api_name")
	assert.True(t, ok)
	assert.Equal(t, "Unknown:TestConstCondition", v.AsString())
}

func TestTraceAssembleWithoutPredicate(t *testing.T) {
	content := `
processor:
  - name: "attribute_filter/common"
    config:
      assemble:
        - destination: "api_name"
          predicate_key: "attributes.rpc.system"
          rules:
            - kind: ""
              first_upper:
                - "attributes.rpc.method"
              keys:
                - "attributes.rpc.method"
                - "const.TestConstCondition"
              separator: ":"
`
	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*attributeFilter)
	assert.NoError(t, err)

	m := map[string]string{
		"http.scheme": "HTTP",
	}
	g := makeTracesAttributesGenerator(int(ptrace.SpanKindUnspecified), m)
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	}
	_, err = factory.Process(&record)
	assert.NoError(t, err)

	span := record.Data.(ptrace.Traces).ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	attrs := span.Attributes()
	v, ok := attrs.Get("api_name")
	assert.True(t, ok)
	assert.Equal(t, "Unknown", v.AsString())
}

func TestTraceAssembleWithNullValue(t *testing.T) {
	content := `
processor:
  - name: "attribute_filter/common"
    config:
      assemble:
        - destination: "api_name"
          predicate_key: "attributes.rpc.system"
          rules:
            - kind: ""
              first_upper:
                - "attributes.rpc.method"
              keys:
                - "attributes.rpc.method"
                - "const.TestConstCondition"
                - "attributes.rpc.target"
              separator: ":"
`
	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*attributeFilter)
	assert.NoError(t, err)

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
	_, err = factory.Process(&record)
	assert.NoError(t, err)

	span := record.Data.(ptrace.Traces).ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	attrs := span.Attributes()
	v, ok := attrs.Get("api_name")
	assert.True(t, ok)
	assert.Equal(t, "RpcMethod:TestConstCondition:Unknown", v.AsString())
}

func TestTraceAsIntAction(t *testing.T) {
	content := `
processor:
  - name: "attribute_filter/common"
    config:
      as_int:
        keys:
          - "attributes.http.status_code"
`
	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*attributeFilter)
	assert.NoError(t, err)

	m := map[string]string{
		"http.status_code": "200",
	}
	g := makeTracesAttributesGenerator(int(ptrace.SpanKindUnspecified), m)
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	}
	_, err = factory.Process(&record)
	assert.NoError(t, err)
	resourceAttr := record.Data.(ptrace.Traces).ResourceSpans().At(0).Resource().Attributes()
	v, ok := resourceAttr.Get(conventions.AttributeHTTPStatusCode)
	assert.True(t, ok)
	assert.Equal(t, pcommon.ValueTypeInt, v.Type())
	assert.Equal(t, int64(200), v.IntVal())

	span := record.Data.(ptrace.Traces).ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	attrs := span.Attributes()
	v, ok = attrs.Get(conventions.AttributeHTTPStatusCode)
	assert.True(t, ok)
	assert.Equal(t, pcommon.ValueTypeInt, v.Type())
	assert.Equal(t, int64(200), v.IntVal())
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
	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*attributeFilter)
	assert.NoError(t, err)

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
	_, err = factory.Process(&record)
	assert.NoError(t, err)
	span := record.Data.(ptrace.Traces).ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	attrs := span.Attributes()

	_, ok := attrs.Get(conventions.AttributeDBStatement)
	assert.False(t, ok)

	_, ok = attrs.Get("db.parameters")
	assert.False(t, ok)
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
	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*attributeFilter)
	assert.NoError(t, err)

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
	_, err = factory.Process(&record)
	assert.NoError(t, err)
	span := record.Data.(ptrace.Traces).ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	attrs := span.Attributes()

	v, ok := attrs.Get(conventions.AttributeDBStatement)
	assert.True(t, ok)
	assert.Equal(t, "testDbStatement", v.AsString())

	v, ok = attrs.Get("db.parameters")
	assert.True(t, ok)
	assert.Equal(t, "testDbParameters", v.AsString())
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
	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*attributeFilter)
	assert.NoError(t, err)

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
	_, err = factory.Process(&record)
	assert.NoError(t, err)
	span := record.Data.(ptrace.Traces).ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	attrs := span.Attributes()

	_, ok := attrs.Get(conventions.AttributeDBStatement)
	assert.False(t, ok)

	_, ok = attrs.Get("db.parameters")
	assert.False(t, ok)
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
	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*attributeFilter)
	assert.NoError(t, err)

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
	_, err = factory.Process(&record)
	assert.NoError(t, err)
	span := record.Data.(ptrace.Traces).ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	attrs := span.Attributes()

	const maxLen = 10

	v, ok := attrs.Get(conventions.AttributeDBStatement)
	assert.True(t, ok)
	assert.Equal(t, maxLen, len(v.AsString()))
	assert.Equal(t, "testDbStatement"[:maxLen], v.AsString())

	v, ok = attrs.Get("db.parameters")
	assert.True(t, ok)
	assert.Equal(t, maxLen, len(v.AsString()))
	assert.Equal(t, "testDbParameters"[:maxLen], v.AsString())
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
	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*attributeFilter)
	assert.NoError(t, err)

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
	_, err = factory.Process(&record)
	assert.NoError(t, err)
	span := record.Data.(ptrace.Traces).ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	attrs := span.Attributes()

	v, ok := attrs.Get(conventions.AttributeDBStatement)
	assert.True(t, ok)
	assert.Equal(t, "testDbStatement", v.AsString())

	v, ok = attrs.Get("db.parameters")
	assert.True(t, ok)
	assert.Equal(t, "testDbParameters", v.AsString())
}

func TestTraceCutActionWithoutMatch(t *testing.T) {
	content := `
processor:
  - name: "attribute_filter/common"
    config:
      cut:
        - predicate_key: "attributes.db.system"
          max_length: 10
          keys:
            - "attributes.db.parameters"
            - "attributes.db.statement"
`
	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*attributeFilter)
	assert.NoError(t, err)

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
	_, err = factory.Process(&record)
	assert.NoError(t, err)
	span := record.Data.(ptrace.Traces).ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	attrs := span.Attributes()

	const maxLen = 10

	v, ok := attrs.Get(conventions.AttributeDBStatement)
	assert.True(t, ok)
	assert.Equal(t, maxLen, len(v.AsString()))
	assert.Equal(t, "testDbStatement"[:maxLen], v.AsString())

	v, ok = attrs.Get("db.parameters")
	assert.True(t, ok)
	assert.Equal(t, maxLen, len(v.AsString()))
	assert.Equal(t, "testDbParameters"[:maxLen], v.AsString())
}
