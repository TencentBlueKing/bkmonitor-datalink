// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lucene_parser

import (
	"strings"

	elastic "github.com/olivere/elastic/v7"
)

const Separator = "."

func (p *Parser) toES(expr Expr, isPrefix bool) elastic.Query {
	baseQuery := p.walkEs(expr, isPrefix, false)

	if baseQuery != nil {
		fieldName := p.getFieldNameFromExprWithSchema(expr)
		if path, isNested := p.esSchema.getNestedPath(fieldName); isNested {
			return elastic.NewNestedQuery(path, baseQuery)
		}
	}

	return baseQuery
}

func (p *Parser) walkEs(expr Expr, isPrefix bool, allowNestedWrap bool) elastic.Query {
	if expr == nil {
		return nil
	}

	var baseQuery elastic.Query

	switch e := expr.(type) {
	case *AndExpr:
		return p.buildAndQueryWithSchema(e, isPrefix)

	case *OrExpr:
		return p.buildOrQueryWithSchema(e, isPrefix)

	case *NotExpr:
		innerQuery := p.walkEs(e.Expr, isPrefix, allowNestedWrap)
		if innerQuery == nil {
			return nil
		}
		return elastic.NewBoolQuery().MustNot(innerQuery)
	case *GroupingExpr:
		baseQuery = p.walkEs(e.Expr, isPrefix, allowNestedWrap)
		if baseQuery != nil && e.Boost > 0 && e.Boost != 1.0 {
			if boostedQuery := applyBoostToQuery(baseQuery, e.Boost); boostedQuery != nil {
				baseQuery = boostedQuery
			}
		}
		return baseQuery

	case *OperatorExpr:
		switch e.Op {
		case OpMatch:
			baseQuery = p.buildOperatorMatchQueryWithSchema(e, isPrefix)
		case OpWildcard:
			baseQuery = p.buildOperatorWildcardQueryWithSchema(e, isPrefix)
		case OpRegex:
			baseQuery = p.buildOperatorRegexpQueryWithSchema(e, isPrefix)
		case OpRange:
			baseQuery = p.buildOperatorRangeQueryWithSchema(e)
		case OpFuzzy:
			baseQuery = p.buildOperatorFuzzyQueryWithSchema(e)
		}

	case *ConditionMatchExpr:
		baseQuery = p.buildConditionMatchQueryWithSchema(e, isPrefix)

	default:
		return nil
	}

	// 检查是否需要nested包装（仅针对单个表达式且允许包装时）
	if baseQuery != nil && allowNestedWrap {
		fieldName := p.getFieldNameFromExprWithSchema(expr)
		if path, isNested := p.esSchema.getNestedPath(fieldName); isNested {
			return elastic.NewNestedQuery(path, baseQuery)
		}
	}

	return baseQuery
}

// getESValueInterface returns the interface{} value for ES queries
func getESValueInterface(expr Expr) any {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case *StringExpr:
		if e.Value == "*" {
			return nil
		}
		return e.Value
	case *NumberExpr:
		return e.Value
	case *BoolExpr:
		return e.Value
	}
	return nil
}

func isSimpleTermsQuery(e *ConditionMatchExpr) bool {
	for _, andGroup := range e.Value.Values {
		if len(andGroup) != 1 {
			return false
		}
	}
	return true
}

func (p *Parser) buildAndQueryWithSchema(e *AndExpr, isPrefix bool) elastic.Query {
	// 1. 收集所有AND条件的表达式
	exprs := collectAndExprs(e)

	// 2. 按nested路径分组
	groupedExprs := make(map[string][]Expr)
	for _, expr := range exprs {
		fieldName := p.getFieldNameFromExprWithSchema(expr)
		if path, isNested := p.esSchema.getNestedPath(fieldName); isNested {
			groupedExprs[path] = append(groupedExprs[path], expr)
		} else {
			// 非nested字段，路径设为空字符串
			groupedExprs[""] = append(groupedExprs[""], expr)
		}
	}

	// 3. 为每个分组构建查询
	finalClauses := make([]elastic.Query, 0)
	for path, expressions := range groupedExprs {
		if path == "" {
			// 处理非nested字段
			for _, exp := range expressions {
				if q := p.walkEs(exp, isPrefix, true); q != nil {
					finalClauses = append(finalClauses, q)
				}
			}
		} else {
			// 处理nested字段
			innerClauses := make([]elastic.Query, 0)
			for _, exp := range expressions {
				if q := p.walkEs(exp, isPrefix, false); q != nil {
					innerClauses = append(innerClauses, q)
				}
			}

			if len(innerClauses) > 0 {
				var nestedQuery elastic.Query
				if len(innerClauses) == 1 {
					nestedQuery = elastic.NewNestedQuery(path, innerClauses[0])
				} else {
					innerBoolQuery := elastic.NewBoolQuery().Must(innerClauses...)
					nestedQuery = elastic.NewNestedQuery(path, innerBoolQuery)
				}
				finalClauses = append(finalClauses, nestedQuery)
			}
		}
	}

	// 4. 组合最终的查询
	if len(finalClauses) == 0 {
		return nil
	}
	if len(finalClauses) == 1 {
		return finalClauses[0]
	}

	return elastic.NewBoolQuery().Must(finalClauses...)
}

func (p *Parser) buildOrQueryWithSchema(e *OrExpr, isPrefix bool) elastic.Query {
	// 1. 收集所有OR条件的表达式
	exprs := collectOrExprs(e)

	// 2. 按nested路径分组
	groupedExprs := make(map[string][]Expr)
	for _, expr := range exprs {
		fieldName := p.getFieldNameFromExprWithSchema(expr)
		if path, isNested := p.esSchema.getNestedPath(fieldName); isNested {
			groupedExprs[path] = append(groupedExprs[path], expr)
		} else {
			// 非nested字段，路径设为空字符串
			groupedExprs[""] = append(groupedExprs[""], expr)
		}
	}

	// 3. 为每个分组构建查询
	finalClauses := make([]elastic.Query, 0)
	for path, expressions := range groupedExprs {
		if path == "" {
			// 处理非nested字段
			for _, exp := range expressions {
				if q := p.walkEs(exp, isPrefix, true); q != nil {
					finalClauses = append(finalClauses, q)
				}
			}
		} else {
			// 处理nested字段
			innerClauses := make([]elastic.Query, 0)
			for _, exp := range expressions {
				if q := p.walkEs(exp, isPrefix, false); q != nil {
					innerClauses = append(innerClauses, q)
				}
			}

			if len(innerClauses) > 0 {
				var nestedQuery elastic.Query
				if len(innerClauses) == 1 {
					nestedQuery = elastic.NewNestedQuery(path, innerClauses[0])
				} else {
					innerBoolQuery := elastic.NewBoolQuery().Should(innerClauses...)
					nestedQuery = elastic.NewNestedQuery(path, innerBoolQuery)
				}
				finalClauses = append(finalClauses, nestedQuery)
			}
		}
	}

	// 4. 组合最终的查询
	if len(finalClauses) == 0 {
		return nil
	}
	if len(finalClauses) == 1 {
		return finalClauses[0]
	}

	boolQuery := elastic.NewBoolQuery().Should(finalClauses...)
	//if len(finalClauses) > 1 {
	//	boolQuery.MinimumShouldMatch("1")
	//}
	return boolQuery
}

func (p *Parser) buildConditionMatchQueryWithSchema(e *ConditionMatchExpr, isPrefix bool) elastic.Query {
	field := p.esSchema.getFieldName(getString(e.Field))
	if e.Value == nil || len(e.Value.Values) == 0 {
		return nil
	}

	fieldType, exist := p.esSchema.getFieldType(field)
	if isSimpleTermsQuery(e) && exist && fieldType == FieldTypeKeyword {
		var terms []any
		for _, andGroup := range e.Value.Values {
			if len(andGroup) == 1 {
				terms = append(terms, getValue(andGroup[0]))
			}
		}
		return elastic.NewTermsQuery(field, terms...)
	}

	boolQuery := elastic.NewBoolQuery().MinimumShouldMatch("1")

	for _, andGroup := range e.Value.Values {
		if len(andGroup) == 0 {
			continue
		}

		if len(andGroup) == 1 {
			value := getValue(andGroup[0])

			if exist {
				switch fieldType {
				case FieldTypeKeyword:
					boolQuery.Should(elastic.NewTermQuery(field, value))
				case FieldTypeText:
					if isPrefix {
						boolQuery.Should(elastic.NewMatchPhrasePrefixQuery(field, value))
					} else {
						boolQuery.Should(elastic.NewMatchPhraseQuery(field, value))
					}
				default:
					if isPrefix {
						boolQuery.Should(elastic.NewMatchPhrasePrefixQuery(field, value))
					} else {
						boolQuery.Should(elastic.NewMatchPhraseQuery(field, value))
					}
				}
			} else {
				if isPrefix {
					boolQuery.Should(elastic.NewMatchPhrasePrefixQuery(field, value))
				} else {
					boolQuery.Should(elastic.NewMatchPhraseQuery(field, value))
				}
			}
		} else {
			andBoolQuery := elastic.NewBoolQuery()
			for _, expr := range andGroup {
				value := getValue(expr)

				if exist && fieldType == FieldTypeKeyword {
					andBoolQuery.Must(elastic.NewTermQuery(field, value))
				} else {
					if isPrefix {
						andBoolQuery.Must(elastic.NewMatchPhrasePrefixQuery(field, value))
					} else {
						andBoolQuery.Must(elastic.NewMatchPhraseQuery(field, value))
					}
				}
			}
			boolQuery.Should(andBoolQuery)
		}
	}

	return boolQuery
}

func (p *Parser) buildOperatorMatchQueryWithSchema(e *OperatorExpr, isPrefix bool) elastic.Query {
	field := p.esSchema.getFieldName(getString(e.Field))
	value := getValue(e.Value)
	valueInterface := getESValueInterface(e.Value)

	fieldType, hasSchema := p.esSchema.getFieldType(field)
	var baseQuery elastic.Query

	if e.IsQuoted {
		if field == Empty && e.Field == nil && e.Slop == 0 {
			baseQuery = createEnhancedQueryStringQuery("\""+value+"\"", isPrefix)
		} else {
			// 对于引号字符串，使用match_phrase查询，并考虑slop
			if e.Slop > 0 {
				// 邻近搜索 (Proximity Search)
				targetField := field
				if targetField == Empty {
					targetField = DefaultEmptyField
				}
				matchPhraseQuery := elastic.NewMatchPhraseQuery(targetField, value).Slop(e.Slop)
				baseQuery = matchPhraseQuery
			} else {
				// 精确短语匹配
				if hasSchema {
					switch fieldType {
					case FieldTypeKeyword:
						baseQuery = elastic.NewTermQuery(field, valueInterface)
					case FieldTypeText:
						if isPrefix {
							baseQuery = elastic.NewMatchPhrasePrefixQuery(field, value)
						} else {
							baseQuery = elastic.NewMatchPhraseQuery(field, value)
						}
					default:
						if isPrefix {
							baseQuery = elastic.NewMatchPhrasePrefixQuery(field, value)
						} else {
							baseQuery = elastic.NewMatchPhraseQuery(field, value)
						}
					}
				} else {
					if isPrefix {
						baseQuery = elastic.NewMatchPhrasePrefixQuery(field, value)
					} else {
						baseQuery = elastic.NewMatchPhraseQuery(field, value)
					}
				}
			}
		}
	} else {
		// 处理非引号字符串
		if hasSchema {
			switch fieldType {
			case FieldTypeKeyword:
				baseQuery = elastic.NewTermQuery(field, valueInterface)
			case FieldTypeText:
				if field == Empty {
					baseQuery = createEnhancedQueryStringQuery(value, isPrefix)
				} else {
					if strings.Contains(value, " ") {
						if isPrefix {
							baseQuery = elastic.NewMatchPhrasePrefixQuery(field, value)
						} else {
							baseQuery = elastic.NewMatchPhraseQuery(field, value)
						}
					} else {
						if isPrefix {
							baseQuery = elastic.NewMatchPhrasePrefixQuery(field, value)
						} else {
							baseQuery = elastic.NewMatchPhraseQuery(field, value)
						}
					}
				}
			case FieldTypeLong, FieldTypeInteger:
				baseQuery = elastic.NewTermQuery(field, valueInterface)
			case FieldTypeFloat, FieldTypeDouble:
				baseQuery = elastic.NewTermQuery(field, valueInterface)
			case FieldTypeBoolean:
				baseQuery = elastic.NewTermQuery(field, valueInterface)
			case FieldTypeDate:
				baseQuery = elastic.NewRangeQuery(field).Gte(valueInterface).Lte(valueInterface)
			default:
				baseQuery = elastic.NewTermQuery(field, valueInterface)
			}
		} else {
			if field == Empty {
				baseQuery = createEnhancedQueryStringQuery(value, isPrefix)
			} else {
				if strings.Contains(value, " ") {
					if isPrefix {
						baseQuery = elastic.NewMatchPhrasePrefixQuery(field, value)
					} else {
						baseQuery = elastic.NewMatchPhraseQuery(field, value)
					}
				} else {
					if _, ok := e.Value.(*NumberExpr); ok {
						baseQuery = elastic.NewTermQuery(field, valueInterface)
					} else {
						baseQuery = elastic.NewTermQuery(field, valueInterface)
					}
				}
			}
		}
	}

	// 应用boost
	if baseQuery != nil && e.Boost > 0 && e.Boost != 1.0 {
		if boostedQuery := applyBoostToQuery(baseQuery, e.Boost); boostedQuery != nil {
			baseQuery = boostedQuery
		}
	}

	return baseQuery
}

func (p *Parser) buildOperatorWildcardQueryWithSchema(e *OperatorExpr, isPrefix bool) elastic.Query {
	field := p.esSchema.getFieldName(getString(e.Field))
	value := getValue(e.Value)

	var baseQuery elastic.Query
	if field == Empty {
		baseQuery = createEnhancedQueryStringQuery(value, isPrefix)
	} else {
		baseQuery = elastic.NewWildcardQuery(field, value)
	}

	// 应用boost
	if baseQuery != nil && e.Boost > 0 && e.Boost != 1.0 {
		if boostedQuery := applyBoostToQuery(baseQuery, e.Boost); boostedQuery != nil {
			baseQuery = boostedQuery
		}
	}

	return baseQuery
}

func (p *Parser) buildOperatorRegexpQueryWithSchema(e *OperatorExpr, isPrefix bool) elastic.Query {
	field := getField(e)
	value := getValue(e.Value)

	var baseQuery elastic.Query
	if field == Empty {
		// 如果没有提供字段名，使用query_string查询
		baseQuery = createEnhancedQueryStringQuery("/"+value+"/", isPrefix)
	} else {
		baseQuery = elastic.NewRegexpQuery(field, value)
	}

	// 应用boost
	if baseQuery != nil && e.Boost > 0 && e.Boost != 1.0 {
		if boostedQuery := applyBoostToQuery(baseQuery, e.Boost); boostedQuery != nil {
			baseQuery = boostedQuery
		}
	}

	return baseQuery
}

func (p *Parser) buildOperatorRangeQueryWithSchema(e *OperatorExpr) elastic.Query {
	field := p.esSchema.getFieldName(getString(e.Field))
	rangeExpr, ok := e.Value.(*RangeExpr)
	if !ok {
		return nil
	}

	query := elastic.NewRangeQuery(field)
	if rangeExpr.Start != nil {
		startValue := getESValueInterface(rangeExpr.Start)
		if startValue != nil {
			if b, ok := rangeExpr.IncludeStart.(*BoolExpr); ok && b.Value {
				query = query.Gte(startValue)
			} else {
				query = query.Gt(startValue)
			}
		} else {
			// When start is "*", we still need to set the include_lower based on IncludeStart
			if b, ok := rangeExpr.IncludeStart.(*BoolExpr); ok {
				query = query.IncludeLower(b.Value)
			}
		}
	}

	if rangeExpr.End != nil {
		endValue := getESValueInterface(rangeExpr.End)
		if endValue != nil {
			if b, ok := rangeExpr.IncludeEnd.(*BoolExpr); ok && b.Value {
				query = query.Lte(endValue)
			} else {
				query = query.Lt(endValue)
			}
		} else {
			// When end is "*", we still need to set the include_upper based on IncludeEnd
			if b, ok := rangeExpr.IncludeEnd.(*BoolExpr); ok {
				query = query.IncludeUpper(b.Value)
			}
		}
	}

	// 应用boost
	if e.Boost > 0 && e.Boost != 1.0 {
		query = query.Boost(e.Boost)
	}

	return query
}

func createEnhancedQueryStringQuery(query string, isPrefix ...bool) elastic.Query {
	q := elastic.NewQueryStringQuery(query).
		AnalyzeWildcard(true).
		Field("*").
		Field("__*").
		Lenient(true)
	if len(isPrefix) > 0 && isPrefix[0] {
		q.Type("phrase_prefix")
	}
	return q
}

func getFieldNameFromExpr(expr Expr) string {
	switch e := expr.(type) {
	case *OperatorExpr:
		return getString(e.Field)
	case *ConditionMatchExpr:
		return getString(e.Field)
	case *GroupingExpr:
		return getFieldNameFromExpr(e.Expr)
	default:
		return ""
	}
}

func (p *Parser) getFieldNameFromExprWithSchema(expr Expr) string {
	fieldName := getFieldNameFromExpr(expr)
	if fieldName != Empty {
		return p.esSchema.getAlias(fieldName)
	}
	return fieldName
}

func collectAndExprs(expr Expr) []Expr {
	clauses := make([]Expr, 0)
	if e, ok := expr.(*AndExpr); ok {
		clauses = append(clauses, collectAndExprs(e.Left)...)
		clauses = append(clauses, collectAndExprs(e.Right)...)
	} else {
		clauses = append(clauses, expr)
	}
	return clauses
}

func collectOrExprs(expr Expr) []Expr {
	clauses := make([]Expr, 0)
	if e, ok := expr.(*OrExpr); ok {
		clauses = append(clauses, collectOrExprs(e.Left)...)
		clauses = append(clauses, collectOrExprs(e.Right)...)
	} else {
		clauses = append(clauses, expr)
	}
	return clauses
}

func (p *Parser) buildOperatorFuzzyQueryWithSchema(e *OperatorExpr) elastic.Query {
	field := p.esSchema.getFieldName(getString(e.Field))
	value := getValue(e.Value)

	targetField := field
	if targetField == Empty {
		targetField = DefaultEmptyField
	}

	// 创建模糊查询
	fuzzyQuery := elastic.NewFuzzyQuery(targetField, value)
	if e.Fuzziness != "" {
		fuzzyQuery.Fuzziness(e.Fuzziness)
	}

	// 应用boost
	if e.Boost > 0 && e.Boost != 1.0 {
		fuzzyQuery.Boost(e.Boost)
	}

	return fuzzyQuery
}

func applyBoostToQuery(query elastic.Query, boost float64) elastic.Query {
	if boost <= 0 || boost == 1.0 {
		return query
	}

	switch q := query.(type) {
	case *elastic.TermQuery:
		return q.Boost(boost)
	case *elastic.MatchQuery:
		return q.Boost(boost)
	case *elastic.MatchPhraseQuery:
		return q.Boost(boost)
	case *elastic.MatchPhrasePrefixQuery:
		return q.Boost(boost)
	case *elastic.WildcardQuery:
		return q.Boost(boost)
	case *elastic.RegexpQuery:
		return q.Boost(boost)
	case *elastic.FuzzyQuery:
		return q.Boost(boost)
	case *elastic.RangeQuery:
		return q.Boost(boost)
	case *elastic.BoolQuery:
		return q.Boost(boost)
	case *elastic.QueryStringQuery:
		return q.Boost(boost)
	default:
		return elastic.NewBoolQuery().Must(query).Boost(boost)
	}
}
