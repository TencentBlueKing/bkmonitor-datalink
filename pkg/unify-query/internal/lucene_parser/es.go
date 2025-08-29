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
)

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

	case *MatchExpr:
		return buildMatchQueryWithSchema(e, schema)

	case *WildcardExpr:
		return buildWildcardQueryWithSchema(e)

	case *RegexpExpr:
		return buildRegexpQueryWithSchema(e)

	case *NumberRangeExpr:
		return buildNumberRangeQueryWithSchema(e)

	case *TimeRangeExpr:
		return buildTimeRangeQueryWithSchema(e)

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
	return DefaultLogField
}

func getESValue(expr Expr) string {
	if expr == nil {
		return ""
	}
	if s, ok := expr.(*StringExpr); ok {
		return s.Value
	}
	return ""
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

func buildMatchQueryWithSchema(e *MatchExpr, schema FieldSchema) elastic.Query {
	field := getESFieldName(e.Field)
	value := getESValue(e.Value)

	fieldType, hasSchema := schema.GetFieldType(field)
	if e.IsQuoted {
		if field == DefaultLogField && e.Field == nil {
			return elastic.NewQueryStringQuery("\"" + value + "\"")
		}

		if hasSchema {
			switch fieldType {
			case FieldTypeKeyword:
				return elastic.NewTermQuery(field, value)
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
			return elastic.NewTermQuery(field, value)
		case FieldTypeText:
			if field == DefaultLogField {
				return elastic.NewQueryStringQuery(value)
			}
			if strings.Contains(value, " ") {
				return elastic.NewMatchQuery(field, value)
			}
			return elastic.NewMatchQuery(field, value)
		case FieldTypeLong, FieldTypeInteger:
			if num, err := strconv.ParseInt(value, 10, 64); err == nil {
				return elastic.NewTermQuery(field, num)
			}
			return elastic.NewTermQuery(field, value)
		case FieldTypeFloat, FieldTypeDouble:
			if num, err := strconv.ParseFloat(value, 64); err == nil {
				return elastic.NewTermQuery(field, num)
			}
			return elastic.NewTermQuery(field, value)
		case FieldTypeBoolean:
			if value == "true" || value == "false" {
				return elastic.NewTermQuery(field, value == "true")
			}
			return elastic.NewTermQuery(field, value)
		case FieldTypeDate:
			return elastic.NewRangeQuery(field).Gte(value).Lte(value)
		default:
			return elastic.NewTermQuery(field, value)
		}
	}

	if field == DefaultLogField {
		return elastic.NewQueryStringQuery(value)
	}

	if strings.Contains(value, " ") {
		return elastic.NewMatchQuery(field, value)
	}

	if isNumeric(value) {
		if num, err := strconv.ParseFloat(value, 64); err == nil {
			return elastic.NewTermQuery(field, num)
		}
	}

	return elastic.NewTermQuery(field, value)
}

func buildWildcardQueryWithSchema(e *WildcardExpr) elastic.Query {
	field := getESFieldName(e.Field)
	value := getESValue(e.Value)

	if field == DefaultLogField {
		return elastic.NewQueryStringQuery(value)
	}

	return elastic.NewWildcardQuery(field, value)
}

func buildRegexpQueryWithSchema(e *RegexpExpr) elastic.Query {
	field := getESFieldName(e.Field)
	value := getESValue(e.Value)

	if field == DefaultLogField {
		return elastic.NewQueryStringQuery("/" + value + "/")
	}

	return elastic.NewRegexpQuery(field, value)
}

func buildNumberRangeQueryWithSchema(e *NumberRangeExpr) elastic.Query {
	field := getESFieldName(e.Field)
	rangeQuery := elastic.NewRangeQuery(field)

	if e.Start != nil {
		startValue := getESValue(e.Start)
		if startValue != "*" {
			if num, err := strconv.ParseFloat(startValue, 64); err == nil {
				if e.IncludeStart != nil {
					if b, ok := e.IncludeStart.(*BoolExpr); ok && b.Value {
						rangeQuery = rangeQuery.Gte(num)
					} else {
						rangeQuery = rangeQuery.Gt(num)
					}
				} else {
					rangeQuery = rangeQuery.Gt(num)
				}
			}
		} else {
			if e.IncludeStart != nil {
				if b, ok := e.IncludeStart.(*BoolExpr); ok {
					rangeQuery = rangeQuery.IncludeLower(b.Value)
				}
			} else {
				rangeQuery = rangeQuery.IncludeLower(true)
			}
		}
	}

	if e.End != nil {
		endValue := getESValue(e.End)
		if endValue != "*" {
			if num, err := strconv.ParseFloat(endValue, 64); err == nil {
				if e.IncludeEnd != nil {
					if b, ok := e.IncludeEnd.(*BoolExpr); ok && b.Value {
						rangeQuery = rangeQuery.Lte(num)
					} else {
						rangeQuery = rangeQuery.Lt(num)
					}
				} else {
					rangeQuery = rangeQuery.Lt(num)
				}
			}
		} else {
			if e.IncludeEnd != nil {
				if b, ok := e.IncludeEnd.(*BoolExpr); ok {
					rangeQuery = rangeQuery.IncludeUpper(b.Value)
				}
			}
		}
	}

	return rangeQuery
}

func buildTimeRangeQueryWithSchema(e *TimeRangeExpr) elastic.Query {
	field := getESFieldName(e.Field)
	if field == DefaultLogField {
		field = "datetime"
	}
	rangeQuery := elastic.NewRangeQuery(field)

	if e.Start != nil {
		startValue := getESValue(e.Start)
		if startValue != "*" {
			rangeQuery.From(startValue)
			if b, ok := e.IncludeStart.(*BoolExpr); ok {
				rangeQuery.IncludeLower(b.Value)
			}
		}
	}

	if e.End != nil {
		endValue := getESValue(e.End)
		if endValue != "*" {
			rangeQuery.To(endValue)
			if b, ok := e.IncludeEnd.(*BoolExpr); ok {
				rangeQuery.IncludeUpper(b.Value)
			}
		}
	}

	return rangeQuery
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
