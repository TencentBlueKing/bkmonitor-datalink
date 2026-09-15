package elasticsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	elastic "github.com/olivere/elastic/v7"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestFTATermsCompletenessMetadata(t *testing.T) {
	complete := `{"sum_other_doc_count":0,"doc_count_error_upper_bound":0,"buckets":[{"key":"x","doc_count_error_upper_bound":0}]}`
	require.NoError(t, validateFTATermsCompleteness([]byte(complete)))
	require.NoError(t, validateFTATermsCompleteness([]byte(`{"sum_other_doc_count":0,"doc_count_error_upper_bound":0,"buckets":[]}`)))
	for _, field := range []string{"sum_other_doc_count", "doc_count_error_upper_bound"} {
		for _, value := range []string{"7", "-1", "-2", "null", `"0"`, "0.5"} {
			t.Run(field+value, func(t *testing.T) {
				body := strings.Replace(complete, `"`+field+`":0`, `"`+field+`":`+value, 1)
				require.Error(t, validateFTATermsCompleteness([]byte(body)))
			})
		}
		require.Error(t, validateFTATermsCompleteness([]byte(strings.Replace(complete, `"`+field+`":0,`, "", 1))))
	}
	for _, field := range []string{``, `,"doc_count_error_upper_bound":null`, `,"doc_count_error_upper_bound":-1`, `,"doc_count_error_upper_bound":3`, `,"doc_count_error_upper_bound":"0"`} {
		body := `{"sum_other_doc_count":0,"doc_count_error_upper_bound":0,"buckets":[{"key":"x"` + field + `}]}`
		require.Error(t, validateFTATermsCompleteness([]byte(body)), body)
	}
}

func TestFTATermsCompletenessEveryDimension(t *testing.T) {
	metadata.InitMetadata()
	for _, dimension := range []string{"tags.host", "alert_name"} {
		for _, inner := range []bool{false, true} {
			for _, other := range []int{0, 7} {
				t.Run(fmt.Sprintf("%s/inner=%v/discarded=%d", dimension, inner, other), func(t *testing.T) {
					f := tagFactory()
					dims := []string{dimension}
					if inner {
						dims = append(dims, "tags.outer")
					}
					_, agg, err := f.EsAgg(metadata.Aggregates{{Name: Count, Dimensions: dims}})
					require.NoError(t, err)
					source, err := agg.Source()
					require.NoError(t, err)
					encoded, err := json.Marshal(source)
					require.NoError(t, err)
					require.Equal(t, len(dims), strings.Count(string(encoded), `"show_term_doc_count_error":true`))

					metric := `"_value":{"value":2}`
					if strings.HasPrefix(dimension, "tags.") {
						metric = `"_reverse":{` + metric + `}`
					}
					terms := fmt.Sprintf(`{"sum_other_doc_count":%d,"doc_count_error_upper_bound":0,"buckets":[{"key":"host-1","doc_count_error_upper_bound":0,%s}]}`, other, metric)
					if strings.HasPrefix(dimension, "tags.") {
						terms = `{"key":{"value":` + terms + `}}`
					}
					body := `{"` + dimension + `":` + terms + `}`
					if inner {
						body = `{"tags.outer":{"key":{"value":{"sum_other_doc_count":0,"doc_count_error_upper_bound":0,"buckets":[{"key":"outer","doc_count_error_upper_bound":0,"_reverse":` + body + `}]}}}}`
					}
					var data elastic.Aggregations
					require.NoError(t, json.Unmarshal([]byte(body), &data))
					result, err := f.AggDataFormat(data, nil)
					if other != 0 {
						require.ErrorContains(t, err, "terms completeness")
						require.Nil(t, result)
					} else {
						require.NoError(t, err)
						require.Len(t, result.Timeseries, 1)
						require.Equal(t, float64(2), result.Timeseries[0].Samples[0].Value)
					}
				})
			}
		}
	}
}

func TestLegacyTermsCompletenessUnchanged(t *testing.T) {
	metadata.InitMetadata()
	f := newSeriesFormatFactory(context.Background(), &metadata.Query{Field: "_index"}, nil, time.Time{}, time.Time{}, "", 1440)
	_, agg, err := f.EsAgg(metadata.Aggregates{{Name: Count, Dimensions: []string{"host"}}})
	require.NoError(t, err)
	source, err := agg.Source()
	require.NoError(t, err)
	encoded, err := json.Marshal(source)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "show_term_doc_count_error")
	var data elastic.Aggregations
	require.NoError(t, json.Unmarshal([]byte(`{"host":{"sum_other_doc_count":7,"doc_count_error_upper_bound":-1,"buckets":[{"key":"host-1","_value":{"value":2}}]}}`), &data))
	result, err := f.AggDataFormat(data, nil)
	require.NoError(t, err)
	require.Len(t, result.Timeseries, 1)
	require.Equal(t, float64(2), result.Timeseries[0].Samples[0].Value)
}
