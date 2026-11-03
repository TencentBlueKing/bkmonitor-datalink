// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

// mockEmptyDBDataLabel 注册一个 data_label 下的两张 ES 结果表：一张有 db，一张 db 为空（如日志采集项结果表与索引集路由表共用 data_label）。
func mockEmptyDBDataLabel(t *testing.T, ctx context.Context, dataLabel, okRT, badRT string) {
	t.Helper()
	influxdb.MockSpaceRouter(ctx)
	router, err := influxdb.GetSpaceTsDbRouter()
	assert.NoError(t, err)

	tables := ir.ResultTableList{}
	if okRT != "" {
		tables = append(tables, okRT)
		err = router.Add(ctx, ir.ResultTableDetailKey, okRT, &ir.ResultTableDetail{
			StorageId: 3, TableId: okRT, DB: "info_empty_db_ok_idx", StorageType: metadata.ElasticsearchStorageType, DataLabel: dataLabel,
		})
		assert.NoError(t, err)
	}
	tables = append(tables, badRT)
	err = router.Add(ctx, ir.ResultTableDetailKey, badRT, &ir.ResultTableDetail{
		StorageId: 3, TableId: badRT, DB: "", StorageType: metadata.ElasticsearchStorageType, DataLabel: dataLabel,
	})
	assert.NoError(t, err)
	err = router.Add(ctx, ir.DataLabelToResultTableKey, dataLabel, &tables)
	assert.NoError(t, err)

	space := router.GetSpace(ctx, "bkcc__2")
	if space == nil {
		space = make(ir.Space)
	}
	for _, rt := range tables {
		space[rt] = &ir.SpaceResultTable{TableId: rt}
	}
	err = router.Add(ctx, ir.SpaceToResultTableKey, "bkcc__2", &space)
	assert.NoError(t, err)
}

func infoTableIDs(queryRef metadata.QueryReference) []string {
	var ids []string
	queryRef.Range("", func(qry *metadata.Query) {
		ids = append(ids, qry.TableID)
	})
	sort.Strings(ids)
	return ids
}

// TestInfoParamsToQueryRef_ES_empty_db_skipped 信息类接口（field_map、tag_keys、tag_values、series、field_keys）
// 与 query/raw 一样跳过 db/dbs 为空的 ES 结果表：同一 data_label 下有 db 的结果表照常查询，不再因空 db 报错。
func TestInfoParamsToQueryRef_ES_empty_db_skipped(t *testing.T) {
	mock.Init()

	for name, c := range map[string]struct {
		okRT string
		want []string
	}{
		"mixed":   {okRT: "info_empty_db_ok_rt", want: []string{"info_empty_db_ok_rt"}},
		"all_bad": {okRT: "", want: nil},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			metadata.SetUser(ctx, &metadata.User{SpaceUID: "bkcc__2"})
			dataLabel := "info_empty_db_" + name
			mockEmptyDBDataLabel(t, ctx, dataLabel, c.okRT, "info_empty_db_bad_rt_"+name)

			queryRef, err := infoParamsToQueryRef(ctx, &Params{
				TableID: structured.TableID(dataLabel),
				Start:   "1752141400",
				End:     "1752141900",
			})
			assert.NoError(t, err)
			assert.Equal(t, c.want, infoTableIDs(queryRef))
		})
	}
}
