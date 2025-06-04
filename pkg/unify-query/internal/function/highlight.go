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

type HighLightStruct struct {
	LabelMap          map[string][]string `json:"label_map,omitempty"`
	MaxAnalyzedOffset int                 `json:"max_analyzed_offset,omitempty"`
}

type HighLightOption func(h *HighLightStruct)

func WithLabelMap(labelMap map[string][]string) HighLightOption {
	return func(h *HighLightStruct) {
		if h.LabelMap == nil {
			h.LabelMap = make(map[string][]string)
		}
		for k, v := range labelMap {
			h.LabelMap[k] = v
		}
	}
}

func WithMaxAnalyzedOffset(maxAnalyzedOffset int) HighLightOption {
	return func(h *HighLightStruct) {
		h.MaxAnalyzedOffset = maxAnalyzedOffset
	}
}

func NewHighLightStruct(options ...HighLightOption) *HighLightStruct {
	h := &HighLightStruct{
		LabelMap:          make(map[string][]string),
		MaxAnalyzedOffset: 0,
	}
	for _, opt := range options {
		opt(h)
	}
	return h
}

func HighLight(data map[string]any, options ...HighLightOption) (newData map[string]any) {
	if len(options) == 0 {
		return data
	}

	h := NewHighLightStruct(options...)

	newData = make(map[string]any)
	processedCount := 0

	for k, vs := range h.LabelMap {
		processedCount++

		if vs == nil {
			continue
		}

		if d, ok := data[k]; ok {
			var (
				mark1 string
				mark2 string
			)

			switch s := d.(type) {
			case string:
				if h.MaxAnalyzedOffset > 0 && len(s) > h.MaxAnalyzedOffset {
					mark1 = s[0:h.MaxAnalyzedOffset]
					mark2 = s[h.MaxAnalyzedOffset:]
				} else {
					mark1 = s
				}

				for _, v := range vs {
					mark1 = strings.ReplaceAll(mark1, v, fmt.Sprintf("<mark>%s</mark>", v))
				}

				res := fmt.Sprintf("%s%s", mark1, mark2)
				if res != d {
					newData[k] = []string{res}
				}
			case []string:
				newArrData := make([]string, 0, len(s))
				for _, v := range s {
					if h.MaxAnalyzedOffset > 0 && len(v) > h.MaxAnalyzedOffset {
						mark1 = v[0:h.MaxAnalyzedOffset]
						mark2 = v[h.MaxAnalyzedOffset:]
					} else {
						mark1 = v
					}

					for _, highlight := range vs {
						mark1 = strings.ReplaceAll(mark1, highlight, fmt.Sprintf("<mark>%s</mark>", highlight))
					}

					res := fmt.Sprintf("%s%s", mark1, mark2)
					if res != v {
						newArrData = append(newArrData, res)
					}
				}
				if len(newArrData) > 0 {
					newData[k] = newArrData
				}
			}

		}
	}

	return
}
