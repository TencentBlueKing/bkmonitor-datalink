// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package querystring

func LabelMap(query string) map[string][]string {
	if query == "" || query == "*" {
		return nil
	}

	labelMap := make(map[string][]string)

	expr, err := Parse(query)
	if err != nil || expr == nil {
		return labelMap
	}

	parseExprToKeyValue(query, expr, labelMap)

	return labelMap
}

func parseExprToKeyValue(query string, expr Expr, kv map[string][]string) {
	if expr == nil {
		return
	}

	switch e := expr.(type) {
	case *NotExpr:
		parseExprToKeyValue(query, e.Expr, kv)
	case *OrExpr:
		parseExprToKeyValue(query, e.Left, kv)
		parseExprToKeyValue(query, e.Right, kv)
	case *AndExpr:
		parseExprToKeyValue(query, e.Left, kv)
		parseExprToKeyValue(query, e.Right, kv)
	case *WildcardExpr:
		field := e.Field
		if field == "" {
			field = "log" // 默认字段，与 Doris 的 DefaultKey 一致
		}
		addValueToMap(kv, field, e.Value)
	case *MatchExpr:
		field := e.Field
		if field == "" {
			field = "log" // 默认字段，与 Doris 的 DefaultKey 一致
		}
		addValueToMap(kv, field, e.Value)
	case *NumberRangeExpr:
		// NumberRangeExpr 通常用于数值范围查询，不提取为标签
	}
}

func addValueToMap(kv map[string][]string, field, value string) {
	if value == "" {
		return
	}

	for _, v := range kv[field] {
		if v == value {
			return
		}
	}

	kv[field] = append(kv[field], value)
}
