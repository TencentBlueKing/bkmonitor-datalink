// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"testing"
	"time"

	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	localprom "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestPathResourcesRequiredRouteQuality(t *testing.T) {
	oldEngine, oldYolo := localprom.GlobalEngine, yoloMode
	localprom.GlobalEngine = pl.NewEngine(pl.EngineOpts{Timeout: time.Second, MaxSamples: 10000})
	t.Cleanup(func() { localprom.GlobalEngine, yoloMode = oldEngine, oldYolo })
	for _, yolo := range []bool{false, true} {
		for _, mode := range []string{"instant", "range"} {
			for _, missing := range []bool{false, true} {
				t.Run(fmt.Sprintf("yolo=%t/%s/missing=%t", yolo, mode, missing), func(t *testing.T) {
					yoloMode = yolo
					ctx := initTimeGraphQueryTestEnvironment()
					model := legacyRegressionModel(legacyAlternativeProvider(true))
					if missing {
						model.timeGraphQueryReference = func(qctx context.Context, q *structured.QueryTs) (metadata.QueryReference, error) {
							if err := q.ToTime(qctx); err != nil {
								return nil, err
							}
							ref := metadata.QueryReference{}
							metadata.SetQueryReference(qctx, ref)
							return ref, nil
						}
					} else {
						// A routed backend with no observations is a complete empty result.
						model.timeGraphVMQuery = func(context.Context, *structured.QueryTs, string, bool, time.Time, time.Time, time.Duration) (pl.Matrix, error) {
							return nil, nil
						}
					}
					var result []cmdb.PathResourcesResult
					var err error
					paths := [][]cmdb.Resource{{"source", "target"}}
					if mode == "instant" {
						result, err = model.QueryPathResources(ctx, "5m", "space", "1700000040", "source", []cmdb.Resource{"target"}, paths, cmdb.Matcher{"source_id": "a"})
					} else {
						result, err = model.QueryPathResourcesRange(ctx, "5m", "space", "1m", "1700000040", "1700000100", "source", []cmdb.Resource{"target"}, paths, cmdb.Matcher{"source_id": "a"})
					}
					if missing {
						var routeErr *relationNoPhysicalRouteError
						require.ErrorAs(t, err, &routeErr)
						require.Equal(t, "source_target_flow", routeErr.metric)
					} else {
						require.NoError(t, err)
					}
					require.Empty(t, result)
					require.Nil(t, metadata.GetStatus(ctx), "new path queries fail missing routes instead of applying legacy partial compatibility")
				})
			}
		}
	}
}

func TestPathResourcesDepthQuality(t *testing.T) {
	oldYolo, oldHops := yoloMode, MaxAllowedHops
	MaxAllowedHops = 5
	t.Cleanup(func() { yoloMode, MaxAllowedHops = oldYolo, oldHops })
	for _, yolo := range []bool{false, true} {
		for _, mode := range []string{"instant", "range"} {
			for _, tc := range []struct {
				name     string
				provider SchemaProvider
				source   cmdb.Resource
				targets  []cmdb.Resource
				paths    [][]cmdb.Resource
				partial  bool
			}{
				{"default schema", NewStaticSchemaProvider(), "deployment", []cmdb.Resource{"datasource"}, nil, true},
				{"truncated then complete target", NewStaticSchemaProvider(), "deployment", []cmdb.Resource{"datasource", "deployment"}, nil, true},
				{"no bounded candidate then complete target", yoloLongPathProvider(), "r0", []cmdb.Resource{"r12", "r0"}, nil, true},
				{"explicit complete path", NewStaticSchemaProvider(), "deployment", []cmdb.Resource{"replicaset"}, [][]cmdb.Resource{{"deployment", "replicaset"}}, false},
				{"complete short schema", legacyAlternativeProvider(true), "source", []cmdb.Resource{"target"}, nil, false},
			} {
				t.Run(fmt.Sprintf("yolo=%t/%s/%s", yolo, mode, tc.name), func(t *testing.T) {
					yoloMode = yolo
					ctx := initTimeGraphQueryTestEnvironment()
					model := legacyRegressionModel(tc.provider)
					model.timeGraphVMQuery = func(context.Context, *structured.QueryTs, string, bool, time.Time, time.Time, time.Duration) (pl.Matrix, error) {
						return nil, nil
					}
					var result []cmdb.PathResourcesResult
					var err error
					if mode == "instant" {
						result, err = model.QueryPathResources(ctx, "5m", "space", "1700000040", tc.source, tc.targets, tc.paths, nil)
					} else {
						result, err = model.QueryPathResourcesRange(ctx, "5m", "space", "1m", "1700000040", "1700000100", tc.source, tc.targets, tc.paths, nil)
					}
					require.NoError(t, err)
					require.Empty(t, result)
					if tc.partial && !yolo {
						require.Equal(t, &metadata.Status{Code: metadata.QueryTsPartial, Message: "path_search_depth_limit"}, metadata.GetStatus(ctx))
					} else {
						require.Nil(t, metadata.GetStatus(ctx))
					}
				})
			}
		}
	}
}

func TestPathDiscoveryPreservesExistingQuality(t *testing.T) {
	oldYolo, oldHops := yoloMode, MaxAllowedHops
	yoloMode, MaxAllowedHops = false, 5
	t.Cleanup(func() { yoloMode, MaxAllowedHops = oldYolo, oldHops })
	ctx := initTimeGraphQueryTestEnvironment()
	metadata.SetStatus(ctx, metadata.QueryTsPartial, "backend_partial")
	model := legacyRegressionModel(NewStaticSchemaProvider())
	paths, err := model.resolveTimeGraphPaths(ctx, "space", "deployment", []cmdb.Resource{"datasource", "deployment", "datasource"}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	require.Equal(t, &metadata.Status{Code: metadata.QueryTsPartial, Message: "backend_partial;path_search_depth_limit"}, metadata.GetStatus(ctx))
}
