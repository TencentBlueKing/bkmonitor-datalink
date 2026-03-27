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

func TestFieldAlias_InjectOriginalKeysWhenAliasPresent(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		fa       FieldAlias
		before   map[string]any
		expected map[string]any
	}{
		"当别名字段存在时写入与别名同值的原始字段名": {
			fa:       FieldAlias{"orig": "alias"},
			before:   map[string]any{"alias": "v"},
			expected: map[string]any{"orig": "v", "alias": "v"},
		},
		"多组映射分别注入": {
			fa: FieldAlias{
				"raw_a": "show_a",
				"raw_b": "show_b",
			},
			before: map[string]any{
				"show_a": 1,
				"show_b": "x",
				"other":  true,
			},
			expected: map[string]any{
				"raw_a":  1,
				"raw_b":  "x",
				"show_a": 1,
				"show_b": "x",
				"other":  true,
			},
		},
		"别名字段不存在时不新增原始 key": {
			fa:       FieldAlias{"orig": "alias"},
			before:   map[string]any{"only_other": 1},
			expected: map[string]any{"only_other": 1},
		},
		"原始与别名同时存在时用别名值覆盖原始 key": {
			fa:       FieldAlias{"orig": "alias"},
			before:   map[string]any{"orig": "old", "alias": "new"},
			expected: map[string]any{"orig": "new", "alias": "new"},
		},
		"FieldAlias 为空时不修改 map": {
			fa:       nil,
			before:   map[string]any{"alias": "v", "k": 2},
			expected: map[string]any{"alias": "v", "k": 2},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data := cloneAnyMap(c.before)
			c.fa.InjectOriginalKeysWhenAliasPresent(data)
			assert.Equal(t, c.expected, data)
		})
	}

	t.Run("data 为 nil 时不 panic", func(t *testing.T) {
		t.Parallel()
		fa := FieldAlias{"orig": "alias"}
		fa.InjectOriginalKeysWhenAliasPresent(nil)
		var empty FieldAlias
		empty.InjectOriginalKeysWhenAliasPresent(nil)
	})
}

func cloneAnyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

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
