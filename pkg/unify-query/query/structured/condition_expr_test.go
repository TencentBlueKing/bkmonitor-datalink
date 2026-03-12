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
			expr:     &TableIDConditionExpr{Conditions: []LabelCondition{{Key: "scene", Op: LabelOpEq, Value: "log"}}},
			expected: true,
		},
		"eq_mismatch": {
			expr:     &TableIDConditionExpr{Conditions: []LabelCondition{{Key: "scene", Op: LabelOpEq, Value: "k8s"}}},
			expected: false,
		},
		"neq_match": {
			expr:     &TableIDConditionExpr{Conditions: []LabelCondition{{Key: "scene", Op: LabelOpNeq, Value: "k8s"}}},
			expected: true,
		},
		"neq_mismatch": {
			expr:     &TableIDConditionExpr{Conditions: []LabelCondition{{Key: "scene", Op: LabelOpNeq, Value: "log"}}},
			expected: false,
		},
		"reg_match": {
			expr:     &TableIDConditionExpr{Conditions: []LabelCondition{{Key: "scene", Op: LabelOpReg, Value: "lo."}}},
			expected: true,
		},
		"reg_mismatch": {
			expr:     &TableIDConditionExpr{Conditions: []LabelCondition{{Key: "scene", Op: LabelOpReg, Value: "^k8s"}}},
			expected: false,
		},
		"nreg_match": {
			expr:     &TableIDConditionExpr{Conditions: []LabelCondition{{Key: "scene", Op: LabelOpNreg, Value: "^k8s"}}},
			expected: true,
		},
		"nreg_mismatch": {
			expr:     &TableIDConditionExpr{Conditions: []LabelCondition{{Key: "scene", Op: LabelOpNreg, Value: "log"}}},
			expected: false,
		},
		"multi_conditions_match": {
			expr: &TableIDConditionExpr{Conditions: []LabelCondition{
				{Key: "scene", Op: LabelOpEq, Value: "log"},
				{Key: "cluster_id", Op: LabelOpEq, Value: "BCS-K8S-00001"},
			}},
			expected: true,
		},
		"multi_conditions_one_fail": {
			expr: &TableIDConditionExpr{Conditions: []LabelCondition{
				{Key: "scene", Op: LabelOpEq, Value: "log"},
				{Key: "cluster_id", Op: LabelOpEq, Value: "other"},
			}},
			expected: false,
		},
		"nil_expr": {
			expr:     nil,
			expected: false,
		},
		"empty_expr": {
			expr:     &TableIDConditionExpr{Conditions: nil},
			expected: false,
		},
		"empty_labels": {
			expr:     &TableIDConditionExpr{Conditions: []LabelCondition{{Key: "scene", Op: LabelOpEq, Value: "log"}}},
			expected: false,
		},
		// neq 时 key 不存在，value 非空 → true（不存在的 key != "log" 为 true）
		"neq_key_missing_nonempty_value": {
			expr:     &TableIDConditionExpr{Conditions: []LabelCondition{{Key: "not_exist", Op: LabelOpNeq, Value: "log"}}},
			expected: true,
		},
		// neq 时 key 不存在，value 为空 → false（不存在的 key != "" 为 false）
		"neq_key_missing_empty_value": {
			expr:     &TableIDConditionExpr{Conditions: []LabelCondition{{Key: "not_exist", Op: LabelOpNeq, Value: ""}}},
			expected: false,
		},
		// nreg 时 key 不存在 → true（实现上视为匹配）
		"nreg_key_missing": {
			expr:     &TableIDConditionExpr{Conditions: []LabelCondition{{Key: "not_exist", Op: LabelOpNreg, Value: ".*"}}},
			expected: true,
		},
		// 非法 op → false
		"invalid_op": {
			expr:     &TableIDConditionExpr{Conditions: []LabelCondition{{Key: "scene", Op: "unknown_op", Value: "log"}}},
			expected: false,
		},
		// reg 正则编译失败 → false
		"reg_invalid_regex": {
			expr:     &TableIDConditionExpr{Conditions: []LabelCondition{{Key: "scene", Op: LabelOpReg, Value: "[invalid"}}},
			expected: false,
		},
		// nreg 正则编译失败 → true
		"nreg_invalid_regex": {
			expr:     &TableIDConditionExpr{Conditions: []LabelCondition{{Key: "scene", Op: LabelOpNreg, Value: "[invalid"}}},
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
		"empty_conditions": {
			expr:     &TableIDConditionExpr{Conditions: nil},
			expected: true,
		},
		"empty_slice": {
			expr:     &TableIDConditionExpr{Conditions: []LabelCondition{}},
			expected: true,
		},
		"non_empty": {
			expr:     &TableIDConditionExpr{Conditions: []LabelCondition{{Key: "scene", Op: LabelOpEq, Value: "log"}}},
			expected: false,
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, c.expected, c.expr.Empty())
		})
	}
}
