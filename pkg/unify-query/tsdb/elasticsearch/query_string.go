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

	elastic "github.com/olivere/elastic/v7"

	qs "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/querystring"
)

type QueryString struct {
	q     string
	query elastic.Query

	checkNestedField func(string) string

	nestedFields map[string]struct{}
}

// NewQueryString 解析 es query string，该逻辑暂时不使用，直接透传 query string 到 es 代替
func NewQueryString(q string, checkNestedField func(string) string) *QueryString {
	return &QueryString{
		q:                q,
		query:            elastic.NewBoolQuery(),
		checkNestedField: checkNestedField,
		nestedFields:     make(map[string]struct{}),
	}
}

func (s *QueryString) NestedFields() map[string]struct{} {
	return s.nestedFields
}

func (s *QueryString) queryString(str string) elastic.Query {
	return elastic.NewQueryStringQuery(str).AnalyzeWildcard(true)
}

func (s *QueryString) ToDSL() (elastic.Query, error) {
	if s.q == "" || s.q == "*" {
		return nil, nil
	}

	q := s.queryString(s.q)
	ast, err := qs.Parse(s.q)
	if err != nil {
		return q, nil
	}

	conditionQuery, err := s.walk(ast)
	if err != nil {
		return q, nil
	}

	if len(s.nestedFields) == 0 {
		return q, nil
	}

	for nestedKey := range s.nestedFields {
		conditionQuery = elastic.NewNestedQuery(nestedKey, conditionQuery)
	}

	return conditionQuery, nil
}

func (s *QueryString) check(field string) {
	if key := s.checkNestedField(field); key != "" {
		if _, ok := s.nestedFields[key]; !ok {
			s.nestedFields[key] = struct{}{}
		}
	}
}

func (s *QueryString) walk(expr qs.Expr) (elastic.Query, error) {
	var (
		leftQ  elastic.Query
		rightQ elastic.Query
		err    error
	)
	switch c := expr.(type) {
	case *qs.NotExpr:
		leftQ, err = s.walk(c.Expr)
		if err != nil {
			return nil, err
		}
		leftQ = elastic.NewBoolQuery().MustNot(leftQ)
	case *qs.OrExpr:
		leftQ, err = s.walk(c.Left)
		if err != nil {
			return nil, err
		}
		rightQ, err = s.walk(c.Right)
		if err != nil {
			return nil, err
		}
		leftQ = elastic.NewBoolQuery().Should(leftQ, rightQ)
	case *qs.AndExpr:
		leftQ, err = s.walk(c.Left)
		if err != nil {
			return nil, err
		}
		rightQ, err = s.walk(c.Right)
		if err != nil {
			return nil, err
		}
		leftQ = elastic.NewBoolQuery().Must(leftQ, rightQ)
	case *qs.MatchExpr:
		if c.Field != "" {
			leftQ = elastic.NewMatchPhraseQuery(c.Field, c.Value)
			s.check(c.Field)
		} else {
			leftQ = s.queryString(fmt.Sprintf(`"%s"`, c.Value))
		}
	case *qs.NumberRangeExpr:
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
		s.check(c.Field)
		leftQ = q
	case *qs.WildcardExpr:
		if c.Field != "" {
			leftQ = elastic.NewWildcardQuery(c.Field, c.Value)
			s.check(c.Field)
		} else {
			leftQ = s.queryString(c.Value)
		}
	default:
		err = fmt.Errorf("expr type is not match %T", expr)
	}
	return leftQ, err
}
