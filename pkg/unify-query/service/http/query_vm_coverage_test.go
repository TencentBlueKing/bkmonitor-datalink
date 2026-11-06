// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package http

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/memcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	ffclient "github.com/thomaspoignant/go-feature-flag"
)

func TestQueryTsVMCoverageUsesExecutedReferences(t *testing.T) {
	mock.Init()
	promql.MockEngine()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetUser(ctx, &metadata.User{Key: "username:test", SpaceUID: "bkcc__2", SkipSpace: "true"})
	// Some settings tests reset Viper, removing package init defaults.
	for key, value := range map[string]any{
		memcache.RistrettoNumCountersPath: 1e7, memcache.RistrettoMaxCostPath: 1 << 30,
		memcache.RistrettoBufferItemsPath: 64, memcache.RistrettoExpiredTimePath: 60,
		memcache.RistrettoExpiredTimeFluxValuePath: 20,
	} {
		old := viper.Get(key)
		viper.Set(key, value)
		t.Cleanup(func() { viper.Set(key, old) })
	}
	influxdb.MockSpaceRouter(ctx)
	var savedFlags []byte
	require.NoError(t, featureFlag.WithClientLock(func() error {
		flags, err := ffclient.GetFlagsFromCache()
		if err != nil {
			return err
		}
		savedFlags, err = json.Marshal(flags)
		return err
	}))
	router, err := influxdb.GetSpaceTsDbRouter()
	require.NoError(t, err)
	require.NoError(t, featureFlag.MockFeatureFlag(ctx, `{"must-vm-query":{"variations":{"Default":true},"defaultRule":{"variation":"Default"}}}`))
	t.Cleanup(func() {
		mock.Vm.Clear()
		require.NoError(t, featureFlag.MockFeatureFlag(context.Background(), string(savedFlags)))
	})
	for _, r := range []struct{ table, rt, storage string }{{"coverage_a.__default__", "rt-a", "storage-a"}, {"coverage_b.__default__", "rt-b", "storage-b"}} {
		require.NoError(t, router.Add(ctx, ir.ResultTableDetailKey, r.table, &ir.ResultTableDetail{StorageId: 2, StorageName: r.storage, TableId: r.table, VmRt: r.rt, StorageType: metadata.VictoriaMetricsStorageType, Fields: []string{"up"}, MeasurementType: "bk_split_measurement"}))
	}
	for _, regexp := range []bool{false, true} {
		for _, instant := range []bool{true, false} {
			for _, named := range []bool{false, true} {
				for _, reverse := range []bool{false, true} {
					t.Run(fmt.Sprintf("regexp=%t/instant=%t/named=%t/reverse=%t", regexp, instant, named, reverse), func(t *testing.T) {
						ctx := metadata.InitHashID(context.Background())
						metadata.SetUser(ctx, &metadata.User{Key: "username:test", SpaceUID: "bkcc__2", SkipSpace: "true"})
						data := `{"resultType":"matrix","result":[{"metric":{"service":"api"},"values":[[1700000000,"7"],[1700000060,"8"]]}]}`
						key := "query_range:1700000000170000006060"
						end := "1700000060"
						if instant {
							data = `{"resultType":"vector","result":[{"metric":{"service":"api"},"value":[1700000000,"7"]}]}`
							key = "query:1700000000"
							end = "1700000000"
						}
						raw := `{"result":true,"code":"00","data":{"vm_query_cluster":{"storage_cluster_list":["storage-a"]},"list":[{"status":"success","isPartial":false,"data":` + data + `}]}}`
						a, b := "a", "b"
						if regexp {
							a = `{__name__=~"a"}`
							b = `{__name__=~"b"}`
						}
						mock.Vm.Set(map[string]any{key + "count_over_time(" + a + "[5m]) or count_over_time(" + b + "[5m])": raw, key + "count_over_time(" + a + "[5m])": raw})
						q := &structured.QueryTs{SpaceUid: "bkcc__2", Start: "1700000000", End: end, Step: "60s", Instant: instant, NotTimeAlign: true, MetricMerge: "a", QueryList: []*structured.Query{
							{TableID: "coverage_a.__default__", FieldName: "up", IsRegexp: regexp, ReferenceName: "a", TimeAggregation: structured.TimeAggregation{Function: "count_over_time", Window: "5m"}},
							{TableID: "coverage_b.__default__", FieldName: "up", IsRegexp: regexp, ReferenceName: "b", TimeAggregation: structured.TimeAggregation{Function: "count_over_time", Window: "5m"}},
						}}
						if !named {
							if reverse {
								q.MetricMerge = "a or b"
							}
							result, err := queryTsWithPromEngine(ctx, q)
							require.NoError(t, err)
							response := result.(*PromData)
							require.NotEmpty(t, response.Tables)
							require.Equal(t, reverse, response.IsPartial)
							if reverse {
								require.Equal(t, metadata.QueryTsPartial, response.Status.Code)
							} else {
								require.Nil(t, response.Status)
							}
							return
						}
						q.MetricMerge = "a or b"
						q.ResponseContract = structured.NamedOutputsV1
						q.LegacyOutputRef = "A"
						q.OutputList = []structured.QueryOutput{{ReferenceName: "A", Expression: "a or b"}, {ReferenceName: "B", Expression: "a"}}
						if reverse {
							q.OutputList[0], q.OutputList[1] = q.OutputList[1], q.OutputList[0]
						}
						result, err := queryTsNamedOutputs(ctx, q)
						require.NoError(t, err)
						require.Len(t, result.Outputs, 2)
						for _, output := range result.Outputs {
							require.NotEmpty(t, output.Tables)
							require.Equal(t, output.ReferenceName == "A", output.IsPartial)
							if output.ReferenceName == "A" {
								require.Equal(t, metadata.QueryTsPartial, output.Status.Code)
							} else {
								require.Nil(t, output.Status)
							}
						}
						require.Nil(t, metadata.GetExpand(ctx), "output expansion must not leak into the parent")
					})
				}
			}
		}
	}
	for _, instant := range []bool{true, false} {
		for _, backendPartial := range []bool{false, true} {
			t.Run(fmt.Sprintf("constant/instant=%t/backendPartial=%t", instant, backendPartial), func(t *testing.T) {
				ctx := metadata.InitHashID(context.Background())
				metadata.SetUser(ctx, &metadata.User{Key: "username:test", SpaceUID: "bkcc__2", SkipSpace: "true"})
				data := `{"resultType":"matrix","result":[{"metric":{},"values":[[1700000000,"1"],[1700000060,"1"]]}]}`
				key, end := "query_range:1700000000170000006060vector(1)", "1700000060"
				if instant {
					data = `{"resultType":"vector","result":[{"metric":{},"value":[1700000000,"1"]}]}`
					key = "query:1700000000vector(1)"
					end = "1700000000"
				}
				raw := fmt.Sprintf(`{"result":true,"code":"00","data":{"vm_query_cluster":{"storage_cluster_list":["storage-a"]},"list":[{"status":"success","isPartial":%t,"data":%s}]}}`, backendPartial, data)
				mock.Vm.Set(map[string]any{key: raw})
				q := &structured.QueryTs{SpaceUid: "bkcc__2", Start: "1700000000", End: end, Step: "60s", Instant: instant, NotTimeAlign: true, MetricMerge: "vector(1)", QueryList: []*structured.Query{
					{TableID: "coverage_a.__default__", FieldName: "up", ReferenceName: "a"},
					{TableID: "coverage_b.__default__", FieldName: "up", ReferenceName: "b"},
				}}
				result, err := queryTsWithPromEngine(ctx, q)
				require.NoError(t, err)
				response := result.(*PromData)
				require.NotEmpty(t, response.Tables)
				require.Equal(t, backendPartial, response.IsPartial)
			})
		}
	}
	t.Run("optional error does not taint following output", func(t *testing.T) {
		ctx := metadata.InitHashID(context.Background())
		metadata.SetUser(ctx, &metadata.User{Key: "username:test", SpaceUID: "bkcc__2", SkipSpace: "true"})
		mock.Vm.Set(map[string]any{"query:1700000000a": `{"result":true,"code":"00","data":{"vm_query_cluster":{"storage_cluster_list":["storage-a"]},"list":[{"status":"success","isPartial":false,"data":{"resultType":"vector","result":[{"metric":{"service":"api"},"value":[1700000000,"7"]}]}}]}}`})
		q := &structured.QueryTs{
			SpaceUid: "bkcc__2", Start: "1700000000", End: "1700000000", Step: "60s", Instant: true, NotTimeAlign: true, MetricMerge: "a", ResponseContract: structured.NamedOutputsV1, LegacyOutputRef: "A",
			OutputList: []structured.QueryOutput{{ReferenceName: "A", Expression: "a"}, {ReferenceName: "B", Expression: "b"}, {ReferenceName: "C", Expression: "a"}},
			QueryList:  []*structured.Query{{TableID: "coverage_a.__default__", FieldName: "up", ReferenceName: "a"}, {TableID: "coverage_b.__default__", FieldName: "up", ReferenceName: "b"}},
		}
		result, err := queryTsNamedOutputs(ctx, q)
		require.NoError(t, err)
		require.Equal(t, OutputState("SUCCESS"), result.Outputs[0].State)
		require.Equal(t, OutputState("ERROR"), result.Outputs[1].State)
		require.Equal(t, OutputState("SUCCESS"), result.Outputs[2].State)
		require.False(t, result.Outputs[2].IsPartial)
		require.Nil(t, metadata.GetExpand(ctx))
	})
}
