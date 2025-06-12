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
	"fmt"
	"sort"
	"strings"
)

const (
	KeyHighLight = "__highlight"
)

type LabelMapFactory struct {
	labelMap                   map[string][]string
	highlightMaxAnalyzedOffset int
}

func NewLabelMapFactory(labelMap map[string][]string, highlightMaxAnalyzedOffset int) *LabelMapFactory {
	return &LabelMapFactory{
		labelMap:                   labelMap,
		highlightMaxAnalyzedOffset: highlightMaxAnalyzedOffset,
	}
}

func (h *LabelMapFactory) ProcessHighlight(data map[string]any) map[string]any {
	newData := make(map[string]any)

	for key, value := range data {
		// 获取全字段匹配，字段名为空
		keywords := append([]string{}, h.labelMap[""]...)
		// 获取使用字段查询的值
		keywords = append(keywords, h.labelMap[key]...)

		if highlightedValue := h.processHighlightField(value, keywords); highlightedValue != nil {
			newData[key] = highlightedValue
		}
	}

	return newData
}

func (h *LabelMapFactory) processHighlightField(fieldValue any, keywords []string) any {
	var newValue string
	switch value := fieldValue.(type) {
	case string:
		newValue = value
	case int:
		newValue = fmt.Sprintf("%d", value)
	default:
		return nil
	}

	if highlighted := h.highlightString(newValue, keywords); highlighted != newValue {
		return []string{highlighted}
	}
	return nil
}

func (h *LabelMapFactory) FetchIncludeFieldValues(fieldName string) ([]string, bool) {
	if values, ok := h.labelMap[fieldName]; ok {
		return values, true
	} else {
		return nil, false
	}
}

func (h *LabelMapFactory) highlightString(text string, keywords []string) string {
	if text == "" || len(keywords) == 0 {
		return text
	}

	analyzablePart, remainingPart := h.splitTextForAnalysis(text)

	// 移除 keywords 中存在叠加的数据，例如: ["a", "abc"]，则只保留 ["abc"]
	// 排序后，长的关键词在前面
	sort.SliceStable(keywords, func(i, j int) bool {
		return len(keywords[i]) > len(keywords[j])
	})
	var newKeywords []string
	for _, keyword := range keywords {
		isContains := func() bool {
			for _, newKeyword := range newKeywords {
				if strings.Contains(newKeyword, keyword) {
					return true
				}
			}
			return false
		}()
		if !isContains {
			newKeywords = append(newKeywords, keyword)
		}
	}

	for _, keyword := range newKeywords {
		analyzablePart = strings.ReplaceAll(analyzablePart, keyword, fmt.Sprintf("<mark>%s</mark>", keyword))
	}

	return analyzablePart + remainingPart
}

func (h *LabelMapFactory) splitTextForAnalysis(text string) (analyzable, remaining string) {
	if h.highlightMaxAnalyzedOffset > 0 && len(text) > h.highlightMaxAnalyzedOffset {
		return text[:h.highlightMaxAnalyzedOffset], text[h.highlightMaxAnalyzedOffset:]
	}
	return text, ""
}
