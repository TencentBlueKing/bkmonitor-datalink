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

// 验证结构化 addition 的单值逗号串规范化结果：eq/ne 会拆分，其他输入保持原样。
func TestNormalizeCommaConditionValues(t *testing.T) {
	for name, c := range map[string]struct {
		condition ConditionField
		wantValue []string
	}{
		"eq 单值逗号串拆成多值": {
			condition: ConditionField{
				DimensionName: "result",
				Operator:      ConditionEqual,
				Value:         []string{"-4000, -3999,-3888"},
			},
			wantValue: []string{"-4000", "-3999", "-3888"},
		},
		"ne 单值逗号串拆成多值": {
			condition: ConditionField{
				DimensionName: "result",
				Operator:      ConditionNotEqual,
				Value:         []string{"-4000,-3999,-3888"},
			},
			wantValue: []string{"-4000", "-3999", "-3888"},
		},
		"已是多值数组不拆分": {
			condition: ConditionField{
				DimensionName: "result",
				Operator:      ConditionEqual,
				Value:         []string{"-4000", "-3999", "-3888"},
			},
			wantValue: []string{"-4000", "-3999", "-3888"},
		},
		"contains 操作符不拆分": {
			condition: ConditionField{
				DimensionName: "result",
				Operator:      ConditionContains,
				Value:         []string{"-4000,-3999,-3888"},
			},
			wantValue: []string{"-4000,-3999,-3888"},
		},
		"保留空候选": {
			condition: ConditionField{
				DimensionName: "result",
				Operator:      ConditionEqual,
				Value:         []string{"-4000,"},
			},
			wantValue: []string{"-4000", ""},
		},
	} {
		t.Run(name, func(t *testing.T) {
			conditions := AllConditions{{c.condition}}
			original := cloneAllConditions(conditions)
			normalized := normalizeCommaConditionValues(conditions)

			assert.Equal(t, c.wantValue, normalized[0][0].Value)
			assert.Equal(t, original, conditions)
		})
	}
}
