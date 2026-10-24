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
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/metricmeta"
)

func newMetricMetadataExporter(config MetricMetadataConfig) (*metricmeta.Exporter, error) {
	if config.Endpoint == "" {
		if config.InfoRetention <= 0 {
			return nil, fmt.Errorf("metric_metadata.info_retention must be positive")
		}
		info := newMetadataInfoCollector(config.MaxPendingEntries, config.InfoRetention)
		exp, err := metricmeta.NewExporter(config.Config, info.Export)
		if err != nil {
			return nil, err
		}
		metadataInfo.Store(info)
		return exp, nil
	}
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil {
		return nil, fmt.Errorf("metric_metadata.endpoint must be an HTTP(S) URL without userinfo")
	}
	client := &http.Client{
		Timeout:       config.ExportTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return metricmeta.NewExporter(config.Config, func(ctx context.Context, body []byte) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.Endpoint, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("metric metadata export returned HTTP %d", resp.StatusCode)
		}
		return nil
	})
}

var metadataStats atomic.Pointer[metricmeta.Store]

type metadataCollector struct{ descriptors map[string]*prometheus.Desc }

func init() {
	names := []string{"pending_entries", "bloom_versions", "bloom_rotations_total", "cleans_total", "forced_entries_total", "export_entries_total", "suppressed_total", "debounced_total", "export_failures_total", "dropped_total", "flush_duration_seconds"}
	c := metadataCollector{descriptors: make(map[string]*prometheus.Desc)}
	for _, name := range names {
		var labels []string
		if name == "dropped_total" {
			labels = []string{"reason"}
		}
		c.descriptors[name] = prometheus.NewDesc("bk_collector_metric_metadata_"+name, "Metric metadata "+name, labels, nil)
	}
	prometheus.MustRegister(c, metadataInfoProxy{})
}

func (c metadataCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range c.descriptors {
		ch <- desc
	}
}

func (c metadataCollector) Collect(ch chan<- prometheus.Metric) {
	store := metadataStats.Load()
	if store == nil {
		return
	}
	s := store.Stats()
	ch <- prometheus.MustNewConstHistogram(c.descriptors["flush_duration_seconds"], s.FlushCount, s.FlushSeconds, s.FlushBuckets)
	for name, value := range map[string]int{"pending_entries": s.Pending, "bloom_versions": s.BloomVersions} {
		ch <- prometheus.MustNewConstMetric(c.descriptors[name], prometheus.GaugeValue, float64(value))
	}
	for name, value := range map[string]uint64{
		"bloom_rotations_total": s.Rotations, "cleans_total": s.Cleans, "forced_entries_total": s.Forced,
		"export_entries_total": s.Exported, "suppressed_total": s.Suppressed, "debounced_total": s.Debounced, "export_failures_total": s.Failures,
	} {
		ch <- prometheus.MustNewConstMetric(c.descriptors[name], prometheus.CounterValue, float64(value))
	}
	for reason, value := range s.Dropped {
		ch <- prometheus.MustNewConstMetric(c.descriptors["dropped_total"], prometheus.CounterValue, float64(value), reason)
	}
}
