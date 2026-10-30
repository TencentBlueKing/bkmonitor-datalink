//go:build integration

// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License").

package influxdb

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	goRedis "github.com/go-redis/redis/v8"
	"github.com/influxdata/influxdb/prometheus/remote"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	influxrouter "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

func startInfluxRouterRedis(t *testing.T) {
	t.Helper()
	binary, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("integration test requires redis-server")
	}
	dir, err := os.MkdirTemp(os.TempDir(), "influx-redis-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(dir)) })
	log, err := os.Create(filepath.Join(dir, "redis.log"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, log.Close()) })
	socket := filepath.Join(dir, "redis.sock")
	cmd := exec.Command(binary, "--port", "0", "--unixsocket", socket,
		"--save", "", "--appendonly", "no", "--dir", dir)
	cmd.Stdout, cmd.Stderr = log, log
	require.NoError(t, cmd.Start())
	stopped := make(chan struct{})
	go func() { _ = cmd.Wait(); close(stopped) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-stopped })
	options := &goRedis.UniversalOptions{
		Addrs: []string{socket}, MaxRetries: -1,
		Dialer: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}
	client := goRedis.NewUniversalClient(options)
	defer client.Close()
	require.Eventually(t, func() bool { return client.Ping(context.Background()).Err() == nil },
		5*time.Second, 10*time.Millisecond, "local Redis did not start: %s", log.Name())
	var previousOptions *goRedis.UniversalOptions
	var previousName string
	if previous, ok := redis.Client().(*goRedis.Client); ok {
		opts := previous.Options()
		previousName = redis.ServiceName()
		previousOptions = &goRedis.UniversalOptions{
			Addrs: []string{opts.Addr}, DB: opts.DB, Username: opts.Username, Password: opts.Password, Dialer: opts.Dialer,
		}
	}
	require.NoError(t, redis.SetInstance(context.Background(), "", "grpc-failures", options))
	t.Cleanup(func() {
		redis.Close()
		if previousOptions != nil {
			require.NoError(t, redis.SetInstance(context.Background(), "", previousName, previousOptions))
		}
	})
}

func TestInfluxGRPCStartupFailuresIntegration(t *testing.T) {
	startInfluxRouterRedis(t)
	router := influxrouter.GetInfluxDBRouter()
	require.NoError(t, router.ReloadRouter(context.Background(), "grpc-failures",
		[]grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}))
	t.Cleanup(func() { require.NoError(t, router.Stop()) })
	for _, healthy := range []bool{false, true} {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		host, portText, err := net.SplitHostPort(listener.Addr().String())
		require.NoError(t, err)
		port, err := strconv.Atoi(portText)
		require.NoError(t, err)
		if healthy {
			server := grpc.NewServer()
			remote.RegisterQueryTimeSeriesServiceServer(server, &unlimitedInfluxRawServer{requests: make(chan *remote.FilterRequest, 10)})
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(server.Stop)
		} else {
			// 真实关闭监听端口，使 gRPC 在创建流时收到连接失败。
			require.NoError(t, listener.Close())
		}
		for _, strict := range []bool{false, true} {
			t.Run("healthy_"+strconv.FormatBool(healthy)+"/strict_"+strconv.FormatBool(strict), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(strictInfluxContext(t, strict), 3*time.Second)
				defer cancel()
				i, err := NewInstance(ctx, &Options{
					Host: host, GrpcPort: port, Protocol: influxrouter.GRPC,
					Timeout: time.Second, ReadRateLimit: 100, MaxLimit: 100, MaxSlimit: 100,
				})
				require.NoError(t, err)
				set := i.grpcStream(ctx, "db", "rp", "good", "value", "", "", 100, 100)
				values, err := strictInfluxValues(set)
				if !healthy && strict {
					require.ErrorContains(t, err, "Unavailable")
				} else {
					require.NoError(t, err)
				}
				if healthy {
					require.Equal(t, []float64{1, 2, 3}, values)
				} else {
					require.Empty(t, values)
				}
				q := strictInfluxQuery([]string{"good"}, []string{"value", "other"})
				q.Aggregates = nil
				_, err = strictInfluxValues(i.QuerySeriesSet(ctx, q, time.Unix(1700000000, 0), time.Unix(1700000060, 0)))
				if !healthy && strict {
					require.ErrorContains(t, err, "influxdb subqueries failed")
				} else {
					require.NoError(t, err)
				}
				require.NoError(t, ctx.Err())
			})
		}
	}
}
