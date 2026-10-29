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
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/metricmeta"
)

var metadataInfo atomic.Pointer[metadataInfoCollector]

var metadataInfoDesc = prometheus.NewDesc("bk_collector_metric_metadata_info",
	"Recently exported metric metadata; value is 1, labels describe the metric.",
	[]string{"dataid", "scope_type", "scope", "metric", "name", "description", "unit", "temporality", "interval", "original_type", "type", "usage", "present"}, nil)

var metadataInfoEntriesDesc = prometheus.NewDesc("bk_collector_metric_metadata_info_entries",
	"Metric metadata entries retained for scraping.", nil, nil)

type metadataInfoProxy struct{}

func (metadataInfoProxy) Describe(ch chan<- *prometheus.Desc) {
	ch <- metadataInfoDesc
	ch <- metadataInfoEntriesDesc
}

func (metadataInfoProxy) Collect(ch chan<- prometheus.Metric) {
	if c := metadataInfo.Load(); c != nil {
		c.Collect(ch)
	}
}

type metadataInfoEntry struct {
	metric  prometheus.Metric
	expires time.Time
}

type metadataInfoCollector struct {
	mu         sync.Mutex
	entries    map[metricmeta.Key]metadataInfoEntry
	capacity   int
	retention  time.Duration
	nextExpiry time.Time
	now        func() time.Time
}

func newMetadataInfoCollector(capacity int, retention time.Duration) *metadataInfoCollector {
	return &metadataInfoCollector{entries: make(map[metricmeta.Key]metadataInfoEntry), capacity: capacity, retention: retention, now: time.Now}
}

// prune 仅在最早的条目到期时扫描，避免每个发送批次遍历整张表。
func (c *metadataInfoCollector) prune(now time.Time) {
	if c.nextExpiry.IsZero() || now.Before(c.nextExpiry) {
		return
	}
	c.nextExpiry = time.Time{}
	for key, entry := range c.entries {
		if !now.Before(entry.expires) {
			delete(c.entries, key)
		} else if c.nextExpiry.IsZero() || entry.expires.Before(c.nextExpiry) {
			c.nextExpiry = entry.expires
		}
	}
}

// Export 确认整批进入有界采集窗口；采集与远端持久化由现有监控链路负责。
func (c *metadataInfoCollector) Export(ctx context.Context, body []byte) error {
	var rows []metricmeta.Entry
	if err := json.Unmarshal(body, &rows); err != nil {
		return err
	}
	batch := make(map[metricmeta.Key]prometheus.Metric, len(rows))
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		k, v := row.Key, row.Value
		interval := ""
		if v.Present&metricmeta.PresentInterval != 0 {
			interval = strconv.FormatInt(v.Interval, 10)
		}
		metric, err := prometheus.NewConstMetric(metadataInfoDesc, prometheus.GaugeValue, 1,
			strconv.Itoa(int(k.DataID)), string(k.ScopeType), k.Scope, k.Metric, v.Name, v.Description,
			v.Unit, v.Temporality, interval, v.OriginalType, v.Type, v.Usage, strconv.Itoa(int(v.Present)))
		if err != nil {
			return err
		}
		batch[k] = metric
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	now := c.now()
	c.prune(now)
	count := len(c.entries)
	for key := range batch {
		if _, ok := c.entries[key]; !ok {
			count++
		}
	}
	if count > c.capacity {
		return fmt.Errorf("metric metadata info capacity exceeded: %d > %d", count, c.capacity)
	}
	expires := now.Add(c.retention)
	for key, metric := range batch {
		c.entries[key] = metadataInfoEntry{metric: metric, expires: expires}
	}
	if len(batch) > 0 && (c.nextExpiry.IsZero() || expires.Before(c.nextExpiry)) {
		c.nextExpiry = expires
	}
	return nil
}

func (c *metadataInfoCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	c.prune(c.now())
	metrics := make([]prometheus.Metric, 0, len(c.entries))
	for _, entry := range c.entries {
		metrics = append(metrics, entry.metric)
	}
	c.mu.Unlock()
	ch <- prometheus.MustNewConstMetric(metadataInfoEntriesDesc, prometheus.GaugeValue, float64(len(metrics)))
	for _, metric := range metrics {
		ch <- metric
	}
}
