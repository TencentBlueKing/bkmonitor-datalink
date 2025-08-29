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

type FieldType string

const (
	FieldTypeText    FieldType = "text"
	FieldTypeKeyword FieldType = "keyword"
	FieldTypeLong    FieldType = "long"
	FieldTypeInteger FieldType = "integer"
	FieldTypeFloat   FieldType = "float"
	FieldTypeDouble  FieldType = "double"
	FieldTypeDate    FieldType = "date"
	FieldTypeBoolean FieldType = "boolean"
)

type FieldSchema interface {
	GetFieldType(fieldName string) (FieldType, bool)
}

type Schema struct {
	fieldTypes map[string]FieldType
}

func (d *Schema) GetFieldType(fieldName string) (FieldType, bool) {
	fieldType, exists := d.fieldTypes[fieldName]
	return fieldType, exists
}

func (d *Schema) SetFieldType(fieldName string, fieldType FieldType) {
	d.fieldTypes[fieldName] = fieldType
}

func es(expr Expr, mappings ...FieldSchema) elastic.Query {
	var schema FieldSchema
	if len(mappings) > 0 {
		schema = mappings[0]
	} else {
		schema = &Schema{fieldTypes: make(map[string]FieldType)}
	}
	return ToESWithSchema(expr, schema)
}

func ToESWithSchema(expr Expr, schema FieldSchema) elastic.Query {
	if expr == nil {
		return nil
	}
	return walkESWithSchema(expr, schema)
}

func walkESWithSchema(expr Expr, schema FieldSchema) elastic.Query {
	if expr == nil {
		return nil
	}

	switch e := expr.(type) {
	case *AndExpr:
		return buildAndQueryWithSchema(e, schema)

	case *OrExpr:
		return buildOrQueryWithSchema(e, schema)

	case *NotExpr:
		innerQuery := walkESWithSchema(e.Expr, schema)
		if innerQuery == nil {
			return nil
		}
		return elastic.NewBoolQuery().MustNot(innerQuery)
	case *GroupingExpr:
		return walkESWithSchema(e.Expr, schema)

	case *OperatorExpr:
		switch e.Op {
		case OpMatch:
			return buildOperatorMatchQueryWithSchema(e, schema)
		case OpWildcard:
			return buildOperatorWildcardQueryWithSchema(e)
		case OpRegex:
			return buildOperatorRegexpQueryWithSchema(e)
		case OpRange:
			return buildOperatorRangeQueryWithSchema(e)
		}
		return nil

	case *ConditionMatchExpr:
		return buildConditionMatchQueryWithSchema(e, schema)

	default:
		return nil
	}
}

func getESFieldName(fieldExpr Expr) string {
	if fieldExpr != nil {
		if s, ok := fieldExpr.(*StringExpr); ok {
			return s.Value
		}
	}
	return DefaultEmptyField
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

func collectAndClauses(expr Expr, schema FieldSchema) []elastic.Query {
	clauses := make([]elastic.Query, 0)

	if _, ok := expr.(*GroupingExpr); ok {
		if q := walkESWithSchema(expr, schema); q != nil {
			clauses = append(clauses, q)
		}
		return clauses
	}

	if e, ok := expr.(*AndExpr); ok {
		clauses = append(clauses, collectAndClauses(e.Left, schema)...)
		clauses = append(clauses, collectAndClauses(e.Right, schema)...)
	} else {
		if q := walkESWithSchema(expr, schema); q != nil {
			clauses = append(clauses, q)
		}
	}
	return clauses
}

func collectOrClauses(expr Expr, schema FieldSchema) []elastic.Query {
	clauses := make([]elastic.Query, 0)

	if _, ok := expr.(*GroupingExpr); ok {
		if q := walkESWithSchema(expr, schema); q != nil {
			clauses = append(clauses, q)
		}
		return clauses
	}

	if e, ok := expr.(*OrExpr); ok {
		clauses = append(clauses, collectOrClauses(e.Left, schema)...)
		clauses = append(clauses, collectOrClauses(e.Right, schema)...)
	} else {
		// When a non-OrExpr (like a grouped AND) is found, convert it as a single unit
		if q := walkESWithSchema(expr, schema); q != nil {
			clauses = append(clauses, q)
		}
	}
	return clauses
}

func buildAndQueryWithSchema(e *AndExpr, schema FieldSchema) elastic.Query {
	clauses := collectAndClauses(e, schema)
	if len(clauses) == 0 {
		return nil
	}
	if len(clauses) == 1 {
		return clauses[0]
	}

	return elastic.NewBoolQuery().Must(clauses...)
}

func buildOrQueryWithSchema(e *OrExpr, schema FieldSchema) elastic.Query {
	clauses := collectOrClauses(e, schema)
	if len(clauses) == 0 {
		return nil
	}
	if len(clauses) == 1 {
		return clauses[0]
	}

	return elastic.NewBoolQuery().Should(clauses...)
}

func buildConditionMatchQueryWithSchema(e *ConditionMatchExpr, schema FieldSchema) elastic.Query {
	field := getESFieldName(e.Field)

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
					boolQuery.Should(elastic.NewMatchPhraseQuery(field, value))
				default:
					boolQuery.Should(elastic.NewMatchPhraseQuery(field, value))
				}
			} else {
				boolQuery.Should(elastic.NewMatchPhraseQuery(field, value))
			}
		} else {
			andBoolQuery := elastic.NewBoolQuery()
			for _, expr := range andGroup {
				value := getESValue(expr)

				if hasSchema && fieldType == FieldTypeKeyword {
					andBoolQuery.Must(elastic.NewTermQuery(field, value))
				} else {
					andBoolQuery.Must(elastic.NewMatchPhraseQuery(field, value))
				}
			}
			boolQuery.Should(andBoolQuery)
		}
	}

	return boolQuery
}

// 新的 OperatorExpr 构建函数
func buildOperatorMatchQueryWithSchema(e *OperatorExpr, schema FieldSchema) elastic.Query {
	field := getESFieldName(e.Field)
	value := getESValue(e.Value)
	valueInterface := getESValueInterface(e.Value)

	fieldType, hasSchema := schema.GetFieldType(field)
	if e.IsQuoted {
		if field == DefaultEmptyField && e.Field == nil {
			return elastic.NewQueryStringQuery("\"" + value + "\"")
		}

		if hasSchema {
			switch fieldType {
			case FieldTypeKeyword:
				return elastic.NewTermQuery(field, valueInterface)
			case FieldTypeText:
				return elastic.NewMatchPhraseQuery(field, value)
			default:
				return elastic.NewMatchPhraseQuery(field, value)
			}
		}

		return elastic.NewMatchPhraseQuery(field, value)
	}

	if hasSchema {
		switch fieldType {
		case FieldTypeKeyword:
			return elastic.NewTermQuery(field, valueInterface)
		case FieldTypeText:
			if field == DefaultEmptyField {
				return elastic.NewQueryStringQuery(value)
			}
			if strings.Contains(value, " ") {
				return elastic.NewMatchQuery(field, value)
			}
			return elastic.NewMatchQuery(field, value)
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
		return elastic.NewQueryStringQuery(value)
	}

	if strings.Contains(value, " ") {
		return elastic.NewMatchQuery(field, value)
	}

	if _, ok := e.Value.(*NumberExpr); ok {
		return elastic.NewTermQuery(field, valueInterface)
	}

	return elastic.NewTermQuery(field, valueInterface)
}

func buildOperatorWildcardQueryWithSchema(e *OperatorExpr) elastic.Query {
	field := getESFieldName(e.Field)
	value := getESValue(e.Value)

	return elastic.NewWildcardQuery(field, value)
}

func buildOperatorRegexpQueryWithSchema(e *OperatorExpr) elastic.Query {
	field := getESFieldName(e.Field)
	value := getESValue(e.Value)

	return elastic.NewRegexpQuery(field, value)
}

func buildOperatorRangeQueryWithSchema(e *OperatorExpr) elastic.Query {
	field := getESFieldName(e.Field)
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
