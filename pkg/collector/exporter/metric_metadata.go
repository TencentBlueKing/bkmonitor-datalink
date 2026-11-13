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
	"strconv"
	"sync/atomic"
	"time"

	"github.com/elastic/beats/libbeat/common"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/exporter/queue"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/metricmeta"
)

const metadataMetricName = "bk_collector_metric_metadata_info"

// sendMetadata 提交标准时序批次；成功表示本地发送客户端接纳，不代表已入库。
func sendMetadata(ctx context.Context, dataID int32, rows []metricmeta.Entry) error {
	points := make([]common.MapStr, 0, len(rows))
	now := time.Now().UnixMilli()
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		k, v := row.Key, row.Value
		if v.Present&metricmeta.PresentDescription == 0 {
			v.Description = ""
		}
		if v.Present&metricmeta.PresentUnit == 0 {
			v.Unit = ""
		}
		if v.Present&metricmeta.PresentUsage == 0 {
			v.Usage = ""
		}
		interval := ""
		if v.Present&metricmeta.PresentInterval != 0 {
			interval = strconv.FormatInt(v.Interval, 10)
		}
		points = append(points, common.MapStr{
			"metrics":   map[string]float64{metadataMetricName: 1},
			"timestamp": now,
			"dimension": map[string]string{
				"bk_biz_id":     strconv.FormatInt(int64(k.BizID), 10),
				"dataid":        strconv.FormatInt(int64(k.DataID), 10),
				"scope_type":    string(k.ScopeType),
				"scope":         k.Scope,
				"metric":        k.Metric,
				"name":          v.Name,
				"description":   v.Description,
				"unit":          v.Unit,
				"temporality":   v.Temporality,
				"interval":      interval,
				"original_type": v.OriginalType,
				"type":          v.Type,
				"usage":         v.Usage,
				"present":       strconv.Itoa(int(v.Present)),
			},
		})
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !SentFunc(queue.NewMetricsMapStr(dataID, points)) {
		return fmt.Errorf("metric metadata: output DataID %d unavailable", dataID)
	}
	return nil
}

var metadataStats atomic.Pointer[metricmeta.Store]

type metadataCollector struct{ descriptors map[string]*prometheus.Desc }

func init() {
	names := []string{"pending_entries", "bloom_versions", "bloom_rotations_total", "cleans_total", "forced_entries_total", "export_entries_total", "suppressed_total", "export_failures_total", "dropped_total", "flush_duration_seconds"}
	c := metadataCollector{descriptors: make(map[string]*prometheus.Desc)}
	for _, name := range names {
		var labels []string
		if name == "dropped_total" {
			labels = []string{"reason"}
		}
		c.descriptors[name] = prometheus.NewDesc("bk_collector_metric_metadata_"+name, "Metric metadata "+name, labels, nil)
	}
	prometheus.MustRegister(c)
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
		"export_entries_total": s.Exported, "suppressed_total": s.Suppressed, "export_failures_total": s.Failures,
	} {
		ch <- prometheus.MustNewConstMetric(c.descriptors[name], prometheus.CounterValue, float64(value))
	}
	for reason, value := range s.Dropped {
		ch <- prometheus.MustNewConstMetric(c.descriptors["dropped_total"], prometheus.CounterValue, float64(value), reason)
	}
}
