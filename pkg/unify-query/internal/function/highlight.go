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
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	KeyHighLight = "__highlight"
)

type HighLightFactory struct {
	labelMap          map[string][]LabelMapValue
	maxAnalyzedOffset int
}

type LabelMapValue struct {
	Value    string `json:"value"`
	Operator string `json:"operator"`
}

func NewHighLightFactory(labelMap map[string][]LabelMapValue, maxAnalyzedOffset int) *HighLightFactory {
	return &HighLightFactory{
		labelMap:          labelMap,
		maxAnalyzedOffset: maxAnalyzedOffset,
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

	var newValue string
	switch value := fieldValue.(type) {
	case string:
		newValue = value
	case int, int32, int64, uint32, uint64:
		newValue = fmt.Sprintf("%d", value)
	case float64:
		newValue = strconv.FormatFloat(value, 'f', -1, 64)
	case float32:
		newValue = strconv.FormatFloat(float64(value), 'f', -1, 32)
	default:
		newValue = fmt.Sprintf("%v", value)
	}

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

	// 移除 keywords 中存在叠加的数据，例如: ["a", "abc"]，则只保留 ["abc"]
	// 排序后，长的关键词在前面
	sort.SliceStable(keywords, func(i, j int) bool {
		return len(keywords[i].Value) > len(keywords[j].Value)
	})
	var newKeywords []string
	for _, keyword := range keywords {
		check := func() bool {
			// 检查是否已经叠加
			for _, newKeyword := range newKeywords {
				if strings.Contains(newKeyword, keyword.Value) {
					return true
				}
			}
			return false
		}()

		if !check {
			// 高亮替换需要把头尾的*去掉
			nv := strings.Trim(keyword.Value, "*")

			// 如果为空的情况下不要进行判定
			if nv != "" {
				newKeywords = append(newKeywords, nv)
			}
		}
	}

	for _, keyword := range newKeywords {
		if keyword == "" {
			continue
		}

		re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(keyword))
		matches := re.FindAllString(analyzablePart, -1)
		for _, m := range matches {
			if m != "" {
				analyzablePart = strings.ReplaceAll(analyzablePart, m, fmt.Sprintf("<mark>%s</mark>", m))
			}
		}
	}

	return analyzablePart + remainingPart
}

func (h *HighLightFactory) splitTextForAnalysis(text string) (analyzable, remaining string) {
	if h.maxAnalyzedOffset > 0 && len(text) > h.maxAnalyzedOffset {
		return text[:h.maxAnalyzedOffset], text[h.maxAnalyzedOffset:]
	}
	return text, ""
}
