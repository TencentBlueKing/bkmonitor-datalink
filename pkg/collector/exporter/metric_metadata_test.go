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
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/metricmeta"
)

func TestMetricMetadataHTTPConfigAndFinalFlush(t *testing.T) {
	rows := make(chan []map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Error("invalid request")
		}
		var batch []map[string]any
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Error(err)
		}
		rows <- batch
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	config := confengine.MustLoadConfigContent(fmt.Sprintf(`exporter:
  metric_metadata:
    enabled: true
    endpoint: %q
    full_report_interval: 2h
    debounce_interval: 2m
    blocked_dataids: [9]
    dataid_limits: [{dataid: 3, max_updates: 10}]
  converter:
    tars:
      disable_aggregate: true
`, server.URL))
	e, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if e.cfg.MetricMetadata.DebounceInterval != 2*time.Minute || e.cfg.MetricMetadata.BloomCapacity != 100000 || len(e.cfg.MetricMetadata.DataIDLimits) != 1 || e.cfg.MetricMetadata.DataIDLimits[0].Limit != 10 {
		t.Fatalf("configuration not unpacked: %+v", e.cfg.MetricMetadata)
	}
	m := pmetric.NewMetrics()
	rm := m.ResourceMetrics().AppendEmpty()
	rm.Resource().Attributes().PutString("service_name", "checkout-api")
	sm := rm.ScopeMetrics().AppendEmpty()
	sm.Scope().SetName("scope-x")
	metric := sm.Metrics().AppendEmpty()
	metric.SetName("cpu.usage")
	metric.SetDescription("CPU 使用率")
	metric.SetUnit("1")
	metric.SetEmptyGauge().DataPoints().AppendEmpty().SetDoubleValue(42)
	e.converter.Convert(&define.Record{RecordType: define.RecordMetrics, Token: define.Token{MetricsDataId: 3}, Data: m}, func(events ...define.Event) {
		if len(events) != 1 || events[0].DataId() != 3 {
			t.Error("numeric conversion changed")
		}
	})
	e.Stop() // 收尾跳过防抖，未 Start 也有独立发送期限。
	batch := <-rows
	if len(batch) != 1 || batch[0]["__metric"] != "cpu_usage" || batch[0]["__name"] != "cpu.usage" || batch[0]["__scope"] != "checkout%2Dapi-scope%2Dx" || batch[0]["dataid"] != float64(3) {
		t.Fatalf("unexpected metadata: %v", batch)
	}
	if e.metricMetadata.Store().Stats().Exported != 1 {
		t.Fatal("missing successful acknowledgment")
	}
}

func TestMetricMetadataHTTPFailureRetryAndCancellation(t *testing.T) {
	var mu sync.Mutex
	fail := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusTooManyRequests)
			fail = false
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	c := MetricMetadataConfig{Endpoint: server.URL, Config: metricmeta.DefaultConfig()}
	c.DebounceInterval = 0
	e, err := newMetricMetadataExporter(c)
	if err != nil {
		t.Fatal(err)
	}
	e.Store().Observe(metricmeta.NewKey(1, metricmeta.Scope{Type: metricmeta.ScopeDefault}, "m"), metricmeta.Value{Name: "m", Type: "gauge", OriginalType: "gauge", Temporality: "delta"})
	if e.Flush(context.Background()) == nil || e.Store().Stats().Pending != 1 {
		t.Fatal("429 must retain unconfirmed metadata")
	}
	if err := e.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.Store().Stats().Exported != 1 {
		t.Fatal("retry not acknowledged")
	}
	if err := e.Stop(); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"", "file:///tmp/x", "https://user:pass@example.com"} {
		c.Endpoint = endpoint
		if _, err := newMetricMetadataExporter(c); err == nil {
			t.Fatalf("invalid endpoint accepted: %s", endpoint)
		}
	}
	entered := make(chan struct{})
	var once sync.Once
	cancelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		wait := false
		once.Do(func() { close(entered); wait = true })
		if wait {
			select {
			case <-r.Context().Done():
			case <-time.After(time.Second):
			}
		}
	}))
	defer cancelServer.Close()
	c.Endpoint = cancelServer.URL
	cancelExporter, err := newMetricMetadataExporter(c)
	if err != nil {
		t.Fatal(err)
	}
	defer cancelExporter.Stop()
	cancelExporter.Store().Observe(metricmeta.NewKey(1, metricmeta.Scope{Type: metricmeta.ScopeDefault}, "cancel"), metricmeta.Value{Name: "cancel", Type: "gauge", OriginalType: "gauge", Temporality: "delta"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- cancelExporter.Flush(ctx) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request not started")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP callback ignored cancellation")
	}
}

func TestExporterConcurrentLifecycleDisabled(t *testing.T) {
	e, err := New(confengine.MustLoadConfigContent(`exporter: {metric_metadata: {enabled: false}}`))
	if err != nil {
		t.Fatal(err)
	}
	if e.metricMetadata != nil {
		t.Fatal("feature must honor explicit disable")
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
