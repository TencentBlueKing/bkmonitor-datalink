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
	"strconv"
	"strings"

	elastic "github.com/olivere/elastic/v7"
	"github.com/spf13/cast"
)

const DefaultEmptyField = ""

const Separator = "."

const (
	FieldTypeText    = "text"
	FieldTypeKeyword = "keyword"
	FieldTypeLong    = "long"
	FieldTypeInteger = "integer"
	FieldTypeFloat   = "float"
	FieldTypeDouble  = "double"
	FieldTypeDate    = "date"
	FieldTypeBoolean = "boolean"
)

func (d *Schema) GetFieldType(fieldName string) (string, bool) {
	fieldType, exists := d.mapping[fieldName]
	return fieldType, exists
}

func (d *Schema) GetNestedPath(fieldName string) (string, bool) {
	parts := strings.Split(fieldName, Separator)
	if len(parts) > 1 {
		nestedPath := parts[0]
		if _, ok := d.mapping[nestedPath]; ok {
			return nestedPath, true
		}
	}
	return "", false
}

func walkESWithSchema(expr Expr, schema Schema, isPrefix bool, allowNestedWrap bool) elastic.Query {
	if expr == nil {
		return nil
	}

	var baseQuery elastic.Query

	switch e := expr.(type) {
	case *AndExpr:
		return buildAndQueryWithSchema(e, schema, isPrefix)

	case *OrExpr:
		return buildOrQueryWithSchema(e, schema, isPrefix)

	case *NotExpr:
		innerQuery := walkESWithSchema(e.Expr, schema, isPrefix, allowNestedWrap)
		if innerQuery == nil {
			return nil
		}
		return elastic.NewBoolQuery().MustNot(innerQuery)
	case *GroupingExpr:
		return walkESWithSchema(e.Expr, schema, isPrefix, allowNestedWrap)

	case *OperatorExpr:
		switch e.Op {
		case OpMatch:
			baseQuery = buildOperatorMatchQueryWithSchema(e, schema, isPrefix)
		case OpWildcard:
			baseQuery = buildOperatorWildcardQueryWithSchema(e, schema, isPrefix)
		case OpRegex:
			baseQuery = buildOperatorRegexpQueryWithSchema(e, schema, isPrefix)
		case OpRange:
			baseQuery = buildOperatorRangeQueryWithSchema(e, schema)
		}

	case *ConditionMatchExpr:
		baseQuery = buildConditionMatchQueryWithSchema(e, schema, isPrefix)

	default:
		return nil
	}

	// 检查是否需要nested包装（仅针对单个表达式且允许包装时）
	if baseQuery != nil && allowNestedWrap {
		fieldName := getFieldNameFromExprWithSchema(expr, schema)
		if path, isNested := schema.GetNestedPath(fieldName); isNested {
			return elastic.NewNestedQuery(path, baseQuery)
		}
	}

	return baseQuery
}

func getESFieldName(fieldExpr Expr) string {
	if fieldExpr != nil {
		if s, ok := fieldExpr.(*StringExpr); ok {
			return s.Value
		}
	}
	return DefaultEmptyField
}

func getESFieldNameWithSchema(fieldExpr Expr, schema Schema) string {
	fieldName := getESFieldName(fieldExpr)
	if fieldName != DefaultEmptyField {
		return schema.GetActualFieldName(fieldName)
	}
	return fieldName
}

func getESValue(expr Expr) string {
	if expr == nil {
		return ""
	}
	switch e := expr.(type) {
	case *StringExpr:
		return e.Value
	case *NumberExpr:
		return cast.ToString(e.Value)
	case *BoolExpr:
		return cast.ToString(e.Value)
	}
	return ""
}

// getESValueInterface returns the interface{} value for ES queries
func getESValueInterface(expr Expr) interface{} {
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

func isNumeric(value string) bool {
	_, err := strconv.ParseFloat(value, 64)
	return err == nil
}

func isSimpleTermsQuery(e *ConditionMatchExpr) bool {
	for _, andGroup := range e.Value.Values {
		if len(andGroup) != 1 {
			return false
		}
	}
	return true
}

func buildAndQueryWithSchema(e *AndExpr, schema Schema, isPrefix bool) elastic.Query {
	// 1. 收集所有AND条件的表达式
	exprs := collectAndExprs(e)

	// 2. 按nested路径分组
	groupedExprs := make(map[string][]Expr)
	for _, expr := range exprs {
		fieldName := getFieldNameFromExprWithSchema(expr, schema)
		if path, isNested := schema.GetNestedPath(fieldName); isNested {
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
				if q := walkESWithSchema(exp, schema, isPrefix, true); q != nil {
					finalClauses = append(finalClauses, q)
				}
			}
		} else {
			// 处理nested字段
			innerClauses := make([]elastic.Query, 0)
			for _, exp := range expressions {
				if q := walkESWithSchema(exp, schema, isPrefix, false); q != nil {
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

func buildOrQueryWithSchema(e *OrExpr, schema Schema, isPrefix bool) elastic.Query {
	// 1. 收集所有OR条件的表达式
	exprs := collectOrExprs(e)

	// 2. 按nested路径分组
	groupedExprs := make(map[string][]Expr)
	for _, expr := range exprs {
		fieldName := getFieldNameFromExprWithSchema(expr, schema)
		if path, isNested := schema.GetNestedPath(fieldName); isNested {
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
				if q := walkESWithSchema(exp, schema, isPrefix, true); q != nil {
					finalClauses = append(finalClauses, q)
				}
			}
		} else {
			// 处理nested字段
			innerClauses := make([]elastic.Query, 0)
			for _, exp := range expressions {
				if q := walkESWithSchema(exp, schema, isPrefix, false); q != nil {
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

func buildConditionMatchQueryWithSchema(e *ConditionMatchExpr, schema Schema, isPrefix bool) elastic.Query {
	field := getESFieldNameWithSchema(e.Field, schema)

	if e.Value == nil || len(e.Value.Values) == 0 {
		return nil
	}

	fieldType, hasSchema := schema.GetFieldType(field)

	if isSimpleTermsQuery(e) && hasSchema && fieldType == FieldTypeKeyword {
		var terms []interface{}
		for _, andGroup := range e.Value.Values {
			if len(andGroup) == 1 {
				terms = append(terms, getESValue(andGroup[0]))
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
			value := getESValue(andGroup[0])

			if hasSchema {
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
				value := getESValue(expr)

				if hasSchema && fieldType == FieldTypeKeyword {
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

// 新的 OperatorExpr 构建函数
func buildOperatorMatchQueryWithSchema(e *OperatorExpr, schema Schema, isPrefix bool) elastic.Query {
	field := getESFieldNameWithSchema(e.Field, schema)
	value := getESValue(e.Value)
	valueInterface := getESValueInterface(e.Value)

	fieldType, hasSchema := schema.GetFieldType(field)
	if e.IsQuoted {
		if field == DefaultEmptyField && e.Field == nil {
			return createEnhancedQueryStringQuery("\""+value+"\"", isPrefix)
		}

		if hasSchema {
			switch fieldType {
			case FieldTypeKeyword:
				return elastic.NewTermQuery(field, valueInterface)
			case FieldTypeText:
				if isPrefix {
					return elastic.NewMatchPhrasePrefixQuery(field, value)
				}
				return elastic.NewMatchPhraseQuery(field, value)
			default:
				if isPrefix {
					return elastic.NewMatchPhrasePrefixQuery(field, value)
				}
				return elastic.NewMatchPhraseQuery(field, value)
			}
		}

		if isPrefix {
			return elastic.NewMatchPhrasePrefixQuery(field, value)
		}
		return elastic.NewMatchPhraseQuery(field, value)
	}

	if hasSchema {
		switch fieldType {
		case FieldTypeKeyword:
			return elastic.NewTermQuery(field, valueInterface)
		case FieldTypeText:
			if field == DefaultEmptyField {
				return createEnhancedQueryStringQuery(value, isPrefix)
			}
			if strings.Contains(value, " ") {
				if isPrefix {
					return elastic.NewMatchPhrasePrefixQuery(field, value)
				}
				return elastic.NewMatchPhraseQuery(field, value)
			}
			if isPrefix {
				return elastic.NewMatchPhrasePrefixQuery(field, value)
			}
			return elastic.NewMatchPhraseQuery(field, value)
		case FieldTypeLong, FieldTypeInteger:
			return elastic.NewTermQuery(field, valueInterface)
		case FieldTypeFloat, FieldTypeDouble:
			return elastic.NewTermQuery(field, valueInterface)
		case FieldTypeBoolean:
			return elastic.NewTermQuery(field, valueInterface)
		case FieldTypeDate:
			return elastic.NewRangeQuery(field).Gte(valueInterface).Lte(valueInterface)
		default:
			return elastic.NewTermQuery(field, valueInterface)
		}
	}

	if field == DefaultEmptyField {
		return createEnhancedQueryStringQuery(value, isPrefix)
	}

	if strings.Contains(value, " ") {
		if isPrefix {
			return elastic.NewMatchPhrasePrefixQuery(field, value)
		}
		return elastic.NewMatchPhraseQuery(field, value)
	}

	if _, ok := e.Value.(*NumberExpr); ok {
		return elastic.NewTermQuery(field, valueInterface)
	}

	return elastic.NewTermQuery(field, valueInterface)
}

func buildOperatorWildcardQueryWithSchema(e *OperatorExpr, schema Schema, isPrefix bool) elastic.Query {
	field := getESFieldNameWithSchema(e.Field, schema)
	value := getESValue(e.Value)

	if field == DefaultEmptyField {
		return createEnhancedQueryStringQuery(value, isPrefix)
	}

	return elastic.NewWildcardQuery(field, value)
}

func buildOperatorRegexpQueryWithSchema(e *OperatorExpr, schema Schema, isPrefix bool) elastic.Query {
	field := getESFieldNameWithSchema(e.Field, schema)
	value := getESValue(e.Value)

	if field == DefaultEmptyField {
		return createEnhancedQueryStringQuery("/"+value+"/", isPrefix)
	}

	return elastic.NewRegexpQuery(field, value)
}

func buildOperatorRangeQueryWithSchema(e *OperatorExpr, schema Schema) elastic.Query {
	field := getESFieldNameWithSchema(e.Field, schema)
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
		return getESFieldName(e.Field)
	case *ConditionMatchExpr:
		return getESFieldName(e.Field)
	case *GroupingExpr:
		return getFieldNameFromExpr(e.Expr)
	default:
		return ""
	}
}

func getFieldNameFromExprWithSchema(expr Expr, schema Schema) string {
	fieldName := getFieldNameFromExpr(expr)
	if fieldName != DefaultEmptyField {
		return schema.GetActualFieldName(fieldName)
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
