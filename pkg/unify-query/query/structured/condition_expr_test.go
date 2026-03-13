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
	"github.com/stretchr/testify/require"
)

func TestConditionsToTableIDConditionExpr(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		expr, err := ConditionsToTableIDConditionExpr(nil)
		require.NoError(t, err)
		assert.Nil(t, expr)
	})
	t.Run("empty_field_list", func(t *testing.T) {
		expr, err := ConditionsToTableIDConditionExpr(&Conditions{})
		require.NoError(t, err)
		assert.Nil(t, expr)
	})
	t.Run("single_eq", func(t *testing.T) {
		c := &Conditions{
			FieldList:     []ConditionField{{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual}},
			ConditionList: []string{},
		}
		expr, err := ConditionsToTableIDConditionExpr(c)
		require.NoError(t, err)
		require.NotNil(t, expr)
		require.Len(t, expr.OrGroups, 1)
		require.Len(t, expr.OrGroups[0], 1)
		assert.Equal(t, "scene", expr.OrGroups[0][0].Key)
		assert.Equal(t, "log", expr.OrGroups[0][0].Value)
	})
	t.Run("and_or_roundtrip", func(t *testing.T) {
		// scene=log,cluster_id=1 or scene=k8s
		c := &Conditions{
			FieldList: []ConditionField{
				{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual},
				{DimensionName: "cluster_id", Value: []string{"1"}, Operator: ConditionEqual},
				{DimensionName: "scene", Value: []string{"k8s"}, Operator: ConditionEqual},
			},
			ConditionList: []string{"and", "or"},
		}
		expr, err := ConditionsToTableIDConditionExpr(c)
		require.NoError(t, err)
		require.NotNil(t, expr)
		require.Len(t, expr.OrGroups, 2)
		require.Len(t, expr.OrGroups[0], 2)
		require.Len(t, expr.OrGroups[1], 1)
		back := expr.ToConditions()
		require.Len(t, back.FieldList, 3)
		require.Equal(t, []string{"and", "or"}, back.ConditionList)
	})
}

func TestTableIDConditionExpr_ToConditions(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		var e *TableIDConditionExpr
		c := e.ToConditions()
		require.NotNil(t, c)
		assert.Empty(t, c.FieldList)
	})
	t.Run("empty_or_groups", func(t *testing.T) {
		e := &TableIDConditionExpr{OrGroups: [][]LabelCondition{}}
		c := e.ToConditions()
		require.NotNil(t, c)
		assert.Empty(t, c.FieldList)
	})
	t.Run("single_group", func(t *testing.T) {
		e := &TableIDConditionExpr{OrGroups: [][]LabelCondition{
			{{Key: "scene", Op: ConditionEqual, Value: "log"}},
		}}
		c := e.ToConditions()
		require.Len(t, c.FieldList, 1)
		assert.Equal(t, "scene", c.FieldList[0].DimensionName)
		assert.Equal(t, []string{"log"}, c.FieldList[0].Value)
	})
}

func TestMatchLabelsExpr(t *testing.T) {
	t.Run("nil_expr", func(t *testing.T) {
		assert.True(t, matchLabelsExpr(map[string]string{"a": "1"}, nil))
	})
	t.Run("empty_or_groups", func(t *testing.T) {
		assert.True(t, matchLabelsExpr(map[string]string{"a": "1"}, &TableIDConditionExpr{OrGroups: [][]LabelCondition{}}))
	})
	t.Run("match_single_eq", func(t *testing.T) {
		expr := &TableIDConditionExpr{OrGroups: [][]LabelCondition{
			{{Key: "scene", Op: ConditionEqual, Value: "log"}},
		}}
		assert.True(t, matchLabelsExpr(map[string]string{"scene": "log"}, expr))
		assert.False(t, matchLabelsExpr(map[string]string{"scene": "k8s"}, expr))
		assert.False(t, matchLabelsExpr(map[string]string{}, expr)) // key 不存在
	})
	t.Run("match_or_groups", func(t *testing.T) {
		expr := &TableIDConditionExpr{OrGroups: [][]LabelCondition{
			{{Key: "scene", Op: ConditionEqual, Value: "log"}},
			{{Key: "scene", Op: ConditionEqual, Value: "k8s"}},
		}}
		assert.True(t, matchLabelsExpr(map[string]string{"scene": "log"}, expr))
		assert.True(t, matchLabelsExpr(map[string]string{"scene": "k8s"}, expr))
		assert.False(t, matchLabelsExpr(map[string]string{"scene": "other"}, expr))
	})
	t.Run("match_and_in_group", func(t *testing.T) {
		expr := &TableIDConditionExpr{OrGroups: [][]LabelCondition{
			{
				{Key: "scene", Op: ConditionEqual, Value: "log"},
				{Key: "cluster_id", Op: ConditionEqual, Value: "1"},
			},
		}}
		assert.True(t, matchLabelsExpr(map[string]string{"scene": "log", "cluster_id": "1"}, expr))
		assert.False(t, matchLabelsExpr(map[string]string{"scene": "log", "cluster_id": "2"}, expr))
		assert.False(t, matchLabelsExpr(map[string]string{"scene": "log"}, expr)) // key 缺失
	})
}

func TestMapToTableIDConditionExpr(t *testing.T) {
	t.Run("nil_empty", func(t *testing.T) {
		assert.Nil(t, MapToTableIDConditionExpr(nil))
		assert.Nil(t, MapToTableIDConditionExpr(map[string]string{}))
	})
	t.Run("single", func(t *testing.T) {
		expr := MapToTableIDConditionExpr(map[string]string{"scene": "log"})
		require.NotNil(t, expr)
		require.Len(t, expr.OrGroups, 1)
		require.Len(t, expr.OrGroups[0], 1)
		assert.Equal(t, LabelCondition{Key: "scene", Op: ConditionEqual, Value: "log"}, expr.OrGroups[0][0])
	})
	t.Run("multiple", func(t *testing.T) {
		expr := MapToTableIDConditionExpr(map[string]string{"scene": "log", "cluster_id": "1"})
		require.NotNil(t, expr)
		require.Len(t, expr.OrGroups, 1)
		require.Len(t, expr.OrGroups[0], 2)
		// map 无序，只检查 key 存在
		keys := make(map[string]string)
		for _, lc := range expr.OrGroups[0] {
			keys[lc.Key] = lc.Value
		}
		assert.Equal(t, "log", keys["scene"])
		assert.Equal(t, "1", keys["cluster_id"])
	})
}
