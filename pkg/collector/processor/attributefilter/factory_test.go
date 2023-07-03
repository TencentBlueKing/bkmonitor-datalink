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
	factory, err := newFactory(psc[0].Config, nil)
	assert.NoError(t, err)
	assert.Equal(t, psc[0].Config, factory.MainConfig())

	var c Config
	err = mapstructure.Decode(psc[0].Config, &c)
	assert.NoError(t, err)

	c.AsString.Keys[0] = "http.host"
	assert.Equal(t, c, factory.configs.Get("", "", "").(Config))

	assert.Equal(t, define.ProcessorAttributeFilter, factory.Name())
	assert.False(t, factory.IsDerived())
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
	filter.fromTokenAction(&record)
	val, ok := record.Data.(ptrace.Traces).ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).Attributes().Get("bk_biz_id")
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
	filter.fromTokenAction(&record)
	val, ok := record.Data.(pmetric.Metrics).ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0).Attributes().Get("bk_biz_id")
	assert.True(t, ok)
	assert.Equal(t, val.AsString(), "10086")
}
