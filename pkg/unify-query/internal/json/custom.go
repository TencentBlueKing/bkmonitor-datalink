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
	"fmt"
	"sort"
	"strings"
)

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
