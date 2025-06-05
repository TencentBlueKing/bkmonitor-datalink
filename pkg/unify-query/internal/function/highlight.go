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

// HighLight 处理高亮功能，直接接受labelMap和maxAnalyzedOffset参数
func HighLight(data map[string]any, labelMap map[string][]string, maxAnalyzedOffset int) (newData map[string]any) {
	if len(labelMap) == 0 {
		return nil
	}

	newData = make(map[string]any)

	for k, vs := range labelMap {
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
				if maxAnalyzedOffset > 0 && len(s) > maxAnalyzedOffset {
					mark1 = s[0:maxAnalyzedOffset]
					mark2 = s[maxAnalyzedOffset:]
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
					if maxAnalyzedOffset > 0 && len(v) > maxAnalyzedOffset {
						mark1 = v[0:maxAnalyzedOffset]
						mark2 = v[maxAnalyzedOffset:]
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
