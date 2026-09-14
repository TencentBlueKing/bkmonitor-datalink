package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestFieldSemanticsAcknowledgement(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	q := &structured.QueryTs{QueryList: []*structured.Query{{ReferenceName: "a", FieldSemantics: metadata.FTAEventTagsV1}}}
	first, second := &metadata.FieldSemanticsExecution{}, &metadata.FieldSemanticsExecution{}
	metadata.SetQueryReference(ctx, metadata.QueryReference{"a": {{QueryList: []*metadata.Query{
		{FieldSemantics: metadata.FTAEventTagsV1, FieldSemanticsExecution: first},
		{FieldSemantics: metadata.FTAEventTagsV1, FieldSemanticsExecution: second},
	}}}})
	_, err := fieldSemanticsAcknowledgement(ctx, q, &PromData{})
	require.Error(t, err)
	first.Complete()
	_, err = fieldSemanticsAcknowledgement(ctx, q, &PromData{})
	require.Error(t, err, "one completed route cannot acknowledge two routes")
	second.Complete()
	ack, err := fieldSemanticsAcknowledgement(ctx, q, &PromData{})
	require.NoError(t, err)
	require.True(t, ack, "successful empty data is valid")
	for _, data := range []*PromData{{IsPartial: true}, {Status: &metadata.Status{Code: "error"}}} {
		ack, err = fieldSemanticsAcknowledgement(ctx, q, data)
		require.NoError(t, err)
		require.False(t, ack)
	}
	ack, err = fieldSemanticsAcknowledgement(ctx, &structured.QueryTs{}, &PromData{})
	require.NoError(t, err)
	require.False(t, ack, "legacy requests never acknowledge")
}

func TestFTAAlarmdWireHandlerAcknowledgement(t *testing.T) {
	mock.Init()
	promql.MockEngine()
	body, err := os.ReadFile("testdata/fta_event_alarmd.json")
	require.NoError(t, err)
	// Only the storage ID changes to the existing local mock ES connection.
	body = bytes.ReplaceAll(body, []byte(`"storage_id": "1"`), []byte(`"storage_id": "3"`))
	for _, tc := range []struct {
		name, flags, aggregations string
		wantACK                   bool
	}{
		{"data", ``, `"tags.host":{"key":{"value":{"buckets":[{"key":"host-1","_reverse":{"time":{"buckets":[{"key":1789369260000,"doc_count":2,"_value":{"value":2}}]}}}]}}}`, true},
		{"empty", ``, `"tags.host":{"key":{"value":{"buckets":[]}}}`, true},
		{"timedout", `"timed_out":true,`, `"tags.host":{"key":{"value":{"buckets":[]}}}`, false},
		{"terminated", `"terminated_early":true,`, `"tags.host":{"key":{"value":{"buckets":[]}}}`, false},
		{"shardfailed", `"_shards":{"total":2,"successful":1,"failed":1},`, `"tags.host":{"key":{"value":{"buckets":[]}}}`, false},
		{"malformed", ``, ``, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			influxdb.MockSpaceRouter(ctx)
			calls := 0
			httpmock.RegisterResponder("GET", `=~^http://127.0.0.1:93002/.*bkfta.*`, httpmock.NewStringResponder(200, `{"bkfta_event_test_read":{"mappings":{"properties":{"time":{"type":"date"},"tags":{"type":"nested","properties":{"key":{"type":"keyword"},"value":{"type":"text","fields":{"raw":{"type":"keyword"}}}}}}}}}`))
			httpmock.RegisterResponder("POST", `=~^http://127.0.0.1:93002/.*bkfta.*_search`, func(req *nethttp.Request) (*nethttp.Response, error) {
				calls++
				dsl, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.Contains(t, string(dsl), `"nested"`)
				require.Contains(t, string(dsl), `"tags.key":"host"`)
				require.Contains(t, string(dsl), `"status"`)
				return httpmock.NewStringResponse(200, `{`+tc.flags+`"aggregations":{`+tc.aggregations+`}}`), nil
			})
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/query/ts", bytes.NewReader(body)).WithContext(ctx)
			HandlerQueryTs(c)
			require.Greater(t, calls, 0, rec.Body.String())
			if tc.wantACK {
				require.Equal(t, 200, rec.Code, rec.Body.String())
				require.Equal(t, metadata.FTAEventTagsV1, rec.Header().Get(fieldSemanticsHeader))
				if tc.name == "data" {
					var decoded struct {
						Series []struct {
							Values [][]float64 `json:"values"`
							Groups []string    `json:"group_values"`
						} `json:"series"`
					}
					require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &decoded))
					require.Len(t, decoded.Series, 1)
					require.Equal(t, []string{"host-1"}, decoded.Series[0].Groups)
					require.Equal(t, [][]float64{{1789369260000, 2}}, decoded.Series[0].Values)
				}
			} else {
				require.Empty(t, rec.Header().Get(fieldSemanticsHeader), rec.Body.String())
			}
		})
	}
}

func TestFieldSemanticsLegacyAndInvalidRequest(t *testing.T) {
	require.NoError(t, validateFieldSemanticsRequest(&structured.QueryTs{ResponseContract: structured.NamedOutputsV1}))
	for _, query := range []*structured.Query{
		{FieldSemantics: "unknown"},
		{SourceConditions: &structured.Conditions{}},
		{FieldSemantics: metadata.FTAEventTagsV1},
	} {
		require.Error(t, validateFieldSemanticsRequest(&structured.QueryTs{QueryList: []*structured.Query{query}}))
	}
	require.Error(t, validateFieldSemanticsRequest(&structured.QueryTs{ResponseContract: structured.NamedOutputsV1, QueryList: []*structured.Query{{FieldSemantics: metadata.FTAEventTagsV1}}}))
}

func TestFieldSemanticsUnsupportedEndpoints(t *testing.T) {
	metadata.InitMetadata()
	for _, handler := range []struct {
		name string
		fn   gin.HandlerFunc
	}{
		{"raw", HandlerQueryRaw}, {"scroll", HandlerQueryRawWithScroll}, {"exemplar", HandlerQueryExemplar},
		{"reference", HandlerQueryReference}, {"cluster", HandlerQueryTsClusterMetrics},
	} {
		for _, fields := range []string{`"field_semantics":"fta_event_tags/v1"`, `"field_semantics":"unknown"`, `"source_conditions":{}`} {
			t.Run(handler.name+fields, func(t *testing.T) {
				ctx := metadata.InitHashID(context.Background())
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", "/query/"+handler.name, bytes.NewBufferString(`{"query_list":[{`+fields+`}]}`)).WithContext(ctx)
				handler.fn(c)
				require.Equal(t, 400, rec.Code, rec.Body.String())
				require.Contains(t, rec.Body.String(), "only supported by /query/ts")
				require.Empty(t, rec.Header().Get(fieldSemanticsHeader))
			})
		}
	}
	require.NoError(t, rejectFieldSemantics(&structured.QueryTs{QueryList: []*structured.Query{{}}}))
}
