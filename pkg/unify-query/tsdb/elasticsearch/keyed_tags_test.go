package elasticsearch

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	elastic "github.com/olivere/elastic/v7"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func tagFactory() *FormatFactory {
	return newSeriesFormatFactory(context.Background(), &metadata.Query{
		FieldSemantics: metadata.FTAEventTagsV1,
		Field:          "*", TimeField: metadata.TimeField{Name: "time", Type: "date", Unit: "ms"},
	}, nil, time.Time{}, time.Time{}, "", 1440)
}

func TestFTAKeyedTagConditions(t *testing.T) {
	metadata.InitMetadata()
	f := tagFactory()
	q, err := f.Query(metadata.AllConditions{{
		{DimensionName: "tags.env", Operator: "eq", Value: []string{"prod"}},
		{DimensionName: "tags.team", Operator: "eq", Value: []string{"prod"}},
	}})
	require.NoError(t, err)
	source, err := q.Source()
	require.NoError(t, err)
	b, err := json.Marshal(source)
	require.NoError(t, err)
	// Python SQLCompiler emits one independent nested query per key. A single
	// nested bool requiring key=env AND key=team could never match.
	require.JSONEq(t, `{"bool":{"minimum_should_match":"1","should":{"bool":{"must":[{"nested":{"path":"tags","query":{"bool":{"must":[{"term":{"tags.key":"env"}},{"terms":{"tags.value.raw":["prod"]}}]}}}},{"nested":{"path":"tags","query":{"bool":{"must":[{"term":{"tags.key":"team"}},{"terms":{"tags.value.raw":["prod"]}}]}}}}]}}}}`, string(b))
	for _, op := range []string{"ne", "ncontains"} {
		queries, occurrence, err := ftaFieldQueries("tags.absent", metadata.ConditionField{Operator: op, Value: []string{"x"}})
		require.NoError(t, err)
		require.Equal(t, "must_not", occurrence)
		s, err := queries[0].Source()
		require.NoError(t, err)
		require.Contains(t, s, "nested", "negation applies outside nested so absent keys match")
	}
}

func TestFTAKeyedTagCrossGrouping(t *testing.T) {
	metadata.InitMetadata()
	f := tagFactory()
	name, agg, err := f.EsAgg(metadata.Aggregates{{Name: Count, Dimensions: []string{"tags.env", "tags.team"}}})
	require.NoError(t, err)
	require.Equal(t, "tags.team", name)
	s, err := agg.Source()
	require.NoError(t, err)
	b, err := json.Marshal(s)
	require.NoError(t, err)
	// The reverse_nested boundary is repeated between dimensions, as in the
	// FTA compiler. Values belonging to another key cannot enter either terms bucket.
	require.JSONEq(t, `{
	  "nested": {"path": "tags"},
	  "aggregations": {"key": {
	    "filter": {"term": {"tags.key": "team"}},
	    "aggregations": {"value": {
	      "terms": {"field": "tags.value.raw", "size": 1440},
	      "aggregations": {"_reverse": {
	        "reverse_nested": {},
	        "aggregations": {"tags.env": {
	          "nested": {"path": "tags"},
	          "aggregations": {"key": {
	            "filter": {"term": {"tags.key": "env"}},
	            "aggregations": {"value": {
	              "terms": {"field": "tags.value.raw", "size": 1440},
	              "aggregations": {"_reverse": {
	                "reverse_nested": {},
	                "aggregations": {"_value": {"value_count": {"field": "_index"}}}
	              }}
	            }}
	          }}
	        }}
	      }}
	    }}
	  }}
	}`, string(b))
	var response elastic.SearchResult
	require.NoError(t, json.Unmarshal([]byte(`{"aggregations":{"tags.team":{"doc_count":9,"key":{"doc_count":3,"value":{"buckets":[{"key":"prod","doc_count":2,"_reverse":{"doc_count":2,"tags.env":{"doc_count":4,"key":{"doc_count":2,"value":{"buckets":[{"key":"prod","doc_count":1,"_reverse":{"doc_count":1,"_value":{"value":1}}},{"key":"dev","doc_count":1,"_reverse":{"doc_count":1,"_value":{"value":1}}}]}}}}},{"key":"empty","doc_count":1,"_reverse":{"doc_count":1,"tags.env":{"doc_count":1,"key":{"doc_count":0,"value":{"buckets":[]}}}}}]}}}}}`), &response))
	result, err := f.AggDataFormat(response.Aggregations, nil)
	require.NoError(t, err)
	require.Len(t, result.Timeseries, 2)
	got := make([]map[string]string, 0, 2)
	for _, series := range result.Timeseries {
		labels := map[string]string{}
		for _, label := range series.Labels {
			labels[label.Name] = label.Value
		}
		got = append(got, labels)
		require.Equal(t, float64(1), series.Samples[0].Value)
	}
	encode := metadata.GetFieldFormat(context.Background()).EncodeFunc()
	require.ElementsMatch(t, []map[string]string{
		{encode("tags.env"): "prod", encode("tags.team"): "prod"},
		{encode("tags.env"): "dev", encode("tags.team"): "prod"},
	}, got)
}

func TestFTAFieldOperatorsAndBoolCompatibility(t *testing.T) {
	metadata.InitMetadata()
	for _, tc := range []struct{ op, occurrence, clause string }{
		{"eq", "must", `{"terms":{"tags.value.raw":["a","z"]}}`},
		{"ne", "must_not", `{"terms":{"tags.value.raw":["a","z"]}}`},
		{"gt", "must", `{"range":{"tags.value.raw":{"from":"z","include_lower":false,"include_upper":true,"to":null}}}`},
		{"gte", "must", `{"range":{"tags.value.raw":{"from":"z","include_lower":true,"include_upper":true,"to":null}}}`},
		{"lt", "must", `{"range":{"tags.value.raw":{"from":null,"include_lower":true,"include_upper":false,"to":"a"}}}`},
		{"lte", "must", `{"range":{"tags.value.raw":{"from":null,"include_lower":true,"include_upper":true,"to":"a"}}}`},
	} {
		t.Run(tc.op, func(t *testing.T) {
			queries, occurrence, err := ftaFieldQueries("tags.env", metadata.ConditionField{Operator: tc.op, Value: []string{"a", "z"}})
			require.NoError(t, err)
			require.Equal(t, tc.occurrence, occurrence)
			require.Len(t, queries, 1)
			s, err := queries[0].Source()
			require.NoError(t, err)
			b, err := json.Marshal(s)
			require.NoError(t, err)
			require.JSONEq(t, `{"nested":{"path":"tags","query":{"bool":{"must":[{"term":{"tags.key":"env"}},`+tc.clause+`]}}}}`, string(b))
		})
	}
	for _, op := range []string{"req", "contains", "ncontains"} {
		queries, occurrence, err := ftaFieldQueries("tags.env", metadata.ConditionField{Operator: op, Value: []string{"a*", "z"}})
		require.NoError(t, err)
		require.Len(t, queries, 2)
		require.Equal(t, map[string]string{"req": "must", "contains": "should", "ncontains": "must_not"}[op], occurrence)
	}
	_, _, err := ftaFieldQueries("tags.env", metadata.ConditionField{Operator: "eq", IsPrefix: true})
	require.Error(t, err)
	_, _, err = ftaFieldQueries("tags.env", metadata.ConditionField{Operator: "nreq"})
	require.Error(t, err)
	_, err = tagFactory().WithFieldSemantics("unknown").Query(nil)
	require.Error(t, err)
	q, err := tagFactory().Query(metadata.AllConditions{{
		{DimensionName: "alert_name", Operator: "eq", Value: []string{"alarm"}},
		{DimensionName: "tags.env", Operator: "contains", Value: []string{"prod"}},
		{DimensionName: "tags.team", Operator: "contains", Value: []string{"ops"}},
	}})
	require.NoError(t, err)
	s, err := q.Source()
	require.NoError(t, err)
	outer := s.(map[string]interface{})["bool"].(map[string]interface{})
	inner := outer["should"].(map[string]interface{})["bool"].(map[string]interface{})
	require.Contains(t, inner, "must")
	require.Len(t, inner["should"], 2)
	// Preserve Python's bool semantics: should is optional when must exists.
	require.NotContains(t, inner, "minimum_should_match")
}

func TestFTANormalDimensionKeepsLogicalName(t *testing.T) {
	metadata.InitMetadata()
	f := tagFactory()
	name, agg, err := f.EsAgg(metadata.Aggregates{{Name: Count, Dimensions: []string{"alert_name"}}})
	require.NoError(t, err)
	require.Equal(t, "alert_name", name)
	s, err := agg.Source()
	require.NoError(t, err)
	b, err := json.Marshal(s)
	require.NoError(t, err)
	require.JSONEq(t, `{"terms":{"field":"alert_name.raw","size":1440},"aggregations":{"_value":{"value_count":{"field":"_index"}}}}`, string(b))
}

func TestFTASourceFiltersPreserveUserShould(t *testing.T) {
	metadata.InitMetadata()
	f := tagFactory().WithSourceConditions(metadata.AllConditions{{{DimensionName: "status", Operator: "eq", Value: []string{"ABNORMAL"}}}})
	q, err := f.Query(metadata.AllConditions{{{DimensionName: "tags.env", Operator: "contains", Value: []string{"prod"}}}})
	require.NoError(t, err)
	s, err := q.Source()
	require.NoError(t, err)
	filters := s.(map[string]interface{})["bool"].(map[string]interface{})["filter"].([]interface{})
	require.Len(t, filters, 2)
	userOuter := filters[1].(map[string]interface{})["bool"].(map[string]interface{})
	userInner := userOuter["should"].(map[string]interface{})["bool"].(map[string]interface{})
	// A should-only bool requires a match. Moving status into this group
	// would turn include into an optional clause and admit unrelated events.
	require.Contains(t, userInner, "should")
	require.NotContains(t, userInner, "must")
	_, err = f.WithFieldSemantics("").Query(nil)
	require.ErrorContains(t, err, "source_conditions requires")
}

func TestFTAEventDateUsesSecondsForQueryAndMillisecondsForBuckets(t *testing.T) {
	metadata.InitMetadata()
	// EventDocument accepts epoch_second dates; Elasticsearch date histogram
	// keys are nevertheless milliseconds. TimeField.Unit describes bucket
	// normalization, while WithQuery's unit controls the request range format.
	start, end := time.Unix(1700000040, 0), time.Unix(1700000100, 0)
	f := newSeriesFormatFactory(context.Background(), &metadata.Query{
		FieldSemantics: metadata.FTAEventTagsV1, Field: "_index",
		TimeField: metadata.TimeField{Name: "time", Type: "date", Unit: "millisecond"},
	}, nil, start, end, "second", 1440)
	rangeQuery, err := f.RangeQuery()
	require.NoError(t, err)
	source, err := rangeQuery.Source()
	require.NoError(t, err)
	encoded, err := json.Marshal(source)
	require.NoError(t, err)
	require.JSONEq(t, `{"range":{"time":{"from":1700000040,"to":1700000100,"include_lower":true,"include_upper":true,"format":"epoch_second"}}}`, string(encoded))
	name, aggregation, err := f.EsAgg(metadata.Aggregates{{Name: Count, Window: time.Minute}})
	require.NoError(t, err)
	require.Equal(t, "time", name)
	source, err = aggregation.Source()
	require.NoError(t, err)
	encoded, err = json.Marshal(source)
	require.NoError(t, err)
	// "1m" is the formatter's canonical spelling of the requested 60 seconds.
	require.JSONEq(t, `{"date_histogram":{"field":"time","interval":"1m","min_doc_count":0,"extended_bounds":{"min":1700000040000,"max":1700000100000}},"aggregations":{"_value":{"value_count":{"field":"_index"}}}}`, string(encoded))
	var response elastic.SearchResult
	require.NoError(t, json.Unmarshal([]byte(`{"aggregations":{"time":{"buckets":[{"key":1700000040000,"doc_count":2,"_value":{"value":2}}]}}}`), &response))
	result, err := f.AggDataFormat(response.Aggregations, nil)
	require.NoError(t, err)
	require.Len(t, result.Timeseries, 1)
	require.Len(t, result.Timeseries[0].Samples, 1)
	require.Equal(t, int64(1700000040000), result.Timeseries[0].Samples[0].Timestamp)
	require.Equal(t, float64(2), result.Timeseries[0].Samples[0].Value)
}
