// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bytes"
	"sort"
)

func toPromFormat(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteString(sliMetric)
	buf.WriteString(`{`)

	var n int
	for _, k := range keys {
		if n > 0 {
			buf.WriteString(`,`)
		}
		n++
		buf.WriteString(k)
		buf.WriteString(`="`)
		buf.WriteString(labels[k])
		buf.WriteString(`"`)
	}
	buf.WriteString("} 1")

	return buf.String()
}
