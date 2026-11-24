// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License").

package prometheus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

func strictMappingFailureESQuery(t *testing.T) (*metadata.Query, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	if http.DefaultTransport == httpmock.DefaultTransport {
		httpmock.Deactivate()
		t.Cleanup(httpmock.Activate)
	}
	mappingRequests, searches := &atomic.Int32{}, &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && (r.URL.Path == "/strict-mapping-failure" || r.URL.Path == "/strict-mapping-failure/_mapping/"):
			mappingRequests.Add(1)
			http.Error(w, "synthetic mapping unavailable", http.StatusServiceUnavailable)
		case r.Method == http.MethodPost && r.URL.Path == "/strict-mapping-failure/_search":
			searches.Add(1)
			t.Error("mapping failure must stop this route before search")
			http.Error(w, "unexpected search", http.StatusInternalServerError)
		default:
			t.Errorf("unexpected ES request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	storageID := "pr1499-mapping-failure-" + t.Name()
	previous, _ := tsdb.GetStorage(context.Background(), storageID)
	tsdb.SetStorage(storageID, &tsdb.Storage{Type: metadata.ElasticsearchStorageType, Address: server.URL})
	t.Cleanup(func() { tsdb.SetStorage(storageID, previous) })
	return &metadata.Query{
		StorageType: metadata.ElasticsearchStorageType, StorageID: storageID,
		DataSource: structured.BkLog, TableID: "strict.synthetic", DB: "strict-mapping-failure", Field: "value",
		TimeField:  metadata.TimeField{Name: "time", Type: "date", Unit: "millisecond"},
		Aggregates: metadata.Aggregates{{Name: "count"}},
	}, mappingRequests, searches
}

func TestStrictMappingQueryFailureResults(t *testing.T) {
	for _, tt := range []struct {
		name                                        string
		strict, good, empty, wantError, wantPartial bool
	}{
		{name: "strict_all_failed", strict: true, wantError: true},
		{name: "strict_success_and_failed", strict: true, good: true, wantPartial: true},
		{name: "strict_empty_success_and_failed", strict: true, good: true, empty: true, wantPartial: true},
		{name: "ordinary_all_failed"},
		{name: "ordinary_success_and_failed", good: true},
		{name: "ordinary_empty_success_and_failed", good: true, empty: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := strictInstanceTestContext(t)
			if tt.strict {
				ctx = metadata.WithStrictQueryFailures(ctx)
			}
			bad, mappingRequests, badSearches := strictMappingFailureESQuery(t)
			queries := []*metadata.Query{bad}
			var goodSearches *atomic.Int32
			if tt.good {
				var good *metadata.Query
				good, goodSearches = strictGoodESQuery(t, tt.empty, nil)
				queries = append(queries, good)
			}
			strictSetQueryReference(ctx, queries...)
			values, err := strictSelect(t, ctx, nil, strictInstanceTestHints())
			if tt.wantError {
				require.ErrorContains(t, err, "503")
				require.Empty(t, values)
			} else {
				require.NoError(t, err)
				if tt.good && !tt.empty {
					require.Equal(t, []float64{7}, values)
				} else {
					require.Empty(t, values)
				}
			}
			require.GreaterOrEqual(t, mappingRequests.Load(), int32(2), "both the index and fallback mapping requests must fail")
			require.Zero(t, badSearches.Load())
			if tt.good {
				require.EqualValues(t, 1, goodSearches.Load())
			}
			status := metadata.GetStatus(ctx)
			if tt.wantPartial {
				require.NotNil(t, status)
				require.Equal(t, metadata.QueryTsPartial, status.Code)
				require.Contains(t, status.Message, "503")
			} else {
				require.Nil(t, status)
			}
		})
	}
}
