// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
)

func TestContainElement(t *testing.T) {
	testCases := map[string]struct {
		slice   []string
		element string
		result  bool
	}{
		"elementContain": {
			slice:   []string{"A", "B", "C"},
			element: "A",
			result:  true,
		},
		"elementNotContain": {
			slice:   []string{"A", "B", "C"},
			element: "D",
			result:  false,
		},
		"elementEmpty": {
			slice:   []string{"A", "B", "C"},
			element: "",
			result:  false,
		},
		"sliceEmpty": {
			slice:   nil,
			element: "",
			result:  false,
		},
	}
	for k, v := range testCases {
		t.Run(k, func(t *testing.T) {
			result := containElement(v.slice, v.element)
			assert.Equal(t, v.result, result)
		})
	}
}

func TestJudgeFilter(t *testing.T) {

	testCases := map[string]struct {
		filters []query.Filter
		satisfy bool
		length  int
	}{
		"filter is nil": {
			filters: nil,
			satisfy: false,
			length:  0,
		},
		"filter is satisfy": {
			filters: []query.Filter{
				map[string]string{"bcs_cluster_id": "bcs_cluster_id1", "nameSpace": "nameSpace1"},
				map[string]string{"bcs_cluster_id": "bcs_cluster_id2", "nameSpace": "nameSpace2"},
			},
			satisfy: true,
			length:  2,
		},
		"filter is not satisfy": {
			filters: []query.Filter{
				map[string]string{"bcs_cluster_id": "bcs_cluster_id1", "nameSpace": "nameSpace1"},
				map[string]string{"project_id": "project_id1", "nameSpace": "nameSpace1"},
			},
			satisfy: false,
			length:  0,
		},
	}

	for k, v := range testCases {
		t.Run(k, func(t *testing.T) {
			satisfy, tKeys := judgeFilter(v.filters)
			assert.Equal(t, v.satisfy, satisfy)
			assert.Equal(t, v.length, len(tKeys))
		})
	}

}

func TestCompressFilterCondition(t *testing.T) {
	t.Run("test with multiple nameSpace value", func(t *testing.T) {
		tKeys := []string{"bcs_cluster_id", "nameSpace"}
		filters := []query.Filter{
			map[string]string{"bcs_cluster_id": "bcs_cluster_id1", "nameSpace": "nameSpace1"},
			map[string]string{"bcs_cluster_id": "bcs_cluster_id1", "nameSpace": "nameSpace2"},
		}
		condition := compressFilterCondition(tKeys, filters)
		assert.Equal(t, 1, len(condition))
		assert.Equal(t, "[][]structured.ConditionField", reflect.TypeOf(condition).String())
		for _, cond := range condition[0] {
			if cond.DimensionName == "bcs_cluster_id" {
				assert.Equal(t, 1, len(cond.Value))
				assert.Equal(t, []string{"bcs_cluster_id1"}, cond.Value)
			}
			if cond.DimensionName == "nameSpace" {
				assert.Equal(t, 2, len(cond.Value))
				assert.Equal(t, []string{"nameSpace1", "nameSpace2"}, cond.Value)
			}
		}
	})
	t.Run("test with multiple bcs_cluster_id value", func(t *testing.T) {
		tKeys := []string{"bcs_cluster_id", "nameSpace"}
		filters := []query.Filter{
			map[string]string{"bcs_cluster_id": "bcs_cluster_id1", "nameSpace": "nameSpace1"},
			map[string]string{"bcs_cluster_id": "bcs_cluster_id2", "nameSpace": "nameSpace1"},
		}
		condition := compressFilterCondition(tKeys, filters)
		assert.Equal(t, 1, len(condition))
		assert.Equal(t, "[][]structured.ConditionField", reflect.TypeOf(condition).String())
		for _, cond := range condition[0] {
			if cond.DimensionName == "bcs_cluster_id" {
				assert.Equal(t, 2, len(cond.Value))
				assert.Equal(t, []string{"bcs_cluster_id1", "bcs_cluster_id2"}, cond.Value)
			}
			if cond.DimensionName == "nameSpace" {
				assert.Equal(t, 1, len(cond.Value))
				assert.Equal(t, []string{"nameSpace1"}, cond.Value)
			}
		}
	})
	t.Run("test with multiple bcs_cluster_id and nameSpace value", func(t *testing.T) {
		tKeys := []string{"bcs_cluster_id", "nameSpace"}
		filters := []query.Filter{
			map[string]string{"bcs_cluster_id": "bcs_cluster_id1", "nameSpace": "nameSpace1"},
			map[string]string{"bcs_cluster_id": "bcs_cluster_id2", "nameSpace": "nameSpace2"},
			map[string]string{"bcs_cluster_id": "bcs_cluster_id3", "nameSpace": "nameSpace2"},
		}
		condition := compressFilterCondition(tKeys, filters)
		assert.Equal(t, 2, len(condition))
		assert.Equal(t, "[][]structured.ConditionField", reflect.TypeOf(condition).String())
	})
}

func TestCompareClusterId(t *testing.T) {
	testCases := []struct {
		name         string
		bcsClusterId string
		conditions   Conditions
		expect       bool
	}{
		{
			name:         "matchType: normal, with eq condition. result: matched",
			bcsClusterId: "bcs_cluster_id1",
			conditions: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: ClusterID,
						Value:         []string{"bcs_cluster_id1"},
						Operator:      ConditionEqual,
					},
				},
			},
			expect: true,
		},
		{
			name:         "matchType: normal, with contain condition. result: matched",
			bcsClusterId: "bcs_cluster_id1",
			conditions: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: ClusterID,
						Value:         []string{"bcs_cluster_id1"},
						Operator:      ConditionContains,
					},
				},
			},
			expect: true,
		},
		{
			name:         "matchType: normal, with eq condition. result: unmatched",
			bcsClusterId: "bcs_cluster_id2",
			conditions: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: ClusterID,
						Value:         []string{"bcs_cluster_id1"},
						Operator:      ConditionEqual,
					},
				},
			},
			expect: false,
		},
		{
			name:         "matchType: normal, with contain condition. result: unmatched",
			bcsClusterId: "bcs_cluster_id2",
			conditions: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: ClusterID,
						Value:         []string{"bcs_cluster_id1"},
						Operator:      ConditionContains,
					},
				},
			},
			expect: false,
		},
		{
			name:         "matchType: regex, with req condition. result: matched",
			bcsClusterId: "bcs_cluster_id",
			conditions: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: ClusterID,
						Value:         []string{"bcs.*"},
						Operator:      ConditionRegEqual,
					},
				},
			},
			expect: true,
		},
		{
			name:         "matchType: regex, with req condition. result: unmatched",
			bcsClusterId: "bcs_cluster_id",
			conditions: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: ClusterID,
						Value:         []string{"kk.*"},
						Operator:      ConditionRegEqual,
					},
				},
			},
			expect: false,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.expect, compareClusterId(testCase.conditions, testCase.bcsClusterId))
		})
	}
}
