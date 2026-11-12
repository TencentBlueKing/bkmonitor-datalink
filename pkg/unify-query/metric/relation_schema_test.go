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
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func gatherRelationSchema(t *testing.T) map[string]float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	values := map[string]float64{}
	for _, family := range families {
		if !strings.HasPrefix(family.GetName(), "unify_query_relation_schema_") {
			continue
		}
		for _, item := range family.GetMetric() {
			key := family.GetName()
			for _, label := range item.GetLabel() {
				key += "/" + label.GetValue()
			}
			if item.GetCounter() != nil {
				values[key] = item.GetCounter().GetValue()
			} else {
				values[key] = item.GetGauge().GetValue()
			}
		}
	}
	return values
}

func TestRelationSchemaMetricsReadSnapshotAtScrape(t *testing.T) {
	t.Cleanup(func() { SetRelationSchemaSnapshot(nil) })
	SetRelationSchemaSnapshot(nil)
	require.Empty(t, gatherRelationSchema(t))

	snapshot := RelationSchemaSnapshot{
		Counts: []RelationSchemaSourceCount{
			{Kind: "ResourceDefinition", Source: "builtin", Count: 26},
			{Kind: "ResourceDefinition", Source: "override", Count: 0},
		},
		Failures: map[string]int64{"client_missing": 0, "read_failed": 2, "decode_failed": 0},
	}
	SetRelationSchemaSnapshot(func() (RelationSchemaSnapshot, bool) { return snapshot, true })
	require.Equal(t, map[string]float64{
		"unify_query_relation_schema_definitions/ResourceDefinition/builtin":  26,
		"unify_query_relation_schema_definitions/ResourceDefinition/override": 0,
		"unify_query_relation_schema_overlay_loaded":                          0,
		"unify_query_relation_schema_overlay_last_success_timestamp_seconds":  0,
		"unify_query_relation_schema_overlay_failures_total/client_missing":   0,
		"unify_query_relation_schema_overlay_failures_total/decode_failed":    0,
		"unify_query_relation_schema_overlay_failures_total/read_failed":      2,
	}, gatherRelationSchema(t))

	// 每次抓取都重新读取，不缓存上一次的值。
	snapshot.Counts = []RelationSchemaSourceCount{{Kind: "ResourceDefinition", Source: "override", Count: 26}}
	snapshot.OverlayLoaded = true
	snapshot.LastSuccess = time.Unix(1700000000, 500*int64(time.Millisecond))
	values := gatherRelationSchema(t)
	require.EqualValues(t, 26, values["unify_query_relation_schema_definitions/ResourceDefinition/override"])
	require.NotContains(t, values, "unify_query_relation_schema_definitions/ResourceDefinition/builtin")
	require.EqualValues(t, 1, values["unify_query_relation_schema_overlay_loaded"])
	require.InDelta(t, 1700000000.5, values["unify_query_relation_schema_overlay_last_success_timestamp_seconds"], 1e-6)

	SetRelationSchemaSnapshot(func() (RelationSchemaSnapshot, bool) { return RelationSchemaSnapshot{}, false })
	require.Empty(t, gatherRelationSchema(t))
}
