// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// promQLAliasQuery is one named PromQL query of a multi-PromQL item.
type promQLAliasQuery struct {
	Alias, PromQL string
}

var (
	promQLAliasPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	promQLOperators    = map[string]bool{"and": true, "or": true, "unless": true, "bool": true}
	promQLModifiers    = map[string]bool{"on": true, "ignoring": true, "group_left": true, "group_right": true, "by": true, "without": true}
)

func promQLReservedAlias(alias string) bool {
	lower := strings.ToLower(alias)
	return promQLOperators[lower] || promQLModifiers[lower] || lower == "offset"
}

// compilePromQLExpression expands an expression over named PromQL queries
// into one PromQL, as the platform's compile_promql_expression does: each
// bare alias becomes its parenthesised query, function names, aggregators,
// operators, strings and vector-matching label lists stay as written, and an
// identifier that is none of these, a range selector or an outer offset is
// refused. Whitespace is kept as written. The platform's own outputs are the
// golden fixtures this is held to (testdata/named_outputs).
//
// The callers check the alias, query and interval facts before; this is the
// expression alone.
func compilePromQLExpression(queries []promQLAliasQuery, expression string) (string, error) {
	if len(queries) < 2 {
		return "", errors.New("multiple PromQL queries are required")
	}
	if strings.TrimFunc(expression, pythonSpace) == "" {
		return "", errors.New("PromQL expression is required")
	}
	byAlias := make(map[string]string, len(queries))
	for _, query := range queries {
		byAlias[query.Alias] = query.PromQL
	}
	var result strings.Builder
	labelList, pendingModifier := false, ""
	for position := 0; position < len(expression); {
		token, ok := nextPromQLToken(expression[position:])
		if !ok {
			if expression[position] == '[' {
				return "", errors.New("range selectors must be specified in an individual PromQL query")
			}
			return "", fmt.Errorf("unsupported PromQL expression token at byte %d", position+1)
		}
		position += len(token)
		if strings.TrimFunc(token, pythonSpace) == "" {
			result.WriteString(token)
			continue
		}
		if pendingModifier != "" {
			if token == "(" {
				pendingModifier, labelList = "", true
				result.WriteString(token)
				continue
			}
			if pendingModifier == "on" || pendingModifier == "ignoring" || pendingModifier == "by" || pendingModifier == "without" {
				return "", errors.New("vector matching modifier requires a label list")
			}
			pendingModifier = ""
		}
		if labelList {
			if token == ")" {
				labelList = false
			} else if token != "," && !promQLAliasPattern.MatchString(token) {
				return "", errors.New("invalid vector matching label list")
			}
			result.WriteString(token)
			continue
		}
		if !promQLAliasPattern.MatchString(token) {
			result.WriteString(token)
			continue
		}
		keyword := strings.ToLower(token)
		rest := expression[position:]
		switch {
		case promQLModifiers[keyword]:
			pendingModifier = keyword
			result.WriteString(token)
		case promQLOperators[keyword]:
			result.WriteString(token)
		case keyword == "offset":
			return "", errors.New("offset must be specified in an individual PromQL query")
		case byAlias[token] != "":
			if strings.HasPrefix(strings.TrimLeftFunc(rest, pythonSpace), "(") {
				return "", fmt.Errorf("PromQL query alias conflicts with a function call: %s", token)
			}
			result.WriteString("(" + byAlias[token] + ")")
		case followedByCallOrGrouping(rest):
			// Functions and aggregators are PromQL's own; a bare metric has
			// to be a declared alias.
			result.WriteString(token)
		default:
			return "", fmt.Errorf("unknown PromQL query alias: %s", token)
		}
	}
	if pendingModifier != "" || labelList {
		return "", errors.New("incomplete vector matching modifier")
	}
	return result.String(), nil
}

// followedByCallOrGrouping is the platform's `\s*(?:\(|(?:by|without)\b)`
// read after an identifier.
func followedByCallOrGrouping(rest string) bool {
	rest = strings.TrimLeftFunc(rest, pythonSpace)
	if strings.HasPrefix(rest, "(") {
		return true
	}
	for _, word := range []string{"by", "without"} {
		if strings.HasPrefix(rest, word) {
			next, _ := utf8.DecodeRuneInString(rest[len(word):])
			if len(rest) == len(word) || !pythonWordRune(next) {
				return true
			}
		}
	}
	return false
}

// nextPromQLToken is the platform tokenizer's next match at the start of s:
// whitespace, a quoted string, a number, an identifier, a two-character
// comparison or a single operator or bracket.
func nextPromQLToken(s string) (string, bool) {
	first, size := utf8.DecodeRuneInString(s)
	switch {
	case pythonSpace(first):
		end := size
		for end < len(s) {
			r, n := utf8.DecodeRuneInString(s[end:])
			if !pythonSpace(r) {
				break
			}
			end += n
		}
		return s[:end], true
	case first == '"' || first == '\'':
		for end := 1; end < len(s); end++ {
			switch s[end] {
			case '\\':
				end++
			case byte(first):
				return s[:end+1], true
			}
		}
		return "", false
	case first == '`':
		if end := strings.IndexByte(s[1:], '`'); end >= 0 {
			return s[:end+2], true
		}
		return "", false
	case unicode.IsDigit(first) || first == '.':
		return promQLNumber(s)
	case first == '_' || first < utf8.RuneSelf && unicode.IsLetter(first):
		end := 1
		for end < len(s) && (s[end] == '_' || s[end] < utf8.RuneSelf && (unicode.IsLetter(rune(s[end])) || unicode.IsDigit(rune(s[end])))) {
			end++
		}
		return s[:end], true
	}
	if len(s) >= 2 {
		switch s[:2] {
		case "==", "!=", "<=", ">=":
			return s[:2], true
		}
	}
	if strings.ContainsRune("()+-*/%^,<>", first) {
		return s[:1], true
	}
	return "", false
}

// promQLNumber is `(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?`, with `\d` the
// Unicode decimal digits a Python pattern means by it.
func promQLNumber(s string) (string, bool) {
	digits := func(from int) int {
		for from < len(s) {
			r, n := utf8.DecodeRuneInString(s[from:])
			if !unicode.IsDigit(r) {
				break
			}
			from += n
		}
		return from
	}
	end := digits(0)
	if end == 0 {
		if !strings.HasPrefix(s, ".") {
			return "", false
		}
		end = digits(1)
		if end == 1 {
			return "", false
		}
	} else if end < len(s) && s[end] == '.' {
		end = digits(end + 1)
	}
	if end < len(s) && (s[end] == 'e' || s[end] == 'E') {
		exponent := end + 1
		if exponent < len(s) && (s[exponent] == '+' || s[exponent] == '-') {
			exponent++
		}
		if after := digits(exponent); after > exponent {
			end = after
		}
	}
	return s[:end], true
}

// pythonSpace is a Python str's isspace, which its `\s` matches: Go's
// unicode.IsSpace plus the four ASCII separators U+001C to U+001F.
func pythonSpace(r rune) bool {
	return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f
}

// pythonWordRune is a Python `\w` character, which `\b` reads.
func pythonWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Nl, r) ||
		unicode.Is(unicode.No, r)
}
