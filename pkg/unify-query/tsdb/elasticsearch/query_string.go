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
	"context"
	"fmt"

	elastic "github.com/olivere/elastic/v7"

	qs "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/querystring_parser"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

type QueryString struct {
	q        string
	query    elastic.Query
	isPrefix bool

	checkNestedField func(string) string
	nestedFields     map[string]struct{}
}

// NewQueryString 解析 es query string，该逻辑暂时不使用，直接透传 query string 到 es 代替
func NewQueryString(q string, isPrefix bool, checkNestedField func(string) string) *QueryString {
	return &QueryString{
		q:                q,
		isPrefix:         isPrefix,
		query:            elastic.NewBoolQuery(),
		checkNestedField: checkNestedField,
		nestedFields:     make(map[string]struct{}),
	}
}

func (s *QueryString) NestedFields() map[string]struct{} {
	return s.nestedFields
}

func (s *QueryString) queryString(str string) elastic.Query {
	q := elastic.NewQueryStringQuery(str).AnalyzeWildcard(true).Field("*").Field("__*").Lenient(true)
	if s.isPrefix {
		q.Type("phrase_prefix")
	}
	return q
}

func (s *QueryString) ToDSL(ctx context.Context, fieldAlias metadata.FieldAlias) (elastic.Query, error) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf(ctx, "querystring(%s) todsl panic: %v", s.q, r)
		}
	}()

	if s.q == "" || s.q == "*" {
		return nil, nil
	}

	// 解析失败，或者没有 nested 字段，则使用透传的方式查询
	q := s.queryString(s.q)
	ast, err := qs.ParseWithFieldAlias(s.q, fieldAlias)
	if err != nil {
		log.Errorf(ctx, "querystring(%s) parse error: %v", s.q, err)
		return q, nil
	}

	conditionQuery, err := s.walk(ast)
	if err != nil {
		log.Errorf(ctx, "querystring(%s) walk error: %v", s.q, err)
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
	case *qs.RegexpExpr:
		if c.Field != "" {
			leftQ = elastic.NewRegexpQuery(c.Field, c.Value)
			s.check(c.Field)
		} else {
			val := c.Value
			// 保留正则的识别/
			val = fmt.Sprintf(`/%s/`, val)
			leftQ = s.queryString(val)
		}
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
			if s.isPrefix {
				leftQ = elastic.NewMatchPhrasePrefixQuery(c.Field, c.Value)
			} else {
				leftQ = elastic.NewMatchPhraseQuery(c.Field, c.Value)
			}
			s.check(c.Field)
		} else {
			val := c.Value
			// 为了保证保留传递的双引号，所以进来必须拼接一个，保证字符串的完整
			val = fmt.Sprintf(`"%s"`, val)
			leftQ = s.queryString(val)
		}
	case *qs.ConditionMatchExpr:
		if len(c.Value.Values) == 1 {
			row := c.Value.Values[0]
			if len(row) == 1 {
				leftQ = elastic.NewTermQuery(c.Field, row[0])
			} else {
				boolQuery := elastic.NewBoolQuery()
				for _, value := range row {
					boolQuery.Must(elastic.NewTermQuery(c.Field, value))
				}
				leftQ = boolQuery
			}
		} else {
			// 多行，使用 OR 逻辑（should 查询）
			boolQuery := elastic.NewBoolQuery()
			for _, row := range c.Value.Values {
				var rowQuery elastic.Query
				if len(row) == 1 {
					// 单行单个值
					rowQuery = elastic.NewTermQuery(c.Field, row[0])
				} else {
					// 单行多个值，使用 AND 逻辑
					rowBoolQuery := elastic.NewBoolQuery()
					for _, value := range row {
						rowBoolQuery.Must(elastic.NewTermQuery(c.Field, value))
					}
					rowQuery = rowBoolQuery
				}
				boolQuery.Should(rowQuery)
			}
			leftQ = boolQuery
		}
		s.check(c.Field)

	case *qs.NumberRangeExpr:
		q := elastic.NewRangeQuery(c.Field)
		if c.Start == nil && c.End == nil {
			return nil, fmt.Errorf("start and end is nil")
		}

		if c.Start != nil && *c.Start != "*" {
			if c.IncludeStart {
				q.Gte(*c.Start)
			} else {
				q.Gt(*c.Start)
			}
		}
		if c.End != nil && *c.End != "*" {
			if c.IncludeEnd {
				q.Lte(*c.End)
			} else {
				q.Lt(*c.End)
			}
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
