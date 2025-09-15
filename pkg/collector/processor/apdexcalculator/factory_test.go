// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package apdexcalculator

import (
	"testing"
	"time"

	"github.com/pkg/errors"
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

const dst = "bk_apm_duration_destination"

func TestFactory(t *testing.T) {
	content := `
processor:
  - name: "apdex_calculator/standard"
    config:
      calculator:
        type: "standard"
      rules:
        - kind: ""
          metric_name: "bk_apm_duration"
          destination: "apdex_type"
          apdex_t: 20 # ms
`
	mainConf := processor.MustLoadConfigs(content)[0].Config

	customContent := `
processor:
  - name: "apdex_calculator/standard"
    config:
      calculator:
        type: "standard"
      rules:
        - kind: ""
          metric_name: "bk_apm_duration"
          destination: "apdex_type"
          apdex_t: 50 # ms
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
	factory := obj.(*apdexCalculator)
	assert.NoError(t, err)
	assert.Equal(t, mainConf, factory.MainConfig())

	var c1 Config
	assert.NoError(t, mapstructure.Decode(mainConf, &c1))
	mainConfig := factory.configs.GetGlobal().(*Config)
	assert.Equal(t, c1.Rules, mainConfig.Rules)

	var c2 Config
	assert.NoError(t, mapstructure.Decode(customConf, &c2))
	customConfig := factory.configs.GetByToken("token1").(*Config)
	assert.Equal(t, c2.Rules, customConfig.Rules)

	assert.Equal(t, define.ProcessorApdexCalculator, factory.Name())
	assert.False(t, factory.IsDerived())
	assert.False(t, factory.IsPreCheck())

	factory.Reload(mainConf, nil)
	assert.Equal(t, mainConf, factory.MainConfig())
}

func testMetricsDimension(t *testing.T, data any, conf *Config, exist bool) {
	confMap := make(map[string]any)
	assert.NoError(t, mapstructure.Decode(conf, &confMap))

	factory, _ := NewFactory(confMap, nil)
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}
	testkits.MustProcess(t, factory, record)

	pdMetrics := record.Data.(pmetric.Metrics)
	assert.Equal(t, 1, pdMetrics.MetricCount())
	foreach.Metrics(pdMetrics.ResourceMetrics(), func(metric pmetric.Metric) {
		switch metric.DataType() {
		case pmetric.MetricDataTypeGauge:
			dps := metric.Gauge().DataPoints()
			for n := 0; n < dps.Len(); n++ {
				dp := dps.At(n)
				_, ok := dp.Attributes().Get(dst)
				assert.Equal(t, exist, ok)
			}
		}
	})
}

func TestProcessMetricsFixedCalculator(t *testing.T) {
	g := generator.NewMetricsGenerator(define.MetricsOptions{
		MetricName: "bk_apm_duration",
		GaugeCount: 1,
		GeneratorOptions: define.GeneratorOptions{
			Attributes: map[string]string{"kind": "2"},
		},
	})

	t.Run("default rule", func(t *testing.T) {
		config := &Config{
			Calculator: CalculatorConfig{
				Type:        "fixed",
				ApdexStatus: apdexSatisfied,
			},
			Rules: []RuleConfig{{
				Kind:        "",
				MetricName:  "bk_apm_duration",
				Destination: dst,
			}},
		}
		testMetricsDimension(t, g.Generate(), config, true)
	})

	t.Run("server/kind rule not exist", func(t *testing.T) {
		config := &Config{
			Calculator: CalculatorConfig{
				Type:        "fixed",
				ApdexStatus: apdexSatisfied,
			},
			Rules: []RuleConfig{{
				Kind:        "SPAN_KIND_CLIENT",
				MetricName:  "bk_apm_duration",
				Destination: dst,
			}},
		}
		testMetricsDimension(t, g.Generate(), config, false)
	})

	t.Run("server/kind rule exist", func(t *testing.T) {
		config := &Config{
			Calculator: CalculatorConfig{
				Type:        "fixed",
				ApdexStatus: apdexSatisfied,
			},
			Rules: []RuleConfig{{
				Kind:        "SPAN_KIND_SERVER",
				MetricName:  "bk_apm_duration",
				Destination: dst,
			}},
		}
		testMetricsDimension(t, g.Generate(), config, true)
	})
}

func TestProcessMetricsStandardCalculator(t *testing.T) {
	threshold := float64(2) // 2ms
	t.Run("apdexSatisfied: val <= threshold", func(t *testing.T) {
		ok, err := testProcessMetricsStandardCalculator(time.Millisecond, threshold, apdexSatisfied)
		assert.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("apdexTolerating: val <= 4*threshold", func(t *testing.T) {
		ok, err := testProcessMetricsStandardCalculator(time.Millisecond*5, threshold, apdexTolerating)
		assert.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("apdexFrustrated: val > 4*threshold", func(t *testing.T) {
		ok, err := testProcessMetricsStandardCalculator(time.Millisecond*100, threshold, apdexFrustrated)
		assert.NoError(t, err)
		assert.True(t, ok)
	})
}

func testProcessMetricsStandardCalculator(val time.Duration, threshold float64, status string) (bool, error) {
	fv := float64(val)
	g := generator.NewMetricsGenerator(define.MetricsOptions{
		MetricName: "bk_apm_duration",
		GaugeCount: 1,
		Value:      &fv,
	})

	data := g.Generate()
	config := &Config{
		Calculator: CalculatorConfig{
			Type: "standard",
		},
		Rules: []RuleConfig{{
			MetricName:  "bk_apm_duration",
			Destination: dst,
			ApdexT:      threshold,
		}},
	}

	confMap := make(map[string]any)
	if err := mapstructure.Decode(config, &confMap); err != nil {
		return false, err
	}

	factory, err := NewFactory(confMap, nil)
	if err != nil {
		return false, err
	}

	record := &define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}
	_, err = factory.Process(record)
	if err != nil {
		return false, err
	}

	var errs []error
	foreach.Metrics(record.Data.(pmetric.Metrics).ResourceMetrics(), func(metric pmetric.Metric) {
		switch metric.DataType() {
		case pmetric.MetricDataTypeGauge:
			dps := metric.Gauge().DataPoints()
			for n := 0; n < dps.Len(); n++ {
				dp := dps.At(n)
				v, ok := dp.Attributes().Get(dst)
				if !ok || status != v.AsString() {
					errs = append(errs, errors.Errorf("attribute does not exist, apdex_type=%v", v.AsString()))
				}
			}
		}
	})
	if len(errs) > 0 {
		return false, errs[0]
	}
	return true, nil
}

func TestProcessTracesStandardCalculator(t *testing.T) {
	threshold := float64(1000) // 1000ms
	t.Run("apdexSatisfied: val <= threshold", func(t *testing.T) {
		status, err := testProcessTracesStandardCalculator(time.Second, time.Second*2, threshold)
		assert.NoError(t, err)
		assert.Equal(t, apdexSatisfied, status)
	})

	t.Run("apdexTolerating: val <= 4*threshold", func(t *testing.T) {
		status, err := testProcessTracesStandardCalculator(time.Second, time.Second*3, threshold)
		assert.NoError(t, err)
		assert.Equal(t, apdexTolerating, status)
	})

	t.Run("apdexFrustrated: val > 4*threshold", func(t *testing.T) {
		status, err := testProcessTracesStandardCalculator(time.Second, time.Second*10, threshold)
		assert.NoError(t, err)
		assert.Equal(t, apdexFrustrated, status)
	})
}

func testProcessTracesStandardCalculator(startTime, endTime time.Duration, threshold float64) (string, error) {
	g := generator.NewTracesGenerator(define.TracesOptions{
		SpanCount: 1,
		GeneratorOptions: define.GeneratorOptions{
			RandomAttributeKeys: []string{
				processor.KeyService,
				processor.KeyInstance,
			},
		},
	})
	data := g.Generate()
	span := testkits.FirstSpan(data)
	span.SetStartTimestamp(pcommon.Timestamp(startTime))
	span.SetEndTimestamp(pcommon.Timestamp(endTime))

	config := &Config{
		Calculator: CalculatorConfig{
			Type: "standard",
		},
		Rules: []RuleConfig{{
			Destination: "apdex_type",
			ApdexT:      threshold,
		}},
	}

	confMap := make(map[string]any)
	if err := mapstructure.Decode(config, &confMap); err != nil {
		return "", err
	}

	factory, err := NewFactory(confMap, nil)
	if err != nil {
		return "", err
	}

	record := &define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	}
	_, err = factory.Process(record)
	if err != nil {
		return "", err
	}

	span = testkits.FirstSpan(data)
	v, ok := span.Attributes().Get("apdex_type")
	if !ok {
		return "", errors.New("no 'apdex_type' attribute found")
	}

	return v.AsString(), nil
}

func TestFindMetricsAttributes(t *testing.T) {
	t.Run("Exist", func(t *testing.T) {
		m := pcommon.NewMap()
		m.InsertString("net.host", "host")
		m.InsertString("net.port", "port")

		found := findMetricsAttributes("attributes.net.port", m)
		assert.True(t, found)
	})

	t.Run("Exist but empty value", func(t *testing.T) {
		m := pcommon.NewMap()
		m.InsertString("net.host", "host")
		m.InsertString("net.port", "")

		found := findMetricsAttributes("attributes.net.port", m)
		assert.False(t, found)
	})

	t.Run("NotExist", func(t *testing.T) {
		m := pcommon.NewMap()
		found := findMetricsAttributes("attributes.net.port", m)
		assert.False(t, found)
	})
}
