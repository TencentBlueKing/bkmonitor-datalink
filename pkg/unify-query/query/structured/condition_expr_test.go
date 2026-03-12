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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatchLabelsExpr(t *testing.T) {
	labels := map[string]string{"scene": "log", "cluster_id": "BCS-K8S-00001", "env": "prod"}

	testCases := map[string]struct {
		expr     *TableIDConditionExpr
		expected bool
	}{
		"eq_match": {
			expr:     &TableIDConditionExpr{OrGroups: [][]LabelCondition{{{Key: "scene", Op: LabelOpEq, Value: "log"}}}},
			expected: true,
		},
		"eq_mismatch": {
			expr:     &TableIDConditionExpr{OrGroups: [][]LabelCondition{{{Key: "scene", Op: LabelOpEq, Value: "k8s"}}}},
			expected: false,
		},
		"neq_match": {
			expr:     &TableIDConditionExpr{OrGroups: [][]LabelCondition{{{Key: "scene", Op: LabelOpNeq, Value: "k8s"}}}},
			expected: true,
		},
		"neq_mismatch": {
			expr:     &TableIDConditionExpr{OrGroups: [][]LabelCondition{{{Key: "scene", Op: LabelOpNeq, Value: "log"}}}},
			expected: false,
		},
		"reg_match": {
			expr:     &TableIDConditionExpr{OrGroups: [][]LabelCondition{{{Key: "scene", Op: LabelOpReg, Value: "lo."}}}},
			expected: true,
		},
		"reg_mismatch": {
			expr:     &TableIDConditionExpr{OrGroups: [][]LabelCondition{{{Key: "scene", Op: LabelOpReg, Value: "^k8s"}}}},
			expected: false,
		},
		"nreg_match": {
			expr:     &TableIDConditionExpr{OrGroups: [][]LabelCondition{{{Key: "scene", Op: LabelOpNreg, Value: "^k8s"}}}},
			expected: true,
		},
		"nreg_mismatch": {
			expr:     &TableIDConditionExpr{OrGroups: [][]LabelCondition{{{Key: "scene", Op: LabelOpNreg, Value: "log"}}}},
			expected: false,
		},
		"multi_conditions_match": {
			expr: &TableIDConditionExpr{OrGroups: [][]LabelCondition{{
				{Key: "scene", Op: LabelOpEq, Value: "log"},
				{Key: "cluster_id", Op: LabelOpEq, Value: "BCS-K8S-00001"},
			}}},
			expected: true,
		},
		"multi_conditions_one_fail": {
			expr: &TableIDConditionExpr{OrGroups: [][]LabelCondition{{
				{Key: "scene", Op: LabelOpEq, Value: "log"},
				{Key: "cluster_id", Op: LabelOpEq, Value: "other"},
			}}},
			expected: false,
		},
		"or_any_group_match": {
			expr: &TableIDConditionExpr{OrGroups: [][]LabelCondition{
				{{Key: "scene", Op: LabelOpEq, Value: "k8s"}},
				{{Key: "scene", Op: LabelOpEq, Value: "log"}},
			}},
			expected: true,
		},
		"or_no_group_match": {
			expr: &TableIDConditionExpr{OrGroups: [][]LabelCondition{
				{{Key: "scene", Op: LabelOpEq, Value: "k8s"}},
				{{Key: "scene", Op: LabelOpEq, Value: "metric"}},
			}},
			expected: false,
		},
		"nil_expr": {
			expr:     nil,
			expected: false,
		},
		"empty_expr": {
			expr:     &TableIDConditionExpr{OrGroups: nil},
			expected: false,
		},
		"empty_labels": {
			expr:     &TableIDConditionExpr{OrGroups: [][]LabelCondition{{{Key: "scene", Op: LabelOpEq, Value: "log"}}}},
			expected: false,
		},
		// 表标签过滤：key 不存在则视为不满足，排除该表
		"neq_key_missing_nonempty_value": {
			expr:     &TableIDConditionExpr{OrGroups: [][]LabelCondition{{{Key: "not_exist", Op: LabelOpNeq, Value: "log"}}}},
			expected: false,
		},
		"neq_key_missing_empty_value": {
			expr:     &TableIDConditionExpr{OrGroups: [][]LabelCondition{{{Key: "not_exist", Op: LabelOpNeq, Value: ""}}}},
			expected: false,
		},
		"nreg_key_missing": {
			expr:     &TableIDConditionExpr{OrGroups: [][]LabelCondition{{{Key: "not_exist", Op: LabelOpNreg, Value: ".*"}}}},
			expected: false,
		},
		// 非法 op → false
		"invalid_op": {
			expr:     &TableIDConditionExpr{OrGroups: [][]LabelCondition{{{Key: "scene", Op: "unknown_op", Value: "log"}}}},
			expected: false,
		},
		// reg 正则编译失败 → false
		"reg_invalid_regex": {
			expr:     &TableIDConditionExpr{OrGroups: [][]LabelCondition{{{Key: "scene", Op: LabelOpReg, Value: "[invalid"}}}},
			expected: false,
		},
		// nreg 正则编译失败 → true
		"nreg_invalid_regex": {
			expr:     &TableIDConditionExpr{OrGroups: [][]LabelCondition{{{Key: "scene", Op: LabelOpNreg, Value: "[invalid"}}}},
			expected: true,
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			labelsToUse := labels
			if name == "empty_labels" {
				labelsToUse = map[string]string{}
			}
			result := matchLabelsExpr(labelsToUse, c.expr)
			assert.Equal(t, c.expected, result)
		})
	}
}

// TestTableIDConditionExpr_Empty 单独测试 Empty 方法
func TestTableIDConditionExpr_Empty(t *testing.T) {
	testCases := map[string]struct {
		expr     *TableIDConditionExpr
		expected bool
	}{
		"nil_expr": {
			expr:     nil,
			expected: true,
		},
		"empty_or_groups": {
			expr:     &TableIDConditionExpr{OrGroups: nil},
			expected: true,
		},
		"empty_slice": {
			expr:     &TableIDConditionExpr{OrGroups: [][]LabelCondition{}},
			expected: true,
		},
		"non_empty": {
			expr:     &TableIDConditionExpr{OrGroups: [][]LabelCondition{{{Key: "scene", Op: LabelOpEq, Value: "log"}}}},
			expected: false,
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, c.expected, c.expr.Empty())
		})
	}
}

func TestConditionsToTableIDConditionExpr(t *testing.T) {
	labels := map[string]string{"scene": "log", "cluster_id": "BCS-K8S-00001"}

	testCases := map[string]struct {
		conditions Conditions
		expectNil  bool
		match      map[string]string // 若非空，对 expr 用 matchLabelsExpr(labels, expr) 应为 true
		noMatch    map[string]string // 若非空，对 expr 用 matchLabelsExpr(labels, expr) 应为 false
	}{
		"empty": {
			conditions: Conditions{},
			expectNil:  true,
		},
		"eq_ne_req_nreq": {
			conditions: Conditions{
				FieldList: []ConditionField{
					{DimensionName: "scene", Operator: ConditionEqual, Value: []string{"log"}},
					{DimensionName: "cluster_id", Operator: ConditionNotEqual, Value: []string{"other"}},
					{DimensionName: "scene", Operator: ConditionRegEqual, Value: []string{"lo."}},
					{DimensionName: "cluster_id", Operator: ConditionNotRegEqual, Value: []string{"^x"}},
				},
				ConditionList: []string{"and", "and", "and"},
			},
			expectNil: false,
			match:     labels,
		},
		"contains_single_value_as_eq": {
			conditions: Conditions{
				FieldList:     []ConditionField{{DimensionName: "scene", Operator: ConditionContains, Value: []string{"log"}}},
				ConditionList: nil,
			},
			expectNil: false,
			match:     labels,
		},
		"ncontains_single_value_as_neq": {
			conditions: Conditions{
				FieldList:     []ConditionField{{DimensionName: "scene", Operator: ConditionNotContains, Value: []string{"k8s"}}},
				ConditionList: nil,
			},
			expectNil: false,
			match:     labels,
		},
		"one_fail_no_match": {
			conditions: Conditions{
				FieldList: []ConditionField{
					{DimensionName: "scene", Operator: ConditionEqual, Value: []string{"log"}},
					{DimensionName: "cluster_id", Operator: ConditionEqual, Value: []string{"other"}},
				},
				ConditionList: []string{"and"},
			},
			expectNil: false,
			noMatch:   labels,
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			expr := ConditionsToTableIDConditionExpr(c.conditions)
			if c.expectNil {
				assert.Nil(t, expr)
				return
			}
			assert.NotNil(t, expr)
			assert.False(t, expr.Empty())
			if len(c.match) > 0 {
				assert.True(t, matchLabelsExpr(c.match, expr), "expected match")
			}
			if len(c.noMatch) > 0 {
				assert.False(t, matchLabelsExpr(c.noMatch, expr), "expected no match")
			}
		})
	}
}

func TestTableIDConditionsValue_UnmarshalAndExpr(t *testing.T) {
	labels := map[string]string{"scene": "log", "cluster_id": "BCS-K8S-00001"}

	t.Run("map_form", func(t *testing.T) {
		var v TableIDConditionsValue
		err := json.Unmarshal([]byte(`{"scene":"log","cluster_id":"BCS-K8S-00001"}`), &v)
		assert.NoError(t, err)
		assert.False(t, v.Empty())
		expr := v.ToTableIDConditionExpr()
		assert.NotNil(t, expr)
		assert.True(t, matchLabelsExpr(labels, expr))
	})

	t.Run("conditions_form", func(t *testing.T) {
		var v TableIDConditionsValue
		err := json.Unmarshal([]byte(`{"field_list":[{"field_name":"scene","value":["log"],"op":"eq"},{"field_name":"cluster_id","value":["BCS-.*"],"op":"req"}],"condition_list":["and"]}`), &v)
		assert.NoError(t, err)
		assert.False(t, v.Empty())
		expr := v.ToTableIDConditionExpr()
		assert.NotNil(t, expr)
		assert.True(t, matchLabelsExpr(labels, expr))
	})

	t.Run("empty_object", func(t *testing.T) {
		var v TableIDConditionsValue
		err := json.Unmarshal([]byte(`{}`), &v)
		assert.NoError(t, err)
		assert.True(t, v.Empty())
		assert.Nil(t, v.ToTableIDConditionExpr())
	})
}
