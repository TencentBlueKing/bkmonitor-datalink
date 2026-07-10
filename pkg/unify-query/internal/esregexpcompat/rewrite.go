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
	// negativeLookaheadPrefix/negativeLookaheadSuffix 只识别用于表达“字段值不包含某段内容”的
	// 固定前缀/后缀形式，例如 ^(?!.*foo).* 或 ^(?!.*foo).*$。
	// ES regexp 不支持该不包含前缀形式，命中该形态后会改写为字段存在且不匹配 .*foo.*。
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
	return rewrite(pattern, false)
}

// RewriteForQueryString 在 Rewrite 基础上兼容 query_string 字段正则的方括号短语写法。
func RewriteForQueryString(pattern string) RewriteResult {
	return rewrite(pattern, true)
}

func rewrite(pattern string, keepBracketPhrase bool) RewriteResult {
	if inner, ok := extractNegativeLookahead(pattern); ok {
		return RewriteResult{
			Pattern:  rewritePositivePattern(inner, false),
			Negative: true,
		}
	}

	return RewriteResult{Pattern: rewritePositivePattern(pattern, keepBracketPhrase)}
}

// rewritePositivePattern 将正向 regexp 改写为 ES 整值匹配下的等价 pattern。
func rewritePositivePattern(pattern string, keepBracketPhrase bool) string {
	if alternatives, ok := splitTopLevelAlternation(pattern); ok {
		rewritten := make([]string, 0, len(alternatives))
		for _, alternative := range alternatives {
			rewritten = append(rewritten, rewriteSinglePattern(alternative, keepBracketPhrase))
		}
		return "(" + strings.Join(rewritten, "|") + ")"
	}

	return rewriteSinglePattern(pattern, keepBracketPhrase)
}

// rewriteSinglePattern 改写不含顶层 | 的单个正则分支。
// 未显式锚定的分支会补齐 .* 以模拟历史包含匹配；^/$ 锚点会被转换成 ES 整值匹配下的前缀/后缀约束。
func rewriteSinglePattern(pattern string, keepBracketPhrase bool) string {
	if isExplicitContains(pattern) {
		return pattern
	}
	if keepBracketPhrase {
		if rewritten, ok := rewriteBracketPhrasePattern(pattern); ok {
			return rewritten
		}
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

// extractNegativeLookahead 提取“字段值不包含某段内容”的固定前缀/后缀形式。
// 例如 ^(?!.*foo).* 返回 foo；其他不满足固定形态的表达式返回 ok=false，
// 继续走普通正则改写，避免把更复杂的正则误改成反向匹配。
func extractNegativeLookahead(pattern string) (string, bool) {
	if !strings.HasPrefix(pattern, negativeLookaheadPrefix) {
		return "", false
	}

	// 只处理 ^(?!.*foo).* 或 ^(?!.*foo).*$ 这两种固定形态。
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

// rewriteBracketPhrasePattern 递归处理透明分组里的方括号短语，例如 (([Page Error])|foo)。
func rewriteBracketPhrasePattern(pattern string) (string, bool) {
	if isLikelyBracketPhrase(pattern) {
		return pattern, true
	}

	if alternatives, ok := splitTopLevelAlternation(pattern); ok {
		hasBracketPhrase := false
		rewritten := make([]string, 0, len(alternatives))
		for _, alternative := range alternatives {
			if rewrittenAlternative, ok := rewriteBracketPhrasePattern(alternative); ok {
				hasBracketPhrase = true
				rewritten = append(rewritten, rewrittenAlternative)
				continue
			}
			rewritten = append(rewritten, rewriteSinglePattern(alternative, true))
		}
		if !hasBracketPhrase {
			return "", false
		}
		return strings.Join(rewritten, "|"), true
	}

	inner, ok := trimOuterParens(pattern)
	if !ok {
		return "", false
	}
	rewrittenInner, ok := rewriteBracketPhrasePattern(inner)
	if !ok {
		return "", false
	}
	return "(" + rewrittenInner + ")", true
}

// isLikelyBracketPhrase 判断表达式是否像误写成字符类的方括号短语，例如 [Page Error]。
func isLikelyBracketPhrase(pattern string) bool {
	body, ok := standaloneCharClassBody(pattern)
	trimmedBody := strings.TrimSpace(body)
	if !ok || trimmedBody == "" || strings.HasPrefix(trimmedBody, "^") {
		return false
	}

	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '\\', '-', '|', '^', '[', ']', '(', ')':
			return false
		}
	}

	longTokens := 0
	for _, token := range strings.Fields(body) {
		if len(token) > 1 {
			longTokens++
		}
	}
	return longTokens >= 2
}

func trimOuterParens(pattern string) (string, bool) {
	if len(pattern) < 2 || pattern[0] != '(' || pattern[len(pattern)-1] != ')' {
		return "", false
	}

	escaped := false
	bracketDepth := 0
	parenDepth := 0
	for i := 0; i < len(pattern); i++ {
		if escaped {
			escaped = false
			continue
		}
		switch pattern[i] {
		case '\\':
			escaped = true
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
			if bracketDepth == 0 {
				parenDepth--
				if parenDepth < 0 || (parenDepth == 0 && i != len(pattern)-1) {
					return "", false
				}
			}
		}
	}
	if parenDepth != 0 || bracketDepth != 0 {
		return "", false
	}
	return pattern[1 : len(pattern)-1], true
}

func standaloneCharClassBody(pattern string) (string, bool) {
	if !strings.HasPrefix(pattern, "[") {
		return "", false
	}

	escaped := false
	for i := 1; i < len(pattern); i++ {
		if escaped {
			escaped = false
			continue
		}
		switch pattern[i] {
		case '\\':
			escaped = true
		case ']':
			if i != len(pattern)-1 {
				return "", false
			}
			return pattern[1:i], true
		}
	}
	return "", false
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
