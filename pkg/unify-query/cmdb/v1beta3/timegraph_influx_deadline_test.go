// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License").

package v1beta3

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/go-redis/redis/v8"
	"github.com/jarcoal/httpmock"
	"github.com/prometheus/prometheus/model/labels"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	influxrouter "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	localprom "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	uqredis "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	tsdbservice "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/influxdb"
	prombackend "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
	routerconfig "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

// Route through the real Redis-loaded instance factory and InfluxDB HTTP adapter.
// Only the remote HTTP server and routing metadata are fixtures.
func setupTimeGraphInfluxDeadline(t *testing.T) *atomic.Int32 {
	t.Helper()
	mock.Init()
	if http.DefaultTransport == httpmock.DefaultTransport {
		httpmock.Deactivate()
		t.Cleanup(httpmock.Activate)
	}
	timeouts := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/query" {
			t.Errorf("unexpected InfluxDB path: %s", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		sql := r.URL.Query().Get("q")
		parts := strings.Fields(sql)
		measurement := ""
		for n, part := range parts {
			if part == "FROM" && n+1 < len(parts) {
				measurement = strings.Trim(parts[n+1], `"`)
				break
			}
		}
		if measurement == "slow" || measurement == "middle_info_relation" {
			timeouts.Add(1)
			<-r.Context().Done()
			return
		}
		if measurement == "failed" {
			http.Error(w, "synthetic execution failure", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if measurement == "empty" {
			_, _ = io.WriteString(w, `{"results":[{"statement_id":0}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"results":[{"series":[{"name":"relation","tags":{"source_id":"a","middle_id":"b"},"columns":["_time","_value"],"values":[["2023-11-14T22:13:20Z",7]]}]}]}`)
	}))
	t.Cleanup(server.Close)
	endpoint, err := url.Parse(server.URL)
	require.NoError(t, err)
	host, portString, err := net.SplitHostPort(endpoint.Host)
	require.NoError(t, err)
	port, err := strconv.Atoi(portString)
	require.NoError(t, err)

	redisServer := miniredis.RunT(t)
	cluster, err := json.Marshal(&routerconfig.Cluster{HostList: []string{"strict"}})
	require.NoError(t, err)
	hostConfig, err := json.Marshal(&routerconfig.Host{DomainName: host, Port: port, Protocol: influxrouter.HTTP})
	require.NoError(t, err)
	redisServer.HSet("strict-causes:cluster_info", "strict", string(cluster))
	redisServer.HSet("strict-causes:host_info", "strict", string(hostConfig))
	require.NoError(t, uqredis.SetInstance(context.Background(), "strict-causes", &goredis.UniversalOptions{Addrs: []string{redisServer.Addr()}}))
	router := influxrouter.GetInfluxDBRouter()
	require.NoError(t, router.ReloadRouter(context.Background(), "strict-causes", nil))
	t.Cleanup(func() { require.NoError(t, router.Stop()) })

	previousTimeout, previousContentType := tsdbservice.InfluxDBTimeout, tsdbservice.InfluxDBContentType
	previousLimit, previousSLimit := tsdbservice.InfluxDBMaxLimit, tsdbservice.InfluxDBMaxSLimit
	tsdbservice.InfluxDBTimeout, tsdbservice.InfluxDBContentType = 100*time.Millisecond, influxdb.ContentTypeJson
	tsdbservice.InfluxDBMaxLimit, tsdbservice.InfluxDBMaxSLimit = 100, 100
	t.Cleanup(func() {
		tsdbservice.InfluxDBTimeout, tsdbservice.InfluxDBContentType = previousTimeout, previousContentType
		tsdbservice.InfluxDBMaxLimit, tsdbservice.InfluxDBMaxSLimit = previousLimit, previousSLimit
	})
	return timeouts
}

func timeGraphInfluxPhysicalQuery(measurement string) *metadata.Query {
	return &metadata.Query{
		StorageType: metadata.InfluxDBStorageType, ClusterName: "strict", DB: "strict", TableID: "strict.synthetic",
		Measurement: measurement, Measurements: []string{measurement}, Field: "value", Fields: []string{"value"},
		Aggregates: metadata.Aggregates{{Name: "count"}}, MetricNames: []string{"a"},
	}
}

func TestTimeGraphInfluxDeadlineCause(t *testing.T) {
	timeouts := setupTimeGraphInfluxDeadline(t)
	at := time.Unix(1700000000, 0)
	for _, mode := range []string{"querier", "instant", "range"} {
		for _, tt := range []struct {
			name                   string
			strict                 bool
			measurements           []string
			wantError, wantPartial bool
		}{
			{name: "strict_single_timeout", strict: true, measurements: []string{"slow"}, wantError: true},
			{name: "strict_multiple_failures", strict: true, measurements: []string{"slow", "failed"}, wantError: true},
			{name: "strict_partial", strict: true, measurements: []string{"slow", "good"}, wantPartial: true},
			{name: "strict_partial_empty", strict: true, measurements: []string{"slow", "empty"}, wantPartial: true},
			{name: "ordinary_timeout", measurements: []string{"slow"}},
		} {
			t.Run(mode+"/"+tt.name, func(t *testing.T) {
				ctx := initTimeGraphQueryTestEnvironment()
				metadata.SetUser(ctx, &metadata.User{SpaceUID: "strict-cause-test"})
				metadata.GetQueryParams(ctx).SetTime(at.Add(-time.Minute), at.Add(-time.Minute), at, time.Minute, "millisecond", "UTC")
				if tt.strict {
					ctx = metadata.WithStrictQueryFailures(ctx)
				}
				var queries metadata.QueryList
				for _, measurement := range tt.measurements {
					queries = append(queries, timeGraphInfluxPhysicalQuery(measurement))
				}
				metadata.SetQueryReference(ctx, metadata.QueryReference{"a": {{ReferenceName: "a", MetricName: "a", QueryList: queries}}})
				before := timeouts.Load()
				var err error
				values := 0
				if mode == "querier" {
					q := prombackend.NewQuerier(ctx, at.Add(-time.Minute), at, 2, time.Second)
					set := q.Select(false, &storage.SelectHints{Start: at.Add(-time.Minute).UnixMilli(), End: at.UnixMilli()}, labels.MustNewMatcher(labels.MatchEqual, labels.MetricName, "a"))
					for set.Next() {
						it := set.At().Iterator(nil)
						for it.Next() != chunkenc.ValNone {
							values++
						}
						require.NoError(t, it.Err())
					}
					err = set.Err()
				} else {
					engine := pl.NewEngine(pl.EngineOpts{Timeout: 5 * time.Second, MaxSamples: 10000})
					instance := prombackend.NewInstance(ctx, engine, &prombackend.QueryRangeStorage{QueryMaxRouting: 2, Timeout: time.Second}, time.Minute, 2)
					if mode == "instant" {
						var vector pl.Vector
						vector, err = instance.DirectQuery(ctx, "a", at)
						values = len(vector)
					} else {
						var matrix pl.Matrix
						matrix, _, err = instance.DirectQueryRange(ctx, "a", at, at.Add(time.Minute), time.Minute)
						values = len(matrix)
					}
				}
				require.NoError(t, ctx.Err(), "only the InfluxDB child deadline may expire")
				require.Greater(t, timeouts.Load(), before, "the real HTTP request must run")
				if tt.wantError {
					require.ErrorIs(t, err, context.DeadlineExceeded)
					if tt.name == "strict_multiple_failures" {
						require.ErrorContains(t, err, "503", "both physical route failures must be retained")
					}
				} else {
					require.NoError(t, err)
					if tt.name == "strict_partial" {
						require.Greater(t, values, 0)
					} else {
						require.Zero(t, values)
					}
				}
				if tt.wantPartial {
					require.NotNil(t, metadata.GetStatus(ctx))
					require.Equal(t, metadata.QueryTsPartial, metadata.GetStatus(ctx).Code)
				} else {
					require.Nil(t, metadata.GetStatus(ctx))
				}
			})
		}
	}
}

func TestLegacyTargetInfoInfluxDeadlineIsFatal(t *testing.T) {
	timeouts := setupTimeGraphInfluxDeadline(t)
	oldEngine, oldYolo := localprom.GlobalEngine, yoloMode
	localprom.GlobalEngine = pl.NewEngine(pl.EngineOpts{Timeout: 5 * time.Second, MaxSamples: 10000})
	// Ordinary TimeGraph requests retain the backend child deadline. YOLO
	// requests bypass it and are covered by the backend deadline opt-out tests.
	yoloMode = false
	t.Cleanup(func() { localprom.GlobalEngine, yoloMode = oldEngine, oldYolo })
	for _, mode := range []string{"instant", "range"} {
		t.Run(mode, func(t *testing.T) {
			ctx := initTimeGraphQueryTestEnvironment()
			provider := sharedTopologyQueryProvider().(contractSchemaProvider)
			provider.schemas = provider.schemas[:1]
			provider.fields["middle"] = append(provider.fields["middle"], "zone")
			m := &Model{schemaProvider: provider}
			m.SetTimeGraphResolver(func(context.Context, string) (cmdb.CMDB, error) { return m, nil })
			m.timeGraphQueryReference = func(qctx context.Context, q *structured.QueryTs) (metadata.QueryReference, error) {
				if err := q.ToTime(qctx); err != nil {
					return nil, err
				}
				ref := metadata.QueryReference{}
				for _, query := range q.QueryList {
					physical := timeGraphInfluxPhysicalQuery(query.FieldName)
					physical.MetricNames = []string{query.ReferenceName}
					ref[query.ReferenceName] = append(ref[query.ReferenceName], &metadata.QueryMetric{ReferenceName: query.ReferenceName, MetricName: query.FieldName, QueryList: metadata.QueryList{physical}})
				}
				metadata.SetQueryReference(qctx, ref)
				return ref, nil
			}
			before := timeouts.Load()
			var err error
			if mode == "instant" {
				_, _, _, _, _, err = m.QueryResourceMatcher(ctx, "5m", "space", "1700000000", "middle", "source", cmdb.Matcher{"source_id": "a"}, nil, true, []cmdb.Resource{"source", "middle"})
			} else {
				_, _, _, _, _, err = m.QueryResourceMatcherRange(ctx, "5m", "space", "1m", "1700000000", "1700000120", "middle", "source", cmdb.Matcher{"source_id": "a"}, nil, true, []cmdb.Resource{"source", "middle"})
			}
			require.NoError(t, ctx.Err())
			require.Greater(t, timeouts.Load(), before, "target-info must reach the real timed-out HTTP query")
			require.ErrorIs(t, err, context.DeadlineExceeded, fmt.Sprintf("%s target-info timeout must stay fatal", mode))
			require.Nil(t, metadata.GetStatus(ctx), "fatal timeout must not become target_info_failed partial")
		})
	}
}
