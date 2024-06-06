// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"fmt"

	parser "github.com/bytedance/go-querystring-parser"
)

type QueryString struct {
	q string

	Conditions Conditions
}

// NewQueryString 解析 es query string，该逻辑暂时不使用，直接透传 query string 到 es 代替
func NewQueryString(q string) *QueryString {
	return &QueryString{
		q: q,
		Conditions: Conditions{
			FieldList:     make([]ConditionField, 0),
			ConditionList: make([]string, 0),
		},
	}
}

func (s *QueryString) Parser() error {
	if s.q == "" {
		return nil
	}
	ast, err := parser.Parse(s.q)
	if err != nil {
		return err
	}

	s.Walk(ast)
	return nil
}

func (s *QueryString) Walk(conditions ...parser.Condition) {
	for _, condition := range conditions {
		switch c := condition.(type) {
		case *parser.OrCondition:
			s.Walk(c.Left, c.Right)
			s.Conditions.ConditionList = append(
				s.Conditions.ConditionList,
				ConditionOr,
			)
		case *parser.AndCondition:
			s.Walk(c.Left, c.Right)
			s.Conditions.ConditionList = append(
				s.Conditions.ConditionList,
				ConditionAnd,
			)
		case *parser.MatchCondition:
			s.Conditions.FieldList = append(s.Conditions.FieldList, ConditionField{
				DimensionName: c.Field,
				Value:         []string{c.Value},
				Operator:      ConditionEqual,
			})
		case *parser.NumberRangeCondition:
			var operator string
			if c.IncludeStart {
				operator = ConditionGte
			} else {
				operator = ConditionGt
			}
			s.Conditions.FieldList = append(s.Conditions.FieldList, ConditionField{
				DimensionName: c.Field,
				Value:         []string{*c.Start},
				Operator:      operator,
			})

			if c.IncludeStart {
				operator = ConditionLte
			} else {
				operator = ConditionLt
			}
			s.Conditions.FieldList = append(s.Conditions.FieldList, ConditionField{
				DimensionName: c.Field,
				Value:         []string{*c.End},
				Operator:      operator,
			})

			s.Conditions.ConditionList = append(
				s.Conditions.ConditionList,
				ConditionAnd,
			)
		case *parser.WildcardCondition:
			s.Conditions.FieldList = append(s.Conditions.FieldList, ConditionField{
				DimensionName: c.Field,
				Value:         []string{c.Value},
				Operator:      ConditionEqual,
			})
		default:
			panic(fmt.Sprintf("type is wrong %T", c))
		}
	}
}
