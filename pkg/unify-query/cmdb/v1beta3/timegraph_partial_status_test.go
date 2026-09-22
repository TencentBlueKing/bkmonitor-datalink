// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/prompb"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
	promremote "github.com/prometheus/prometheus/storage/remote"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	prombackend "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
)

func TestSharedTopologyPartialStatusCases(t *testing.T) {
	for _, mode := range []string{"instant", "range"} {
		for _, tt := range []struct {
			name          string
			partialStage  string
			statusCode    string
			parentPartial bool
			backendBit    bool
			empty         bool
			queryError    bool
			wantPartial   bool
			wantResult    string
		}{
			{name: "完整非空", wantResult: "success"},
			{name: "完整空图", empty: true, wantResult: "empty"},
			{name: "源信息部分成功", partialStage: "source-info", statusCode: metadata.QueryTsPartial, wantPartial: true, wantResult: "partial"},
			{name: "关系部分成功", partialStage: "relation-edge", statusCode: metadata.QueryTsPartial, wantPartial: true, wantResult: "partial"},
			{name: "目标信息部分成功", partialStage: "target-info", statusCode: metadata.QueryTsPartial, wantPartial: true, wantResult: "partial"},
			{name: "成功路由为空但仍不完整", partialStage: "source-info", statusCode: metadata.QueryTsPartial, empty: true, wantPartial: true, wantResult: "partial"},
			{name: "父请求状态不污染子查询", parentPartial: true, wantResult: "success"},
			{name: "其他状态不误判部分成功", partialStage: "source-info", statusCode: metadata.SpaceTableIDFieldMissingFallback, wantResult: "success"},
			{name: "后端布尔位仍然有效", partialStage: "source-info", backendBit: true, wantPartial: true, wantResult: "partial"},
			{name: "错误优先于部分成功", partialStage: "source-info", statusCode: metadata.QueryTsPartial, queryError: true, wantResult: "failed"},
		} {
			t.Run(mode+"/"+tt.name, func(t *testing.T) {
				ctx := withTimeGraphTargetInfoShow(initTimeGraphQueryTestEnvironment(), true)
				if tt.parentPartial {
					metadata.SetStatus(ctx, metadata.QueryTsPartial, "父请求的状态")
				}
				parentStatus := metadata.GetStatus(ctx)
				recorder := tracetest.NewSpanRecorder()
				provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
				previousProvider := otel.GetTracerProvider()
				otel.SetTracerProvider(provider)
				t.Cleanup(func() {
					otel.SetTracerProvider(previousProvider)
					require.NoError(t, provider.Shutdown(context.Background()))
				})
				engine := pl.NewEngine(pl.EngineOpts{Timeout: 10 * time.Second, MaxSamples: 1000})
				schema := sharedTopologyQueryProvider().(contractSchemaProvider)
				schema.schemas = schema.schemas[:1]
				model := &Model{schemaProvider: schema, timeGraphQueryReference: timeGraphTestQueryReference}
				stages := make(map[string]string)
				beforeStages := make(map[string]float64)
				model.timeGraphVMQueryWithPartial = func(queryCtx context.Context, q *structured.QueryTs, _ string, instant bool, start, end time.Time, step time.Duration) (pl.Matrix, bool, error) {
					stage := timeGraphQueryStage(queryCtx)
					require.Nil(t, metadata.GetStatus(queryCtx), "父请求和此前子查询的状态必须隔离")
					outcome := "success"
					if tt.empty {
						outcome = "empty"
					}
					if stage == tt.partialStage && tt.wantPartial {
						outcome = "partial"
					}
					if tt.queryError {
						outcome = "failed"
					}
					stages[stage] = outcome
					beforeStages[stage] = readTimeGraphMetric(t, "cmdb_timegraph_stage_seconds", map[string]string{"stage": stage, "result": outcome}, "count")
					queryable := storage.QueryableFunc(func(storageCtx context.Context, _, _ int64) (storage.Querier, error) {
						return &storage.MockQuerier{SelectMockFunction: func(bool, *storage.SelectHints, ...*labels.Matcher) storage.SeriesSet {
							// 使用真实 Engine 和 Instance，仅在存储边界模拟多路部分成功的 metadata 协议。
							if stage == tt.partialStage {
								metadata.SetStatus(storageCtx, tt.statusCode, "一路失败，保留其他路由的成功结果")
							}
							if tt.queryError {
								return storage.ErrSeriesSet(errors.New("查询执行失败"))
							}
							if tt.empty {
								return storage.EmptySeriesSet()
							}
							labelSet := map[string][]prompb.Label{
								"source_info_relation": {{Name: "source_id", Value: "a"}},
								"source_middle_flow":   {{Name: "source_id", Value: "a"}, {Name: "middle_id", Value: "b"}},
								"middle_info_relation": {{Name: "middle_id", Value: "b"}},
							}[q.QueryList[0].FieldName]
							require.NotEmpty(t, labelSet)
							series := &prompb.TimeSeries{Labels: labelSet}
							for ts := start; !ts.After(end); ts = ts.Add(step) {
								series.Samples = append(series.Samples, prompb.Sample{Timestamp: ts.UnixMilli(), Value: 1})
							}
							return promremote.FromQueryResult(true, &prompb.QueryResult{Timeseries: []*prompb.TimeSeries{series}})
						}}, nil
					})
					backend := prombackend.NewInstance(queryCtx, engine, queryable, 5*time.Minute, 2)
					var matrix pl.Matrix
					var partial bool
					var err error
					if instant {
						var vector pl.Vector
						vector, partial, err = queryTimeGraphInstant(queryCtx, backend, "a", end)
						matrix = vectorToMatrix(vector)
					} else {
						matrix, partial, err = backend.DirectQueryRange(queryCtx, "a", start, end, step)
					}
					return matrix, partial || (stage == tt.partialStage && tt.backendBit), err
				}
				request := cmdb.SharedTopologyQuery{SpaceUID: "space", Timestamp: 1700000001, SourceType: "source", MaxHops: 1}
				wantPoints := 1
				if mode == "range" {
					request.Timestamp = 0
					request.StartTime, request.EndTime, request.Step = 1700000001, 1700000121, "1m"
					wantPoints = 3
				}
				operationLabels := map[string]string{"scope": "query", "query_mode": mode, "result": tt.wantResult}
				beforeOperations := readTimeGraphMetric(t, "cmdb_topology_operations_total", operationLabels, "counter")
				result, err := model.QuerySharedTopology(ctx, request)
				if !tt.queryError && !tt.empty {
					found := false
					for _, span := range recorder.Ended() {
						if span.Name() == "timegraph-apply-target-info-matrix" {
							found = true
						}
					}
					require.True(t, found, "target-info Matrix writes must have their own span")
				}

				require.Equal(t, parentStatus, metadata.GetStatus(ctx), "子查询不能反向修改父请求状态")
				if tt.queryError {
					require.ErrorContains(t, err, "查询执行失败")
					require.Empty(t, result.Snapshots)
				} else {
					require.NoError(t, err)
					require.Len(t, result.Snapshots, wantPoints)
					for _, snapshot := range result.Snapshots {
						require.Equal(t, tt.wantPartial, snapshot.Partial)
						if tt.wantPartial {
							require.Equal(t, "backend_partial", snapshot.PartialReason)
						} else {
							require.Empty(t, snapshot.PartialReason)
						}
						if tt.empty {
							require.Empty(t, snapshot.Nodes)
							require.Empty(t, snapshot.Edges)
						} else {
							require.Len(t, snapshot.Nodes, 2)
							require.Len(t, snapshot.Edges, 1)
						}
					}
				}
				wantStages := 3
				if tt.empty {
					wantStages = 2
				}
				if tt.queryError {
					wantStages = 1
				}
				require.Len(t, stages, wantStages)
				require.Equal(t, beforeOperations+1, readTimeGraphMetric(t, "cmdb_topology_operations_total", operationLabels, "counter"))
				for stage, outcome := range stages {
					require.Equal(t, beforeStages[stage]+1, readTimeGraphMetric(t, "cmdb_timegraph_stage_seconds", map[string]string{"stage": stage, "result": outcome}, "count"), stage)
				}
				matrixSpans := 0
				for _, span := range recorder.Ended() {
					if span.Name() != "timegraph-query-matrix" {
						continue
					}
					matrixSpans++
					var stage string
					var partial bool
					for _, attr := range span.Attributes() {
						switch attr.Key {
						case "query-stage":
							stage = attr.Value.AsString()
						case "matrix-partial":
							partial = attr.Value.AsBool()
						}
					}
					if !tt.queryError {
						require.Equal(t, stages[stage] == "partial", partial, stage)
					}
				}
				require.Equal(t, wantStages, matrixSpans)
			})
		}
	}
}
