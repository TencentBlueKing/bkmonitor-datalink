// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sql_parser

import (
	"fmt"
	"strings"
)

type walkParse struct {
	fieldAlias map[string]string
	fieldMap   map[string]string
}

func (w *walkParse) alias(k string) string {
	if alias, ok := w.fieldAlias[k]; ok {
		return alias
	}
	return k
}

func (w *walkParse) walk(q string) string {
	for alias, key := range w.fieldAlias {
		// 判断别名是否在 sql 语句中
		if strings.Contains(q, alias) {
			fieldType, _ := w.fieldMap[key]

			values := strings.Split(key, ".")
			if len(values) > 1 {
				if fieldType == "string" {
					key = fmt.Sprintf("JSON_EXTRACT_STRING(%s, '$.%s')", values[0], strings.Join(values[1:], "."))
				}
			}

			q = strings.ReplaceAll(q, alias, key)
		}
	}
	return q
}

func ParseWithFieldAlias(query string, fieldAlias, fieldMap map[string]string) (string, error) {
	p := &walkParse{
		fieldAlias: fieldAlias,
		fieldMap:   fieldMap,
	}

	return p.walk(query), nil
}
