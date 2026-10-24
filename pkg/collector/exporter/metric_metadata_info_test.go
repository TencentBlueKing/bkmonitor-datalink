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
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/metricmeta"
)

func infoEntry(name string) metricmeta.Entry {
	return metricmeta.Entry{
		Key:   metricmeta.NewKey(3, metricmeta.Scope{Type: metricmeta.ScopeAPM, Name: "checkout-sdk"}, name),
		Value: metricmeta.Value{Name: name, Type: "gauge", OriginalType: "gauge", Temporality: "delta", Description: "CPU 使用率", Present: metricmeta.PresentDescription},
	}
}

func TestMetadataInfoReplacementCapacityAndExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	c := newMetadataInfoCollector(2, time.Minute)
	c.now = func() time.Time { return now }
	export := func(entries ...metricmeta.Entry) error {
		body, err := json.Marshal(entries)
		if err != nil {
			t.Fatal(err)
		}
		return c.Export(context.Background(), body)
	}
	first := infoEntry("cpu_usage")
	if err := export(first); err != nil {
		t.Fatal(err)
	}
	now = now.Add(30 * time.Second)
	newer := first
	newer.Value.Description = "新描述"
	if err := export(newer, infoEntry("memory")); err != nil {
		t.Fatal(err)
	}
	if err := export(infoEntry("disk"), infoEntry("network")); err == nil {
		t.Fatal("capacity must reject the complete batch")
	}
	if len(c.entries) != 2 {
		t.Fatal("partial batch admitted")
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(collectorForTest{c})
	scrape := func() string {
		w := httptest.NewRecorder()
		promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
		if w.Code != 200 {
			t.Fatalf("scrape: %s", w.Body.String())
		}
		return w.Body.String()
	}
	text := scrape()
	if !strings.Contains(text, `description="新描述"`) || strings.Contains(text, `description="CPU 使用率",interval="",metric="cpu_usage"`) || !strings.Contains(text, `dataid="3"`) {
		t.Fatal(text)
	}
	now = now.Add(31 * time.Second)
	if !strings.Contains(scrape(), "bk_collector_metric_metadata_info_entries 2") {
		t.Fatal("updated values expired too soon")
	}
	now = now.Add(29 * time.Second)
	if text := scrape(); strings.Contains(text, "bk_collector_metric_metadata_info{") || !strings.Contains(text, "bk_collector_metric_metadata_info_entries 0") {
		t.Fatal(text)
	}
	if err := export(infoEntry("disk")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	body, _ := json.Marshal([]metricmeta.Entry{first})
	if err := c.Export(ctx, body); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := c.Export(context.Background(), []byte(`[{}]`)); err == nil {
		t.Fatal("invalid metadata accepted")
	}
	if len(c.entries) != 1 {
		t.Fatal("failed export changed cache")
	}
}

type collectorForTest struct{ *metadataInfoCollector }

func (collectorForTest) Describe(ch chan<- *prometheus.Desc) {
	ch <- metadataInfoDesc
	ch <- metadataInfoEntriesDesc
}

func TestMetadataDefaultInfoExporter(t *testing.T) {
	conf := confengine.MustLoadConfigContent(`exporter:
  metric_metadata:
    enabled: true
    debounce_interval: 0s
  converter:
    tars:
      disable_aggregate: true
`)
	e, err := New(conf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Stop(); metadataInfo.Store(nil); metadataStats.Store(nil) })
	row := infoEntry("default_info")
	e.metricMetadata.Store().Observe(row.Key, row.Value)
	if err := e.metricMetadata.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	reg := prometheus.NewRegistry()
	reg.MustRegister(metadataInfoProxy{})
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range families {
		if f.GetName() == "bk_collector_metric_metadata_info" {
			found = true
			if len(f.Metric) != 1 || f.Metric[0].GetGauge().GetValue() != 1 {
				t.Fatal(f)
			}
		}
	}
	if !found {
		t.Fatal("default exporter did not expose info metric")
	}
	if e.metricMetadata.Store().Stats().Exported != 1 {
		t.Fatal("missing publication acknowledgment")
	}
	if err := e.metricMetadata.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.metricMetadata.Store().Stats().Exported != 1 {
		t.Fatal("duplicate publication")
	}
}

func TestMetadataInfoConcurrentExportAndScrape(t *testing.T) {
	c := newMetadataInfoCollector(4, time.Minute)
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectorForTest{c})
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			row := infoEntry(fmt.Sprintf("metric_%d", worker))
			for version := 0; version < 100; version++ {
				row.Value.Description = fmt.Sprint(version)
				body, err := json.Marshal([]metricmeta.Entry{row})
				if err != nil {
					t.Error(err)
					return
				}
				if err := c.Export(context.Background(), body); err != nil {
					t.Error(err)
					return
				}
			}
		}(worker)
	}
	for i := 0; i < 100; i++ {
		if _, err := reg.Gather(); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "bk_collector_metric_metadata_info" {
			continue
		}
		if len(family.Metric) != 4 {
			t.Fatalf("want four current entries, got %d", len(family.Metric))
		}
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if label.GetName() == "description" && label.GetValue() != "99" {
					t.Fatalf("scrape retained an old version: %v", label)
				}
			}
		}
	}
}
