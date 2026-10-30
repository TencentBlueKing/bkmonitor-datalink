// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License").

package influxdb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/influxdata/influxdb/prometheus/remote"
	"github.com/jarcoal/httpmock"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb/chunkenc"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	influxrouter "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func strictInfluxContext(t *testing.T, strict bool) context.Context {
	t.Helper()
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{SpaceUID: "strict-influx-test"})
	if strict {
		ctx = metadata.WithStrictQueryFailures(ctx)
	}
	return ctx
}

func strictInfluxValues(set storage.SeriesSet) ([]float64, error) {
	var values []float64
	for set.Next() {
		it := set.At().Iterator(nil)
		for kind := it.Next(); kind != chunkenc.ValNone; kind = it.Next() {
			if kind == chunkenc.ValFloat {
				_, value := it.At()
				values = append(values, value)
			}
		}
		if err := it.Err(); err != nil {
			return values, err
		}
	}
	sort.Float64s(values)
	return values, set.Err()
}

func strictInfluxHTTPInstance(t *testing.T, ctx context.Context, cancelOn string, cancel context.CancelFunc) (*Instance, *atomic.Int32) {
	t.Helper()
	if http.DefaultTransport == httpmock.DefaultTransport {
		httpmock.Deactivate()
		t.Cleanup(httpmock.Activate)
	}
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/query" {
			t.Errorf("unexpected InfluxDB path: %s", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		sql := r.URL.Query().Get("q")
		parts := strings.Fields(sql)
		var measurement string
		for n, part := range parts {
			if part == "FROM" && n+1 < len(parts) {
				measurement = strings.Trim(parts[n+1], `"`)
				break
			}
		}
		if cancelOn != "" && measurement == cancelOn {
			cancel()
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case measurement == "failed" || strings.Contains(sql, `count("bad")`):
			http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		case measurement == "backend_error":
			_, _ = io.WriteString(w, `{"results":[{"error":"fixture storage execution failed"}]}`)
		case measurement == "empty":
			_, _ = io.WriteString(w, `{"results":[{"statement_id":0}]}`)
		case measurement == "good" || measurement == "another":
			value := 7
			if measurement == "another" {
				value = 9
			}
			_, _ = fmt.Fprintf(w, `{"results":[{"series":[{"name":%q,"columns":["_time","_value"],"values":[["2023-11-14T22:13:20Z",%d]]}]}]}`, measurement, value)
		default:
			t.Errorf("unexpected InfluxDB SQL: %s", sql)
			http.Error(w, "unexpected query", http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	address, err := url.Parse(server.URL)
	require.NoError(t, err)
	host, portText, err := net.SplitHostPort(address.Host)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	instance, err := NewInstance(ctx, &Options{
		Host: host, Port: port, Timeout: time.Second,
		Protocol: influxrouter.HTTP, ContentType: ContentTypeJson, MaxLimit: 100, MaxSlimit: 100, Curl: &curl.HttpCurl{},
	})
	require.NoError(t, err)
	return instance, calls
}

func strictInfluxQuery(measurements, fields []string) *metadata.Query {
	var names []string
	for _, measurement := range measurements {
		for _, field := range fields {
			names = append(names, "strict:"+measurement+":"+field)
		}
	}
	return &metadata.Query{
		StorageType: metadata.InfluxDBStorageType, DB: "strict", TableID: "strict.synthetic",
		Measurement: measurements[0], Measurements: measurements, Field: fields[0], Fields: fields,
		Aggregates: metadata.Aggregates{{Name: "count"}}, MetricNames: names,
	}
}

func TestStrictInfluxHTTPFailuresPreserveSuccessfulSubqueries(t *testing.T) {
	for _, tt := range []struct {
		name                   string
		strict                 bool
		measurements, fields   []string
		wantValues             []float64
		wantError, wantPartial bool
	}{
		{name: "strict_all_http_fail", strict: true, measurements: []string{"failed"}, wantError: true},
		{name: "strict_backend_error", strict: true, measurements: []string{"backend_error"}, wantError: true},
		{name: "strict_mixed_measurements", strict: true, measurements: []string{"failed", "good", "another"}, wantValues: []float64{7, 9}, wantPartial: true},
		{name: "strict_success_empty", strict: true, measurements: []string{"failed", "empty"}, wantPartial: true},
		{name: "strict_mixed_fields", strict: true, measurements: []string{"good"}, fields: []string{"bad", "value"}, wantValues: []float64{7}, wantPartial: true},
		{name: "strict_complete_empty", strict: true, measurements: []string{"empty"}},
		{name: "ordinary_all_http_fail", measurements: []string{"failed"}},
		{name: "ordinary_mixed", measurements: []string{"failed", "good", "another"}, wantValues: []float64{7, 9}},
		{name: "ordinary_success_empty", measurements: []string{"failed", "empty"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := strictInfluxContext(t, tt.strict)
			if len(tt.fields) == 0 {
				tt.fields = []string{"value"}
			}
			instance, calls := strictInfluxHTTPInstance(t, ctx, "", nil)
			set := instance.QuerySeriesSet(ctx, strictInfluxQuery(tt.measurements, tt.fields),
				time.Unix(1700000000, 0), time.Unix(1700000060, 0))
			values, err := strictInfluxValues(set)
			if tt.wantError {
				require.Error(t, err)
				require.Empty(t, values)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.wantValues, values)
			}
			require.EqualValues(t, len(tt.measurements)*len(tt.fields), calls.Load(), "one failed subquery must not discard later successes")
			status := metadata.GetStatus(ctx)
			if tt.wantPartial {
				require.NotNil(t, status)
				require.Equal(t, metadata.QueryTsPartial, status.Code)
				require.Equal(t, "influxdb_subquery_failed", status.Message)
			} else {
				require.Nil(t, status)
			}
		})
	}
}

func TestStrictInfluxFailurePreservesPreviousPartialReason(t *testing.T) {
	ctx := strictInfluxContext(t, true)
	metadata.SetStatus(ctx, metadata.QueryTsPartial, "previous_partial")
	instance, _ := strictInfluxHTTPInstance(t, ctx, "", nil)
	set := instance.QuerySeriesSet(ctx, strictInfluxQuery([]string{"failed", "good"}, []string{"value"}),
		time.Unix(1700000000, 0), time.Unix(1700000060, 0))
	values, err := strictInfluxValues(set)
	require.NoError(t, err)
	require.Equal(t, []float64{7}, values)
	require.Equal(t, "previous_partial; influxdb_subquery_failed", metadata.GetStatus(ctx).Message)
}

func TestStrictInfluxHTTPParentCancellation(t *testing.T) {
	for _, alreadyCanceled := range []bool{false, true} {
		t.Run(fmt.Sprintf("already_canceled_%t", alreadyCanceled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(strictInfluxContext(t, true))
			t.Cleanup(cancel)
			instance, calls := strictInfluxHTTPInstance(t, ctx, "good", cancel)
			if alreadyCanceled {
				cancel()
			}
			set := instance.QuerySeriesSet(ctx, strictInfluxQuery([]string{"good", "failed"}, []string{"value"}),
				time.Unix(1700000000, 0), time.Unix(1700000060, 0))
			values, err := strictInfluxValues(set)
			require.ErrorIs(t, err, context.Canceled)
			require.Empty(t, values)
			require.Nil(t, metadata.GetStatus(ctx), "cancellation cannot become partial")
			wantCalls := 1
			if alreadyCanceled {
				wantCalls = 0
			}
			require.EqualValues(t, wantCalls, calls.Load())
		})
	}
}

type strictRawStream struct {
	grpc.ClientStream
	frames  []*remote.TimeSeries
	failure error
	index   int
}

func (s *strictRawStream) Recv() (*remote.TimeSeries, error) {
	if s.index < len(s.frames) {
		frame := s.frames[s.index]
		s.index++
		return frame, nil
	}
	if s.failure != nil {
		return nil, s.failure
	}
	return nil, io.EOF
}

func TestStrictInfluxStreamReceiveFailureAndEOF(t *testing.T) {
	failure := errors.New("fixture stream disconnected")
	for _, strict := range []bool{false, true} {
		for _, frames := range []int{0, 1} {
			for _, fail := range []bool{false, true} {
				t.Run(fmt.Sprintf("strict_%t_frames_%d_error_%t", strict, frames, fail), func(t *testing.T) {
					ctx := strictInfluxContext(t, strict)
					raw := &strictRawStream{}
					if frames > 0 {
						raw.frames = []*remote.TimeSeries{{Samples: []*remote.Sample{{TimestampMs: 1700000000000, Value: 7}}}}
					}
					if fail {
						raw.failure = failure
					}
					set := StartStreamSeriesSet(ctx, "strict-stream", &StreamSeriesSetOption{Stream: raw, Timeout: time.Second})
					values, err := strictInfluxValues(set)
					if strict && fail {
						require.ErrorIs(t, err, failure)
					} else {
						require.NoError(t, err)
					}
					if frames > 0 {
						require.Equal(t, []float64{7}, values)
					} else {
						require.Empty(t, values)
					}
				})
			}
		}
	}
}

type strictBlockedRawStream struct {
	grpc.ClientStream
	release chan struct{}
}

func (s *strictBlockedRawStream) Recv() (*remote.TimeSeries, error) {
	<-s.release
	return nil, io.EOF
}

func TestStrictInfluxStreamReceiveTimeout(t *testing.T) {
	for _, strict := range []bool{false, true} {
		t.Run(fmt.Sprintf("strict_%t", strict), func(t *testing.T) {
			ctx := strictInfluxContext(t, strict)
			raw := &strictBlockedRawStream{release: make(chan struct{})}
			t.Cleanup(func() { close(raw.release) })
			set := StartStreamSeriesSet(ctx, "strict-timeout", &StreamSeriesSetOption{
				Stream: raw, Timeout: 20 * time.Millisecond,
			})
			values, err := strictInfluxValues(set)
			require.Empty(t, values)
			if strict {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
