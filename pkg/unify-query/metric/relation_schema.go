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
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// RelationSchemaSourceCount 是 unify_query_relation_schema_definitions 的一条序列。
// Kind 取 ResourceDefinition|RelationDefinition，Source 取 builtin|override|extension|namespace。
type RelationSchemaSourceCount struct {
	Kind   string
	Source string
	Count  int
}

var (
	relationSchemaDefinitionsDesc = prometheus.NewDesc(
		"unify_query_relation_schema_definitions",
		"Effective relation schema definitions by kind and source: builtin, override (Redis __all__ replaces a builtin), "+
			"extension (Redis __all__ only) and namespace (sum of business namespaces in Redis). Read from the current provider at scrape time.",
		[]string{"kind", "source"}, nil,
	)
	relationSchemaCountsMu sync.RWMutex
	relationSchemaCounts   func() []RelationSchemaSourceCount
)

// relationSchemaCollector 在抓取时读取当前生效定义，加载、Pub/Sub 重载和对账之后的状态都直接反映出来。
type relationSchemaCollector struct{}

func (relationSchemaCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- relationSchemaDefinitionsDesc
}

func (relationSchemaCollector) Collect(ch chan<- prometheus.Metric) {
	relationSchemaCountsMu.RLock()
	counts := relationSchemaCounts
	relationSchemaCountsMu.RUnlock()
	if counts == nil {
		return
	}
	for _, item := range counts() {
		ch <- prometheus.MustNewConstMetric(relationSchemaDefinitionsDesc, prometheus.GaugeValue, float64(item.Count), item.Kind, item.Source)
	}
}

func init() {
	prometheus.MustRegister(relationSchemaCollector{})
}

// SetRelationSchemaSourceCounts 设置抓取时读取生效定义来源计数的函数，nil 表示不输出。
func SetRelationSchemaSourceCounts(counts func() []RelationSchemaSourceCount) {
	relationSchemaCountsMu.Lock()
	relationSchemaCounts = counts
	relationSchemaCountsMu.Unlock()
}
