// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License").

package elasticsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	elastic "github.com/olivere/elastic/v7"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func unlimitedESContext(unlimited bool) context.Context {
	mock.Init()
	ctx := metadata.WithStrictQueryFailures(metadata.InitHashID(context.Background()))
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "unlimited-es"})
	if unlimited {
		ctx = promql.WithQueryLimitsDisabled(ctx)
	}
	return ctx
}
func unlimitedESQuery() *metadata.Query {
	return &metadata.Query{DataSource: "bkdata", TableID: "graph.default", DB: "graph", Field: "value",
		TimeField:  metadata.TimeField{Name: "ts", Type: "long", Unit: "millisecond"},
		Aggregates: metadata.Aggregates{{Name: "count", Window: time.Minute, Dimensions: []string{"src", "dst"}}}}
}
func unlimitedESConsume(set storage.SeriesSet) (int, error) {
	count := 0
	for set.Next() {
		count++
	}
	return count, set.Err()
}

func unlimitedESResponse(size int, mode string) map[string]any {
	dstCount := min(size, 2)
	dstBuckets := make([]any, 0, dstCount)
	for d := 0; d < dstCount; d++ {
		srcCount := min(size, 3)
		srcBuckets := make([]any, 0, srcCount)
		for s := 0; s < srcCount; s++ {
			srcBuckets = append(srcBuckets, map[string]any{"key": fmt.Sprintf("s%d", s), "doc_count": 1,
				"ts": map[string]any{"buckets": []any{map[string]any{"key": 1700000000000, "doc_count": 1, "_value": map[string]any{"value": 1}}}}})
		}
		inner := map[string]any{"doc_count_error_upper_bound": 0, "sum_other_doc_count": 3 - srcCount, "buckets": srcBuckets}
		if mode == "unknown" {
			inner["doc_count_error_upper_bound"] = -1
		}
		if mode == "missing" {
			delete(inner, "sum_other_doc_count")
		}
		if mode == "error" && size < 4 {
			inner["doc_count_error_upper_bound"] = 1
		}
		dstBuckets = append(dstBuckets, map[string]any{"key": fmt.Sprintf("d%d", d), "doc_count": 3, "src": inner})
	}
	return map[string]any{"took": 1, "timed_out": mode == "timeout",
		"_shards":      map[string]any{"total": 1, "successful": 1, "failed": 0},
		"hits":         map[string]any{"total": map[string]any{"value": 6, "relation": "eq"}, "hits": []any{}},
		"aggregations": map[string]any{"dst": map[string]any{"doc_count_error_upper_bound": 0, "sum_other_doc_count": 2 - dstCount, "buckets": dstBuckets}}}
}

func unlimitedESServer(t *testing.T, mode string, cancel context.CancelFunc) (*Instance, *[]int, *sync.Mutex) {
	t.Helper()
	if http.DefaultTransport == httpmock.DefaultTransport {
		httpmock.Deactivate()
		t.Cleanup(httpmock.Activate)
	}
	sizes := &[]int{}
	mu := &sync.Mutex{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/":
			_, _ = fmt.Fprint(w, `{"version":{"number":"7.17.0"}}`)
		case r.URL.Path == "/graph":
			_, _ = fmt.Fprint(w, `{"graph":{"settings":{},"mappings":{"properties":{"ts":{"type":"long"},"value":{"type":"double"},"src":{"type":"keyword"},"dst":{"type":"keyword"}}},"aliases":{}}}`)
		case strings.HasSuffix(r.URL.Path, "/_search"):
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			aggs, ok := body["aggregations"].(map[string]any)
			if !ok {
				aggs = body["aggs"].(map[string]any)
			}
			dst := aggs["dst"].(map[string]any)
			size := int(dst["terms"].(map[string]any)["size"].(float64))
			mu.Lock()
			*sizes = append(*sizes, size)
			mu.Unlock()
			if mode == "max_buckets" && size > 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = fmt.Fprint(w, `{"error":{"type":"too_many_buckets_exception","reason":"max_buckets fixture"},"status":400}`)
				return
			}
			responseMode := mode
			if mode == "timeout" || mode == "failed" {
				size = 4
			}
			response := unlimitedESResponse(size, responseMode)
			if mode == "failed" {
				response["_shards"] = map[string]any{"total": 1, "successful": 0, "failed": 1}
			}
			if err := json.NewEncoder(w).Encode(response); err != nil {
				t.Error(err)
			}
			if cancel != nil {
				cancel()
			}
		default:
			t.Errorf("unexpected ES request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	i, err := NewInstance(unlimitedESContext(true), &InstanceOption{Connect: Connect{Address: server.URL}, MaxSize: 1, Timeout: time.Second})
	require.NoError(t, err)
	return i, sizes, mu
}

func TestUnlimitedESCompleteTermsAndIsolation(t *testing.T) {
	mock.Init()
	i, sizes, mu := unlimitedESServer(t, "", nil)
	type outcome struct {
		unlimited bool
		count     int
		err       error
	}
	ch := make(chan outcome, 2)
	for _, unlimited := range []bool{false, true} {
		go func(unlimited bool) {
			count, err := unlimitedESConsume(i.QuerySeriesSet(unlimitedESContext(unlimited), unlimitedESQuery(), time.Unix(1700000000, 0), time.Unix(1700000060, 0)))
			ch <- outcome{unlimited, count, err}
		}(unlimited)
	}
	for n := 0; n < 2; n++ {
		got := <-ch
		require.NoError(t, got.err)
		if got.unlimited {
			require.Equal(t, 6, got.count)
		} else {
			require.Equal(t, 1, got.count)
		}
	}
	mu.Lock()
	observed := append([]int(nil), (*sizes)...)
	mu.Unlock()
	require.ElementsMatch(t, []int{1, 1, 2, 4}, observed)
	require.Equal(t, 1, i.maxSize)
	for _, size := range []int{1, 9} {
		q := unlimitedESQuery()
		q.Size = size
		count, err := unlimitedESConsume(i.QuerySeriesSet(unlimitedESContext(true), q, time.Unix(1700000000, 0), time.Unix(1700000060, 0)))
		require.NoError(t, err)
		require.Equal(t, min(size, 2)*min(size, 3), count)
		mu.Lock()
		require.Equal(t, size, (*sizes)[len(*sizes)-1])
		mu.Unlock()
	}
}

func TestUnlimitedESIncompleteSearchErrors(t *testing.T) {
	for _, mode := range []string{"unknown", "missing", "max_buckets", "timeout", "failed", "error"} {
		t.Run(mode, func(t *testing.T) {
			mock.Init()
			i, sizes, mu := unlimitedESServer(t, mode, nil)
			if mode == "timeout" || mode == "failed" {
				ordinary := metadata.InitHashID(context.Background())
				_, err := unlimitedESConsume(i.QuerySeriesSet(ordinary, unlimitedESQuery(), time.Unix(1700000000, 0), time.Unix(1700000060, 0)))
				require.NoError(t, err)
			}
			count, err := unlimitedESConsume(i.QuerySeriesSet(unlimitedESContext(true), unlimitedESQuery(), time.Unix(1700000000, 0), time.Unix(1700000060, 0)))
			if mode == "error" {
				require.NoError(t, err)
				require.Equal(t, 6, count)
			} else {
				require.Error(t, err)
				require.Zero(t, count)
				if mode == "timeout" {
					require.ErrorIs(t, err, context.DeadlineExceeded)
				}
			}
			if mode == "timeout" || mode == "failed" {
				explicit := unlimitedESQuery()
				explicit.Size = 9
				_, err = unlimitedESConsume(i.QuerySeriesSet(unlimitedESContext(true), explicit, time.Unix(1700000000, 0), time.Unix(1700000060, 0)))
				require.Error(t, err)
				if mode == "timeout" {
					require.ErrorIs(t, err, context.DeadlineExceeded)
				}
			}
			mu.Lock()
			calls := len(*sizes)
			mu.Unlock()
			if mode == "unknown" || mode == "missing" {
				require.Equal(t, 2, calls)
			}
		})
	}
}

func TestUnlimitedESCancellationBetweenRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(unlimitedESContext(true))
	defer cancel()
	i, sizes, mu := unlimitedESServer(t, "", cancel)
	_, err := unlimitedESConsume(i.QuerySeriesSet(ctx, unlimitedESQuery(), time.Unix(1700000000, 0), time.Unix(1700000060, 0)))
	require.ErrorIs(t, err, context.Canceled)
	mu.Lock()
	require.Len(t, *sizes, 1)
	mu.Unlock()
}

func TestUnlimitedESTermsNestedCompleteness(t *testing.T) {
	infos := aggInfoList{ValueAgg{}, TimeAgg{Name: "ts"}, TermAgg{Name: "src"}, NestedAgg{Name: "nested"}, TermAgg{Name: "dst"}}
	for _, inner := range []string{
		`{"sum_other_doc_count":0,"doc_count_error_upper_bound":0,"buckets":[]}`,
		`{"sum_other_doc_count":1,"doc_count_error_upper_bound":0,"buckets":[]}`,
		`{"sum_other_doc_count":0,"doc_count_error_upper_bound":1,"buckets":[]}`,
		`{"sum_other_doc_count":0,"doc_count_error_upper_bound":-1,"buckets":[]}`,
		`{"sum_other_doc_count":0,"buckets":[]}`,
	} {
		raw := fmt.Sprintf(`{"dst":{"sum_other_doc_count":0,"doc_count_error_upper_bound":0,"buckets":[{"key":"d","nested":{"doc_count":1,"src":%s}}]}}`, inner)
		var data elastic.Aggregations
		require.NoError(t, json.Unmarshal([]byte(raw), &data))
		complete, err := termsAggregationsComplete(data, infos)
		if strings.Contains(inner, `"sum_other_doc_count":1`) || strings.Contains(inner, `"doc_count_error_upper_bound":1`) {
			require.NoError(t, err)
			require.False(t, complete)
		} else if strings.Contains(inner, `"doc_count_error_upper_bound":0`) {
			require.NoError(t, err)
			require.True(t, complete)
		} else {
			require.Error(t, err)
		}
	}
}

func TestUnlimitedESUnknownBucketsAreNotComplete(t *testing.T) {
	for _, raw := range []string{
		`{"src":{"sum_other_doc_count":0,"doc_count_error_upper_bound":0}}`,
		`{"src":{"sum_other_doc_count":0,"doc_count_error_upper_bound":0,"buckets":null}}`,
	} {
		var data elastic.Aggregations
		require.NoError(t, json.Unmarshal([]byte(raw), &data))
		complete, err := termsAggregationsComplete(data, aggInfoList{ValueAgg{}, TermAgg{Name: "src"}})
		require.Error(t, err)
		require.False(t, complete)
	}
}

func TestUnlimitedESPreservesExplicitHistoricalRange(t *testing.T) {
	start := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	i := &Instance{}
	ordinary := i.explainDB(unlimitedESContext(false), "graph", true, start, end, "")
	unlimited := i.explainDB(unlimitedESContext(true), "graph", true, start, end, "")
	require.NotContains(t, ordinary, "graph_202301*")
	require.Contains(t, unlimited, "graph_202301*")
}
