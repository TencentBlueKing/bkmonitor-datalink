// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta3

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

func TestSameTypeQueryHonorsHopBudget(t *testing.T) {
	provider := NewSchemaProviderFromRelation(relation.NewStaticSchemaProvider(relation.StaticProviderConfig{
		ResourcePrimaryKeys: map[string][]string{"pod": {"pod"}, "system": {"bk_target_ip"}},
		RelationSchemas: []relation.RelationSchema{
			{RelationName: "pod_to_pod", Category: relation.RelationCategoryDynamic, FromType: "pod", ToType: "pod", IsDirectional: true},
			{RelationName: "pod_to_system", Category: relation.RelationCategoryDynamic, FromType: "pod", ToType: "system", IsDirectional: true},
			{RelationName: "system_to_pod", Category: relation.RelationCategoryDynamic, FromType: "system", ToType: "pod", IsDirectional: true},
		},
	}))
	for _, hops := range []int{1, 2, 3} {
		t.Run(string(rune('0'+hops)), func(t *testing.T) {
			executor := &mockGraphQueryExecutor{}
			model, err := NewModel(context.Background(), executor)
			require.NoError(t, err)
			model.SetSchemaProvider(provider)
			req := &QueryRequest{Timestamp: 300000, SourceType: ResourceTypePod, SourceInfo: map[string]string{"pod": "p1"}, TargetType: ResourceTypePod, TargetTypeExplicit: true, MaxHops: hops, AllowedRelationTypes: []RelationCategory{RelationCategoryDynamic}, DynamicRelationDirection: DirectionOutbound}
			_, paths, _, err := model.QueryLivenessGraph(context.Background(), req)
			require.NoError(t, err)
			require.Equal(t, hops, req.MaxHops)
			require.NotEmpty(t, paths)
			longest := 0
			for _, path := range paths {
				length := len(path.Steps) - 1
				require.LessOrEqual(t, length, hops)
				if length > longest {
					longest = length
				}
			}
			require.Equal(t, hops, longest, "repeated pod types must not terminate discovery at the first pod")
		})
	}
}

func TestRelationAdaptersValidateHTTPHopBudget(t *testing.T) {
	for _, payload := range []string{
		`{"query_list":[{"max_hops":0}]}`,
		`{"query_list":[{"max_hops":-1}]}`,
		`{"query_list":[{"max_hops":999}]}`,
	} {
		t.Run(payload, func(t *testing.T) {
			var instant cmdb.RelationMultiResourceRequest
			var ranged cmdb.RelationMultiResourceRangeRequest
			require.NoError(t, json.Unmarshal([]byte(payload), &instant))
			require.NoError(t, json.Unmarshal([]byte(payload), &ranged))
			executor := &mockGraphQueryExecutor{}
			model, err := NewModel(context.Background(), executor)
			require.NoError(t, err)
			_, _, _, _, _, err = model.QueryResourceMatcherWithMaxHops(context.Background(), "", "", "300", "pod", "pod", cmdb.Matcher{"pod": "p1"}, nil, false, nil, instant.QueryList[0].MaxHops)
			require.ErrorContains(t, err, "max_allowed_hops")
			_, _, _, _, _, err = model.QueryResourceMatcherRangeWithMaxHops(context.Background(), "", "", "60s", "120", "300", "pod", "pod", cmdb.Matcher{"pod": "p1"}, nil, false, nil, ranged.QueryList[0].MaxHops)
			require.ErrorContains(t, err, "max_allowed_hops")
			require.Empty(t, executor.sqls)
		})
	}
}

func TestQueryLivenessGraphRejectsExcessiveHopBudget(t *testing.T) {
	executor := &mockGraphQueryExecutor{}
	model, err := NewModel(context.Background(), executor)
	require.NoError(t, err)
	req := &QueryRequest{Timestamp: 300000, SourceType: ResourceTypePod, SourceInfo: map[string]string{"pod": "p1"}, TargetType: ResourceTypePod, TargetTypeExplicit: true, MaxHops: MaxAllowedHops + 1}
	_, _, _, err = model.QueryLivenessGraph(context.Background(), req)
	require.ErrorContains(t, err, "max_allowed_hops")
	require.Equal(t, MaxAllowedHops+1, req.MaxHops)
	require.Empty(t, executor.sqls)
}

func TestRelationAdaptersHonorSameTypeHopBudget(t *testing.T) {
	provider := NewSchemaProviderFromRelation(relation.NewStaticSchemaProvider(relation.StaticProviderConfig{
		ResourcePrimaryKeys: map[string][]string{"pod": {"pod"}, "system": {"bk_target_ip"}},
		RelationSchemas:     []relation.RelationSchema{{RelationName: "pod_with_system", Category: relation.RelationCategoryStatic, FromType: "pod", ToType: "system"}},
	}))
	for _, hops := range []int{1, 3} {
		t.Run(string(rune('0'+hops)), func(t *testing.T) {
			executor := &mockGraphQueryExecutor{}
			model, err := NewModel(context.Background(), executor)
			require.NoError(t, err)
			model.SetSchemaProvider(provider)
			_, _, _, _, _, instantErr := model.QueryResourceMatcherWithMaxHops(context.Background(), "", "", "300", "pod", "pod", cmdb.Matcher{"pod": "p1"}, nil, false, nil, &hops)
			_, _, _, _, _, rangeErr := model.QueryResourceMatcherRangeWithMaxHops(context.Background(), "", "", "60s", "120", "300", "pod", "pod", cmdb.Matcher{"pod": "p1"}, nil, false, nil, &hops)
			if hops == 1 {
				require.ErrorContains(t, instantErr, "empty paths")
				require.ErrorContains(t, rangeErr, "empty paths")
				require.Empty(t, executor.sqls)
			} else {
				require.NoError(t, instantErr)
				require.NoError(t, rangeErr)
				require.NotEmpty(t, executor.sqls)
			}
		})
	}
}

func TestSameTypeExtractionPreservesPathAndHopBound(t *testing.T) {
	graph := NewLivenessGraph(0, 100)
	graph.RootID = "p1"
	hops := 2
	graph.maxTargetHops = &hops
	periods := []*VisiblePeriod{{Start: 0, End: 100}}
	for _, id := range []string{"p1", "p2", "p3", "p4"} {
		graph.AddNode(&NodeLiveness{ResourceID: id, ResourceType: ResourceTypePod, Labels: map[string]string{"pod": id}, RawPeriods: periods})
	}
	for _, edge := range [][2]string{{"p1", "p2"}, {"p2", "p3"}, {"p3", "p4"}} {
		graph.AddEdge(&EdgeLiveness{RelationID: edge[0] + edge[1], FromID: edge[0], ToID: edge[1], RawPeriods: periods})
	}
	merged := mergeLivenessGraphsByRoot([]*LivenessGraph{graph})
	require.Len(t, merged, 1)
	for _, paths := range [][]*TargetPath{
		merged[0].TargetPaths(ResourceTypePod, []ResourceType{ResourceTypePod, ResourceTypePod, ResourceTypePod}, false),
		merged[0].TargetPathsFromFilteredInstantQuery(ResourceTypePod, []ResourceType{ResourceTypePod, ResourceTypePod, ResourceTypePod}, false),
	} {
		require.Len(t, paths, 1)
		require.Equal(t, "p3", paths[0].Target.ResourceID)
	}
}
