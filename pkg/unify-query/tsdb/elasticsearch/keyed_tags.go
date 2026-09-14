package elasticsearch

import (
	"fmt"
	"strings"

	elastic "github.com/olivere/elastic/v7"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// KeyedTagAgg enters the tag array and returns to parent documents before
// evaluating the next dimension or metric. Keys therefore never share a nested row.
type KeyedTagAgg struct{ Name string }

func (f *FormatFactory) WithFieldSemantics(semantics string) *FormatFactory {
	f.fieldSemantics = semantics
	return f
}

func (f *FormatFactory) WithSourceConditions(conditions metadata.AllConditions) *FormatFactory {
	f.sourceConditions = conditions
	return f
}

func (f *FormatFactory) isKeyedTag(name string) bool {
	return f.fieldSemantics == metadata.FTAEventTagsV1 && strings.HasPrefix(name, "tags.")
}

// Match the FTA SQLCompiler tag operators, including root-level negation:
// a document without this key satisfies neq/exclude.
func (f *FormatFactory) ftaQuery(conditions metadata.AllConditions) (elastic.Query, error) {
	if len(conditions) == 0 {
		return nil, nil
	}
	or := elastic.NewBoolQuery().MinimumNumberShouldMatch(1)
	for _, group := range conditions {
		and := elastic.NewBoolQuery()
		for _, con := range group {
			name := con.DimensionName
			if f.decode != nil {
				name = f.decode(name)
			}
			queries, occurrence, err := ftaFieldQueries(name, con)
			if err != nil {
				return nil, err
			}
			switch occurrence {
			case "should":
				and.Should(queries...)
			case "must_not":
				and.MustNot(queries...)
			default:
				and.Must(queries...)
			}
		}
		or.Should(and)
	}
	return or, nil
}

func ftaFieldQueries(name string, con metadata.ConditionField) ([]elastic.Query, string, error) {
	if con.IsPrefix || con.IsSuffix {
		return nil, "", fmt.Errorf("FTA fields do not support prefix/suffix flags")
	}
	switch con.Operator {
	case structured.ConditionEqual, structured.ConditionNotEqual, structured.ConditionContains, structured.ConditionNotContains,
		structured.ConditionRegEqual, structured.ConditionGt, structured.ConditionGte, structured.ConditionLt, structured.ConditionLte:
	default:
		return nil, "", fmt.Errorf("unsupported FTA field operator %q", con.Operator)
	}
	field := name
	if strings.HasPrefix(name, "tags.") {
		field = "tags.value.raw"
	}
	wrap := func(q elastic.Query) elastic.Query {
		if !strings.HasPrefix(name, "tags.") {
			return q
		}
		return elastic.NewNestedQuery("tags", elastic.NewBoolQuery().Must(
			elastic.NewTermQuery("tags.key", strings.TrimPrefix(name, "tags.")), q))
	}
	if con.Operator == structured.ConditionEqual || con.Operator == structured.ConditionNotEqual {
		values := make([]interface{}, len(con.Value))
		for i, value := range con.Value {
			values[i] = value
		}
		q := wrap(elastic.NewTermsQuery(field, values...))
		if con.Operator == structured.ConditionNotEqual {
			return []elastic.Query{q}, "must_not", nil
		}
		return []elastic.Query{q}, "must", nil
	}
	values := con.Value
	if con.Operator == structured.ConditionGt || con.Operator == structured.ConditionGte || con.Operator == structured.ConditionLt || con.Operator == structured.ConditionLte {
		if len(values) == 0 {
			return nil, "", fmt.Errorf("FTA range condition requires a value")
		}
		bound := values[0]
		for _, value := range values[1:] {
			if (con.Operator == structured.ConditionGt || con.Operator == structured.ConditionGte) && value > bound || (con.Operator == structured.ConditionLt || con.Operator == structured.ConditionLte) && value < bound {
				bound = value
			}
		}
		values = []string{bound}
	}
	queries := make([]elastic.Query, 0, len(con.Value))
	for _, value := range values {
		var q elastic.Query
		switch con.Operator {
		case structured.ConditionContains, structured.ConditionNotContains:
			q = elastic.NewWildcardQuery(field, "*"+value+"*")
		case structured.ConditionRegEqual:
			q = elastic.NewRegexpQuery(field, value)
		case structured.ConditionGt:
			q = elastic.NewRangeQuery(field).Gt(value)
		case structured.ConditionGte:
			q = elastic.NewRangeQuery(field).Gte(value)
		case structured.ConditionLt:
			q = elastic.NewRangeQuery(field).Lt(value)
		case structured.ConditionLte:
			q = elastic.NewRangeQuery(field).Lte(value)
		default:
			return nil, "", fmt.Errorf("unsupported FTA field operator %q", con.Operator)
		}
		queries = append(queries, wrap(q))
	}
	switch con.Operator {
	case structured.ConditionContains:
		return queries, "should", nil
	case structured.ConditionNotContains:
		return queries, "must_not", nil
	default:
		return queries, "must", nil
	}
}
