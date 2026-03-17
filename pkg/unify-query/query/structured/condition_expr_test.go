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

func TestAllConditionsQueryLabelSelectorString(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		assert.Empty(t, AllConditions(nil).QueryLabelSelectorString())
		assert.Empty(t, AllConditions{}.QueryLabelSelectorString())
	})
	t.Run("single_group", func(t *testing.T) {
		all := AllConditions{
			{{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual}},
		}
		assert.Equal(t, "scene=log", all.QueryLabelSelectorString())
	})
	t.Run("single_group_and", func(t *testing.T) {
		all := AllConditions{
			{
				{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual},
				{DimensionName: "cluster_id", Value: []string{"1"}, Operator: ConditionEqual},
			},
		}
		assert.Equal(t, "scene=log,cluster_id=1", all.QueryLabelSelectorString())
	})
	t.Run("or_groups", func(t *testing.T) {
		all := AllConditions{
			{
				{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual},
				{DimensionName: "cluster_id", Value: []string{"1"}, Operator: ConditionEqual},
			},
			{{DimensionName: "scene", Value: []string{"k8s"}, Operator: ConditionEqual}},
		}
		assert.Equal(t, "scene=log,cluster_id=1 or scene=k8s", all.QueryLabelSelectorString())
	})
	t.Run("single_ne", func(t *testing.T) {
		all := AllConditions{{{DimensionName: "scene", Value: []string{"metric"}, Operator: ConditionNotEqual}}}
		assert.Equal(t, "scene!=metric", all.QueryLabelSelectorString())
	})
	t.Run("single_req", func(t *testing.T) {
		all := AllConditions{{{DimensionName: "scene", Value: []string{"log.*"}, Operator: ConditionRegEqual}}}
		assert.Equal(t, `scene=~"log.*"`, all.QueryLabelSelectorString())
	})
	t.Run("single_nreq", func(t *testing.T) {
		all := AllConditions{{{DimensionName: "scene", Value: []string{"metric.*"}, Operator: ConditionNotRegEqual}}}
		assert.Equal(t, `scene!~"metric.*"`, all.QueryLabelSelectorString())
	})
	t.Run("req_value_with_quote_escaped", func(t *testing.T) {
		all := AllConditions{{{DimensionName: "key", Value: []string{`say "hi"`}, Operator: ConditionRegEqual}}}
		assert.Equal(t, `key=~"say \"hi\""`, all.QueryLabelSelectorString())
	})
	t.Run("mixed_ops_in_group", func(t *testing.T) {
		all := AllConditions{{
			{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual},
			{DimensionName: "env", Value: []string{"prod"}, Operator: ConditionNotEqual},
		}}
		assert.Equal(t, "scene=log,env!=prod", all.QueryLabelSelectorString())
	})
	t.Run("three_or_groups", func(t *testing.T) {
		all := AllConditions{
			{{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual}},
			{{DimensionName: "scene", Value: []string{"k8s"}, Operator: ConditionEqual}},
			{{DimensionName: "scene", Value: []string{"metric"}, Operator: ConditionEqual}},
		}
		assert.Equal(t, "scene=log or scene=k8s or scene=metric", all.QueryLabelSelectorString())
	})
	t.Run("empty_group_skipped", func(t *testing.T) {
		all := AllConditions{
			{{DimensionName: "a", Value: []string{"1"}, Operator: ConditionEqual}},
			{}, // 空组被跳过，不产出 or 片段
			{{DimensionName: "b", Value: []string{"2"}, Operator: ConditionEqual}},
		}
		assert.Equal(t, "a=1 or b=2", all.QueryLabelSelectorString())
	})
	t.Run("group_with_req_and_eq", func(t *testing.T) {
		all := AllConditions{{
			{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual},
			{DimensionName: "cluster_id", Value: []string{"BCS-.*"}, Operator: ConditionRegEqual},
		}}
		assert.Equal(t, `scene=log,cluster_id=~"BCS-.*"`, all.QueryLabelSelectorString())
	})
	t.Run("empty_value", func(t *testing.T) {
		all := AllConditions{{{DimensionName: "key", Value: []string{}, Operator: ConditionEqual}}}
		assert.Equal(t, "key=", all.QueryLabelSelectorString())
	})
	t.Run("nil_value_slice", func(t *testing.T) {
		all := AllConditions{{{DimensionName: "key", Value: nil, Operator: ConditionEqual}}}
		assert.Equal(t, "key=", all.QueryLabelSelectorString())
	})
}

func TestAllConditionsMatchesLabels(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		assert.True(t, AllConditions(nil).MatchesLabels(map[string]string{"a": "1"}))
		assert.True(t, AllConditions{}.MatchesLabels(map[string]string{"a": "1"}))
	})
	t.Run("match_single", func(t *testing.T) {
		all := AllConditions{{{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual}}}
		assert.True(t, all.MatchesLabels(map[string]string{"scene": "log"}))
		assert.False(t, all.MatchesLabels(map[string]string{"scene": "k8s"}))
	})
	t.Run("or_groups", func(t *testing.T) {
		all := AllConditions{
			{{DimensionName: "scene", Value: []string{"log"}, Operator: ConditionEqual}},
			{{DimensionName: "scene", Value: []string{"k8s"}, Operator: ConditionEqual}},
		}
		assert.True(t, all.MatchesLabels(map[string]string{"scene": "log"}))
		assert.True(t, all.MatchesLabels(map[string]string{"scene": "k8s"}))
		assert.False(t, all.MatchesLabels(map[string]string{"scene": "other"}))
	})
}
