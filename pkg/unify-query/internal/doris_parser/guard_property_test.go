// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package doris_parser

import (
	"fmt"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/doris_parser/gen"
)

// 测试期性质（不在运行时使用）：重写只允许增加内容（字段映射、别名、括号、注入条件），不允许丢失或重排
// 用户写的内容。把用户 SQL 的骨架与渲染结果比对，骨架必须是渲染结果的子序列。
// 骨架：去掉 FROM 与 LIMIT / OFFSET 子句（这两处按设计由 UQ 改写）后的全部词法单元，标识符统一成占位符
// （字段映射会改名或展开成多段）。

const guardIdentifier = "<id>"

// sqlSkeleton returns the tokens a rewrite must keep, in order.
func sqlSkeleton(sql string) []string {
	lexer := gen.NewDorisLexer(antlr.NewInputStream(sql))
	lexer.RemoveErrorListeners()
	tokens := antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel)
	tokens.Fill()

	var out []string
	// skipDepth >= 0 while inside a FROM / LIMIT clause opened at that parenthesis depth.
	depth, skipDepth := 0, -1
	for _, tk := range tokens.GetAllTokens() {
		if tk.GetChannel() != antlr.TokenDefaultChannel || tk.GetTokenType() == antlr.TokenEOF {
			continue
		}
		text := tk.GetText()
		upper := strings.ToUpper(text)
		switch text {
		case "(":
			depth++
		case ")":
			if skipDepth == depth {
				skipDepth = -1
			}
			depth--
		}
		if skipDepth >= 0 {
			if depth == skipDepth && isClauseKeyword(upper) && upper != "FROM" && upper != "LIMIT" && upper != "OFFSET" {
				skipDepth = -1
			} else {
				continue
			}
		}
		if upper == "FROM" || upper == "LIMIT" || upper == "OFFSET" {
			skipDepth = depth
			continue
		}
		if isIdentifierToken(tk) {
			out = append(out, guardIdentifier)
			continue
		}
		out = append(out, upper)
	}
	return out
}

func isClauseKeyword(upper string) bool {
	switch upper {
	case "WHERE", "GROUP", "HAVING", "ORDER", "LIMIT", "OFFSET", "UNION", "EXCEPT", "INTERSECT", "QUALIFY", "WINDOW":
		return true
	}
	return false
}

// semanticKeywords are the words whose loss changes a query's meaning. Every other word-like token (column,
// alias, function name, non-reserved keyword used as a name) is an identifier: field mapping may rename it.
var semanticKeywords = map[string]struct{}{
	"SELECT": {}, "DISTINCT": {}, "AS": {}, "WHERE": {}, "AND": {}, "OR": {}, "NOT": {}, "XOR": {}, "IN": {}, "IS": {},
	"NULL": {}, "TRUE": {}, "FALSE": {}, "BETWEEN": {}, "LIKE": {}, "RLIKE": {}, "REGEXP": {}, "CASE": {},
	"WHEN": {}, "THEN": {}, "ELSE": {}, "END": {}, "CAST": {}, "GROUP": {}, "BY": {}, "HAVING": {}, "ORDER": {},
	"ASC": {}, "DESC": {}, "NULLS": {}, "FIRST": {}, "LAST": {}, "UNION": {}, "EXCEPT": {}, "INTERSECT": {},
	"ALL": {}, "JOIN": {}, "INNER": {}, "LEFT": {}, "RIGHT": {}, "FULL": {}, "CROSS": {}, "ON": {}, "USING": {},
	"EXISTS": {}, "OVER": {}, "PARTITION": {}, "ROWS": {}, "RANGE": {}, "WITH": {}, "ROLLUP": {}, "CUBE": {},
	"GROUPING": {}, "SETS": {}, "QUALIFY": {}, "WINDOW": {}, "INTERVAL": {}, "DIV": {}, "MOD": {},
	"MATCH_ANY": {}, "MATCH_ALL": {}, "MATCH_PHRASE": {}, "MATCH_PHRASE_PREFIX": {}, "MATCH_REGEXP": {},
	"MATCH_PHRASE_EDGE": {},
}

func isIdentifierToken(tk antlr.Token) bool {
	text := tk.GetText()
	if tk.GetTokenType() == gen.DorisLexerBACKQUOTED_IDENTIFIER {
		return true
	}
	if text == "" || !(text[0] == '_' || text[0] >= 'A' && text[0] <= 'Z' || text[0] >= 'a' && text[0] <= 'z') {
		return false
	}
	for _, r := range text {
		if !(r == '_' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
			return false
		}
	}
	_, keyword := semanticKeywords[strings.ToUpper(text)]
	return !keyword
}

// dropItemParentheses removes a parenthesis pair that wraps a whole SELECT / GROUP BY / ORDER BY item or a
// whole function argument, e.g. DISTINCT(x) AS id or (a + b) AS x: such a pair never changes the result and
// the rewrite may drop it. A pair that is an operand, (a + b) * c, stays.
func dropItemParentheses(tokens []string) []string {
	match := make([]int, len(tokens))
	var stack []int
	for i, tk := range tokens {
		match[i] = -1
		switch tk {
		case "(":
			stack = append(stack, i)
		case ")":
			if len(stack) > 0 {
				open := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				match[open], match[i] = i, open
			}
		}
	}
	drop := make([]bool, len(tokens))
	for i, tk := range tokens {
		if tk != "(" || match[i] < 0 || i == 0 {
			continue
		}
		closer := match[i]
		next := "<end>"
		if closer+1 < len(tokens) {
			next = tokens[closer+1]
		}
		if itemStart(tokens[i-1]) && itemEnd(next) {
			drop[i], drop[closer] = true, true
		}
	}
	out := make([]string, 0, len(tokens))
	for i, tk := range tokens {
		if !drop[i] {
			out = append(out, tk)
		}
	}
	return out
}

func itemStart(tk string) bool {
	switch tk {
	case "SELECT", "DISTINCT", "BY", ",":
		return true
	}
	return false
}

func itemEnd(tk string) bool {
	switch tk {
	case ",", "AS", "WHERE", "GROUP", "HAVING", "ORDER", "ASC", "DESC", ")", "<end>":
		return true
	}
	return false
}

// checkRewritePreservesInput rejects a rewrite that loses or reorders anything the user wrote.
func checkRewritePreservesInput(input, output string) error {
	in, out := dropItemParentheses(sqlSkeleton(input)), dropItemParentheses(sqlSkeleton(output))
	j := 0
	for i, want := range in {
		// 未知字段按设计改写成 NULL，NULL 可以顶替一个标识符。
		for j < len(out) && out[j] != want && !(want == guardIdentifier && out[j] == "NULL") {
			j++
		}
		if j == len(out) {
			return fmt.Errorf("unsupported Doris SQL: %s", describeLost(in, i))
		}
		j++
	}
	return nil
}

func describeLost(in []string, i int) string {
	lo, hi := i-3, i+4
	if lo < 0 {
		lo = 0
	}
	if hi > len(in) {
		hi = len(in)
	}
	return fmt.Sprintf("%q near %q cannot be kept by the rewrite", in[i], strings.Join(in[lo:hi], " "))
}
