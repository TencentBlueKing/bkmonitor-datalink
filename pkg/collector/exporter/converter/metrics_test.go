// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package converter

import (
	"testing"

	"github.com/elastic/beats/libbeat/common"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
)

func TestConvertGaugeMetrics(t *testing.T) {
	opts := define.MetricsOptions{
		GaugeCount: 1,
		MetricName: "bkm.usage",
		GeneratorOptions: define.GeneratorOptions{
			Attributes: map[string]string{"a1": "v1"},
			Resources:  map[string]string{"r1": "v1"},
		},
	}

	excepted := common.MapStr{
		"metrics": map[string]float64{
			"bkm_usage": float64(1024),
		},
		"target": define.Identity(),
		"dimension": map[string]string{
			"scope_name": generator.ScopeName,
			"a1":         "v1",
			"r1":         "v1",
		},
		"timestamp": int64(0),
	}

	g := generator.NewMetricsGenerator(opts)

	t.Run("DoubleValue", func(t *testing.T) {
		events := make([]define.Event, 0)
		gather := func(evts ...define.Event) {
			events = append(events, evts...)
		}

		metrics := g.Generate()
		dp := testkits.FirstGaugeDataPoint(metrics)
		dp.SetTimestamp(0)
		dp.SetDoubleVal(1024)
		assert.Equal(t, pmetric.NumberDataPointValueTypeDouble, dp.ValueType())

		NewCommonConverter(nil).Convert(&define.Record{RecordType: define.RecordMetrics, Data: metrics}, gather)
		event := events[0]
		event.Data()

		assert.Equal(t, excepted, event.Data())
		assert.Equal(t, event.RecordType(), define.RecordMetrics)
	})

	t.Run("IntValue", func(t *testing.T) {
		events := make([]define.Event, 0)
		gather := func(evts ...define.Event) {
			events = append(events, evts...)
		}

		metrics := g.Generate()
		dp := testkits.FirstGaugeDataPoint(metrics)
		dp.SetTimestamp(0)
		dp.SetIntVal(1024)
		assert.Equal(t, pmetric.NumberDataPointValueTypeInt, dp.ValueType())

		NewCommonConverter(nil).Convert(&define.Record{RecordType: define.RecordMetrics, Data: metrics}, gather)
		event := events[0]
		event.Data()

		assert.Equal(t, excepted, event.Data())
		assert.Equal(t, event.RecordType(), define.RecordMetrics)
	})
}

func TestConvertHistogramMetrics(t *testing.T) {
	opts := define.MetricsOptions{
		HistogramCount: 1,
		MetricName:     "bk_apm_duration",
		GeneratorOptions: define.GeneratorOptions{
			Attributes: map[string]string{"a1": "v1"},
			Resources:  map[string]string{"r1": "v1"},
		},
	}

	g := generator.NewMetricsGenerator(opts)
	metrics := g.Generate()

	events := make([]define.Event, 0)
	gather := func(evts ...define.Event) {
		events = append(events, evts...)
	}

	dp := testkits.FirstHistogramPoint(metrics)
	dp.SetTimestamp(0)
	dp.SetMExplicitBounds([]float64{1, 2, 3})
	dp.SetMBucketCounts([]uint64{4, 3, 2, 1})
	dp.SetSum(100)
	dp.SetCount(10)
	dp.SetMin(1)
	dp.SetMax(66)

	MetricsConverter.Convert(&define.Record{RecordType: define.RecordMetrics, Data: metrics}, gather)
	excepted := []common.MapStr{
		{
			"metrics": map[string]float64{
				"bk_apm_duration_sum": float64(100),
			},
			"target": define.Identity(),
			"dimension": map[string]string{
				"scope_name": generator.ScopeName,
				"a1":         "v1",
				"r1":         "v1",
			},
			"timestamp": int64(0),
		},
		{
			"metrics": map[string]float64{
				"bk_apm_duration_min": float64(1),
			},
			"target": define.Identity(),
			"dimension": map[string]string{
				"scope_name": generator.ScopeName,
				"a1":         "v1",
				"r1":         "v1",
			},
			"timestamp": int64(0),
		},
		{
			"metrics": map[string]float64{
				"bk_apm_duration_max": float64(66),
			},
			"target": define.Identity(),
			"dimension": map[string]string{
				"scope_name": generator.ScopeName,
				"a1":         "v1",
				"r1":         "v1",
			},
			"timestamp": int64(0),
		},
		{
			"metrics": map[string]float64{
				"bk_apm_duration_count": float64(10),
			},
			"target": define.Identity(),
			"dimension": map[string]string{
				"scope_name": generator.ScopeName,
				"a1":         "v1",
				"r1":         "v1",
			},
			"timestamp": int64(0),
		},
		{
			"metrics": map[string]float64{
				"bk_apm_duration_bucket": float64(4),
			},
			"target": define.Identity(),
			"dimension": map[string]string{
				"scope_name": generator.ScopeName,
				"a1":         "v1",
				"r1":         "v1",
				"le":         "1",
			},
			"timestamp": int64(0),
		},
		{
			"metrics": map[string]float64{
				"bk_apm_duration_bucket": float64(7),
			},
			"target": define.Identity(),
			"dimension": map[string]string{
				"scope_name": generator.ScopeName,
				"a1":         "v1",
				"r1":         "v1",
				"le":         "2",
			},
			"timestamp": int64(0),
		},
		{
			"metrics": map[string]float64{
				"bk_apm_duration_bucket": float64(9),
			},
			"target": define.Identity(),
			"dimension": map[string]string{
				"scope_name": generator.ScopeName,
				"a1":         "v1",
				"r1":         "v1",
				"le":         "3",
			},
			"timestamp": int64(0),
		},
		{
			"metrics": map[string]float64{
				"bk_apm_duration_bucket": float64(10),
			},
			"target": define.Identity(),
			"dimension": map[string]string{
				"scope_name": generator.ScopeName,
				"a1":         "v1",
				"r1":         "v1",
				"le":         "+Inf",
			},
			"timestamp": int64(0),
		},
	}

	for index, m := range excepted {
		assert.Equal(t, m, events[index].Data())
	}
}

func TestConvertSummaryMetrics(t *testing.T) {
	opts := define.MetricsOptions{
		SummaryCount: 1,
		MetricName:   "bk_apm_duration",
		GeneratorOptions: define.GeneratorOptions{
			Attributes: map[string]string{"a1": "v1"},
			Resources:  map[string]string{"r1": "v1"},
		},
	}

	g := generator.NewMetricsGenerator(opts)
	metrics := g.Generate()
	events := make([]define.Event, 0)
	gather := func(evts ...define.Event) {
		events = append(events, evts...)
	}

	dp := testkits.FirstSummaryPoint(metrics)
	dp.SetTimestamp(0)
	dp.SetSum(10)
	dp.SetCount(1)

	MetricsConverter.Convert(&define.Record{RecordType: define.RecordMetrics, Data: metrics}, gather)

	event := events[0]
	event.Data()

	assert.Equal(t, common.MapStr{
		"metrics": map[string]float64{
			"bk_apm_duration_sum": float64(10),
		},
		"target": define.Identity(),
		"dimension": map[string]string{
			"scope_name": generator.ScopeName,
			"a1":         "v1",
			"r1":         "v1",
		},
		"timestamp": int64(0),
	}, event.Data())
	assert.Equal(t, event.RecordType(), define.RecordMetrics)
}

func TestConvertSumMetrics(t *testing.T) {
	opts := define.MetricsOptions{
		CounterCount: 1,
		MetricName:   "bk_apm_duration",
		GeneratorOptions: define.GeneratorOptions{
			Attributes: map[string]string{"a1": "v1"},
			Resources:  map[string]string{"r1": "v1"},
		},
	}

	excepted := common.MapStr{
		"metrics": map[string]float64{
			"bk_apm_duration": float64(1024),
		},
		"target": define.Identity(),
		"dimension": map[string]string{
			"scope_name": generator.ScopeName,
			"a1":         "v1",
			"r1":         "v1",
		},
		"timestamp": int64(0),
	}

	g := generator.NewMetricsGenerator(opts)

	t.Run("DoubleValue", func(t *testing.T) {
		metrics := g.Generate()
		events := make([]define.Event, 0)
		gather := func(evts ...define.Event) {
			events = append(events, evts...)
		}

		dp := testkits.FirstSumPoint(metrics)
		dp.SetTimestamp(0)
		dp.SetDoubleVal(1024)
		assert.Equal(t, pmetric.NumberDataPointValueTypeDouble, dp.ValueType())

		MetricsConverter.Convert(&define.Record{RecordType: define.RecordMetrics, Data: metrics}, gather)
		event := events[0]
		event.Data()

		assert.Equal(t, excepted, event.Data())
		assert.Equal(t, event.RecordType(), define.RecordMetrics)
	})

	t.Run("IntValue", func(t *testing.T) {
		metrics := g.Generate()
		events := make([]define.Event, 0)
		gather := func(evts ...define.Event) {
			events = append(events, evts...)
		}

		dp := testkits.FirstSumPoint(metrics)
		dp.SetTimestamp(0)
		dp.SetIntVal(1024)
		assert.Equal(t, pmetric.NumberDataPointValueTypeInt, dp.ValueType())

		MetricsConverter.Convert(&define.Record{RecordType: define.RecordMetrics, Data: metrics}, gather)
		event := events[0]
		event.Data()

		assert.Equal(t, excepted, event.Data())
		assert.Equal(t, event.RecordType(), define.RecordMetrics)
	})
}

type generatorConfig struct {
	gauge     int
	counter   int
	histogram int
	summary   int
}

func makeMetricsGenerator(conf generatorConfig) *generator.MetricsGenerator {
	opts := define.MetricsOptions{
		GaugeCount:     conf.gauge,
		CounterCount:   conf.counter,
		HistogramCount: conf.histogram,
		SummaryCount:   conf.summary,
	}
	opts.RandomAttributeKeys = attributeKeys
	opts.RandomResourceKeys = resourceKeys
	return generator.NewMetricsGenerator(opts)
}

func BenchmarkMetricsConvert_10_Gauge_DataPoint(b *testing.B) {
	g := makeMetricsGenerator(generatorConfig{gauge: 10})
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		MetricsConverter.Convert(&record, gather)
	}
}

func BenchmarkMetricsConvert_10_Counter_DataPoint(b *testing.B) {
	g := makeMetricsGenerator(generatorConfig{counter: 10})
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		MetricsConverter.Convert(&record, gather)
	}
}

func BenchmarkMetricsConvert_10_Histogram_DataPoint(b *testing.B) {
	g := makeMetricsGenerator(generatorConfig{histogram: 10})
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		MetricsConverter.Convert(&record, gather)
	}
}

func BenchmarkMetricsConvert_10_Summary_DataPoint(b *testing.B) {
	g := makeMetricsGenerator(generatorConfig{summary: 10})
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		MetricsConverter.Convert(&record, gather)
	}
}

func BenchmarkMetricsConvert_100_Gauge_DataPoint(b *testing.B) {
	g := makeMetricsGenerator(generatorConfig{gauge: 100})
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		MetricsConverter.Convert(&record, gather)
	}
}

func BenchmarkMetricsConvert_100_Counter_DataPoint(b *testing.B) {
	g := makeMetricsGenerator(generatorConfig{counter: 100})
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		MetricsConverter.Convert(&record, gather)
	}
}

func BenchmarkMetricsConvert_100_Histogram_DataPoint(b *testing.B) {
	g := makeMetricsGenerator(generatorConfig{histogram: 100})
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		MetricsConverter.Convert(&record, gather)
	}
}

func BenchmarkMetricsConvert_100_Summary_DataPoint(b *testing.B) {
	g := makeMetricsGenerator(generatorConfig{summary: 100})
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		MetricsConverter.Convert(&record, gather)
	}
}

func BenchmarkMetricsConvert_1000_Gauge_DataPoint(b *testing.B) {
	g := makeMetricsGenerator(generatorConfig{gauge: 1000})
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		MetricsConverter.Convert(&record, gather)
	}
}

func BenchmarkMetricsConvert_1000_Counter_DataPoint(b *testing.B) {
	g := makeMetricsGenerator(generatorConfig{counter: 1000})
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		MetricsConverter.Convert(&record, gather)
	}
}

func BenchmarkMetricsConvert_1000_Histogram_DataPoint(b *testing.B) {
	g := makeMetricsGenerator(generatorConfig{histogram: 1000})
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		MetricsConverter.Convert(&record, gather)
	}
}

func BenchmarkMetricsConvert_1000_Summary_DataPoint(b *testing.B) {
	g := makeMetricsGenerator(generatorConfig{summary: 1000})
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordMetrics,
		Data:       data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		MetricsConverter.Convert(&record, gather)
	}
}
