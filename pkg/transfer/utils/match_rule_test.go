// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRuleMatch(t *testing.T) {
	tests := []struct {
		name     string
		rules    []*MatchRule
		data     map[string]interface{}
		expected bool
	}{
		{
			name: "eq match",
			rules: []*MatchRule{
				{
					Key:       "dimensions.k1",
					Value:     []string{"v1"},
					Method:    RuleMethodEq,
					Condition: "or",
				},
			},
			data: map[string]interface{}{
				"dimensions": map[string]interface{}{"k1": "v1"},
			},
			expected: true,
		},
		{
			name: "eq not match",
			rules: []*MatchRule{
				{
					Key:       "dimensions.k1",
					Value:     []string{"v1"},
					Method:    "eq",
					Condition: "or",
				},
			},
			data: map[string]interface{}{
				"dimensions": map[string]interface{}{"k1": "v2"},
			},
			expected: false,
		},
		{
			name: "neq match",
			rules: []*MatchRule{
				{
					Key:       "dimensions.k1",
					Value:     []string{"v1"},
					Method:    "neq",
					Condition: "or",
				},
			},
			data: map[string]interface{}{
				"dimensions": map[string]interface{}{"k1": "v2"},
			},
			expected: true,
		},
		{
			name: "regex match",
			rules: []*MatchRule{
				{
					Key:       "dimensions.k1",
					Value:     []string{"^v\\d+"},
					Method:    "reg",
					Condition: "or",
				},
			},
			data: map[string]interface{}{
				"dimensions": map[string]interface{}{"k1": "v123"},
			},
			expected: true,
		},
		{
			name: "nreg match",
			rules: []*MatchRule{
				{
					Key:       "dimensions.k1",
					Value:     []string{"^v\\d+"},
					Method:    "nreg",
					Condition: "or",
				},
			},
			data: map[string]interface{}{
				"dimensions": map[string]interface{}{"k1": "abc"},
			},
			expected: true,
		},
		{
			name: "include match",
			rules: []*MatchRule{
				{
					Key:       "dimensions.k1",
					Value:     []string{"test"},
					Method:    "include",
					Condition: "or",
				},
			},
			data: map[string]interface{}{
				"dimensions": map[string]interface{}{"k1": "this is test"},
			},
			expected: true,
		},
		{
			name: "exclude match",
			rules: []*MatchRule{
				{
					Key:       "dimensions.k1",
					Value:     []string{"test"},
					Method:    "exclude",
					Condition: "or",
				},
			},
			data: map[string]interface{}{
				"dimensions": map[string]interface{}{"k1": "no data"},
			},
			expected: true,
		},
		{
			name: "or 和 and 条件全部满足",
			rules: []*MatchRule{
				{
					Key:       "dimensions.k1",
					Value:     []string{"v1"},
					Method:    "eq",
					Condition: "or",
				},
				{
					Key:       "dimensions.k2",
					Value:     []string{"v2"},
					Method:    "eq",
					Condition: "and",
				},
			},
			data: map[string]interface{}{
				"dimensions": map[string]interface{}{"k1": "v1", "k2": "v2"},
			},
			expected: true,
		},
		{
			name: "or 和 and 条件部分满足",
			rules: []*MatchRule{
				{
					Key:       "dimensions.k1",
					Value:     []string{"v1"},
					Method:    "eq",
					Condition: "or",
				},
				{
					Key:       "dimensions.k2",
					Value:     []string{"v2"},
					Method:    "eq",
					Condition: "and",
				},
			},
			data: map[string]interface{}{
				"dimensions": map[string]interface{}{"k1": "v1", "k2": "v3"},
			},
			expected: false,
		},
		{
			name: "多个 and 条件全部满足",
			rules: []*MatchRule{
				{
					Key:       "dimensions.k1",
					Value:     []string{"v1"},
					Method:    "eq",
					Condition: "and",
				},
				{
					Key:       "dimensions.k2",
					Value:     []string{"v2"},
					Method:    "eq",
					Condition: "and",
				},
			},
			data: map[string]interface{}{
				"dimensions": map[string]interface{}{"k1": "v1", "k2": "v2"},
			},
			expected: true,
		},
		{
			name: "多个 and 条件部分满足",
			rules: []*MatchRule{
				{
					Key:       "dimensions.k1",
					Value:     []string{"v1"},
					Method:    "eq",
					Condition: "and",
				},
				{
					Key:       "dimensions.k2",
					Value:     []string{"v2"},
					Method:    "eq",
					Condition: "and",
				},
			},
			data: map[string]interface{}{
				"dimensions": map[string]interface{}{"k1": "v1", "k2": "v3"},
			},
			expected: false,
		},
		{
			name: "复杂条件组合",
			rules: []*MatchRule{
				{
					Key:       "dimensions.k1",
					Value:     []string{"v1", "v2"},
					Method:    "include",
					Condition: "or",
				},
				{
					Key:       "dimensions.k2",
					Value:     []string{"v3", "v4"},
					Method:    "include",
					Condition: "and",
				},
			},
			data: map[string]interface{}{
				"dimensions": map[string]interface{}{"k1": "v1", "k2": "v3 v4"},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i := 0; i < len(tt.rules); i++ {
				assert.NoError(t, tt.rules[i].Init())
			}
			assert.Equal(t, tt.expected, IsRulesMatch(tt.rules, tt.data))
		})
	}
}
