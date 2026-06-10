// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package esregexpcompat

import "strings"

const (
	negativeLookaheadPrefix = `^(?!.*`
	negativeLookaheadSuffix = `).*`
)

// RewriteResult 表示 ES regexp 兼容改写结果。
type RewriteResult struct {
	// Pattern 是可以下发给 ES regexp query 的表达式。
	Pattern string
	// Negative 表示 Pattern 需要作为反向 regexp 条件使用。
	// 例如 ^(?!.*foo).* 会被改写成 Pattern=.*foo.* 且 Negative=true；
	// 调用方应表达为“字段存在且不匹配 Pattern”，以保留原正则“字段值不包含 foo”的语义。
	Negative bool
}

// Rewrite 将 Python/Doris 搜索语义下的基础正则，改写成 ES regexp 整值匹配语义。
func Rewrite(pattern string) RewriteResult {
	if inner, ok := extractNegativeLookahead(pattern); ok {
		return RewriteResult{
			Pattern:  ".*" + inner + ".*",
			Negative: true,
		}
	}

	if alternatives, ok := splitTopLevelAlternation(pattern); ok {
		rewritten := make([]string, 0, len(alternatives))
		for _, alternative := range alternatives {
			rewritten = append(rewritten, rewriteSinglePattern(alternative))
		}
		return RewriteResult{Pattern: "(" + strings.Join(rewritten, "|") + ")"}
	}

	return RewriteResult{Pattern: rewriteSinglePattern(pattern)}
}

// rewriteSinglePattern 改写不含顶层 | 的单个正则分支。
// 未显式锚定的分支会补齐 .* 以模拟历史包含匹配；^/$ 锚点会被转换成 ES 整值匹配下的前缀/后缀约束。
func rewriteSinglePattern(pattern string) string {
	if isExplicitContains(pattern) {
		return pattern
	}

	hasPrefix := hasUnescapedPrefixAnchor(pattern)
	hasSuffix := hasUnescapedSuffixAnchor(pattern)

	switch {
	case hasPrefix && hasSuffix:
		return trimSuffixAnchor(trimPrefixAnchor(pattern))
	case hasPrefix:
		return addContainsSuffix(trimPrefixAnchor(pattern))
	case hasSuffix:
		return addContainsPrefix(trimSuffixAnchor(pattern))
	default:
		return addContainsSuffix(addContainsPrefix(pattern))
	}
}

// splitTopLevelAlternation 按最外层未转义的 | 拆分正则。
// 括号、字符类和转义后的 | 不拆分，避免破坏原有正则结构。
func splitTopLevelAlternation(pattern string) ([]string, bool) {
	var (
		alternatives []string
		start        int
		parenDepth   int
		bracketDepth int
		escaped      bool
	)
	for i, r := range pattern {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}

		switch r {
		case '[':
			if bracketDepth == 0 {
				bracketDepth = 1
			}
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case '(':
			if bracketDepth == 0 {
				parenDepth++
			}
		case ')':
			if bracketDepth == 0 && parenDepth > 0 {
				parenDepth--
			}
		case '|':
			if bracketDepth == 0 && parenDepth == 0 {
				alternatives = append(alternatives, pattern[start:i])
				start = i + len(string(r))
			}
		}
	}
	if len(alternatives) == 0 {
		return nil, false
	}
	alternatives = append(alternatives, pattern[start:])
	return alternatives, true
}

// extractNegativeLookahead 提取历史策略中出现的基础负向前瞻形式。
// 返回的 inner 是被排除的内容，例如 ^(?!.*foo).* 返回 foo；
// 更复杂的负向前瞻不在当前兼容范围内，会返回 ok=false 并走普通正则改写。
func extractNegativeLookahead(pattern string) (string, bool) {
	if !strings.HasPrefix(pattern, negativeLookaheadPrefix) {
		return "", false
	}

	// 当前只兼容历史策略中出现的基础形式：^(?!.*foo).* 或 ^(?!.*foo).*$。
	body := strings.TrimPrefix(pattern, negativeLookaheadPrefix)
	switch {
	case strings.HasSuffix(body, negativeLookaheadSuffix+"$"):
		body = strings.TrimSuffix(body, negativeLookaheadSuffix+"$")
	case strings.HasSuffix(body, negativeLookaheadSuffix):
		body = strings.TrimSuffix(body, negativeLookaheadSuffix)
	default:
		return "", false
	}

	if body == "" {
		return "", false
	}
	return body, true
}

// hasUnescapedPrefixAnchor 判断正则是否以未转义的 ^ 锚定开头。
func hasUnescapedPrefixAnchor(pattern string) bool {
	return strings.HasPrefix(pattern, "^")
}

// hasUnescapedSuffixAnchor 判断正则是否以未转义的 $ 锚定结尾。
func hasUnescapedSuffixAnchor(pattern string) bool {
	if !strings.HasSuffix(pattern, "$") {
		return false
	}

	// 末尾 $ 前有奇数个连续反斜杠时，说明 $ 是被转义的普通字符。
	backslashes := 0
	for i := len(pattern) - 2; i >= 0 && pattern[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 0
}

// isExplicitContains 判断表达式是否已经显式写成 .*foo.* 这类包含匹配。
func isExplicitContains(pattern string) bool {
	return hasUnescapedPrefixLiteral(pattern, ".*") && hasUnescapedSuffixLiteral(pattern, ".*")
}

// addContainsPrefix 在缺少前置 .* 时补齐包含匹配前缀。
func addContainsPrefix(pattern string) string {
	if hasUnescapedPrefixLiteral(pattern, ".*") {
		return pattern
	}
	return ".*" + pattern
}

// addContainsSuffix 在缺少后置 .* 时补齐包含匹配后缀。
func addContainsSuffix(pattern string) string {
	if hasUnescapedSuffixLiteral(pattern, ".*") {
		return pattern
	}
	return pattern + ".*"
}

// hasUnescapedPrefixLiteral 判断 pattern 是否以指定 literal 开头。
func hasUnescapedPrefixLiteral(pattern string, literal string) bool {
	return strings.HasPrefix(pattern, literal)
}

// hasUnescapedSuffixLiteral 判断 pattern 是否以未转义的指定 literal 结尾。
func hasUnescapedSuffixLiteral(pattern string, literal string) bool {
	if !strings.HasSuffix(pattern, literal) {
		return false
	}
	start := len(pattern) - len(literal)
	if start == 0 {
		return true
	}

	// literal 起始位置前有奇数个反斜杠时，说明该 literal 被转义。
	backslashes := 0
	for i := start - 1; i >= 0 && pattern[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 0
}

// trimPrefixAnchor 去掉开头的 ^ 锚点。
func trimPrefixAnchor(pattern string) string {
	return strings.TrimPrefix(pattern, "^")
}

// trimSuffixAnchor 去掉未转义的结尾 $ 锚点。
func trimSuffixAnchor(pattern string) string {
	if hasUnescapedSuffixAnchor(pattern) {
		return pattern[:len(pattern)-1]
	}
	return pattern
}
