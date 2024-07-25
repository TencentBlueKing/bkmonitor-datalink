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
	"fmt"

	parser "github.com/bytedance/go-querystring-parser"
	"github.com/olivere/elastic/v7"
)

type QueryString struct {
	q     string
	query elastic.Query

	nestedField func(string) string
}

// NewQueryString 解析 es query string，该逻辑暂时不使用，直接透传 query string 到 es 代替
func NewQueryString(q string, nestedField func(string) string) *QueryString {
	return &QueryString{
		q:           q,
		query:       elastic.NewBoolQuery(),
		nestedField: nestedField,
	}
}

func (s *QueryString) Parser() (elastic.Query, error) {
	if s.q == "" {
		return s.query, nil
	}
	ast, err := parser.Parse(s.q)
	if err != nil {
		return s.query, err
	}

	return s.walk(ast)
}

func (s *QueryString) walk(condition parser.Condition) (elastic.Query, error) {
	var (
		leftQ  elastic.Query
		rightQ elastic.Query
		err    error
	)
	switch c := condition.(type) {
	case *parser.NotCondition:
		leftQ, err = s.walk(c.Condition)
		if err != nil {
			return nil, err
		}
		leftQ = elastic.NewBoolQuery().MustNot(leftQ)
	case *parser.OrCondition:
		leftQ, err = s.walk(c.Left)
		if err != nil {
			return nil, err
		}
		rightQ, err = s.walk(c.Right)
		if err != nil {
			return nil, err
		}
		leftQ = elastic.NewBoolQuery().Should(leftQ, rightQ)
	case *parser.AndCondition:
		leftQ, err = s.walk(c.Left)
		if err != nil {
			return nil, err
		}
		rightQ, err = s.walk(c.Right)
		if err != nil {
			return nil, err
		}
		leftQ = elastic.NewBoolQuery().Must(leftQ, rightQ)
	case *parser.MatchCondition:
		if c.Field != "" {
			leftQ = elastic.NewMatchPhraseQuery(c.Field, c.Value)
			if key := s.nestedField(c.Field); key != "" {
				leftQ = elastic.NewNestedQuery(key, leftQ)
			}
		} else {
			leftQ = elastic.NewQueryStringQuery(c.Value)
		}
	case *parser.NumberRangeCondition:
		q := elastic.NewRangeQuery(c.Field)
		if c.IncludeStart {
			q.Gte(*c.Start)
		} else {
			q.Gt(*c.Start)
		}
		if c.IncludeEnd {
			q.Lte(*c.End)
		} else {
			q.Lt(*c.End)
		}
		if key := s.nestedField(c.Field); key != "" {
			leftQ = elastic.NewNestedQuery(key, q)
		}
		leftQ = q
	case *parser.WildcardCondition:
		if c.Field != "" {
			leftQ = elastic.NewWildcardQuery(c.Field, c.Value)
			if key := s.nestedField(c.Field); key != "" {
				leftQ = elastic.NewNestedQuery(key, leftQ)
			}
		} else {
			leftQ = elastic.NewQueryStringQuery(c.Value)
		}
	default:
		err = fmt.Errorf("condition type is not match %T", condition)
	}
	return leftQ, err
}
