// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v2

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

func TestPathFinder_FindAllPaths(t *testing.T) {
	testCases := []struct {
		Name             string
		Source           ResourceType
		Target           ResourceType
		PathResource     []ResourceType
		AllowedCategory  []RelationCategory
		DynamicDirection TraversalDirection
		MaxHops          int
		Expected         []cmdb.PathV2
		ExpectError      bool
	}{
		{
			Name:            "node_to_system_static",
			Source:          ResourceTypeNode,
			Target:          ResourceTypeSystem,
			AllowedCategory: []RelationCategory{RelationCategoryStatic},
			MaxHops:         2,
			Expected: []cmdb.PathV2{
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "node", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "system", RelationType: "node_with_system", Category: "static", Direction: "outbound"},
				}},
			},
		},
		{
			Name:             "system_to_pod_dynamic_outbound",
			Source:           ResourceTypeSystem,
			Target:           ResourceTypePod,
			AllowedCategory:  []RelationCategory{RelationCategoryDynamic},
			DynamicDirection: DirectionOutbound,
			MaxHops:          2,
			Expected: []cmdb.PathV2{
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "system", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "pod", RelationType: "system_to_pod", Category: "dynamic", Direction: "outbound"},
				}},
			},
		},
		{
			Name:             "pod_to_system_dynamic_outbound",
			Source:           ResourceTypePod,
			Target:           ResourceTypeSystem,
			AllowedCategory:  []RelationCategory{RelationCategoryDynamic},
			DynamicDirection: DirectionOutbound,
			MaxHops:          2,
			Expected: []cmdb.PathV2{
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "pod", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "system", RelationType: "pod_to_system", Category: "dynamic", Direction: "outbound"},
				}},
			},
		},
		{
			Name:             "pod_to_system_dynamic_inbound",
			Source:           ResourceTypePod,
			Target:           ResourceTypeSystem,
			AllowedCategory:  []RelationCategory{RelationCategoryDynamic},
			DynamicDirection: DirectionInbound,
			MaxHops:          2,
			Expected: []cmdb.PathV2{
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "pod", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "system", RelationType: "system_to_pod", Category: "dynamic", Direction: "inbound"},
				}},
			},
		},
		{
			Name:             "pod_to_system_dynamic_both",
			Source:           ResourceTypePod,
			Target:           ResourceTypeSystem,
			AllowedCategory:  []RelationCategory{RelationCategoryDynamic},
			DynamicDirection: DirectionBoth,
			MaxHops:          2,
			Expected: []cmdb.PathV2{
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "pod", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "system", RelationType: "pod_to_system", Category: "dynamic", Direction: "outbound"},
				}},
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "pod", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "system", RelationType: "system_to_pod", Category: "dynamic", Direction: "inbound"},
				}},
			},
		},
		{
			Name:             "system_to_node_static_and_dynamic",
			Source:           ResourceTypeSystem,
			Target:           ResourceTypeNode,
			AllowedCategory:  []RelationCategory{RelationCategoryStatic, RelationCategoryDynamic},
			DynamicDirection: DirectionOutbound,
			MaxHops:          2,
			Expected: []cmdb.PathV2{
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "system", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "node", RelationType: "node_with_system", Category: "static", Direction: "inbound"},
				}},
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "system", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "pod", RelationType: "system_to_pod", Category: "dynamic", Direction: "outbound"},
					{ResourceType: "node", RelationType: "node_with_pod", Category: "static", Direction: "inbound"},
				}},
			},
		},
		{
			Name:             "node_to_system_static_and_dynamic_both",
			Source:           ResourceTypeNode,
			Target:           ResourceTypeSystem,
			AllowedCategory:  []RelationCategory{RelationCategoryStatic, RelationCategoryDynamic},
			DynamicDirection: DirectionBoth,
			MaxHops:          2,
			Expected: []cmdb.PathV2{
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "node", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "system", RelationType: "node_with_system", Category: "static", Direction: "outbound"},
				}},
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "node", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "pod", RelationType: "node_with_pod", Category: "static", Direction: "outbound"},
					{ResourceType: "system", RelationType: "pod_to_system", Category: "dynamic", Direction: "outbound"},
				}},
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "node", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "pod", RelationType: "node_with_pod", Category: "static", Direction: "outbound"},
					{ResourceType: "system", RelationType: "system_to_pod", Category: "dynamic", Direction: "inbound"},
				}},
			},
		},
		{
			Name:             "node_to_system_static_and_dynamic_both_maxhops3",
			Source:           ResourceTypeNode,
			Target:           ResourceTypeSystem,
			AllowedCategory:  []RelationCategory{RelationCategoryStatic, RelationCategoryDynamic},
			DynamicDirection: DirectionBoth,
			MaxHops:          3,
			Expected: []cmdb.PathV2{
				// 1跳: node -> system (静态)
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "node", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "system", RelationType: "node_with_system", Category: "static", Direction: "outbound"},
				}},
				// 3跳: node -> pod -> apm_service_instance -> system (静态)
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "node", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "pod", RelationType: "node_with_pod", Category: "static", Direction: "outbound"},
					{ResourceType: "apm_service_instance", RelationType: "apm_service_instance_with_pod", Category: "static", Direction: "inbound"},
					{ResourceType: "system", RelationType: "apm_service_instance_with_system", Category: "static", Direction: "outbound"},
				}},
				// 2跳: node -> pod -> system (动态 outbound)
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "node", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "pod", RelationType: "node_with_pod", Category: "static", Direction: "outbound"},
					{ResourceType: "system", RelationType: "pod_to_system", Category: "dynamic", Direction: "outbound"},
				}},
				// 2跳: node -> pod -> system (动态 inbound)
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "node", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "pod", RelationType: "node_with_pod", Category: "static", Direction: "outbound"},
					{ResourceType: "system", RelationType: "system_to_pod", Category: "dynamic", Direction: "inbound"},
				}},
				// 3跳: node -> datasource -> pod -> system (动态 outbound)
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "node", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "datasource", RelationType: "datasource_with_node", Category: "static", Direction: "inbound"},
					{ResourceType: "pod", RelationType: "datasource_with_pod", Category: "static", Direction: "outbound"},
					{ResourceType: "system", RelationType: "pod_to_system", Category: "dynamic", Direction: "outbound"},
				}},
				// 3跳: node -> datasource -> pod -> system (动态 inbound)
				{Steps: []cmdb.PathStepV2{
					{ResourceType: "node", RelationType: "", Category: "", Direction: ""},
					{ResourceType: "datasource", RelationType: "datasource_with_node", Category: "static", Direction: "inbound"},
					{ResourceType: "pod", RelationType: "datasource_with_pod", Category: "static", Direction: "outbound"},
					{ResourceType: "system", RelationType: "system_to_pod", Category: "dynamic", Direction: "inbound"},
				}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			opts := []PathFinderOption{
				WithMaxHops(tc.MaxHops),
			}
			if len(tc.AllowedCategory) > 0 {
				opts = append(opts, WithAllowedCategories(tc.AllowedCategory...))
			}
			if tc.DynamicDirection != "" {
				opts = append(opts, WithDynamicDirection(tc.DynamicDirection))
			}

			pf := NewPathFinder(opts...)
			paths, err := pf.FindAllPaths(tc.Source, tc.Target, tc.PathResource)

			if tc.ExpectError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tc.Expected, paths)
		})
	}
}
