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

// RewriteResult 表示 ES regexp 兼容改写后的表达式，以及是否需要改成反向正则条件。
type RewriteResult struct {
	Pattern  string
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

	if isExplicitContains(pattern) {
		return RewriteResult{Pattern: pattern}
	}

	hasPrefix := hasUnescapedPrefixAnchor(pattern)
	hasSuffix := hasUnescapedSuffixAnchor(pattern)

	switch {
	case hasPrefix && hasSuffix:
		return RewriteResult{Pattern: trimSuffixAnchor(trimPrefixAnchor(pattern))}
	case hasPrefix:
		return RewriteResult{Pattern: trimPrefixAnchor(pattern) + ".*"}
	case hasSuffix:
		return RewriteResult{Pattern: ".*" + trimSuffixAnchor(pattern)}
	default:
		return RewriteResult{Pattern: ".*" + wrapTopLevelAlternation(pattern) + ".*"}
	}
}

func wrapTopLevelAlternation(pattern string) string {
	if !hasTopLevelAlternation(pattern) {
		return pattern
	}
	// ES regexp 是整值匹配，补齐前后 .* 时需要把顶层或表达式作为整体处理。
	return "(" + pattern + ")"
}

func hasTopLevelAlternation(pattern string) bool {
	// 只识别最外层未转义的 |；括号、字符类或转义后的 | 都保持原有正则语义。
	var (
		parenDepth   int
		bracketDepth int
		escaped      bool
	)
	for _, r := range pattern {
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
				return true
			}
		}
	}
	return false
}

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

func hasUnescapedPrefixAnchor(pattern string) bool {
	return strings.HasPrefix(pattern, "^")
}

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

func isExplicitContains(pattern string) bool {
	return hasUnescapedPrefixLiteral(pattern, ".*") && hasUnescapedSuffixLiteral(pattern, ".*")
}

func hasUnescapedPrefixLiteral(pattern string, literal string) bool {
	return strings.HasPrefix(pattern, literal)
}

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

func trimPrefixAnchor(pattern string) string {
	return strings.TrimPrefix(pattern, "^")
}

func trimSuffixAnchor(pattern string) string {
	if hasUnescapedSuffixAnchor(pattern) {
		return pattern[:len(pattern)-1]
	}
	return pattern
}
