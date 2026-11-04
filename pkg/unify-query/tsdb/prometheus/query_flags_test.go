// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package prometheus

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql"
)

func TestDorisQueryFlagsKeepBKBaseWithoutPhysicalRoute(t *testing.T) {
	mock.Init()
	for _, header := range []string{"", `{"doris_direct_query":false}`, `{"doris_direct_query":true}`} {
		flags, err := metadata.ParseQueryFlags(header)
		require.NoError(t, err)
		ctx := metadata.WithQueryFlags(metadata.InitHashID(context.Background()), flags)
		for _, measurement := range []string{metadata.DorisStorageType, "tspider"} {
			for _, q := range []*metadata.Query{
				{StorageType: metadata.BkSqlStorageType, Measurement: measurement, DB: "1001_demo"},
				{StorageType: metadata.BkSqlStorageType, Measurement: measurement, DBs: []string{"1001_demo", "1001_demo_history"}},
			} {
				require.IsType(t, &bksql.Instance{}, GetTsDbInstance(ctx, q), header)
				require.Equal(t, metadata.BkSqlStorageType, q.StorageType)
			}
		}
	}
}
