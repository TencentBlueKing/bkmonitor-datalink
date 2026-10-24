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
	"context"
	"fmt"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/elastic/beats/libbeat/common"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/prompb"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/metricmeta"
)

func BenchmarkMetadataOTLP(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		b.Run(fmt.Sprintf("enabled=%t", enabled), func(b *testing.B) {
			metrics := pmetric.NewMetrics()
			rm := metrics.ResourceMetrics().AppendEmpty()
			rm.Resource().Attributes().PutString("service_name", "metricmeta-scale-00")
			sm := rm.ScopeMetrics().AppendEmpty()
			sm.Scope().SetName("metadata-benchmark-00")
			for i := 0; i < 1000; i++ {
				m := sm.Metrics().AppendEmpty()
				m.SetName(fmt.Sprintf("metricmeta_acceptance_%d", i))
				m.SetDescription("acceptance version 0")
				m.SetUnit("1")
				m.SetEmptyGauge().DataPoints().AppendEmpty().SetIntValue(1)
			}
			record := &define.Record{Token: define.Token{MetricsDataId: 7}, Data: metrics}
			gather := func(...define.Event) {}
			var conv metricsConverter
			if enabled {
				cfg := metricmeta.DefaultConfig()
				cfg.DebounceInterval = 0
				exporter, err := metricmeta.NewExporter(cfg, func(context.Context, []byte) error { return nil })
				if err != nil {
					b.Fatal(err)
				}
				b.Cleanup(func() {
					if err := exporter.Stop(); err != nil {
						b.Error(err)
					}
				})
				conv.metadata = exporter.Store()
				conv.Convert(record, gather)
				if err := exporter.Flush(context.Background()); err != nil {
					b.Fatal(err)
				}
				if got := exporter.Store().Stats().BloomVersions; got != 1000 {
					b.Fatalf("Bloom versions = %d, want 1000", got)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				conv.Convert(record, gather)
			}
		})
	}
}

type metadataRecorder struct {
	mu      sync.Mutex
	entries map[metricmeta.Key]metricmeta.Value
}

func (r *metadataRecorder) Observe(k metricmeta.Key, v metricmeta.Value) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = make(map[metricmeta.Key]metricmeta.Value)
	}
	r.entries[k] = v
	return true
}

func TestMetadataOTLPOutputAndIsolation(t *testing.T) {
	r := &metadataRecorder{}
	metrics := pmetric.NewMetrics()
	rm := metrics.ResourceMetrics().AppendEmpty()
	rm.Resource().Attributes().PutString("service_name", "a-b%")
	sm := rm.ScopeMetrics().AppendEmpty()
	sm.Scope().SetName("s-t")
	m := sm.Metrics().AppendEmpty()
	m.SetName("latency.seconds")
	m.SetDescription("耗时")
	m.SetUnit("s")
	h := m.SetEmptyHistogram()
	h.SetAggregationTemporality(pmetric.MetricAggregationTemporalityDelta)
	dp := h.DataPoints().AppendEmpty()
	dp.SetCount(1)
	dp.SetSum(2)
	dp.SetMin(2)
	dp.SetMax(2)
	dp.ExplicitBounds().FromRaw([]float64{3})
	dp.BucketCounts().FromRaw([]uint64{1, 0})
	dp.Attributes().PutString("service_name", "wrong")
	conv := metricsConverter{metadata: r}
	var actual []common.MapStr
	conv.Convert(&define.Record{Token: define.Token{MetricsDataId: 7}, Data: metrics}, func(events ...define.Event) {
		for _, event := range events {
			actual = append(actual, event.Data())
		}
	})
	if len(r.entries) != 5 {
		t.Fatalf("split metrics: %+v", r.entries)
	}
	for key, value := range r.entries {
		if key.DataID != 7 || key.Scope != "a%2Db%25-s%2Dt" || value.Name != "latency.seconds" || value.OriginalType != "histogram" {
			t.Fatalf("identity: %+v %+v", key, value)
		}
		switch key.Metric {
		case "latency_seconds_count", "latency_seconds_bucket":
			if value.Unit != "1" || value.Type != "counter" {
				t.Fatal(value)
			}
		case "latency_seconds_min":
			if value.Usage != "min" || value.Type != "gauge" {
				t.Fatal(value)
			}
		case "latency_seconds_max":
			if value.Usage != "max" || value.Type != "gauge" {
				t.Fatal(value)
			}
		}
	}
	// 同一个输入的数值输出不依赖元信息功能开关。
	var disabled []common.MapStr
	metricsConverter{}.Convert(&define.Record{Token: define.Token{MetricsDataId: 7}, Data: metrics}, func(events ...define.Event) {
		for _, event := range events {
			disabled = append(disabled, event.Data())
		}
	})
	if !reflect.DeepEqual(actual, disabled) {
		t.Fatal("metadata changed numeric output")
	}
	if metadataScope(map[string]string{"service_name": "a-b", "scope_name": "c"}) == metadataScope(map[string]string{"service_name": "a", "scope_name": "b-c"}) {
		t.Fatal("ambiguous scope")
	}
}

func TestMetadataOTLPChangingPointIdentity(t *testing.T) {
	r := &metadataRecorder{}
	metrics := pmetric.NewMetrics()
	rm := metrics.ResourceMetrics().AppendEmpty()
	for _, scopeName := range []string{"group-a", "group-b"} {
		sm := rm.ScopeMetrics().AppendEmpty()
		sm.Scope().SetName(scopeName)
		m := sm.Metrics().AppendEmpty()
		m.SetName("value")
		gauge := m.SetEmptyGauge()
		for _, service := range []string{"", "service-a", "service-a", "service-b", ""} {
			dp := gauge.DataPoints().AppendEmpty()
			dp.SetIntValue(1)
			dp.Attributes().PutString("service_name", service)
		}
	}
	record := &define.Record{Token: define.Token{MetricsDataId: 7}, Data: metrics}
	var enabled, disabled []common.MapStr
	metricsConverter{metadata: r}.Convert(record, func(events ...define.Event) {
		for _, event := range events {
			enabled = append(enabled, event.Data())
		}
	})
	metricsConverter{}.Convert(record, func(events ...define.Event) {
		for _, event := range events {
			disabled = append(disabled, event.Data())
		}
	})
	if len(enabled) != 10 || !reflect.DeepEqual(enabled, disabled) {
		t.Fatal("metadata changed numeric output")
	}
	if len(r.entries) != 6 {
		t.Fatalf("metadata identities = %d, want 6", len(r.entries))
	}
	for _, scopeName := range []string{"group-a", "group-b"} {
		for _, service := range []string{"", "service-a", "service-b"} {
			scope := metadataScope(map[string]string{"service_name": service, "scope_name": scopeName})
			key := metricmeta.NewKey(7, scope, "value")
			if _, ok := r.entries[key]; !ok {
				t.Errorf("missing final identity: %+v", key)
			}
		}
	}
}

func TestMetadataOTLPSumSummaryAndUnknown(t *testing.T) {
	m := pmetric.NewMetric()
	m.SetName("m")
	sum := m.SetEmptySum()
	sum.SetAggregationTemporality(pmetric.MetricAggregationTemporalityCumulative)
	for _, monotonic := range []bool{true, false} {
		sum.SetIsMonotonic(monotonic)
		v, ok := otlpMetadata(m, "m", "m")
		if !ok || (v.Type == "counter") != monotonic || v.OriginalType != "sum" || v.Temporality != "cumulative" {
			t.Fatal(v, ok)
		}
	}
	sum.SetAggregationTemporality(pmetric.MetricAggregationTemporalityUnspecified)
	if _, ok := otlpMetadata(m, "m", "m"); ok {
		t.Fatal("unknown temporality")
	}
	m.SetEmptySummary()
	for _, output := range []string{"m", "m_count", "m_sum"} {
		v, ok := otlpMetadata(m, "m", output)
		if !ok || v.OriginalType != "summary" || (v.Type == "gauge") != (output == "m") {
			t.Fatal(v, ok)
		}
	}
}

func TestMetadataRemoteWriteAlignmentAndFinalIdentity(t *testing.T) {
	r := &metadataRecorder{}
	v := metadataValue("raw", "gauge", "gauge", "delta", "last", "", "")
	record := &define.Record{Token: define.Token{MetricsDataId: 8}, Data: &define.RemoteWriteData{
		Timeseries:        []prompb.TimeSeries{{Labels: []prompb.Label{{Name: "__name__", Value: "rewritten"}, {Name: "service_name", Value: "final"}, {Name: "scope_name", Value: "group"}}, Samples: []prompb.Sample{{Value: math.NaN()}, {Value: 1}}}},
		MetricDescriptors: []*metricmeta.Value{&v},
	}}
	var numeric int
	conv := remoteWriteConverter{metadata: r}
	conv.Convert(record, func(events ...define.Event) { numeric += len(events) })
	key := metricmeta.NewKey(8, metricmeta.Scope{Type: metricmeta.ScopeAPM, Name: "final-group"}, "rewritten")
	if r.entries[key] != v || numeric != 1 {
		t.Fatal(r.entries, numeric)
	}
	r.entries = nil
	record.Data.(*define.RemoteWriteData).MetricDescriptors = []*metricmeta.Value{&v, &v}
	conv.Convert(record, func(events ...define.Event) { numeric += len(events) })
	if len(r.entries) != 0 || numeric != 2 {
		t.Fatal("descriptor mismatch affected numeric output")
	}
	record.Data.(*define.RemoteWriteData).MetricDescriptors = nil
	conv.Convert(record, func(...define.Event) {})
	if len(r.entries) != 0 {
		t.Fatal("native remote write inferred unreliable metadata")
	}
}

func TestMetadataPrometheusTrpcAndSummary(t *testing.T) {
	name, help, typ := "trpc_histogram", "help", dto.MetricType_HISTOGRAM
	sum, count := 2.0, uint64(1)
	metric := &dto.Metric{Histogram: &dto.Histogram{SampleSum: &sum, SampleCount: &count}}
	mf := &dto.MetricFamily{Name: &name, Help: &help, Type: &typ, Metric: []*dto.Metric{metric}}
	r := &metadataRecorder{}
	conv := pushGatewayConverter{metadata: r}
	pd := &define.PushGatewayData{MetricFamilies: mf, Labels: map[string]string{"_name": "request.latency", "_type": "histogram", "service_name": "svc"}}
	conv.publishEventsFromMetricFamily(define.Token{}, pd, 4, 0, func(...define.Event) {})
	if len(r.entries) != 3 {
		t.Fatal(r.entries)
	}
	for key, value := range r.entries {
		if value.Name != "request.latency" || value.Description != "help" || value.OriginalType != "histogram" {
			t.Fatal(value)
		}
		if key.Metric == "request_latency_count" && value.Unit != "1" {
			t.Fatal(value)
		}
	}
	r.entries = nil
	pd.Labels["_type"] = "counter"
	conv.publishEventsFromMetricFamily(define.Token{}, pd, 4, 0, func(...define.Event) {})
	if len(r.entries) != 0 {
		t.Fatal("invalid trpc type accepted")
	}
	// 名称以 _count 结尾的 quantile 仍是原始单位，不能按后缀误判。
	name, typ = "latency_count", dto.MetricType_SUMMARY
	metric.Histogram = nil
	q := 0.5
	metric.Summary = &dto.Summary{SampleSum: &sum, SampleCount: &count, Quantile: []*dto.Quantile{{Quantile: &q, Value: &sum}}}
	pd.Labels = nil
	conv.publishEventsFromMetricFamily(define.Token{}, pd, 4, 0, func(...define.Event) {})
	for key, value := range r.entries {
		if key.Metric == name && (value.Unit != "" || value.Type != "gauge" || value.Usage != "last") {
			t.Fatal(value)
		}
	}
}

func TestMetadataTarsOriginalAggregateAndProperty(t *testing.T) {
	r := &metadataRecorder{}
	c := newTarsConverterWithMetadata(&TarsConfig{IsDropOriginal: true, IsDeriveServerView: true, AggregateInterval: time.Millisecond}, r)
	defer c.Clean()
	done := make(chan struct{}, 1)
	c.Convert(getTarsStatRecord(), func(...define.Event) {
		select {
		case done <- struct{}{}:
		default:
		}
	})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("aggregate not sent")
	}
	c.Clean()
	for key, value := range r.entries {
		if key.Metric[:4] != "rpc_" || value.Interval != 1 || value.Present&metricmeta.PresentInterval == 0 || value.Temporality != "delta" {
			t.Fatal(key, value)
		}
	}
	if len(r.entries) != 8 {
		t.Fatalf("derived service identity lost: %d", len(r.entries))
	}
	for policy, expected := range map[string]string{"Sum": "sum", "Count": "counter", "Distr": "histogram", "Min": "gauge", "Max": "gauge", "Avg": "gauge", "Other": "unknown"} {
		v := tarsPropertyMetadata("property", policy)
		if v.Name != "property" || v.OriginalType != expected || v.Interval != 0 {
			t.Fatal(v)
		}
	}
}
