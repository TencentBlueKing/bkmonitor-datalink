// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License").

package influxdb

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/go-redis/redis/v8"
	"github.com/influxdata/influxdb/prometheus/remote"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	influxrouter "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	uqredis "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

type boundaryGRPCServer struct {
	remote.UnimplementedQueryTimeSeriesServiceServer
	requests chan *remote.FilterRequest
}

func (s *boundaryGRPCServer) Raw(request *remote.FilterRequest, _ remote.QueryTimeSeriesService_RawServer) error {
	s.requests <- request
	return nil
}

func TestInfluxSeriesTimeBoundaries(t *testing.T) {
	start := time.UnixMilli(1700000040001)
	end := time.UnixMilli(1700000100000)
	for _, mode := range []string{"ordinary", "strict_only", "exact_only", "left_open"} {
		t.Run(mode, func(t *testing.T) {
			ctx := strictInfluxContext(t, mode == "strict_only")
			if mode == "exact_only" {
				ctx = metadata.WithExactTimeGrid(ctx)
			}
			if mode == "left_open" {
				ctx = metadata.WithLeftOpenTimeWindow(ctx)
			}
			expected := fmt.Sprintf("time > %d and time < %d", start.UnixNano(), end.UnixNano())
			if mode == "left_open" {
				expected = fmt.Sprintf("time >= %d and time <= %d", start.UnixNano(), end.UnixNano())
			}
			query := strictInfluxQuery([]string{"relation"}, []string{"value"})
			query.Condition = `source_id = 'a'`
			instance, err := NewInstance(ctx, &Options{Host: "127.0.0.1", Timeout: time.Second})
			require.NoError(t, err)
			for _, aggregate := range []bool{false, true} {
				query.Aggregates = nil
				if aggregate {
					query.Aggregates = metadata.Aggregates{{Name: "count"}}
				}
				sql, err := instance.makeSQL(ctx, query, start, end)
				require.NoError(t, err)
				require.Contains(t, sql, "WHERE "+expected+" AND ("+query.Condition+")")
			}

			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			server := grpc.NewServer()
			capture := &boundaryGRPCServer{requests: make(chan *remote.FilterRequest, 1)}
			remote.RegisterQueryTimeSeriesServiceServer(server, capture)
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(server.Stop)
			redisServer := miniredis.RunT(t)
			require.NoError(t, uqredis.SetInstance(context.Background(), "", "time-boundaries", &goredis.UniversalOptions{Addrs: []string{redisServer.Addr()}}))
			router := influxrouter.GetInfluxDBRouter()
			require.NoError(t, router.ReloadRouter(context.Background(), "time-boundaries", []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}))
			t.Cleanup(func() { require.NoError(t, router.Stop()) })
			instance, err = NewInstance(ctx, &Options{Host: "127.0.0.1", GrpcPort: listener.Addr().(*net.TCPAddr).Port, Protocol: influxrouter.GRPC, Timeout: time.Second, ReadRateLimit: 1000})
			require.NoError(t, err)
			query.Aggregates = nil
			values, err := strictInfluxValues(instance.QuerySeriesSet(ctx, query, start, end))
			require.NoError(t, err)
			require.Empty(t, values)
			select {
			case request := <-capture.requests:
				require.Equal(t, expected+" and "+query.Condition, request.Where)
				require.Equal(t, "relation", request.Measurement)
				require.Equal(t, "value", request.Field)
			case <-time.After(time.Second):
				t.Fatal("series query must reach the real gRPC backend")
			}
		})
	}
}
