// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package function

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/lucene_parser"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

const (
	KeyHighLight = "__highlight"
)

type HighLightFactory struct {
	labelMap          map[string][]LabelMapValue
	fieldsMap         metadata.FieldsMap
	maxAnalyzedOffset int
	isCaseSensitive   bool
	regexCache        map[highlightPatternKey]*regexp.Regexp
}

type LabelMapValue struct {
	Value      string `json:"value"`
	Operator   string `json:"operator"`
	IsWildcard bool   `json:"is_wildcard,omitempty"`
}

type LabelMapOption struct {
	Conditions  metadata.AllConditions
	QueryString string
	SQL         string
}

// LabelMap 获取高亮标签
func LabelMap(ctx context.Context, qry *metadata.Query) map[string][]LabelMapValue {
	if qry == nil {
		return nil
	}

	labelMap := make(map[string][]LabelMapValue)
	labelCheck := make(map[string]struct{})

	addLabels := func(key string, operator string, isWildcard bool, values ...string) {
		if len(values) == 0 {
			return
		}

		// 只有正向匹配才需要进行高亮替换
		switch operator {
		case metadata.ConditionEqual, metadata.ConditionContains, metadata.ConditionRegEqual, metadata.ConditionExact:
			for _, value := range values {
				checkKey := key + ":" + value + ":" + operator + ":" + cast.ToString(isWildcard)
				if _, ok := labelCheck[checkKey]; !ok {
					labelCheck[checkKey] = struct{}{}
					labelMap[key] = append(labelMap[key], LabelMapValue{
						Value:      value,
						Operator:   operator,
						IsWildcard: isWildcard,
					})
				}
			}
		}
	}

	for _, condition := range qry.AllConditions {
		for _, cond := range condition {
			addLabels(cond.DimensionName, highlightOperator(cond.Operator, cond.IsWildcard), cond.IsWildcard, cond.Value...)
		}
	}

	if qry.QueryString != "" {
		node := lucene_parser.ParseLuceneWithVisitor(ctx, qry.QueryString, lucene_parser.Option{})
		lucene_parser.ConditionNodeWalk(node, func(key string, operator string, values ...string) {
			addLabels(key, operator, false, values...)
		})
	}

	return labelMap
}

func highlightOperator(operator string, isWildcard bool) string {
	if !isWildcard {
		return operator
	}

	switch operator {
	case metadata.ConditionEqual, metadata.ConditionContains:
		return metadata.ConditionContains
	default:
		return operator
	}
}

func NewHighLightFactory(labelMap map[string][]LabelMapValue, fieldsMap metadata.FieldsMap, maxAnalyzedOffset int) *HighLightFactory {
	return &HighLightFactory{
		labelMap:          labelMap,
		fieldsMap:         fieldsMap,
		maxAnalyzedOffset: maxAnalyzedOffset,
		regexCache:        make(map[highlightPatternKey]*regexp.Regexp),
	}
}

func (h *HighLightFactory) Process(data map[string]any) map[string]any {
	newData := make(map[string]any)

	if h.labelMap == nil {
		return newData
	}

	for key, value := range data {
		// 获取全字段匹配，字段名为空
		keywords := append([]LabelMapValue{}, h.labelMap[""]...)
		// 获取使用字段查询的值
		keywords = append(keywords, h.labelMap[key]...)

		// 从 fieldsMap 中获取字段的大小写敏感性配置
		h.isCaseSensitive = false
		if h.fieldsMap != nil {
			fieldOption := h.fieldsMap.Field(key)
			if fieldOption.Existed() {
				h.isCaseSensitive = fieldOption.IsCaseSensitive
			}
		}

		if highlightedValue := h.processField(value, keywords); highlightedValue != nil {
			newData[key] = highlightedValue
		}
	}

	return newData
}

func (h *HighLightFactory) processField(fieldValue any, keywords []LabelMapValue) any {
	if len(keywords) == 0 {
		return nil
	}

	newValue := cast.ToString(fieldValue)
	if highlighted := h.highlightString(newValue, keywords); highlighted != newValue {
		return []string{highlighted}
	}
	return nil
}

func (h *HighLightFactory) highlightString(text string, keywords []LabelMapValue) string {
	if text == "" || len(keywords) == 0 {
		return text
	}

	analyzablePart, remainingPart := h.splitTextForAnalysis(text)

	intervals := make([]highlightInterval, 0)
	for _, kw := range keywords {
		switch kw.Operator {
		case metadata.ConditionEqual, metadata.ConditionRegEqual, metadata.ConditionContains, metadata.ConditionExact:
			re := h.highlightRegexp(kw)
			if re == nil {
				continue
			}
			for _, match := range re.FindAllStringIndex(analyzablePart, -1) {
				if len(match) != 2 || match[0] == match[1] {
					continue
				}
				intervals = append(intervals, highlightInterval{start: match[0], end: match[1]})
			}
		}
	}

	if len(intervals) == 0 {
		return text
	}

	return renderHighlight(analyzablePart, mergeHighlightIntervals(intervals)) + remainingPart
}

type highlightPatternKey struct {
	value         string
	operator      string
	isWildcard    bool
	caseSensitive bool
}

func (h *HighLightFactory) highlightRegexp(kw LabelMapValue) *regexp.Regexp {
	if h.regexCache == nil {
		h.regexCache = make(map[highlightPatternKey]*regexp.Regexp)
	}

	key := highlightPatternKey{
		value:         kw.Value,
		operator:      kw.Operator,
		isWildcard:    kw.IsWildcard,
		caseSensitive: h.isCaseSensitive,
	}
	if re, ok := h.regexCache[key]; ok {
		return re
	}

	pattern, err := buildHighlightPattern(kw.Value, kw.Operator == metadata.ConditionRegEqual, kw.IsWildcard, h.isCaseSensitive)
	if err != nil || pattern == "" {
		h.regexCache[key] = nil
		return nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		h.regexCache[key] = nil
		return nil
	}
	h.regexCache[key] = re
	return re
}

func (h *HighLightFactory) splitTextForAnalysis(text string) (analyzable, remaining string) {
	if h.maxAnalyzedOffset > 0 && len(text) > h.maxAnalyzedOffset {
		return text[:h.maxAnalyzedOffset], text[h.maxAnalyzedOffset:]
	}
	return text, ""
}

type highlightInterval struct {
	start int
	end   int
}

func buildHighlightPattern(kw string, isRegex bool, isWildcard bool, caseSensitive bool) (string, error) {
	if kw == "" {
		return "", nil
	}

	pattern := kw
	if !isRegex && isWildcard {
		pattern = buildWildcardHighlightPattern(kw)
	} else if !isRegex {
		pattern = regexp.QuoteMeta(kw)
	}
	if !caseSensitive {
		pattern = "(?i:" + pattern + ")"
	}

	if _, err := regexp.Compile(pattern); err != nil {
		return "", err
	}
	return pattern, nil
}

func buildWildcardHighlightPattern(kw string) string {
	chars := []rune(kw)
	start, end := 0, len(chars)
	for start < end && chars[start] == '*' {
		start++
	}
	for end > start && chars[end-1] == '*' && !isEscapedHighlightChar(chars, end-1) {
		end--
	}
	if start >= end {
		return ""
	}

	var builder strings.Builder
	for i := start; i < end; i++ {
		r := chars[i]
		if r == '\\' && i+1 < end && (isHighlightWildcard(chars[i+1]) || chars[i+1] == '\\') {
			builder.WriteString(regexp.QuoteMeta(string(chars[i+1])))
			i++
			continue
		}

		switch r {
		case '*':
			builder.WriteString(".*?")
		case '?':
			builder.WriteByte('.')
		default:
			builder.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	return builder.String()
}

func isHighlightWildcard(c rune) bool {
	return c == '*' || c == '?'
}

func isEscapedHighlightChar(chars []rune, pos int) bool {
	backslashes := 0
	for i := pos - 1; i >= 0 && chars[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
}

func mergeHighlightIntervals(intervals []highlightInterval) []highlightInterval {
	if len(intervals) == 0 {
		return nil
	}

	sort.SliceStable(intervals, func(i, j int) bool {
		if intervals[i].start == intervals[j].start {
			return intervals[i].end < intervals[j].end
		}
		return intervals[i].start < intervals[j].start
	})

	merged := []highlightInterval{intervals[0]}
	for _, interval := range intervals[1:] {
		last := &merged[len(merged)-1]
		if interval.start <= last.end {
			if interval.end > last.end {
				last.end = interval.end
			}
			continue
		}
		merged = append(merged, interval)
	}
	return merged
}

func renderHighlight(text string, intervals []highlightInterval) string {
	var builder strings.Builder
	cursor := 0
	for _, interval := range intervals {
		builder.WriteString(text[cursor:interval.start])
		builder.WriteString("<mark>")
		builder.WriteString(text[interval.start:interval.end])
		builder.WriteString("</mark>")
		cursor = interval.end
	}
	builder.WriteString(text[cursor:])
	return builder.String()
}
