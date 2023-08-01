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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstructure"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
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
	psc := testkits.MustLoadProcessorConfigs(content)
	obj, err := NewFactory(psc[0].Config, nil)
	factory := obj.(*resourceFilter)
	assert.NoError(t, err)
	assert.Equal(t, psc[0].Config, factory.MainConfig())

	var c Config
	err = mapstructure.Decode(psc[0].Config, &c)
	assert.NoError(t, err)

	actualConfig := factory.configs.Get("", "", "").(Config)
	c.Drop.Keys[0] = "service.name"
	assert.Equal(t, c.Drop, actualConfig.Drop)

	assert.Equal(t, define.ProcessorResourceFilter, factory.Name())
	assert.False(t, factory.IsDerived())
}

const (
	resourceKey1 = "resource_key1"
	resourceKey2 = "resource_key2"
	resourceKey3 = "resource_key3"
	resourceKey4 = "resource_key4"
)

func makeTracesGenerator(n int, valueType string) *generator.TracesGenerator {
	opts := define.TracesOptions{SpanCount: n}
	opts.RandomResourceKeys = []string{
		resourceKey1,
		resourceKey2,
		resourceKey3,
		resourceKey4,
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
	g := makeTracesGenerator(1, "string")
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	}

	configs := confengine.NewTierConfig()
	configs.SetGlobal(Config{
		Assemble: []AssembleAction{
			{
				Destination: "resource_final",
				Separator:   ":",
				Keys: []string{
					resourceKey1,
					resourceKey2,
					resourceKey3,
					resourceKey4,
				},
			},
		},
	})

	filter := &resourceFilter{configs: configs}
	_, err := filter.Process(&record)
	assert.NoError(t, err)
	attr := record.Data.(ptrace.Traces).ResourceSpans().At(0).Resource().Attributes()
	val, ok := attr.Get("resource_final")
	assert.True(t, ok)

	fields := strings.Split(val.AsString(), ":")
	assert.Len(t, fields, 4)

	for _, field := range fields {
		assert.True(t, len(field) >= 1)
	}
}

func TestTracesDropAction(t *testing.T) {
	g := makeTracesGenerator(1, "string")
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	}

	configs := confengine.NewTierConfig()
	configs.SetGlobal(Config{
		Drop: DropAction{
			Keys: []string{
				resourceKey1,
			},
		},
	})

	filter := &resourceFilter{configs: configs}
	_, err := filter.Process(&record)
	assert.NoError(t, err)
	attr := record.Data.(ptrace.Traces).ResourceSpans().At(0).Resource().Attributes()
	_, ok := attr.Get("resource_key1")
	assert.False(t, ok)
	_, ok = attr.Get("resource_key2")
	assert.True(t, ok)
	_, ok = attr.Get("resource_key3")
	assert.True(t, ok)
}

func TestMetricsDropAction(t *testing.T) {
	g := makeMetricsGenerator(1, "string")
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}

	configs := confengine.NewTierConfig()
	configs.SetGlobal(Config{
		Drop: DropAction{
			Keys: []string{
				resourceKey1,
			},
		},
	})

	filter := &resourceFilter{configs: configs}
	_, err := filter.Process(&record)
	assert.NoError(t, err)
	attr := record.Data.(pmetric.Metrics).ResourceMetrics().At(0).Resource().Attributes()
	_, ok := attr.Get("resource_key1")
	assert.False(t, ok)
	_, ok = attr.Get("resource_key2")
	assert.True(t, ok)
	_, ok = attr.Get("resource_key3")
	assert.True(t, ok)
}

func TestLogsDropAction(t *testing.T) {
	g := makeLogsGenerator(10, 10, "string")
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordLogs,
		Data:       data,
	}

	configs := confengine.NewTierConfig()
	configs.SetGlobal(Config{
		Drop: DropAction{
			Keys: []string{
				resourceKey1,
			},
		},
	})

	filter := &resourceFilter{configs: configs}
	_, err := filter.Process(&record)
	assert.NoError(t, err)
	attr := record.Data.(plog.Logs).ResourceLogs().At(0).Resource().Attributes()
	_, ok := attr.Get("resource_key1")
	assert.False(t, ok)
	_, ok = attr.Get("resource_key2")
	assert.True(t, ok)
	_, ok = attr.Get("resource_key3")
	assert.True(t, ok)
}

func TestTracesReplaceAction(t *testing.T) {
	g := makeTracesGenerator(1, "string")
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	}

	configs := confengine.NewTierConfig()
	configs.SetGlobal(Config{
		Replace: []ReplaceAction{
			{
				Source:      "resource_key1",
				Destination: "resource_key5",
			},
		},
	})

	filter := &resourceFilter{configs: configs}
	_, err := filter.Process(&record)
	assert.NoError(t, err)
	attr := record.Data.(ptrace.Traces).ResourceSpans().At(0).Resource().Attributes()
	_, ok := attr.Get("resource_key1")
	assert.False(t, ok)
	_, ok = attr.Get("resource_key5")
	assert.True(t, ok)
}

func TestMetricsReplaceAction(t *testing.T) {
	g := makeMetricsGenerator(1, "float")
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}

	configs := confengine.NewTierConfig()
	configs.SetGlobal(Config{
		Replace: []ReplaceAction{
			{
				Source:      "resource_key1",
				Destination: "resource_key5",
			},
		},
	})

	filter := &resourceFilter{configs: configs}
	_, err := filter.Process(&record)
	assert.NoError(t, err)
	attr := record.Data.(pmetric.Metrics).ResourceMetrics().At(0).Resource().Attributes()
	_, ok := attr.Get("resource_key1")
	assert.False(t, ok)
	_, ok = attr.Get("resource_key5")
	assert.True(t, ok)
}

func TestLogsReplaceAction(t *testing.T) {
	g := makeLogsGenerator(10, 10, "float")
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordLogs,
		Data:       data,
	}

	configs := confengine.NewTierConfig()
	configs.SetGlobal(Config{
		Replace: []ReplaceAction{
			{
				Source:      "resource_key1",
				Destination: "resource_key5",
			},
		},
	})

	filter := &resourceFilter{configs: configs}
	_, err := filter.Process(&record)
	assert.NoError(t, err)
	attr := record.Data.(plog.Logs).ResourceLogs().At(0).Resource().Attributes()
	_, ok := attr.Get("resource_key1")
	assert.False(t, ok)
	_, ok = attr.Get("resource_key5")
	assert.True(t, ok)
}

func TestTracesAddAction(t *testing.T) {
	g := makeTracesGenerator(1, "bool")
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	}

	configs := confengine.NewTierConfig()
	configs.SetGlobal(Config{
		Add: []AddAction{
			{
				Label: "new_label_1",
				Value: "new_value_1",
			},
			{
				Label: "new_label_2",
				Value: "new_value_2",
			},
		},
	})

	filter := &resourceFilter{configs: configs}
	_, err := filter.Process(&record)
	assert.NoError(t, err)
	attr := record.Data.(ptrace.Traces).ResourceSpans().At(0).Resource().Attributes()
	val, ok := attr.Get("new_label_1")
	assert.True(t, ok)
	assert.Equal(t, val.AsString(), "new_value_1")

	val, ok = attr.Get("new_label_2")
	assert.True(t, ok)
	assert.Equal(t, val.AsString(), "new_value_2")
}

func TestMetricsAddAction(t *testing.T) {
	g := makeMetricsGenerator(1, "int")
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}

	configs := confengine.NewTierConfig()
	configs.SetGlobal(Config{
		Add: []AddAction{
			{
				Label: "new_label_1",
				Value: "new_value_1",
			},
			{
				Label: "new_label_2",
				Value: "new_value_2",
			},
		},
	})

	filter := &resourceFilter{configs: configs}
	_, err := filter.Process(&record)
	assert.NoError(t, err)

	attr := record.Data.(pmetric.Metrics).ResourceMetrics().At(0).Resource().Attributes()
	val, ok := attr.Get("new_label_1")
	assert.True(t, ok)
	assert.Equal(t, val.AsString(), "new_value_1")

	val, ok = attr.Get("new_label_2")
	assert.True(t, ok)
	assert.Equal(t, val.AsString(), "new_value_2")
}

func TestLogsAddAction(t *testing.T) {
	g := makeLogsGenerator(10, 10, "int")
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordLogs,
		Data:       data,
	}

	configs := confengine.NewTierConfig()
	configs.SetGlobal(Config{
		Add: []AddAction{
			{
				Label: "new_label_1",
				Value: "new_value_1",
			},
			{
				Label: "new_label_2",
				Value: "new_value_2",
			},
		},
	})

	filter := &resourceFilter{configs: configs}
	_, err := filter.Process(&record)
	assert.NoError(t, err)

	attr := record.Data.(plog.Logs).ResourceLogs().At(0).Resource().Attributes()
	val, ok := attr.Get("new_label_1")
	assert.True(t, ok)
	assert.Equal(t, val.AsString(), "new_value_1")

	val, ok = attr.Get("new_label_2")
	assert.True(t, ok)
	assert.Equal(t, val.AsString(), "new_value_2")
}
