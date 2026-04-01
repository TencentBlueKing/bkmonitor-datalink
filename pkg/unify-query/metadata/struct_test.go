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

func TestFieldAlias_AddAliasKeysWhenOriginalFieldPresent(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		fa       FieldAlias
		before   map[string]any
		expected map[string]any
	}{
		"命中原始字段 key 时补全别名字段": {
			fa:       FieldAlias{"alias": "original"},
			before:   map[string]any{"original": "v"},
			expected: map[string]any{"alias": "v", "original": "v"},
		},
		"多组映射分别补全别名字段": {
			fa: FieldAlias{
				"alias_a": "orig_a",
				"alias_b": "orig_b",
			},
			before: map[string]any{
				"orig_a": 1,
				"orig_b": "x",
				"other":  true,
			},
			expected: map[string]any{
				"alias_a": 1,
				"alias_b": "x",
				"orig_a":  1,
				"orig_b":  "x",
				"other":   true,
			},
		},
		"原始字段不存在时不新增别名 key": {
			fa:       FieldAlias{"alias": "original"},
			before:   map[string]any{"only_other": 1},
			expected: map[string]any{"only_other": 1},
		},
		"别名与原始同时存在时以原始字段值写入别名": {
			fa:       FieldAlias{"alias": "original"},
			before:   map[string]any{"alias": "old", "original": "new"},
			expected: map[string]any{"alias": "new", "original": "new"},
		},
		"FieldAlias 为空时不修改 map": {
			fa:       nil,
			before:   map[string]any{"k": 2, "original": "v"},
			expected: map[string]any{"k": 2, "original": "v"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data := cloneAnyMap(c.before)
			c.fa.AddAliasKeysWhenOriginalFieldPresent(data)
			assert.Equal(t, c.expected, data)
		})
	}

	t.Run("data 为 nil 时不 panic", func(t *testing.T) {
		t.Parallel()
		fa := FieldAlias{"alias": "original"}
		fa.AddAliasKeysWhenOriginalFieldPresent(nil)
		var empty FieldAlias
		empty.AddAliasKeysWhenOriginalFieldPresent(nil)
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
