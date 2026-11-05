// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License").

package prometheus

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

var strictInstanceTestStart = time.Unix(1700000000, 0)

func strictInstanceTestContext(t *testing.T) context.Context {
	t.Helper()
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "strict-instance-test"})
	metadata.GetQueryParams(ctx).SetTime(strictInstanceTestStart, strictInstanceTestStart,
		strictInstanceTestStart.Add(time.Minute), time.Minute, "millisecond", "UTC")
	return ctx
}

func strictMissingESQuery(t *testing.T) *metadata.Query {
	t.Helper()
	storageID := "pr1499-missing-es-" + t.Name()
	stg, _ := tsdb.GetStorage(context.Background(), storageID)
	require.Nil(t, stg, "the failing path must use a genuinely missing storage instance")
	return &metadata.Query{
		StorageType: metadata.ElasticsearchStorageType,
		StorageID:   storageID,
		TableID:     "strict.synthetic",
	}
}

func strictSetQueryReference(ctx context.Context, queries ...*metadata.Query) {
	metadata.SetQueryReference(ctx, metadata.QueryReference{
		"a": {{ReferenceName: "a", MetricName: "strict.synthetic", QueryList: queries}},
	})
}

func strictSelect(t *testing.T, ctx context.Context, cache *SelectorCache, hints *storage.SelectHints) ([]float64, error) {
	t.Helper()
	q := NewQuerier(ctx, strictInstanceTestStart, strictInstanceTestStart.Add(time.Minute), 2, time.Second)
	q.selectorCache = cache
	matcher := labels.MustNewMatcher(labels.MatchEqual, labels.MetricName, "a")
	return consumeFloatSeriesSet(q.Select(false, hints, matcher))
}

func strictInstanceTestHints() *storage.SelectHints {
	return &storage.SelectHints{
		Start: strictInstanceTestStart.UnixMilli(),
		End:   strictInstanceTestStart.Add(time.Minute).UnixMilli(), Step: time.Minute.Milliseconds(),
	}
}

// The successful route uses the real instance factory, ES adapter, and HTTP
// response decoding. Only the remote server is a fixture; the missing route
// follows GetStorage -> GetTsDbInstance -> getQueryList without an override.
func strictGoodESQuery(t *testing.T, empty bool, onSearch func()) (*metadata.Query, *atomic.Int32) {
	t.Helper()
	if http.DefaultTransport == httpmock.DefaultTransport {
		httpmock.Deactivate()
		t.Cleanup(httpmock.Activate)
	}
	searches := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/strict-index":
			_, _ = io.WriteString(w, `{"strict-index":{"settings":{},"mappings":{"properties":{"time":{"type":"date"},"value":{"type":"long"}}}}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/strict-index/_search":
			searches.Add(1)
			if onSearch != nil {
				onSearch()
			}
			if empty {
				_, _ = io.WriteString(w, `{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`)
			} else {
				_, _ = io.WriteString(w, `{"hits":{"total":{"value":1,"relation":"eq"},"hits":[]},"aggregations":{"_value":{"value":7}}}`)
			}
		default:
			t.Errorf("unexpected ES request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	storageID := "pr1499-good-es-" + t.Name()
	previous, _ := tsdb.GetStorage(context.Background(), storageID)
	tsdb.SetStorage(storageID, &tsdb.Storage{Type: metadata.ElasticsearchStorageType, Address: server.URL})
	t.Cleanup(func() { tsdb.SetStorage(storageID, previous) })
	return &metadata.Query{
		StorageType: metadata.ElasticsearchStorageType, StorageID: storageID,
		DataSource: structured.BkLog, TableID: "strict.synthetic", DB: "strict-index", Field: "value",
		TimeField:  metadata.TimeField{Name: "time", Type: "date", Unit: "millisecond"},
		Aggregates: metadata.Aggregates{{Name: "count"}},
	}, searches
}

func TestStrictPhysicalInstanceFailureResults(t *testing.T) {
	for _, tt := range []struct {
		name                   string
		strict, good, empty    bool
		wantError, wantPartial bool
	}{
		{name: "strict_all_missing", strict: true, wantError: true},
		{name: "strict_success_and_missing", strict: true, good: true, wantPartial: true},
		{name: "strict_empty_success_and_missing", strict: true, good: true, empty: true, wantPartial: true},
		{name: "ordinary_all_missing"},
		{name: "ordinary_success_and_missing", good: true},
		{name: "ordinary_empty_success_and_missing", good: true, empty: true},
		{name: "exact_grid_is_not_strict"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := strictInstanceTestContext(t)
			if tt.strict {
				ctx = metadata.WithStrictQueryFailures(ctx)
			}
			if tt.name == "exact_grid_is_not_strict" {
				ctx = metadata.WithExactTimeGrid(ctx)
			}
			queries := []*metadata.Query{strictMissingESQuery(t)}
			var searches *atomic.Int32
			if tt.good {
				var good *metadata.Query
				good, searches = strictGoodESQuery(t, tt.empty, nil)
				queries = append(queries, good)
			}
			strictSetQueryReference(ctx, queries...)
			require.Equal(t, len(queries), metadata.GetQueryReference(ctx).Count())
			values, err := strictSelect(t, ctx, nil, strictInstanceTestHints())
			if tt.wantError {
				require.ErrorContains(t, err, "query instance unavailable")
				require.Empty(t, values)
			} else {
				require.NoError(t, err)
				if tt.good && !tt.empty {
					require.Equal(t, []float64{7}, values)
				} else {
					require.Empty(t, values)
				}
			}
			if tt.good {
				require.EqualValues(t, 1, searches.Load(), "the surviving route must execute")
			}
			status := metadata.GetStatus(ctx)
			if tt.wantPartial {
				require.NotNil(t, status)
				require.Equal(t, metadata.QueryTsPartial, status.Code)
				require.Contains(t, status.Message, "query instance unavailable")
			} else {
				require.Nil(t, status)
			}
		})
	}
}

func TestStrictPhysicalInstanceFailureIgnoresUnrelatedTimeRoute(t *testing.T) {
	ctx := metadata.WithStrictQueryFailures(strictInstanceTestContext(t))
	bad := strictMissingESQuery(t)
	bad.RouteStart = strictInstanceTestStart.Add(-2 * time.Hour)
	bad.RouteEnd = strictInstanceTestStart.Add(-time.Hour)
	bad.RouteQueryStart, bad.RouteQueryEnd = bad.RouteStart, bad.RouteEnd
	good, searches := strictGoodESQuery(t, false, nil)
	strictSetQueryReference(ctx, bad, good)
	values, err := strictSelect(t, ctx, nil, strictInstanceTestHints())
	require.NoError(t, err)
	require.Equal(t, []float64{7}, values)
	require.EqualValues(t, 1, searches.Load())
	require.Nil(t, metadata.GetStatus(ctx))
}

func TestStrictPhysicalInstanceFailureCancellation(t *testing.T) {
	t.Run("already_canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(metadata.WithStrictQueryFailures(strictInstanceTestContext(t)))
		strictSetQueryReference(ctx, strictMissingESQuery(t))
		cancel()
		values, err := strictSelect(t, ctx, nil, strictInstanceTestHints())
		require.Error(t, err)
		require.Empty(t, values)
		require.Nil(t, metadata.GetStatus(ctx))
	})
	t.Run("cancel_during_surviving_route", func(t *testing.T) {
		ctx, cancel := context.WithCancel(metadata.WithStrictQueryFailures(strictInstanceTestContext(t)))
		t.Cleanup(cancel)
		good, searches := strictGoodESQuery(t, false, cancel)
		strictSetQueryReference(ctx, strictMissingESQuery(t), good)
		values, err := strictSelect(t, ctx, nil, strictInstanceTestHints())
		require.ErrorIs(t, err, context.Canceled)
		require.Empty(t, values)
		require.EqualValues(t, 1, searches.Load())
		require.Nil(t, metadata.GetStatus(ctx), "parent cancellation takes priority over partial")
	})
}

func TestStrictPhysicalInstanceFailureCacheIsolation(t *testing.T) {
	for _, firstStrict := range []bool{false, true} {
		t.Run(fmt.Sprintf("strict_first_%t", firstStrict), func(t *testing.T) {
			base := strictInstanceTestContext(t)
			strictSetQueryReference(base, strictMissingESQuery(t))
			cache := NewSelectorCache(SelectorCacheLimits{MaxBytes: 1024 * 1024})
			for _, strict := range []bool{firstStrict, !firstStrict, firstStrict, !firstStrict} {
				ctx := base
				if strict {
					ctx = metadata.WithStrictQueryFailures(ctx)
				}
				values, err := strictSelect(t, ctx, cache, strictInstanceTestHints())
				require.Empty(t, values)
				if strict {
					require.ErrorContains(t, err, "query instance unavailable")
				} else {
					require.NoError(t, err)
				}
				require.Nil(t, metadata.GetStatus(ctx))
			}
			require.Equal(t, SelectorCacheStats{Misses: 2, Hits: 2}, cache.Stats())
		})
	}
}

func TestOrdinaryEmptyRouteAcceptsNilSelectHints(t *testing.T) {
	ctx := strictInstanceTestContext(t)
	strictSetQueryReference(ctx)
	values, err := strictSelect(t, ctx, nil, nil)
	require.NoError(t, err)
	require.Empty(t, values)
	require.Nil(t, metadata.GetStatus(ctx))
}
