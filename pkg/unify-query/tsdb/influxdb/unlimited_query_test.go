// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License").

package influxdb

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	goRedis "github.com/go-redis/redis/v8"
	"github.com/influxdata/influxdb/prometheus/remote"
	"github.com/jarcoal/httpmock"
	"github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	influxrouter "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

func unlimitedInfluxContext(t *testing.T, unlimited bool) context.Context {
	ctx := strictInfluxContext(t, true)
	if unlimited {
		ctx = promql.WithQueryLimitsDisabled(ctx)
	}
	return ctx
}

func TestUnlimitedInfluxSQLLimits(t *testing.T) {
	i := &Instance{maxLimit: 2, maxSLimit: 3, tolerance: 1}
	for _, unlimited := range []bool{false, true} {
		ctx := unlimitedInfluxContext(t, unlimited)
		q := strictInfluxQuery([]string{"good"}, []string{"value"})
		sql, err := i.makeSQL(ctx, q, time.Unix(1700000000, 0), time.Unix(1700000060, 0))
		require.NoError(t, err)
		if unlimited {
			require.NotContains(t, sql, " LIMIT ")
			require.NotContains(t, sql, " SLIMIT ")
		} else {
			require.Contains(t, sql, " LIMIT 3 SLIMIT 4")
		}
		q.OffsetInfo = metadata.OffSetInfo{Limit: 9, SLimit: 7}
		sql, err = i.makeSQL(ctx, q, time.Unix(1700000000, 0), time.Unix(1700000060, 0))
		require.NoError(t, err)
		if unlimited {
			require.Contains(t, sql, " LIMIT 9 SLIMIT 7")
		} else {
			require.Contains(t, sql, " LIMIT 3 SLIMIT 4")
		}
	}
	require.Equal(t, 2, i.maxLimit)
	require.Equal(t, 3, i.maxSLimit)
}

func TestUnlimitedInfluxHTTPDeadlineAndStatus(t *testing.T) {
	mockCtx := unlimitedInfluxContext(t, true)
	if http.DefaultTransport == httpmock.DefaultTransport {
		httpmock.Deactivate()
		t.Cleanup(httpmock.Activate)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("q"), "slow") {
			select {
			case <-time.After(80 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
		_, _ = io.WriteString(w, `{"results":[{"series":[{"name":"good","columns":["_time","_value"],"values":[["2023-11-14T22:13:20Z",1],["2023-11-14T22:13:21Z",2],["2023-11-14T22:13:22Z",3]]}]}]}`)
	}))
	defer server.Close()
	address, err := url.Parse(server.URL)
	require.NoError(t, err)
	host, portText, err := net.SplitHostPort(address.Host)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	i, err := NewInstance(mockCtx, &Options{
		Host: host, Port: port, Protocol: influxrouter.HTTP,
		ContentType: ContentTypeJson, Curl: &curl.HttpCurl{}, Timeout: 20 * time.Millisecond, MaxLimit: 1, MaxSlimit: 1,
	})
	require.NoError(t, err)
	for _, unlimited := range []bool{false, true} {
		for _, slow := range []bool{false, true} {
			t.Run(fmt.Sprintf("unlimited_%t_slow_%t", unlimited, slow), func(t *testing.T) {
				ctx := unlimitedInfluxContext(t, unlimited)
				field := "value"
				if slow {
					field = "slow"
				}
				q := strictInfluxQuery([]string{"good"}, []string{field})
				values, err := strictInfluxValues(i.QuerySeriesSet(ctx, q, time.Unix(1700000000, 0), time.Unix(1700000060, 0)))
				if !unlimited && slow {
					require.Error(t, err)
					return
				}
				require.NoError(t, err)
				require.Equal(t, []float64{1, 2, 3}, values)
				if unlimited {
					require.Nil(t, metadata.GetStatus(ctx))
				} else {
					require.NotNil(t, metadata.GetStatus(ctx))
				}
			})
		}
	}
	ctx, cancel := context.WithTimeout(unlimitedInfluxContext(t, true), 10*time.Millisecond)
	defer cancel()
	_, err = strictInfluxValues(i.QuerySeriesSet(ctx, strictInfluxQuery([]string{"good"}, []string{"slow"}), time.Unix(1700000000, 0), time.Unix(1700000060, 0)))
	require.Error(t, err)
	require.Equal(t, 20*time.Millisecond, i.timeout)
	require.Equal(t, 1, i.maxLimit)
}

type unlimitedInfluxRawServer struct {
	remote.UnimplementedQueryTimeSeriesServiceServer
	requests chan *remote.FilterRequest
}

func (s *unlimitedInfluxRawServer) Raw(req *remote.FilterRequest, stream remote.QueryTimeSeriesService_RawServer) error {
	s.requests <- req
	select {
	case <-time.After(80 * time.Millisecond):
	case <-stream.Context().Done():
		return stream.Context().Err()
	}
	for n := 0; n < 3; n++ {
		if err := stream.Send(&remote.TimeSeries{Samples: []*remote.Sample{{TimestampMs: 1700000000000 + int64(n)*1000, Value: float64(n + 1)}}}); err != nil {
			return err
		}
	}
	return nil
}

func TestUnlimitedInfluxRealRawStream(t *testing.T) {
	ctx := unlimitedInfluxContext(t, true)
	var previousOptions *goRedis.UniversalOptions
	var previousName string
	if client := redis.Client(); client != nil {
		previous := client.(*goRedis.Client).Options()
		previousName = redis.ServiceName()
		previousOptions = &goRedis.UniversalOptions{
			Addrs: []string{previous.Addr}, DB: previous.DB, Username: previous.Username, Password: previous.Password,
		}
	}
	mini := miniredis.RunT(t)
	require.NoError(t, redis.SetInstance(ctx, "unlimited-fixture", &goRedis.UniversalOptions{Addrs: []string{mini.Addr()}}))
	t.Cleanup(func() {
		redis.Close()
		if previousOptions != nil {
			require.NoError(t, redis.SetInstance(context.Background(), previousName, previousOptions))
		}
	})
	router := influxrouter.GetInfluxDBRouter()
	require.NoError(t, router.ReloadRouter(ctx, "unlimited-test", []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}))
	t.Cleanup(func() { require.NoError(t, router.Stop()) })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	raw := &unlimitedInfluxRawServer{requests: make(chan *remote.FilterRequest, 10)}
	remote.RegisterQueryTimeSeriesServiceServer(server, raw)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	host, portText, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	i, err := NewInstance(ctx, &Options{
		Host: host, GrpcPort: port, Protocol: influxrouter.GRPC,
		Timeout: 20 * time.Millisecond, ReadRateLimit: 1, MaxLimit: 2, MaxSlimit: 2, Tolerance: 1,
	})
	require.NoError(t, err)
	for _, unlimited := range []bool{false, true} {
		ctx := unlimitedInfluxContext(t, unlimited)
		q := strictInfluxQuery([]string{"good"}, []string{"value"})
		q.Aggregates = nil
		values, err := strictInfluxValues(i.QuerySeriesSet(ctx, q, time.Unix(1700000000, 0), time.Unix(1700000060, 0)))
		req := <-raw.requests
		if unlimited {
			require.NoError(t, err)
			require.Equal(t, []float64{1, 2, 3}, values)
			require.Zero(t, req.Limit)
			require.Zero(t, req.Slimit)
		} else {
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.EqualValues(t, 3, req.Limit)
			require.EqualValues(t, 3, req.Slimit)
		}
	}
	q := strictInfluxQuery([]string{"good"}, []string{"value"})
	q.Aggregates = nil
	q.OffsetInfo = metadata.OffSetInfo{Limit: 9, SLimit: 7}
	values, err := strictInfluxValues(i.QuerySeriesSet(unlimitedInfluxContext(t, true), q, time.Unix(1700000000, 0), time.Unix(1700000060, 0)))
	require.NoError(t, err)
	require.Len(t, values, 3)
	req := <-raw.requests
	require.EqualValues(t, 9, req.Limit)
	require.EqualValues(t, 7, req.Slimit)
	parent, cancel := context.WithTimeout(unlimitedInfluxContext(t, true), 30*time.Millisecond)
	defer cancel()
	_, err = strictInfluxValues(i.QuerySeriesSet(parent, q, time.Unix(1700000000, 0), time.Unix(1700000060, 0)))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, 20*time.Millisecond, i.timeout)
	require.EqualValues(t, 1, i.readRateLimit)
}
