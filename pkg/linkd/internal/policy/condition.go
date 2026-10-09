// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

type compiledCondition struct {
	condition Condition
	kind      FieldKind
	operator  string
	negative  bool
	values    []string
	pattern   *regexp.Regexp
	reference []referencePart
}

func compileCondition(condition Condition, fields FieldCatalog) (compiledCondition, error) {
	result := compiledCondition{condition: condition, operator: condition.Operator}
	if condition.Field == "" || len(condition.Field) > 256 || len(condition.Value) == 0 || len(condition.Value) > 65536 {
		return result, fmt.Errorf("target_key and bounded target_value required")
	}
	if strings.HasPrefix(result.operator, "must_not_") {
		result.negative = true
		result.operator = strings.TrimPrefix(result.operator, "must_not_")
	}
	switch result.operator {
	case "term", "terms", "wildcard", "regexp":
	default:
		return result, fmt.Errorf("unsupported condition operator")
	}
	kind, ok := fields[condition.Field]
	if !ok {
		return result, fmt.Errorf("unsupported target_key %q; register an explicit field mapping", condition.Field)
	}
	if kind != FieldText && kind != FieldKeyword && kind != FieldNumber && kind != FieldBoolean && kind != FieldDate {
		return result, fmt.Errorf("invalid field kind")
	}
	result.kind = kind
	var value any
	decoder := json.NewDecoder(bytes.NewReader(condition.Value))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return result, fmt.Errorf("invalid target_value")
	}
	if condition.Relation != "" {
		target, ok := value.(string)
		if len(condition.Relation) > 256 || !ok || target == "" || len(target) > 128 || (result.operator != "term" && result.operator != "terms") {
			return result, fmt.Errorf("relation requires canonical target model and term/terms operator")
		}
		return result, nil
	}
	list, ok := value.([]any)
	if result.operator == "wildcard" || result.operator == "regexp" {
		if ok {
			return result, fmt.Errorf("text query requires one scalar value")
		}
		if result.operator == "regexp" && (kind != FieldText && kind != FieldKeyword) {
			return result, fmt.Errorf("regexp requires text or keyword field")
		}
		list = []any{value}
	} else if !ok {
		if result.operator == "terms" {
			return result, fmt.Errorf("terms requires array target_value")
		}
		list = []any{value}
	}
	if len(list) == 0 || len(list) > 256 {
		return result, fmt.Errorf("target_value requires 1..256 scalar values")
	}
	for _, v := range list {
		text, err := queryScalar(kind, v)
		if err != nil {
			return result, err
		}
		result.values = append(result.values, text)
	}
	if result.operator == "regexp" {
		pattern, err := compileLuceneSubset(result.values[0])
		if err != nil {
			return result, err
		}
		result.pattern = pattern
	}
	return result, nil
}

func (c compiledCondition) match(ctx context.Context, reader Reader) (bool, error) {
	var matched bool
	if c.condition.Relation != "" {
		positive := c.condition
		positive.Operator = c.operator
		positive.Value = append(json.RawMessage(nil), positive.Value...)
		value, err := reader.Related(ctx, positive)
		if err != nil {
			return false, err
		}
		matched = value
	} else {
		field, err := reader.Field(ctx, c.condition.Field)
		if err != nil {
			return false, err
		}
		if field.Present && field.Data != nil {
			values := []any{}
			nodes := 0
			if err := scalarValues(field.Data, &values, 0, &nodes); err != nil {
				return false, err
			}
			totalBytes := 0
			for _, value := range values {
				if err := ctx.Err(); err != nil {
					return false, err
				}
				text, err := queryScalar(c.kind, value)
				if err != nil {
					return false, err
				}
				totalBytes += len(text)
				if totalBytes > 65536 {
					return false, fmt.Errorf("condition input exceeds 64 KiB")
				}
				if c.operator == "regexp" && len(text) > 8192 {
					return false, fmt.Errorf("regexp input exceeds 8 KiB")
				}
				if c.kind == FieldText && (c.operator == "term" || c.operator == "regexp") && utf16Length(text) > 8191 {
					continue
				}
				for _, wanted := range c.values {
					switch c.operator {
					case "regexp":
						matched = matched || c.pattern.MatchString(text)
					case "wildcard":
						if c.kind == FieldText {
							matched = matched || containsTokens(kacTokens(text), kacTokens(wanted))
						} else {
							matched = matched || text == wanted
						}
					case "terms":
						if c.kind == FieldText {
							for _, token := range kacTokens(text) {
								matched = matched || token == wanted
							}
						} else {
							matched = matched || text == wanted
						}
					case "term":
						matched = matched || text == wanted
					}
				}
			}
		}
	}
	if c.negative {
		return !matched, nil
	}
	return matched, nil
}

func scalarValues(value any, out *[]any, depth int, nodes *int) error {
	*nodes++
	if depth > 16 || *nodes > 512 {
		return fmt.Errorf("condition value budget exceeded")
	}
	if value == nil {
		return nil
	}
	if array, ok := value.([]any); ok {
		for _, item := range array {
			if err := scalarValues(item, out, depth+1, nodes); err != nil {
				return err
			}
		}
		return nil
	}
	if len(*out) >= 256 {
		return fmt.Errorf("condition contains too many values")
	}
	*out = append(*out, value)
	return nil
}

func queryScalar(kind FieldKind, value any) (string, error) {
	if value == nil {
		return "", fmt.Errorf("null is not a query scalar")
	}
	if kind == FieldNumber {
		text, ok := numberText(value)
		if s, isString := value.(string); isString {
			text, ok = s, true
		}
		if !ok {
			return "", fmt.Errorf("numeric field requires finite numeric value")
		}
		return canonicalNumber(text)
	}
	if kind == FieldBoolean {
		if b, ok := value.(bool); ok {
			return strconv.FormatBool(b), nil
		}
		if s, ok := value.(string); ok && (s == "true" || s == "false") {
			return s, nil
		}
		return "", fmt.Errorf("boolean field requires true or false")
	}
	if kind == FieldDate {
		text, ok := value.(string)
		if !ok {
			return "", fmt.Errorf("date field requires explicit date string")
		}
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05"} {
			if parsed, err := time.Parse(layout, text); err == nil {
				return strconv.FormatInt(parsed.UnixMilli(), 10), nil
			}
		}
		return "", fmt.Errorf("unsupported date format or date math")
	}
	if s, ok := value.(string); ok {
		return s, nil
	}
	if b, ok := value.(bool); ok {
		return strconv.FormatBool(b), nil
	}
	if n, ok := numberText(value); ok {
		if _, err := canonicalNumber(n); err != nil {
			return "", err
		}
		return n, nil
	}
	return "", fmt.Errorf("field requires scalar or scalar array")
}

// kacTokens 复现 KAC 的 Java pattern_replace (.+?)->$1<space>、WhitespaceTokenizer 和 lowercase。
// Java whitespace 不包含 NBSP/FIGURE SPACE/NNBSP；NEL 不被 dot 捕获且不被 tokenizer 切分。
func kacTokens(value string) []string {
	var replaced strings.Builder
	for _, r := range value {
		replaced.WriteRune(r)
		if r != '\n' && r != '\r' && r != '\u0085' && r != '\u2028' && r != '\u2029' {
			replaced.WriteByte(' ')
		}
	}
	tokens := strings.FieldsFunc(replaced.String(), javaWhitespace)
	for i := range tokens {
		tokens[i] = strings.Map(unicode.ToLower, tokens[i])
	}
	return tokens
}

func javaWhitespace(r rune) bool {
	return (r >= '\t' && r <= '\r') || (r >= 0x1c && r <= 0x1f) || ((unicode.Is(unicode.Zs, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r)) && r != 0xa0 && r != 0x2007 && r != 0x202f)
}

func containsTokens(value, query []string) bool {
	if len(query) == 0 || len(query) > len(value) {
		return false
	}
	for i := 0; i <= len(value)-len(query); i++ {
		same := true
		for j := range query {
			if value[i+j] != query[j] {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	return false
}

func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// compileLuceneSubset 只接受已对照的公共语法。Lucene 其他操作符和 RE2 专有语法都明确拒绝。
func compileLuceneSubset(pattern string) (*regexp.Regexp, error) {
	if len(pattern) > 1024 || utf8.RuneCountInString(pattern) > 256 {
		return nil, fmt.Errorf("regexp exceeds 256 characters")
	}
	if strings.Contains(pattern, "(?") || strings.Contains(pattern, "[:") {
		return nil, fmt.Errorf("unsupported regexp extension")
	}
	inClass := false
	classFirst := false
	var converted strings.Builder
	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '\\' {
			i++
			if i == len(runes) {
				return nil, fmt.Errorf("unfinished regexp escape")
			}
			next := runes[i]
			if unicode.IsLetter(next) || unicode.IsDigit(next) {
				return nil, fmt.Errorf("regexp character escapes are not equivalent to Lucene")
			}
			converted.WriteString(regexp.QuoteMeta(string(next)))
			continue
		}
		if r == '[' && !inClass {
			inClass = true
			classFirst = true
			converted.WriteRune(r)
			continue
		}
		if r == ']' && inClass {
			inClass = false
			converted.WriteRune(r)
			continue
		}
		if !inClass && strings.ContainsRune("^$#@&~<>\"", r) {
			return nil, fmt.Errorf("unsupported Lucene regexp operator")
		}
		if inClass && r == '^' && !classFirst {
			return nil, fmt.Errorf("ambiguous regexp class anchor")
		}
		classFirst = false
		converted.WriteRune(r)
	}
	compiled, err := regexp.Compile(`\A(?s:` + converted.String() + `)\z`)
	if err != nil {
		return nil, fmt.Errorf("invalid or unsupported regexp syntax")
	}
	return compiled, nil
}
