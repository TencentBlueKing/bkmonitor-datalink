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
	"strings"
)

const (
	KeyHighLight = "__highlight"
)

type HighLightFactory struct {
	labelMap          map[string][]string
	maxAnalyzedOffset int
}

func NewHighLightFactory(labelMap map[string][]string, maxAnalyzedOffset int) *HighLightFactory {
	return &HighLightFactory{
		labelMap:          labelMap,
		maxAnalyzedOffset: maxAnalyzedOffset,
	}
}

func (h *HighLightFactory) Process(data map[string]any) (newData map[string]any) {
	newData = make(map[string]any)

	for k, keywords := range h.labelMap {
		if keywords == nil {
			continue
		}

		if fieldValue, exists := data[k]; exists {
			if highlightedValue := h.processField(fieldValue, keywords); highlightedValue != nil {
				newData[k] = highlightedValue
			}
		}
	}

	return newData
}

func (h *HighLightFactory) processField(fieldValue any, keywords []string) any {
	var newValue string
	switch value := fieldValue.(type) {
	case string:
		newValue = value
	case int:
		newValue = fmt.Sprintf("%d", value)
	default:
		newValue = ""
		return nil
	}

	if highlighted := h.highlightString(newValue, keywords); highlighted != newValue {
		return []string{highlighted}
	}
	return nil
}

func (h *HighLightFactory) highlightString(text string, keywords []string) string {
	if text == "" || len(keywords) == 0 {
		return text
	}

	analyzablePart, remainingPart := h.splitTextForAnalysis(text)

	for _, keyword := range keywords {
		analyzablePart = strings.ReplaceAll(analyzablePart, keyword, fmt.Sprintf("<mark>%s</mark>", keyword))
	}

	return analyzablePart + remainingPart
}

func (h *HighLightFactory) splitTextForAnalysis(text string) (analyzable, remaining string) {
	if h.maxAnalyzedOffset > 0 && len(text) > h.maxAnalyzedOffset {
		return text[:h.maxAnalyzedOffset], text[h.maxAnalyzedOffset:]
	}
	return text, ""
}
