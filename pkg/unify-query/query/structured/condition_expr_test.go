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

func TestAllConditionsToQueryLabelSelectorString(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		assert.Empty(t, AllConditionsToQueryLabelSelectorString(nil))
		assert.Empty(t, AllConditionsToQueryLabelSelectorString(AllConditions{}))
	})
	t.Run("single_group", func(t *testing.T) {
		all := AllConditions{
			{{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual}},
		}
		assert.Equal(t, "scene=log", AllConditionsToQueryLabelSelectorString(all))
	})
	t.Run("single_group_and", func(t *testing.T) {
		all := AllConditions{
			{
				{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual},
				{DimensionName: "cluster_id", Value: []string{"1"}, Operator: ConditionEqual},
			},
		}
		assert.Equal(t, "scene=log,cluster_id=1", AllConditionsToQueryLabelSelectorString(all))
	})
	t.Run("or_groups", func(t *testing.T) {
		all := AllConditions{
			{
				{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual},
				{DimensionName: "cluster_id", Value: []string{"1"}, Operator: ConditionEqual},
			},
			{{DimensionName: "scene", Value: []string{"k8s"}, Operator: ConditionEqual}},
		}
		assert.Equal(t, "scene=log,cluster_id=1 or scene=k8s", AllConditionsToQueryLabelSelectorString(all))
	})
}

func TestMatchLabelsForAllConditions(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		assert.True(t, matchLabelsForAllConditions(map[string]string{"a": "1"}, nil))
		assert.True(t, matchLabelsForAllConditions(map[string]string{"a": "1"}, AllConditions{}))
	})
	t.Run("match_single", func(t *testing.T) {
		all := AllConditions{{{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual}}}
		assert.True(t, matchLabelsForAllConditions(map[string]string{"scene": "log"}, all))
		assert.False(t, matchLabelsForAllConditions(map[string]string{"scene": "k8s"}, all))
	})
	t.Run("or_groups", func(t *testing.T) {
		all := AllConditions{
			{{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual}},
			{{DimensionName: "scene", Value: []string{"k8s"}, Operator: ConditionEqual}},
		}
		assert.True(t, matchLabelsForAllConditions(map[string]string{"scene": "log"}, all))
		assert.True(t, matchLabelsForAllConditions(map[string]string{"scene": "k8s"}, all))
		assert.False(t, matchLabelsForAllConditions(map[string]string{"scene": "other"}, all))
	})
}

func TestMapToTableIDConditions(t *testing.T) {
	t.Run("nil_empty", func(t *testing.T) {
		assert.Nil(t, MapToTableIDConditions(nil))
		assert.Nil(t, MapToTableIDConditions(map[string]string{}))
	})
	t.Run("single", func(t *testing.T) {
		all := MapToTableIDConditions(map[string]string{"scene": "log"})
		require.Len(t, all, 1)
		require.Len(t, all[0], 1)
		assert.Equal(t, "scene", all[0][0].DimensionName)
		assert.Equal(t, []string{"log"}, all[0][0].Value)
	})
}
