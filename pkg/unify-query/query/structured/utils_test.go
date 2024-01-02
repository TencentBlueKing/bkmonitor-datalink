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
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
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
	testCases := []struct {
		name    string
		tKeys   []string
		filters []query.Filter
		expect  [][]ConditionField
	}{
		{
			name:  "test with multiple nameSpace value",
			tKeys: []string{"bcs_cluster_id", "nameSpace"},
			filters: []query.Filter{
				map[string]string{"bcs_cluster_id": "bcs_cluster_id1", "nameSpace": "nameSpace1"},
				map[string]string{"bcs_cluster_id": "bcs_cluster_id1", "nameSpace": "nameSpace2"},
			},
			expect: [][]ConditionField{
				{
					ConditionField{
						DimensionName: "bcs_cluster_id",
						Value:         []string{"bcs_cluster_id1"},
						Operator:      Contains,
					},
					ConditionField{
						DimensionName: "nameSpace",
						Value:         []string{"nameSpace1", "nameSpace2"},
						Operator:      Contains,
					},
				},
			},
		},
		{
			name:  "test with multiple bcs_cluster_id value",
			tKeys: []string{"bcs_cluster_id", "nameSpace"},
			filters: []query.Filter{
				map[string]string{"bcs_cluster_id": "bcs_cluster_id1", "nameSpace": "nameSpace1"},
				map[string]string{"bcs_cluster_id": "bcs_cluster_id2", "nameSpace": "nameSpace1"},
			},
			expect: [][]ConditionField{
				{
					ConditionField{
						DimensionName: "nameSpace",
						Value:         []string{"nameSpace1"},
						Operator:      Contains,
					},
					ConditionField{
						DimensionName: "bcs_cluster_id",
						Value:         []string{"bcs_cluster_id1", "bcs_cluster_id2"},
						Operator:      Contains,
					},
				},
			},
		},
		{
			name:  "test with multiple bcs_cluster_id value",
			tKeys: []string{"bcs_cluster_id", "nameSpace"},
			filters: []query.Filter{
				map[string]string{"bcs_cluster_id": "bcs_cluster_id1", "nameSpace": "nameSpace1"},
				map[string]string{"bcs_cluster_id": "bcs_cluster_id2", "nameSpace": "nameSpace2"},
				map[string]string{"bcs_cluster_id": "bcs_cluster_id3", "nameSpace": "nameSpace2"},
			},
			expect: [][]ConditionField{
				{
					ConditionField{
						DimensionName: "nameSpace",
						Value:         []string{"nameSpace1"},
						Operator:      Contains,
					},
					ConditionField{
						DimensionName: "bcs_cluster_id",
						Value:         []string{"bcs_cluster_id1"},
						Operator:      Contains,
					},
				},
				{
					ConditionField{
						DimensionName: "nameSpace",
						Value:         []string{"nameSpace2"},
						Operator:      Contains,
					},
					ConditionField{
						DimensionName: "bcs_cluster_id",
						Value:         []string{"bcs_cluster_id2", "bcs_cluster_id3"},
						Operator:      Contains,
					},
				},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			condition := compressFilterCondition(testCase.tKeys, testCase.filters)
			assert.Equal(t, testCase.expect, condition)
		})
	}
}

func TestCompareClusterId(t *testing.T) {
	mock.Init()
	testCases := []struct {
		name         string
		bcsClusterId string
		conditions   Conditions
		expect       bool
	}{
		{
			name:         "matchType: normal, with eq condition.",
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
			name:         "matchType: normal, with ne condition.",
			bcsClusterId: "bcs_cluster_id",
			conditions: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: ClusterID,
						Value:         []string{"bcs_cluster_id1"},
						Operator:      ConditionNotEqual,
					},
				},
			},
			expect: true,
		},
		{
			name:         "matchType: normal, with contain condition.",
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
			name:         "matchType: normal, with ncontains condition.",
			bcsClusterId: "bcs_cluster_id1",
			conditions: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: ClusterID,
						Value:         []string{"bcs_cluster_id1"},
						Operator:      ConditionNotContains,
					},
				},
			},
			expect: false,
		},
		{
			name:         "matchType: normal, with re condition. result: unmatched",
			bcsClusterId: "bcs_cluster_id2",
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
			name:         "matchType: regex, with nreq condition. result: unmatched",
			bcsClusterId: "bcs_cluster_id",
			conditions: Conditions{
				FieldList: []ConditionField{
					{
						DimensionName: ClusterID,
						Value:         []string{"kk.*"},
						Operator:      ConditionNotRegEqual,
					},
				},
			},
			expect: true,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			allConditions, err := testCase.conditions.AnalysisConditions()
			assert.Nil(t, err)
			compareResult, err := compareClusterId(allConditions, testCase.bcsClusterId)
			assert.Nil(t, err)
			assert.Equal(t, testCase.expect, compareResult)
		})
	}
}

func Test_reMatchElement(t *testing.T) {
	testCases := []struct {
		name    string
		expr    string
		val     string
		isMatch bool
		expect  bool
		errMsg  string
	}{
		{
			name:    "regex Match with result true",
			expr:    "bcs.*",
			val:     "bcs_cluster_id1",
			isMatch: true,
			expect:  true,
		},
		{
			name:    "regex Match with result false",
			expr:    "bs.*",
			val:     "bcs_cluster_id1",
			isMatch: true,
			expect:  false,
		},
		{
			name:    "regex not Match with result false",
			expr:    "bcs.*",
			val:     "bcs_cluster_id1",
			isMatch: false,
			expect:  false,
		},
		{
			name:    "regex not Match with result true",
			expr:    "bcs.*",
			val:     "bks_cluster_id1",
			isMatch: false,
			expect:  true,
		},
		{
			name:    "regex empty with result false",
			expr:    "",
			val:     "bks_cluster_id1",
			isMatch: false,
			expect:  false,
			errMsg:  fmt.Sprintf("expr: %s, val: %s shouldn't be empty", "", "bks_cluster_id1"),
		},
		{
			name:    "val empty with result false",
			expr:    "bcs.*",
			val:     "",
			isMatch: false,
			expect:  false,
			errMsg:  fmt.Sprintf("expr: %s, val: %s shouldn't be empty", "bcs.*", ""),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res, err := reMatchElement(testCase.expr, testCase.val, testCase.isMatch)
			if err != nil {
				assert.Equal(t, testCase.errMsg, err.Error())
			}
			assert.Equal(t, testCase.expect, res)
		})
	}
}
