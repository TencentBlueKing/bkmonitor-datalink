// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"sort"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// RelationSchemaSourceCount 是 unify_query_relation_schema_definitions 的一条序列。
// Kind 取 ResourceDefinition|RelationDefinition，Source 取 builtin|override|extension|namespace。
type RelationSchemaSourceCount struct {
	Kind   string
	Source string
	Count  int
}

// RelationSchemaSnapshot 是抓取时读到的关联关系定义状态。
type RelationSchemaSnapshot struct {
	Counts []RelationSchemaSourceCount
	// OverlayLoaded Redis 覆盖层至少完成过一次全量加载。
	OverlayLoaded bool
	// LastSuccess 最近一次全量加载成功的时间，零值表示从未成功。
	LastSuccess time.Time
	// Failures 按原因累计的覆盖层读取失败次数，原因取 client_missing|read_failed|decode_failed。
	Failures map[string]int64
}

var (
	relationSchemaDefinitionsDesc = prometheus.NewDesc(
		"unify_query_relation_schema_definitions",
		"Effective relation schema definitions by kind and source: builtin, override (Redis __all__ replaces a builtin), "+
			"extension (Redis __all__ only) and namespace (sum of business namespaces in Redis). Read from the current provider at scrape time.",
		[]string{"kind", "source"}, nil,
	)
	relationSchemaOverlayLoadedDesc = prometheus.NewDesc(
		"unify_query_relation_schema_overlay_loaded",
		"1 when the Redis overlay of relation definitions has been fully loaded at least once, 0 while only builtin definitions are served.",
		nil, nil,
	)
	relationSchemaOverlayLastSuccessDesc = prometheus.NewDesc(
		"unify_query_relation_schema_overlay_last_success_timestamp_seconds",
		"Unix time of the last successful full load of the Redis overlay (startup, subscription, periodic reconcile); 0 if never.",
		nil, nil,
	)
	relationSchemaOverlayFailuresDesc = prometheus.NewDesc(
		"unify_query_relation_schema_overlay_failures_total",
		"Redis overlay read failures of the current provider by reason: client_missing, read_failed, decode_failed. Resets when the provider is recreated.",
		[]string{"reason"}, nil,
	)
	relationSchemaMu       sync.RWMutex
	relationSchemaSnapshot func() (RelationSchemaSnapshot, bool)
)

// relationSchemaCollector 在抓取时读取当前生效定义，加载、Pub/Sub 重载和对账之后的状态都直接反映出来。
type relationSchemaCollector struct{}

func (relationSchemaCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- relationSchemaDefinitionsDesc
	ch <- relationSchemaOverlayLoadedDesc
	ch <- relationSchemaOverlayLastSuccessDesc
	ch <- relationSchemaOverlayFailuresDesc
}

func (relationSchemaCollector) Collect(ch chan<- prometheus.Metric) {
	relationSchemaMu.RLock()
	read := relationSchemaSnapshot
	relationSchemaMu.RUnlock()
	if read == nil {
		return
	}
	snapshot, ok := read()
	if !ok {
		return
	}
	for _, item := range snapshot.Counts {
		ch <- prometheus.MustNewConstMetric(relationSchemaDefinitionsDesc, prometheus.GaugeValue, float64(item.Count), item.Kind, item.Source)
	}
	loaded := 0.0
	if snapshot.OverlayLoaded {
		loaded = 1
	}
	ch <- prometheus.MustNewConstMetric(relationSchemaOverlayLoadedDesc, prometheus.GaugeValue, loaded)
	lastSuccess := 0.0
	if !snapshot.LastSuccess.IsZero() {
		lastSuccess = float64(snapshot.LastSuccess.UnixMilli()) / 1000
	}
	ch <- prometheus.MustNewConstMetric(relationSchemaOverlayLastSuccessDesc, prometheus.GaugeValue, lastSuccess)
	reasons := make([]string, 0, len(snapshot.Failures))
	for reason := range snapshot.Failures {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		ch <- prometheus.MustNewConstMetric(relationSchemaOverlayFailuresDesc, prometheus.CounterValue, float64(snapshot.Failures[reason]), reason)
	}
}

func init() {
	prometheus.MustRegister(relationSchemaCollector{})
}

// SetRelationSchemaSnapshot 设置抓取时读取关联关系定义状态的函数；nil 或返回 false 时不输出。
func SetRelationSchemaSnapshot(read func() (RelationSchemaSnapshot, bool)) {
	relationSchemaMu.Lock()
	relationSchemaSnapshot = read
	relationSchemaMu.Unlock()
}
