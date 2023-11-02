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
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
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

func TestTracesAssembleAction(t *testing.T) {
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
}

func assertDropActionAttrs(t *testing.T, attrs pcommon.Map) {
	testkits.AssertAttrsNotFound(t, attrs, "resource_key1")
	testkits.AssertAttrsFound(t, attrs, "resource_key2")
	testkits.AssertAttrsFound(t, attrs, "resource_key3")
}

func TestTracesDropAction(t *testing.T) {
	content := `
processor:
    - name: "resource_filter/drop"
      config:
        drop:
          keys:
            - "resource.resource_key1"
`
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
}

func TestMetricsDropAction(t *testing.T) {
	content := `
processor:
    - name: "resource_filter/drop"
      config:
        drop:
          keys:
            - "resource.resource_key1"
`
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
}

func TestLogsDropAction(t *testing.T) {
	content := `
processor:
    - name: "resource_filter/drop"
      config:
        drop:
          keys:
            - "resource.resource_key1"
`
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
}

func assertReplaceActionAttrs(t *testing.T, attrs pcommon.Map) {
	testkits.AssertAttrsNotFound(t, attrs, resourceKey1)
	testkits.AssertAttrsFound(t, attrs, resourceKey4)
}

func TestTracesReplaceAction(t *testing.T) {
	content := `
processor:
    - name: "resource_filter/replace"
      config:
        replace:
          - source: resource_key1
            destination: resource_key4
`
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
}

func TestMetricsReplaceAction(t *testing.T) {
	content := `
processor:
    - name: "resource_filter/replace"
      config:
        replace:
          - source: resource_key1
            destination: resource_key4
`
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
}

func TestLogsReplaceAction(t *testing.T) {
	content := `
processor:
    - name: "resource_filter/replace"
      config:
        replace:
          - source: resource_key1
            destination: resource_key4
`
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
}

const (
	label1 = "label1"
	label2 = "label2"
	value1 = "value1"
	value2 = "value2"
)

func assertAddActionLabels(t *testing.T, attrs pcommon.Map) {
	testkits.AssertAttrsFoundStringVal(t, attrs, label1, value1)
	testkits.AssertAttrsFoundStringVal(t, attrs, label2, value2)
}

func TestTracesAddAction(t *testing.T) {
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
}

func TestMetricsAddAction(t *testing.T) {
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
}

func TestLogsAddAction(t *testing.T) {
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
}
