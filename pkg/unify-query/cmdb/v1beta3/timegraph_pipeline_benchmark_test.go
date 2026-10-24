// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package v1beta3

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// A controlled backend delay isolates cross-group shard scheduling. This does
// not measure real VM latency, network capacity, or production graph scale.
func BenchmarkTopologyShardedGroups(b *testing.B) {
	previous := yoloMode
	yoloMode = true
	b.Cleanup(func() { yoloMode = previous })
	provider := selectorProvider()
	provider.schemas = provider.schemas[:1]
	model := &Model{schemaProvider: provider}
	timestamps := make([]int64, 8)
	for i := range timestamps {
		timestamps[i] = 1700000000000 + int64(i)*60000
	}
	grid, err := NewTopologyGrid(timestamps)
	if err != nil {
		b.Fatal(err)
	}
	relations := model.sharedTopologyRelations("space", nil, nil, DirectionBoth)
	for _, ordered := range []bool{true, false} {
		name := "as-ready"
		var collect func(cmdb.Relation, pl.Matrix)
		if ordered {
			name = "ordered"
			collect = func(cmdb.Relation, pl.Matrix) {}
		}
		b.Run(name, func(b *testing.B) {
			var calls atomic.Int64
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				ctx := metadata.WithExactTimeGrid(initTimeGraphQueryTestEnvironment())
				graph, err := newSharedTimeGraph(model.timeGraphConfig("space"), grid)
				if err != nil {
					b.Fatal(err)
				}
				loader := &timeGraphMatrixLoader{model: model, graph: graph, topology: true, start: time.UnixMilli(timestamps[0]), end: time.UnixMilli(timestamps[len(timestamps)-1]), step: time.Minute}
				loader.override = func(_ context.Context, query *structured.QueryTs) (pl.Matrix, error) {
					calls.Add(1)
					time.Sleep(5 * time.Millisecond)
					if query.Start != query.End {
						return nil, metadata.ErrBackendResponseTooLarge
					}
					stamp, err := strconv.ParseInt(query.Start, 10, 64)
					if err != nil {
						return nil, err
					}
					return contractMatrix(map[string]string{"a_id": "root", "b_id": query.SpaceUid}, stamp*1000), nil
				}
				groups := make([]topologyFetchGroup, 2)
				for index := range groups {
					groups[index] = topologyFetchGroup{ctx: ctx, query: &structured.QueryTs{SpaceUid: strconv.Itoa(index), Start: "1700000000", End: "1700000420", Step: "60s"}, relations: relations}
				}
				queries := 0
				err = loader.fetchTopologyGroups(groups, "space", &queries, collect, true)
				if err != nil || graph.nodeBuilder.Length() != 3 || graph.shared.edgeCount() != 4 {
					graph.Clean(ctx)
					b.Fatalf("incomplete graph: queries=%d, error=%v", queries, err)
				}
				graph.Clean(ctx)
			}
			b.ReportMetric(float64(calls.Load())/float64(b.N), "fetches/op")
		})
	}
}
