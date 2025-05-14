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
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	StepString = "."
)

func mapData(prefix string, data map[string]any, res map[string]any) {
	for k, v := range data {
		if prefix != "" {
			k = prefix + StepString + k
		}
		switch v.(type) {
		case map[string]any:
			mapData(k, v.(map[string]any), res)
		default:
			res[k] = v
		}
	}
}

// ParseObject 解析 json，按照层级打平
func ParseObject(prefix, intput string) (map[string]any, error) {
	oldData := make(map[string]any)
	newData := make(map[string]any)

	err := json.Unmarshal([]byte(intput), &oldData)
	if err != nil {
		return newData, err
	}

	mapData(prefix, oldData, newData)
	return newData, nil
}

func MarshalListMap(data []map[string]interface{}) string {
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
			m = append(m, fmt.Sprintf(`"%s":"%v"`, k, d[k]))
		}
		s = append(s, strings.Join(m, ","))
	}

	return fmt.Sprintf(`[{%s}]`, strings.Join(s, "},{"))
}
