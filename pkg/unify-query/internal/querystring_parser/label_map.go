// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package querystring_parser

func LabelMap(query string, addLabel func(key string, operator string, values ...string)) error {
	expr, err := Parse(query)
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
	case *WildcardExpr:
		addLabel(e.Field, "contains", e.Value)
	case *MatchExpr:
		addLabel(e.Field, "eq", e.Value)
	case *ConditionMatchExpr:
		for _, arr := range e.Value.Values {
			for _, v := range arr {
				addLabel(e.Field, "eq", v)
			}
		}
	default:
		return nil
	}
	return nil
}
