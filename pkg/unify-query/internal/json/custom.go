// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package json

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/precision"
)

const (
	StepString = "."
)

func mapData(prefix string, data map[string]any, res map[string]any) {
	for k, v := range data {
		if prefix != "" {
			k = prefix + StepString + k
		}
		switch nv := v.(type) {
		case map[string]any:
			mapData(k, nv, res)
		default:
			res[k] = precision.ProcessValue(nv)
		}
	}
}

// ParseObject 解析 json，按照层级打平
func ParseObject(prefix, intput string) (map[string]any, error) {
	oldData := make(map[string]any)
	newData := make(map[string]any)

	// 使用标准库的 json.Decoder，因为需要 UseNumber() 功能
	decoder := json.NewDecoder(strings.NewReader(intput))
	decoder.UseNumber()
	err := decoder.Decode(&oldData)
	if err != nil {
		return newData, err
	}

	mapData(prefix, oldData, newData)
	return newData, nil
}

func MarshalListMap(data []map[string]any) string {
	if len(data) == 0 {
		return "[]"
	}

	var (
		s  []string
		ks []string
	)
	for _, d := range data {
		if len(ks) == 0 {
			for k := range d {
				ks = append(ks, k)
			}
			sort.Strings(ks)
		}

		var m []string
		for _, k := range ks {
			value := d[k]
			var valueStr string

			// 处理不同类型的值
			switch v := value.(type) {
			case string:
				valueStr = fmt.Sprintf(`"%s"`, v)
			case map[string]any, []any:
				// 对于复杂类型，使用 JSON 序列化，不转义 HTML
				var buf bytes.Buffer
				// 使用标准库的 json.Encoder，因为需要 SetEscapeHTML() 功能
				encoder := json.NewEncoder(&buf)
				encoder.SetEscapeHTML(false)
				if err := encoder.Encode(v); err == nil {
					// 移除编码器添加的换行符
					valueStr = strings.TrimSpace(buf.String())
				} else {
					valueStr = fmt.Sprintf(`"%v"`, v)
				}
			default:
				// 对于其他类型（数字、布尔值等），直接转换
				valueStr = fmt.Sprintf(`%v`, v)
			}

			m = append(m, fmt.Sprintf(`"%s":%s`, k, valueStr))
		}
		s = append(s, strings.Join(m, ","))
	}

	return fmt.Sprintf(`[{%s}]`, strings.Join(s, "},{"))
}
