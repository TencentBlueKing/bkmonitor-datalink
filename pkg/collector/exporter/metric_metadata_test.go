// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package exporter

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elastic/beats/libbeat/common"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/tokenchecker"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/metricmeta"
)

func TestMetadataDestinationAndSourceIdentity(t *testing.T) {
	var sent []common.MapStr
	saved := SentFunc
	defer func() { SentFunc = saved }()
	SentFunc = func(event common.MapStr) bool { sent = append(sent, event); return true }
	e, err := New(confengine.MustLoadConfigContent(`exporter:
  converter: {tars: {disable_aggregate: true}}
  metric_metadata: {enabled: true, dataid: 901}
`))
	if err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	m := pmetric.NewMetrics()
	rm := m.ResourceMetrics().AppendEmpty()
	rm.Resource().Attributes().PutString("service_name", "checkout")
	sm := rm.ScopeMetrics().AppendEmpty()
	sm.Scope().SetName("payments")
	metric := sm.Metrics().AppendEmpty()
	metric.SetName("cpu.usage")
	metric.SetDescription("使用率")
	metric.SetUnit("1")
	metric.SetEmptyGauge().DataPoints().AppendEmpty().SetDoubleValue(42)
	for _, biz := range []int32{17, -21, 17} {
		e.converter.Convert(&define.Record{RecordType: define.RecordMetrics, Token: define.Token{BizId: biz, MetricsDataId: 3}, Data: m}, func(events ...define.Event) {
			if len(events) != 1 || events[0].DataId() != 3 {
				t.Fatal("numeric routing changed")
			}
		})
	}
	e.Stop()
	if len(sent) != 1 || sent[0]["dataid"] != int32(901) {
		t.Fatalf("bad destination: %v", sent)
	}
	rows := sent[0]["data"].([]common.MapStr)
	if len(rows) != 2 {
		t.Fatalf("want two distinct businesses, got %d", len(rows))
	}
	seen := map[string]bool{}
	for _, row := range rows {
		dims := row["dimension"].(map[string]string)
		seen[dims["bk_biz_id"]] = true
		if dims["dataid"] != "3" || dims["description"] != "使用率" || dims["scope"] != "checkout-payments" || row["metrics"].(map[string]float64)[metadataMetricName] != 1 {
			t.Fatal(row)
		}
	}
	if !seen["17"] || !seen["-21"] {
		t.Fatal(seen)
	}
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range families {
		if f.GetName() == metadataMetricName {
			t.Fatal("metadata payload leaked into /metrics")
		}
		if f.GetName() == "bk_collector_metric_metadata_export_entries_total" {
			found = f.Metric[0].Counter.GetValue() == 2
		}
	}
	if !found {
		t.Fatal("metadata export statistics missing")
	}
}

// 来源配置拥有额度，使用不同 token 的 APM 请求也共用这个来源 DataID 的窗口。
func TestMetricSourceQuotaAcrossTokens(t *testing.T) {
	saved := SentFunc
	defer func() { SentFunc = saved }()
	for _, tc := range []struct{ name, source string }{
		{"prometheus", "type: subconfig\nmetrics_dataid: 1001"},
		{"json", "type: report_v2\ndefault: {processor: [{name: token_checker/proxy, config: {type: proxy, proxy_dataid: 1001}}]}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(filename, content string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			apmTokens := []string{"apm-one-" + tc.name, "apm-two-" + tc.name}
			for _, token := range apmTokens {
				write(token+".conf", "type: subconfig\nbk_biz_id: 17\nmetrics_dataid: 1001\ntoken: "+token)
			}
			write("source.conf", tc.source+"\ntoken: source-token")
			cfg := confengine.MustLoadConfigContent(fmt.Sprintf("apm: {patterns: [%q]}\nexporter: {metric_metadata: {enabled: true, dataid: 901, rate_limit: {dataid_per_minute: 1}}, converter: {tars: {disable_aggregate: true}}}", filepath.Join(dir, "*.conf")))
			e, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			sent := 0
			SentFunc = func(event common.MapStr) bool {
				for _, row := range event["data"].([]common.MapStr) {
					if row["dimension"].(map[string]string)["dataid"] != "1001" {
						t.Error("wrong source DataID")
					}
					sent++
				}
				return true
			}
			defer e.Stop()
			decoder := tokenchecker.NewTokenDecoder(tokenchecker.Config{Type: "aes256WithMeta"})
			numeric := 0
			for i, apmToken := range apmTokens {
				token, err := decoder.Decode(apmToken)
				if err != nil || token.Original != apmToken || token.MetricsDataId != 1001 {
					t.Fatalf("APM token: %+v, %v", token, err)
				}
				m := pmetric.NewMetrics()
				metric := m.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
				metric.SetName(fmt.Sprintf("metric_%d", i))
				metric.SetEmptyGauge().DataPoints().AppendEmpty().SetIntValue(1)
				e.converter.Convert(&define.Record{RecordType: define.RecordMetrics, Token: token, Data: m}, func(events ...define.Event) {
					for _, event := range events {
						if event.DataId() != 1001 {
							t.Fatal(event.DataId())
						}
						numeric++
					}
				})
			}
			if s := e.store.Load().Stats(); numeric != 2 || s.Pending != 1 || s.Dropped["dataid_circuit"] != 1 {
				t.Fatalf("numeric=%d stats=%+v", numeric, s)
			}
			e.Stop()
			if sent != 1 {
				t.Fatalf("exported metadata entries = %d, want 1", sent)
			}
		})
	}
}

func TestMetadataReload(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	platform := func(id int32) {
		write("platform.yml", fmt.Sprintf("type: platform\nexporter: {metric_metadata: {enabled: true, dataid: %d, rate_limit: {dataid_per_minute: 2}}}", id))
	}
	cfg := confengine.MustLoadConfigContent(fmt.Sprintf("apm: {patterns: [%q]}\nexporter: {converter: {tars: {disable_aggregate: true}}}", filepath.Join(dir, "*.yml")))
	platform(901)
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	store := e.store.Load()
	if store == nil {
		t.Fatal("platform config not loaded")
	}
	observe := func(id int32, name string) bool {
		return store.Observe(metricmeta.NewKey(id, metricmeta.Scope{Type: metricmeta.ScopeDefault}, name), metricmeta.Value{Name: name, OriginalType: "gauge", Type: "gauge", Temporality: "delta"})
	}
	if !observe(3, "a") || !observe(3, "b") || observe(3, "c") || !observe(4, "a") {
		t.Fatal(store.Stats())
	}
	platform(-1)
	if err = e.Reload(cfg); err == nil || store != e.store.Load() {
		t.Fatal("invalid reload replaced working exporter")
	}
	platform(902)
	if err = e.Reload(cfg); err != nil {
		t.Fatal(err)
	}
	if e.store.Load() == store || e.store.Load().Stats().Pending != 0 || observe(3, "old") {
		t.Fatal("target change retained old store")
	}
	if err = os.Remove(filepath.Join(dir, "platform.yml")); err != nil {
		t.Fatal(err)
	}
	if err = e.Reload(cfg); err != nil || e.store.Load() != nil {
		t.Fatal("platform removal should disable export", err)
	}
}

// 验证旧的 4 KiB 单条和 256 KiB 整批边界均不会截断元信息。
func TestMetadataLongDescriptionsInOneBatch(t *testing.T) {
	description := strings.Repeat("指标描述", 1024)
	const count = 32
	calls := 0
	saved := SentFunc
	defer func() { SentFunc = saved }()
	SentFunc = func(event common.MapStr) bool {
		calls++
		rows := event["data"].([]common.MapStr)
		if len(rows) != count {
			t.Fatalf("got %d metadata points, want %d", len(rows), count)
		}
		for _, row := range rows {
			if row["dimension"].(map[string]string)["description"] != description {
				t.Fatal("description changed during export")
			}
		}
		return true
	}
	c := metricmeta.DefaultConfig()
	e, err := metricmeta.NewBatchExporter(c, func(ctx context.Context, entries []metricmeta.Entry) error {
		return sendMetadata(ctx, 901, entries)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Discard()
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("metric_%d", i)
		key := metricmeta.NewKey(3, metricmeta.Scope{Type: metricmeta.ScopeDefault}, name)
		value := metricmeta.Value{Name: name, Type: "gauge", OriginalType: "gauge", Temporality: "delta",
			Description: description, Present: metricmeta.PresentDescription}
		if !e.Store().Observe(key, value) {
			t.Fatal(e.Store().Stats())
		}
	}
	if err := e.Stop(); err != nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestMetadataAbsentFields(t *testing.T) {
	saved := SentFunc
	defer func() { SentFunc = saved }()
	SentFunc = func(event common.MapStr) bool {
		dims := event["data"].([]common.MapStr)[0]["dimension"].(map[string]string)
		for _, field := range []string{"unit", "description", "usage", "interval"} {
			if dims[field] != "" {
				t.Errorf("absent %s exported as %q", field, dims[field])
			}
		}
		return true
	}
	entry := metricmeta.Entry{Key: metricmeta.NewKey(3, metricmeta.Scope{Type: metricmeta.ScopeDefault}, "m"), Value: metricmeta.Value{
		Name: "m", Type: "gauge", OriginalType: "gauge", Temporality: "delta", Unit: "ignored", Description: "ignored", Usage: "ignored", Interval: 123,
	}}
	if err := sendMetadata(context.Background(), 901, []metricmeta.Entry{entry}); err != nil {
		t.Fatal(err)
	}
}

func TestMetadataSendFailure(t *testing.T) {
	saved := SentFunc
	defer func() { SentFunc = saved }()
	SentFunc = func(common.MapStr) bool { return false }
	c := metricmeta.DefaultConfig()
	e, err := metricmeta.NewBatchExporter(c, func(ctx context.Context, entries []metricmeta.Entry) error { return sendMetadata(ctx, 901, entries) })
	if err != nil {
		t.Fatal(err)
	}
	defer e.Discard()
	e.Store().Observe(metricmeta.NewKey(3, metricmeta.Scope{Type: metricmeta.ScopeDefault}, "m"), metricmeta.Value{Name: "m", Type: "gauge", OriginalType: "gauge", Temporality: "delta"})
	if err := e.Flush(context.Background()); err == nil || e.Store().Stats().Pending != 1 || e.Store().Stats().BloomVersions != 0 {
		t.Fatal("failed send acknowledged")
	}
	SentFunc = func(common.MapStr) bool { return true }
	if err := e.Flush(context.Background()); err != nil || e.Store().Stats().Exported != 1 {
		t.Fatal("retry failed", err)
	}
}

func TestExporterConcurrentLifecycleDisabled(t *testing.T) {
	e, err := New(confengine.MustLoadConfigContent(`exporter: {}`))
	if err != nil {
		t.Fatal(err)
	}
	if e.metricMetadata != nil {
		t.Fatal("feature must default off")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = e.Start(); e.Stop() }()
	}
	wg.Wait()
	if e.Start() != metricmeta.ErrClosed {
		t.Fatal("Start after Stop")
	}
}

func TestMetadataConfigRejectsUnsafeFallback(t *testing.T) {
	for _, line := range []string{
		"{enabled: true}", "{enabled: true, dataid: -1}", "{endpoint: 'http://localhost'}",
		"{enabled: true, dataid: 901, rate_limit: {dataid_per_minute: 0}}",
		"{limits: {max_updates: 3}}",
		"{enabled: true, dataid: 901, exporter: {batch_size: 0}}",
		"{exporter: {max_pending_entries: 30}}",
		"{enabled: true, output_data_id: 901}",
		"{limits: {instance_updates_per_minute: 3}}",
		"{limits: {default_source_updates_per_minute: 3}}",
	} {
		if _, err := New(confengine.MustLoadConfigContent("exporter: {metric_metadata: " + line + "}")); err == nil {
			t.Errorf("accepted unsupported config %s", line)
		}
	}
}

func TestMetadataPlatformExporterConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "platform.yml")
	conf := confengine.MustLoadConfigContent(fmt.Sprintf("apm: {patterns: [%q]}\nexporter: {metric_metadata: {exporter: {timeout: 9s}}}", path))
	platform := func(interval string, batch, queue int) {
		t.Helper()
		content := fmt.Sprintf("type: platform\nexporter: {metric_metadata: {enabled: true, dataid: 901, exporter: {interval: %s, batch_size: %d, queue_size: %d}}}", interval, batch, queue)
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	platform("1h", 2, 2)
	loaded, err := loadExporterConfig(conf)
	if err != nil {
		t.Fatal(err)
	}
	want := metricmeta.ExporterConfig{Interval: time.Hour, Timeout: 9 * time.Second, BatchSize: 2, QueueSize: 2}
	if got := loaded.MetricMetadata.Config.Exporter; got != want {
		t.Fatalf("platform overlay: got %+v, want %+v", got, want)
	}

	batches := make(chan int, 4)
	saved := SentFunc
	SentFunc = func(event common.MapStr) bool { batches <- len(event["data"].([]common.MapStr)); return true }
	defer func() { SentFunc = saved }()
	e, err := New(conf)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	if err := e.metricMetadata.Start(); err != nil {
		t.Fatal(err)
	}
	store := e.store.Load()
	observe := func(name string) bool {
		return store.Observe(metricmeta.NewKey(3, metricmeta.Scope{Type: metricmeta.ScopeDefault}, name), metricmeta.Value{Name: name, Type: "gauge", OriginalType: "gauge", Temporality: "delta"})
	}
	if !observe("a") || !observe("b") || observe("c") {
		t.Fatal(store.Stats())
	}
	platform("10ms", 1, 3)
	if err := e.Reload(conf); err != nil {
		t.Fatal(err)
	}
	if store != e.store.Load() || !observe("c") {
		t.Fatal("reload lost the store or failed to apply queue_size")
	}
	for i := 0; i < 3; i++ {
		select {
		case n := <-batches:
			if n != 1 {
				t.Fatalf("batch size = %d", n)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("updated interval did not send pending entries")
		}
	}
}

func BenchmarkMetadataExport1000(b *testing.B) {
	saved := SentFunc
	SentFunc = func(common.MapStr) bool { return true }
	b.Cleanup(func() { SentFunc = saved })
	cfg := metricmeta.DefaultConfig()
	cfg.Limits.Instance, cfg.Limits.DefaultSource = math.MaxInt, math.MaxInt
	exp, err := metricmeta.NewBatchExporter(cfg, func(ctx context.Context, entries []metricmeta.Entry) error {
		return sendMetadata(ctx, 901, entries)
	})
	if err != nil {
		b.Fatal(err)
	}
	defer exp.Discard()
	rows := make([]metricmeta.Entry, 1000)
	for i := range rows {
		name := fmt.Sprintf("metric_%d", i)
		rows[i] = metricmeta.Entry{
			Key:   metricmeta.NewKey(3, metricmeta.Scope{Type: metricmeta.ScopeAPM, Name: "checkout"}, name),
			Value: metricmeta.Value{Name: name, Type: "gauge", OriginalType: "gauge", Temporality: "delta", Unit: "ms", Description: "耗时", Present: metricmeta.PresentUnit | metricmeta.PresentDescription},
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := exp.Store().Clean(); err != nil {
			b.Fatal(err)
		}
		for _, row := range rows {
			if !exp.Store().Observe(row.Key, row.Value) {
				b.Fatal("observe rejected")
			}
		}
		if err := exp.Flush(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}
