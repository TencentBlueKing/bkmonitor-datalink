// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"encoding/json"
	"fmt"

	parser "github.com/bytedance/go-querystring-parser"
)

type Field struct {
	Key      string
	Value    string
	Operator string
}

type Conditions struct {
	FieldList     []Field
	ConditionList []string
}

type QueryString struct {
	q string

	Conditions Conditions
}

func NewQueryString(q string) *QueryString {
	return &QueryString{
		q: q,
		Conditions: Conditions{
			FieldList:     make([]Field, 0),
			ConditionList: make([]string, 0),
		},
	}
}

func (s *QueryString) ToDsl() error {
	if s.q == "" {
		return nil
	}
	ast, err := parser.Parse(s.q)
	if err != nil {
		return err
	}

	s.Walk(ast)

	cs, err := json.Marshal(s.Conditions)
	if err != nil {
		return err
	}

	fmt.Println(string(cs))

	return nil
}

func (s *QueryString) Walk(conditions ...parser.Condition) {

	for _, condition := range conditions {
		switch c := condition.(type) {
		case *parser.OrCondition:
			s.Walk(c.Left, c.Right)
			s.Conditions.ConditionList = append(
				s.Conditions.ConditionList,
				"or",
			)
		case *parser.AndCondition:
			s.Walk(c.Left, c.Right)
			s.Conditions.ConditionList = append(
				s.Conditions.ConditionList,
				"and",
			)
		case *parser.MatchCondition:
			s.Conditions.FieldList = append(s.Conditions.FieldList, Field{
				Key:      c.Field,
				Value:    c.Value,
				Operator: "eq",
			})
		case *parser.NumberRangeCondition:
			var operator string
			if c.IncludeStart {
				operator = "gte"
			} else {
				operator = "gt"
			}
			s.Conditions.FieldList = append(s.Conditions.FieldList, Field{
				Key:      c.Field,
				Value:    *c.Start,
				Operator: operator,
			})

			if c.IncludeStart {
				operator = "lte"
			} else {
				operator = "lt"
			}
			s.Conditions.FieldList = append(s.Conditions.FieldList, Field{
				Key:      c.Field,
				Value:    *c.End,
				Operator: operator,
			})

			s.Conditions.ConditionList = append(
				s.Conditions.ConditionList,
				"and",
			)
		case *parser.WildcardCondition:
			s.Conditions.FieldList = append(s.Conditions.FieldList, Field{
				Key:      c.Field,
				Value:    c.Value,
				Operator: "reg",
			})
		default:
			panic(fmt.Sprintf("type is wrong %T", c))
		}
	}
}
