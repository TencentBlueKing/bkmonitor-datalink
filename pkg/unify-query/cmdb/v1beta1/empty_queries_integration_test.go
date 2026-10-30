//go:build integration

// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.
package v1beta1

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	goRedis "github.com/go-redis/redis/v8"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

type cancelAfterQuerySpan struct {
	*tracetest.SpanRecorder
	cancel context.CancelFunc
}

func (p *cancelAfterQuerySpan) OnEnd(span sdktrace.ReadOnlySpan) {
	p.SpanRecorder.OnEnd(span)
	if span.Name() == "query-do-request" {
		p.cancel()
	}
}

func TestLegacyEmptyQueriesIntegration(t *testing.T) {
	if os.Getenv("UQ_LEGACY_EMPTY_CHILD") != "1" {
		command := exec.Command(os.Args[0], "-test.run=^TestLegacyEmptyQueriesIntegration$", "-test.v", "-test.timeout=30s")
		command.Env = append(os.Environ(), "UQ_LEGACY_EMPTY_CHILD=1")
		command.Dir = t.TempDir()
		output, err := command.CombinedOutput()
		require.NoError(t, err, "%s", output)
		return
	}
	binary, err := exec.LookPath("redis-server")
	require.NoError(t, err)
	dir, err := os.MkdirTemp(os.TempDir(), "lr-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(dir)) })
	socket := filepath.Join(dir, "r")
	server := exec.Command(binary, "--port", "0", "--unixsocket", socket, "--save", "", "--appendonly", "no", "--dir", dir)
	require.NoError(t, server.Start())
	t.Cleanup(func() { _ = server.Process.Kill(); _ = server.Wait() })
	options := &goRedis.UniversalOptions{
		Addrs: []string{socket}, MaxRetries: -1,
		Dialer: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}
	client := goRedis.NewUniversalClient(options)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	require.Eventually(t, func() bool { return client.Ping(context.Background()).Err() == nil }, 3*time.Second, 10*time.Millisecond)
	log.InitTestLogger()
	metadata.InitMetadata()
	require.NoError(t, redis.SetInstance(context.Background(), dir, "legacy-empty", options))
	t.Cleanup(redis.Close)
	router, err := influxdb.SetSpaceTsDbRouter(context.Background(), filepath.Join(dir, "routes.db"), "routes", "legacy-empty", 1, false)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Stop()) })
	promql.GlobalEngine = pl.NewEngine(pl.EngineOpts{MaxSamples: 1000, Timeout: time.Second})
	for _, instant := range []bool{false, true} {
		for _, collectAll := range []bool{false, true} {
			t.Run(fmt.Sprintf("instant=%t/all=%t", instant, collectAll), func(t *testing.T) {
				ctx := metadata.InitHashID(context.Background())
				metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__empty", SkipSpace: "skip"})
				mode, execution := "range", "first_path"
				if instant {
					mode = "instant"
				}
				if collectAll {
					execution = "all_paths"
				}
				check := observeLegacyQuery(t, mode, execution, "empty")
				pathLabels := map[string]string{"route": "vm_legacy", "query_mode": mode, "result": "empty"}
				before := legacyMetricValue(t, "cmdb_relation_path_result_total", pathLabels)
				_, _, results, _, queryErr := testModel.queryResourceMatcherAll(ctx, QueryResourceOptions{
					SpaceUid: "bkcc__empty", Source: "node", Target: "system", IndexMatcher: cmdb.Matcher{"bcs_cluster_id": "cluster", "node": "node"},
					Start: "1700000000", End: "1700000060", Step: "1m", Instant: instant,
				}, collectAll)
				require.NoError(t, queryErr)
				require.NotEmpty(t, results)
				for _, result := range results {
					require.NotNil(t, result.ts)
					require.Empty(t, result.ts)
				}
				require.Equal(t, before+float64(len(results)), legacyMetricValue(t, "cmdb_relation_path_result_total", pathLabels))
				check(results[len(results)-1].path, 0)
			})
		}
	}
	t.Run("cancel-at-completion", func(t *testing.T) {
		ctx, cancel := context.WithCancel(metadata.InitHashID(context.Background()))
		defer cancel()
		metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__empty", SkipSpace: "skip"})
		processor := &cancelAfterQuerySpan{SpanRecorder: tracetest.NewSpanRecorder(), cancel: cancel}
		provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(processor))
		previous := otel.GetTracerProvider()
		otel.SetTracerProvider(provider)
		t.Cleanup(func() { otel.SetTracerProvider(previous); require.NoError(t, provider.Shutdown(context.Background())) })
		_, _, _, _, queryErr := testModel.queryResourceMatcherAll(ctx, QueryResourceOptions{
			SpaceUid: "bkcc__empty", Source: "node", Target: "system", IndexMatcher: cmdb.Matcher{"bcs_cluster_id": "cluster", "node": "node"},
			Start: "1700000000", End: "1700000000", Instant: true, PathResource: []cmdb.Resource{"node", "system"},
		}, true)
		require.ErrorIs(t, queryErr, context.Canceled)
	})
}
