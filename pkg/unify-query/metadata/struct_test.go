// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReplaceVmCondition(t *testing.T) {
	for name, c := range map[string]struct {
		condition     VmCondition
		replaceLabels ReplaceLabels
		expected      VmCondition
	}{
		"test_1": {
			condition: `tag_1="a"`,
			replaceLabels: ReplaceLabels{
				"tag_1": ReplaceLabel{
					Source: "a",
					Target: "b",
				},
			},
			expected: `tag_1="b"`,
		},
		"test_2": {
			condition: `tag_1="a1"`,
			replaceLabels: ReplaceLabels{
				"tag_1": ReplaceLabel{
					Source: "a",
					Target: "b",
				},
			},
			expected: `tag_1="a1"`,
		},
		"test_3": {
			condition: `tag_1="a"-rr`,
			replaceLabels: ReplaceLabels{
				"tag_1": ReplaceLabel{
					Source: "a",
					Target: "b",
				},
			},
			expected: `tag_1="a"-rr`,
		},
		"test_4": {
			condition: `tag_1="a" or tag_2="good"`,
			replaceLabels: ReplaceLabels{
				"tag_1": ReplaceLabel{
					Source: "a",
					Target: "b",
				},
				"tag_2": ReplaceLabel{
					Source: "good",
					Target: "bad",
				},
			},
			expected: `tag_1="b" or tag_2="bad"`,
		},
		"test_5": {
			condition: `tag_1="a" or tag_2="good", tag_1="a"`,
			replaceLabels: ReplaceLabels{
				"tag_1": ReplaceLabel{
					Source: "a",
					Target: "b",
				},
				"tag_2": ReplaceLabel{
					Source: "good",
					Target: "bad",
				},
			},
			expected: `tag_1="b" or tag_2="bad", tag_1="b"`,
		},
		"test_6": {
			condition: `tag_1="a" or tag_2="good", tag_1="cat"`,
			replaceLabels: ReplaceLabels{
				"tag_1": ReplaceLabel{
					Source: "a",
					Target: "b",
				},
				"tag_2": ReplaceLabel{
					Source: "good",
					Target: "bad",
				},
			},
			expected: `tag_1="b" or tag_2="bad", tag_1="cat"`,
		},
		"test_7": {
			condition: `tag_1="a" or tag_1="cat", tag_3="a", tag_5="a"`,
			replaceLabels: ReplaceLabels{
				"tag_1": ReplaceLabel{
					Source: "a",
					Target: "b",
				},
				"tag_2": ReplaceLabel{
					Source: "good",
					Target: "bad",
				},
			},
			expected: `tag_1="b" or tag_1="cat", tag_3="a", tag_5="a"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			actual := ReplaceVmCondition(c.condition, c.replaceLabels)
			assert.Equal(t, c.expected, actual)
		})
	}
}
