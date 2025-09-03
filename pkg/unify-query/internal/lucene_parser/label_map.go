// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lucene_parser

func LabelMap(query string, addLabel func(key string, operator string, values ...string)) error {
	expr, err := buildExpr(query)
	if err != nil {
		return err
	}

	if err = parseExprToKeyValue(expr, addLabel); err != nil {
		return err
	}

	return nil
}

// parseExprToKeyValue 因为我们并不知道 queryString 中的表达式是否需要被include到 labelMap 中，所以没有是否为positive的判断
func parseExprToKeyValue(expr Expr, addLabel func(key string, operator string, values ...string)) error {
	if expr == nil {
		return nil
	}

	switch e := expr.(type) {
	case *NotExpr:
		// 如果是not表达式，直接返回
		return nil
	case *OrExpr:
		if err := parseExprToKeyValue(e.Left, addLabel); err != nil {
			return err
		}
		if err := parseExprToKeyValue(e.Right, addLabel); err != nil {
			return err
		}
	case *AndExpr:
		if err := parseExprToKeyValue(e.Left, addLabel); err != nil {
			return err
		}
		if err := parseExprToKeyValue(e.Right, addLabel); err != nil {
			return err
		}
	case *GroupingExpr:
		// 递归处理分组表达式
		if err := parseExprToKeyValue(e.Expr, addLabel); err != nil {
			return err
		}
	case *OperatorExpr:
		// 处理统一的操作表达式
		fieldStr := extractStringValue(e.Field)
		valueStr := extractStringValue(e.Value)

		if fieldStr == "" && valueStr == "" {
			return nil
		}

		// 跳过单独的通配符查询，因为它们不适合标签索引
		if fieldStr == "" && valueStr == "*" {
			return nil
		}

		switch e.Op {
		case OpMatch:
			addLabel(fieldStr, "eq", valueStr)
		case OpWildcard:
			addLabel(fieldStr, "contains", valueStr)
		case OpRegex:
			addLabel(fieldStr, "regex", valueStr)
		case OpRange:
			// 范围查询通常不适合用于标签索引，跳过
			return nil
		}
	case *ConditionMatchExpr:
		// 处理条件匹配表达式
		fieldStr := extractStringValue(e.Field)
		if fieldStr == "" {
			return nil
		}

		if e.Value != nil {
			for _, values := range e.Value.Values {
				for _, value := range values {
					valueStr := extractStringValue(value)
					if valueStr != "" {
						addLabel(fieldStr, "eq", valueStr)
					}
				}
			}
		}
	default:
		return nil
	}
	return nil
}

// extractStringValue 从表达式中提取字符串值
func extractStringValue(expr Expr) string {
	if expr == nil {
		return ""
	}

	switch e := expr.(type) {
	case *StringExpr:
		return e.Value
	case *NumberExpr:
		// 可以考虑将数值转换为字符串，但通常数值不用于标签索引
		return ""
	case *BoolExpr:
		// 布尔值通常不用于标签索引
		return ""
	default:
		return ""
	}
}
