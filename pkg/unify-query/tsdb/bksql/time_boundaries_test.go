// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License").

package bksql

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql/sql_expr"
)

func TestBKSQLSeriesTimeBoundaries(t *testing.T) {
	mock.Init()
	if http.DefaultTransport == httpmock.DefaultTransport {
		httpmock.Deactivate()
		t.Cleanup(httpmock.Activate)
	}
	const at int64 = 1700000100000
	start, end := time.UnixMilli(at-59999), time.UnixMilli(at)
	pattern := regexp.MustCompile("`dtEventTimeStamp` >= ([0-9]+) AND `dtEventTimeStamp` (<=|<) ([0-9]+)")
	for _, dialect := range []string{"", sql_expr.TSpider, sql_expr.Doris} {
		for _, mode := range []string{"ordinary", "strict_only", "exact_only", "left_open", "ordinary_cutover", "left_open_cutover"} {
			t.Run(fmt.Sprintf("%s/%s", dialect, mode), func(t *testing.T) {
				ctx := metadata.InitHashID(context.Background())
				metadata.SetUser(ctx, &metadata.User{SpaceUID: "boundary"})
				cutover := strings.HasSuffix(mode, "_cutover")
				switch strings.TrimSuffix(mode, "_cutover") {
				case "strict_only":
					ctx = metadata.WithStrictQueryFailures(ctx)
				case "exact_only":
					ctx = metadata.WithExactTimeGrid(ctx)
				case "left_open":
					ctx = metadata.WithLeftOpenTimeWindow(ctx)
				}
				mappingCalls, dataCalls := &atomic.Int32{}, &atomic.Int32{}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var params Params
					if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
						t.Error(err)
						return
					}
					var rows []map[string]any
					if strings.HasPrefix(params.SQL, "DESC") || strings.HasPrefix(params.SQL, "SHOW") {
						mappingCalls.Add(1)
						rows = []map[string]any{{"Field": "dtEventTimeStamp", "Type": "BIGINT"}, {"Field": "value", "Type": "DOUBLE"}, {"Field": "source_id", "Type": "VARCHAR"}}
					} else {
						dataCalls.Add(1)
						match := pattern.FindStringSubmatch(params.SQL)
						if len(match) != 4 {
							t.Errorf("unexpected SQL: %s", params.SQL)
							http.Error(w, "unexpected SQL", 400)
							return
						}
						lower, err := strconv.ParseInt(match[1], 10, 64)
						if err != nil {
							t.Error(err)
							return
						}
						upper, err := strconv.ParseInt(match[3], 10, 64)
						if err != nil {
							t.Error(err)
							return
						}
						exclusiveEnd := strings.Contains(params.SQL, fmt.Sprintf("AND `dtEventTimeStamp` < %d", upper))
						if cutover && !exclusiveEnd {
							t.Errorf("migration cutover must exclude the old route's endpoint: %s", params.SQL)
						}
						for _, point := range []int64{at - 60000, at - 59999, at, at + 1} {
							if point >= lower && (point < upper || (point == upper && match[2] == "<=" && !exclusiveEnd)) {
								rows = append(rows, map[string]any{sql_expr.TimeStamp: point, sql_expr.Value: 7, "source_id": "a"})
							}
						}
					}
					w.Header().Set("Content-Type", "application/json")
					response := Result{Result: true, Code: StatusOK, Data: &QuerySyncResultData{TotalRecords: len(rows), List: rows}}
					if err := json.NewEncoder(w).Encode(response); err != nil {
						t.Error(err)
					}
				}))
				defer server.Close()
				instance, err := NewInstance(ctx, &Options{Address: server.URL, Timeout: time.Second, MaxLimit: 100, Curl: &curl.HttpCurl{}})
				require.NoError(t, err)
				query := &metadata.Query{StorageType: metadata.BkSqlStorageType, DB: "boundary", Measurement: dialect, Field: "value", MetricNames: []string{"a"}}
				query.RangeEndExclusive = cutover
				set := instance.QuerySeriesSet(ctx, query, start, end)
				var timestamps []int64
				for set.Next() {
					it := set.At().Iterator(nil)
					for kind := it.Next(); kind != chunkenc.ValNone; kind = it.Next() {
						timestamp, value := it.At()
						require.Equal(t, float64(7), value)
						timestamps = append(timestamps, timestamp)
					}
					require.NoError(t, it.Err())
				}
				require.NoError(t, set.Err())
				require.Positive(t, mappingCalls.Load(), "the actual field-map HTTP request must run")
				require.Equal(t, int32(1), dataCalls.Load(), "the actual series SQL must run")
				expected := []int64{at - 59999}
				if !cutover && (mode == "left_open" || dialect == sql_expr.Doris) {
					expected = append(expected, at)
				}
				require.Equal(t, expected, timestamps, "must keep the engine's inclusive bounds only for opted-in queries; future samples stay excluded")
			})
		}
	}
}
