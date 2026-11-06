// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package victoriaMetrics

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/stretchr/testify/require"
)

// Exercise the actual VM response decoder for range and instant queries, including
// successful empty responses whose backend partial flag cannot expose lost storage.
func TestDirectQueryStorageCoverage(t *testing.T) {
	mock.Init()
	for _, mode := range []string{"query", "query_range"} {
		for _, tc := range []struct {
			name           string
			actual         []string
			unknownRequest bool
			emptyList      bool
			data           bool
			backendPartial bool
			wantMismatch   bool
			wantPartial    bool
		}{
			{name: "complete-empty", actual: []string{"storage-a", "storage-b"}},
			{name: "complete-data", actual: []string{"storage-a", "storage-b"}, data: true},
			{name: "missing-storage-empty", actual: []string{"storage-a"}, wantMismatch: true, wantPartial: true},
			{name: "missing-storage-data", actual: []string{"storage-a"}, data: true, wantMismatch: true, wantPartial: true},
			{name: "missing-storage-no-list", actual: []string{"storage-a"}, emptyList: true, wantMismatch: true, wantPartial: true},
			{name: "explicit-empty-coverage", actual: []string{}, wantMismatch: true, wantPartial: true},
			{name: "absent-coverage"},
			{name: "absent-coverage-backend-partial", backendPartial: true, wantPartial: true},
			{name: "unknown-request", actual: []string{"storage-a"}, unknownRequest: true},
			{name: "unknown-request-known-missing", actual: []string{}, unknownRequest: true, wantMismatch: true, wantPartial: true},
			{name: "complete-backend-partial", actual: []string{"storage-a", "storage-b"}, backendPartial: true, wantPartial: true},
			{name: "extra-storage", actual: []string{"storage-a", "storage-b", "storage-c"}},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				ctx := metadata.InitHashID(context.Background())
				second := "storage-b"
				if tc.unknownRequest {
					second = ""
				}
				metadata.SetExpand(ctx, &metadata.VmExpand{ResultTableList: []string{"rt-a", "rt-b"}, RtDetailList: map[string]metadata.RtDetail{
					"rt-a": {TableID: "table-a", StorageName: "storage-a"}, "rt-b": {TableID: "table-b", StorageName: second},
				}})
				var rows []any = []any{}
				resultType := "vector"
				key := "query:1700000000up"
				if mode == "query_range" {
					resultType = "matrix"
					key = "query_range:1700000000170000000060up"
				}
				if tc.data {
					row := map[string]any{"metric": map[string]string{"service": "api"}}
					if mode == "query" {
						row["value"] = []any{1700000000, "7"}
					} else {
						row["values"] = []any{[]any{1700000000, "7"}}
					}
					rows = append(rows, row)
				}
				list := []any{}
				if !tc.emptyList {
					list = append(list, map[string]any{"status": "success", "isPartial": tc.backendPartial, "data": map[string]any{"resultType": resultType, "result": rows}})
				}
				data := map[string]any{"list": list}
				if tc.actual != nil {
					data["vm_query_cluster"] = map[string]any{"storage_cluster_list": tc.actual}
				}
				raw, err := json.Marshal(map[string]any{"result": true, "code": "00", "data": data})
				require.NoError(t, err)
				mock.Vm.Set(map[string]any{key: string(raw)})
				var partial bool
				count := 0
				if mode == "query" {
					vector, p, e := instance.DirectQueryWithPartial(ctx, "up", time.Unix(1700000000, 0))
					partial, err, count = p, e, len(vector)
					if tc.data {
						require.Equal(t, float64(7), vector[0].V)
					}
				} else {
					matrix, p, e := instance.DirectQueryRange(ctx, "up", time.Unix(1700000000, 0), time.Unix(1700000000, 0), time.Minute)
					partial, err, count = p, e, len(matrix)
					if tc.data {
						require.Equal(t, float64(7), matrix[0].Points[0].V)
					}
				}
				require.NoError(t, err)
				require.Equal(t, tc.wantPartial, partial)
				require.Equal(t, map[bool]int{false: 0, true: 1}[tc.data], count)
				status := metadata.GetStatus(ctx)
				if tc.wantMismatch {
					require.NotNil(t, status)
					require.Equal(t, metadata.QueryTsPartial, status.Code)
					require.Contains(t, status.Message, "vm_storage_coverage_mismatch")
				} else if status != nil {
					require.NotContains(t, status.Message, "vm_storage_coverage_mismatch", fmt.Sprint(status))
				}
			})
		}
	}
}

func TestStorageCoveragePartialPreservesExistingReason(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	metadata.SetStatus(ctx, metadata.QueryTsPartial, "existing_partial")
	markStorageCoveragePartial(ctx)
	markStorageCoveragePartial(ctx)
	require.Equal(t, "existing_partial; vm_storage_coverage_mismatch", metadata.GetStatus(ctx).Message)
}
