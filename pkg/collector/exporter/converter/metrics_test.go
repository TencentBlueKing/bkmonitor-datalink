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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
)

func makeMetricsGenerator(gaugeCount, counterCount, histogramCount int) *generator.MetricsGenerator {
	opts := define.MetricsOptions{
		GaugeCount:     gaugeCount,
		CounterCount:   counterCount,
		HistogramCount: histogramCount,
	}
	opts.RandomAttributeKeys = attributeKeys
	opts.RandomResourceKeys = resourceKeys
	return generator.NewMetricsGenerator(opts)
}

func TestConvertGaugeMetrics(t *testing.T) {
	opts := define.MetricsOptions{
		GaugeCount: 1,
		MetricName: "bk_apm_duration",
		GeneratorOptions: define.GeneratorOptions{
			Attributes: map[string]string{
				"attr1": "attr1-value",
				"attr2": "attr2-value",
			},
			Resources: map[string]string{
				"res1": "res1-value",
				"res2": "res2-value",
			},
		},
	}

	g := generator.NewMetricsGenerator(opts)
	m := g.Generate()

	events := make([]define.Event, 0)
	gather := func(evts ...define.Event) {
		events = append(events, evts...)
	}

	assert.Len(t, events, 0)
	m.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Gauge().DataPoints().At(0).SetTimestamp(0)
	NewCommonConverter().Convert(&define.Record{RecordType: define.RecordMetrics, Data: m}, gather)

	event := events[0]
	event.Data()

	assert.Equal(t, common.MapStr{
		"metrics": map[string]float64{
			"bk_apm_duration": float64(0),
		},
		"target": define.Identity(),
		"dimension": map[string]string{
			"attr1": "attr1-value",
			"attr2": "attr2-value",
			"res1":  "res1-value",
			"res2":  "res2-value",
		},
		"timestamp": int64(0),
	}, event.Data())
	assert.Equal(t, event.RecordType(), define.RecordMetrics)
}

func TestConvertHistogramMetrics(t *testing.T) {
	opts := define.MetricsOptions{
		HistogramCount: 1,
		MetricName:     "bk_apm_duration",
		GeneratorOptions: define.GeneratorOptions{
			Attributes: map[string]string{
				"attr1": "attr1-value",
				"attr2": "attr2-value",
			},
			Resources: map[string]string{
				"res1": "res1-value",
				"res2": "res2-value",
			},
		},
	}

	g := generator.NewMetricsGenerator(opts)
	m := g.Generate()

	events := make([]define.Event, 0)
	gather := func(evts ...define.Event) {
		events = append(events, evts...)
	}

	assert.Len(t, events, 0)

	point := m.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Histogram().DataPoints().At(0)
	point.SetTimestamp(0)
	point.SetMExplicitBounds([]float64{1, 2, 3, 4})
	point.SetMBucketCounts([]uint64{4, 3, 2, 1})
	point.SetSum(10)
	point.SetCount(1)

	MetricsConverter.Convert(&define.Record{RecordType: define.RecordMetrics, Data: m}, gather)

	event := events[0]
	event.Data()

	assert.Equal(t, common.MapStr{
		"metrics": map[string]float64{
			"bk_apm_duration_sum": float64(10),
		},
		"target": define.Identity(),
		"dimension": map[string]string{
			"attr1": "attr1-value",
			"attr2": "attr2-value",
			"res1":  "res1-value",
			"res2":  "res2-value",
		},
		"timestamp": int64(0),
	}, event.Data())
	assert.Equal(t, event.RecordType(), define.RecordMetrics)
}

func TestConvertSummaryMetrics(t *testing.T) {
	opts := define.MetricsOptions{
		SummaryCount: 1,
		MetricName:   "bk_apm_duration",
		GeneratorOptions: define.GeneratorOptions{
			Attributes: map[string]string{
				"attr1": "attr1-value",
				"attr2": "attr2-value",
			},
			Resources: map[string]string{
				"res1": "res1-value",
				"res2": "res2-value",
			},
		},
	}

	g := generator.NewMetricsGenerator(opts)
	m := g.Generate()

	events := make([]define.Event, 0)
	gather := func(evts ...define.Event) {
		events = append(events, evts...)
	}

	assert.Len(t, events, 0)
	point := m.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Summary().DataPoints().At(0)
	point.SetTimestamp(0)
	point.SetSum(10)
	point.SetCount(1)

	MetricsConverter.Convert(&define.Record{RecordType: define.RecordMetrics, Data: m}, gather)

	event := events[0]
	event.Data()

	assert.Equal(t, common.MapStr{
		"metrics": map[string]float64{
			"bk_apm_duration_sum": float64(10),
		},
		"target": define.Identity(),
		"dimension": map[string]string{
			"attr1": "attr1-value",
			"attr2": "attr2-value",
			"res1":  "res1-value",
			"res2":  "res2-value",
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
			Attributes: map[string]string{
				"attr1": "attr1-value",
				"attr2": "attr2-value",
			},
			Resources: map[string]string{
				"res1": "res1-value",
				"res2": "res2-value",
			},
		},
	}

	g := generator.NewMetricsGenerator(opts)
	m := g.Generate()

	events := make([]define.Event, 0)
	gather := func(evts ...define.Event) {
		events = append(events, evts...)
	}

	assert.Len(t, events, 0)
	m.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0).Sum().DataPoints().At(0).SetTimestamp(0)
	MetricsConverter.Convert(&define.Record{RecordType: define.RecordMetrics, Data: m}, gather)

	event := events[0]
	event.Data()

	assert.Equal(t, common.MapStr{
		"metrics": map[string]float64{
			"bk_apm_duration": float64(0),
		},
		"target": define.Identity(),
		"dimension": map[string]string{
			"attr1": "attr1-value",
			"attr2": "attr2-value",
			"res1":  "res1-value",
			"res2":  "res2-value",
		},
		"timestamp": int64(0),
	}, event.Data())
	assert.Equal(t, event.RecordType(), define.RecordMetrics)
}

func BenchmarkMetricsConvert_10_Gauge_DataPoint(b *testing.B) {
	g := makeMetricsGenerator(10, 0, 0)
	data := g.Generate()
	record := define.Record{
		RecordType:  define.RecordMetrics,
		RequestType: define.RequestHttp,
		Data:        data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		MetricsConverter.Convert(&record, gather)
	}
}

func BenchmarkMetricsConvert_10_Counter_DataPoint(b *testing.B) {
	g := makeMetricsGenerator(0, 10, 0)
	data := g.Generate()
	record := define.Record{
		RecordType:  define.RecordMetrics,
		RequestType: define.RequestHttp,
		Data:        data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		MetricsConverter.Convert(&record, gather)
	}
}

func BenchmarkMetricsConvert_10_Histogram_DataPoint(b *testing.B) {
	g := makeMetricsGenerator(0, 0, 10)
	data := g.Generate()
	record := define.Record{
		RecordType:  define.RecordMetrics,
		RequestType: define.RequestHttp,
		Data:        data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		MetricsConverter.Convert(&record, gather)
	}
}

func BenchmarkMetricsConvert_100_Gauge_DataPoint(b *testing.B) {
	g := makeMetricsGenerator(100, 0, 0)
	data := g.Generate()
	record := define.Record{
		RecordType:  define.RecordMetrics,
		RequestType: define.RequestHttp,
		Data:        data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		MetricsConverter.Convert(&record, gather)
	}
}

func BenchmarkMetricsConvert_100_Counter_DataPoint(b *testing.B) {
	g := makeMetricsGenerator(0, 100, 0)
	data := g.Generate()
	record := define.Record{
		RecordType:  define.RecordMetrics,
		RequestType: define.RequestHttp,
		Data:        data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		MetricsConverter.Convert(&record, gather)
	}
}

func BenchmarkMetricsConvert_100_Histogram_DataPoint(b *testing.B) {
	g := makeMetricsGenerator(0, 0, 100)
	data := g.Generate()
	record := define.Record{
		RecordType:  define.RecordMetrics,
		RequestType: define.RequestHttp,
		Data:        data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		MetricsConverter.Convert(&record, gather)
	}
}

func BenchmarkMetricsConvert_1000_Gauge_DataPoint(b *testing.B) {
	g := makeMetricsGenerator(1000, 0, 0)
	data := g.Generate()
	record := define.Record{
		RecordType:  define.RecordMetrics,
		RequestType: define.RequestHttp,
		Data:        data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		MetricsConverter.Convert(&record, gather)
	}
}

func BenchmarkMetricsConvert_1000_Counter_DataPoint(b *testing.B) {
	g := makeMetricsGenerator(0, 1000, 0)
	data := g.Generate()
	record := define.Record{
		RecordType:  define.RecordMetrics,
		RequestType: define.RequestHttp,
		Data:        data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		MetricsConverter.Convert(&record, gather)
	}
}

func BenchmarkMetricsConvert_1000_Histogram_DataPoint(b *testing.B) {
	g := makeMetricsGenerator(0, 0, 1000)
	data := g.Generate()
	record := define.Record{
		RecordType:  define.RecordMetrics,
		RequestType: define.RequestHttp,
		Data:        data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		MetricsConverter.Convert(&record, gather)
	}
}

func BenchmarkMetricsConvert_1000_DataPoint(b *testing.B) {
	g := makeMetricsGenerator(1000, 1000, 1000)
	data := g.Generate()
	record := define.Record{
		RecordType:  define.RecordMetrics,
		RequestType: define.RequestHttp,
		Data:        data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		MetricsConverter.Convert(&record, gather)
	}
}
