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
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func gatherRelationSchemaDefinitions(t *testing.T) map[string]float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	values := map[string]float64{}
	for _, family := range families {
		if family.GetName() != "unify_query_relation_schema_definitions" {
			continue
		}
		require.Equal(t, "GAUGE", family.GetType().String())
		for _, item := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range item.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			require.Len(t, labels, 2)
			values[labels["kind"]+"/"+labels["source"]] = item.GetGauge().GetValue()
		}
	}
	return values
}

func TestRelationSchemaDefinitionsReadsCountsAtScrape(t *testing.T) {
	t.Cleanup(func() { SetRelationSchemaSourceCounts(nil) })
	SetRelationSchemaSourceCounts(nil)
	require.Empty(t, gatherRelationSchemaDefinitions(t))

	builtin := 26
	SetRelationSchemaSourceCounts(func() []RelationSchemaSourceCount {
		return []RelationSchemaSourceCount{
			{Kind: "ResourceDefinition", Source: "builtin", Count: builtin},
			{Kind: "ResourceDefinition", Source: "override", Count: 26 - builtin},
		}
	})
	require.Equal(t, map[string]float64{"ResourceDefinition/builtin": 26, "ResourceDefinition/override": 0}, gatherRelationSchemaDefinitions(t))
	// 每次抓取都重新读取，不缓存上一次的值。
	builtin = 0
	require.Equal(t, map[string]float64{"ResourceDefinition/builtin": 0, "ResourceDefinition/override": 26}, gatherRelationSchemaDefinitions(t))
}
