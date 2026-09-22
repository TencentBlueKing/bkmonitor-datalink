// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	vm "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/victoriaMetrics"
)

func pipelineInt(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func pipelineMem() runtime.MemStats {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats
}

// TestSharedTopologyPipelineProbe 使用文件流提供相同 VM 响应，经过真实 HTTP、
// JSON 解码和 VM Matrix 转换后构图、遍历、转换、清理、编码。路由解析与真实
// 存储不在此隔离探针范围内。每种构图方式应在独立进程运行，外部同时采样 RSS。
func TestSharedTopologyPipelineProbe(t *testing.T) {
	mode := os.Getenv("TG_PIPELINE_MODE")
	if mode == "" {
		t.Skip("set TG_PIPELINE_MODE=legacy or shared for isolated capacity measurement")
	}
	require.Contains(t, []string{"legacy", "shared"}, mode)
	n, k, concurrency := pipelineInt("TG_EDGES", 1000), pipelineInt("TG_POINTS", 60), pipelineInt("TG_CONCURRENCY", 1)
	require.LessOrEqual(t, k, 60)
	require.LessOrEqual(t, concurrency, 4)
	require.LessOrEqual(t, n*k*concurrency, 300000)
	mock.Init()
	httpmock.Deactivate()
	t.Cleanup(httpmock.Activate)
	initTimeGraphQueryTestEnvironment()
	// A/B 只改变构图方式。放宽输出预算用于测量同样的大响应，不更改生产默认值。
	oldElements, oldBytes := MaxSharedTopologyOutputElements, MaxSharedTopologyOutputBytes
	MaxSharedTopologyOutputElements, MaxSharedTopologyOutputBytes = 1000000, 256*1024*1024
	t.Cleanup(func() { MaxSharedTopologyOutputElements = oldElements; MaxSharedTopologyOutputBytes = oldBytes })
	times := sharedGraphTestTimes(k)
	grid, err := NewTopologyGrid(times)
	require.NoError(t, err)
	fixture := filepath.Join(t.TempDir(), "response.json")
	f, err := os.Create(fixture)
	require.NoError(t, err)
	_, err = fmt.Fprint(f, `{"result":true,"code":"00","data":{"list":[{"status":"success","isPartial":false,"data":{"resultType":"matrix","result":[`)
	require.NoError(t, err)
	for edge := 0; edge < n; edge++ {
		if edge > 0 {
			fmt.Fprint(f, ",")
		}
		fmt.Fprintf(f, `{"metric":{"source_id":"a","middle_id":"%d"},"values":[`, edge)
		for index, ts := range times {
			if index > 0 {
				fmt.Fprint(f, ",")
			}
			fmt.Fprintf(f, `[%d,"1"]`, ts/1000)
		}
		fmt.Fprint(f, `]} `)
	}
	_, err = fmt.Fprint(f, `]}}]}}`)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	stat, err := os.Stat(fixture)
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			http.Error(w, "request read failed", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), "source_info_relation") {
			_, _ = io.WriteString(w, `{"result":true,"code":"00","data":{"list":[{"status":"success","data":{"resultType":"matrix","result":[]}}]}}`)
			return
		}
		file, openErr := os.Open(fixture)
		if openErr != nil {
			http.Error(w, "fixture open failed", 500)
			return
		}
		defer file.Close()
		_, _ = io.Copy(w, file)
	}))
	defer server.Close()
	instance, err := vm.NewInstance(context.Background(), &vm.Options{Address: server.URL, Timeout: 30 * time.Second, Curl: &curl.HttpCurl{}})
	require.NoError(t, err)
	provider := sharedTopologyQueryProvider().(contractSchemaProvider)
	provider.schemas = provider.schemas[:1]
	model := &Model{schemaProvider: provider}
	runtime.GC()
	base := pipelineMem()
	var peak atomic.Uint64
	stop, sampled := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(sampled)
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				current := pipelineMem().HeapAlloc
				for previous := peak.Load(); current > previous; previous = peak.Load() {
					if peak.CompareAndSwap(previous, current) {
						break
					}
				}
			}
		}
	}()
	var wg sync.WaitGroup
	rows := make([]map[string]any, concurrency)
	outputs := make([][]byte, concurrency)
	start := time.Now()
	for worker := 0; worker < concurrency; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(metadata.InitHashID(context.Background()), 45*time.Second)
			defer cancel()
			ctx = metadata.WithBackendResponseLimit(metadata.WithExactTimeGrid(withTimeGraphForceSourceInfo(ctx)), 16*1024*1024)
			var backend time.Duration
			matrixQuery := func(ctx context.Context, query *structured.QueryTs) (pl.Matrix, error) {
				began := time.Now()
				defer func() { backend += time.Since(began) }()
				metadata.SetExpand(ctx, &metadata.VmExpand{ResultTableList: []string{"synthetic"}})
				matrix, partial, err := instance.DirectQueryRange(ctx, query.QueryList[0].FieldName, time.UnixMilli(times[0]), time.UnixMilli(times[k-1]), time.Second)
				if partial && err == nil {
					err = fmt.Errorf("unexpected partial fixture")
				}
				return matrix, err
			}
			row := map[string]any{}
			result, runErr := func() (cmdb.SharedTopologyResult, error) {
				var directGrid *TopologyGrid
				if mode == "shared" {
					directGrid = &grid
				}
				relations := model.sharedTopologyRelations("space", nil, nil, DirectionOutbound)
				began := time.Now()
				graph, err := model.buildTimeGraph(ctx, "space", time.UnixMilli(times[0]), time.UnixMilli(times[k-1]), time.Second, "source", cmdb.Matcher{"source_id": "a"}, nil, sharedTopologyRootRelationKeys("source", 1, relations), relations, "5m", matrixQuery, directGrid)
				row["build_total_ms"] = float64(time.Since(began).Microseconds()) / 1000
				row["backend_ms"] = float64(backend.Microseconds()) / 1000
				if err != nil {
					return cmdb.SharedTopologyResult{}, err
				}
				defer graph.Clean(ctx)
				row["legacy_graphs"] = len(graph.timeGraph)
				row["edge_time_entries"] = graph.edgeCount
				if graph.shared != nil {
					row["shared_edges"] = len(graph.shared.edgeBits)
				}
				row["after_build_heap"] = pipelineMem().HeapAlloc
				began = time.Now()
				snapshots, err := graph.FindSharedTopology(ctx, grid, SharedTopologyQuery{SourceType: "source", SourceMatcher: cmdb.Matcher{"source_id": "a"}, MaxHops: 1})
				if err != nil {
					return cmdb.SharedTopologyResult{}, err
				}
				if len(snapshots) != k || len(snapshots[0].Nodes) != n+1 || len(snapshots[0].Edges) != n {
					return cmdb.SharedTopologyResult{}, fmt.Errorf("unexpected snapshot shape")
				}
				result := cmdb.SharedTopologyResult{StartTime: times[0], EndTime: times[k-1], Step: "1s", PointCount: k, Snapshots: convertSharedTopologySnapshots(snapshots)}
				row["traverse_convert_ms"] = float64(time.Since(began).Microseconds()) / 1000
				return result, nil
			}()
			if runErr != nil {
				row["error"] = runErr.Error()
				rows[worker] = row
				return
			}
			began := time.Now()
			encoded, err := json.Marshal(result)
			if err != nil {
				row["error"] = err.Error()
				rows[worker] = row
				return
			}
			row["encode_ms"] = float64(time.Since(began).Microseconds()) / 1000
			row["json_bytes"] = len(encoded)
			row["sha256"] = fmt.Sprintf("%x", sha256.Sum256(encoded))
			rows[worker] = row
			outputs[worker] = encoded
		}(worker)
	}
	wg.Wait()
	elapsed := time.Since(start)
	held := pipelineMem()
	close(stop)
	<-sampled
	runtime.GC()
	retained := pipelineMem()
	runtime.KeepAlive(outputs)
	outputs = nil
	runtime.GC()
	after := pipelineMem()
	record := map[string]any{"mode": mode, "edges": n, "points": k, "concurrency": concurrency, "fixture_bytes": stat.Size(), "go": runtime.Version(), "gomaxprocs": runtime.GOMAXPROCS(0), "wall_ms": float64(elapsed.Microseconds()) / 1000, "base_heap": base.HeapAlloc, "sampled_peak_heap": peak.Load(), "held_heap": held.HeapAlloc, "retained_output_heap": retained.HeapAlloc, "post_gc_heap": after.HeapAlloc, "total_alloc_delta": held.TotalAlloc - base.TotalAlloc, "gc_count": held.NumGC - base.NumGC, "gc_pause_ns": held.PauseTotalNs - base.PauseTotalNs, "workers": rows, "output_elements_limit": MaxSharedTopologyOutputElements, "output_byte_limit": MaxSharedTopologyOutputBytes}
	encoded, err := json.Marshal(record)
	require.NoError(t, err)
	fmt.Println("PIPELINE_PROBE " + string(encoded))
	for _, row := range rows {
		require.NotContains(t, row, "error")
	}
}
