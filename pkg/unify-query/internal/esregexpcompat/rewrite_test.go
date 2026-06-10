// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package esregexpcompat

import "testing"

func TestRewrite(t *testing.T) {
	tests := map[string]struct {
		pattern      string
		wantPattern  string
		wantNegative bool
	}{
		"普通文本按包含匹配补齐": {
			pattern:     "TypeError",
			wantPattern: ".*TypeError.*",
		},
		"普通正则按包含匹配补齐": {
			pattern:     "a.*b",
			wantPattern: ".*a.*b.*",
		},
		"顶层或表达式按分支补齐包含匹配": {
			pattern:     "foo|bar",
			wantPattern: "(.*foo.*|.*bar.*)",
		},
		"顶层或表达式按分支保留锚点语义": {
			pattern:     "^foo|bar",
			wantPattern: "(foo.*|.*bar.*)",
		},
		"顶层或表达式按分支处理后缀锚点": {
			pattern:     "foo|bar$",
			wantPattern: "(.*foo.*|.*bar)",
		},
		"顶层或表达式按分支处理显式包含": {
			pattern:     ".*foo|bar.*",
			wantPattern: "(.*foo.*|.*bar.*)",
		},
		"括号内或表达式不重复包裹": {
			pattern:     "(foo|bar)",
			wantPattern: ".*(foo|bar).*",
		},
		"字符类内竖线不视为顶层或表达式": {
			pattern:     "[a|b]",
			wantPattern: ".*[a|b].*",
		},
		"前缀锚点改写为整值前缀匹配": {
			pattern:     "^foo",
			wantPattern: "foo.*",
		},
		"后缀锚点改写为整值后缀匹配": {
			pattern:     "foo$",
			wantPattern: ".*foo",
		},
		"首尾锚点改写为整值匹配": {
			pattern:     "^foo$",
			wantPattern: "foo",
		},
		"已显式包含匹配时保持不变": {
			pattern:     ".*foo.*",
			wantPattern: ".*foo.*",
		},
		"已有单侧前缀包含时只补齐后缀": {
			pattern:     ".*foo",
			wantPattern: ".*foo.*",
		},
		"已有单侧后缀包含时只补齐前缀": {
			pattern:     "foo.*",
			wantPattern: ".*foo.*",
		},
		"前缀锚点后已有后缀包含时不重复补齐": {
			pattern:     "^foo.*",
			wantPattern: "foo.*",
		},
		"负向前瞻改写为反向正则": {
			pattern:      "^(?!.*idip).*",
			wantPattern:  ".*idip.*",
			wantNegative: true,
		},
		"带结尾锚点的负向前瞻改写为反向正则": {
			pattern:      "^(?!.*idip).*$",
			wantPattern:  ".*idip.*",
			wantNegative: true,
		},
		"转义前缀锚点按普通包含处理": {
			pattern:     `\^foo`,
			wantPattern: `.*\^foo.*`,
		},
		"转义后缀锚点按普通包含处理": {
			pattern:     `foo\$`,
			wantPattern: `.*foo\$.*`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := Rewrite(tt.pattern)
			if got.Pattern != tt.wantPattern {
				t.Fatalf("Rewrite(%q).Pattern = %q, want %q", tt.pattern, got.Pattern, tt.wantPattern)
			}
			if got.Negative != tt.wantNegative {
				t.Fatalf("Rewrite(%q).Negative = %v, want %v", tt.pattern, got.Negative, tt.wantNegative)
			}
		})
	}
}
