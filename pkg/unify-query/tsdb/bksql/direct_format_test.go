// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bksql

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestNativeDorisSQLUsesPhysicalTables(t *testing.T) {
	mock.Init()
	for _, userSQL := range []string{"", "SELECT minute1, COUNT(*) AS total GROUP BY minute1 ORDER BY minute1"} {
		ctx := metadata.InitHashID(context.Background())
		q := &metadata.Query{DB: "1001_logical", Measurement: "doris", QueryBackend: "doris", SQL: userSQL, Size: 10, DirectTables: []metadata.PhysicalTable{{Database: "logs", Table: "old"}, {Database: "logs", Table: "new"}}}
		fields := metadata.FieldsMap{"dtEventTimeStamp": {FieldType: "BIGINT"}, "log": {FieldType: "TEXT", IsAnalyzed: true}}
		f := NewQueryFactory(ctx, q).WithRangeTime(time.Unix(1700000000, 0), time.Unix(1700000060, 0)).WithFieldsMap(fields).WithTableFieldsMap(TableFieldsMap{"`logs`.`old`": fields, "`logs`.`new`": fields})
		if userSQL == "" {
			q.Aggregates = metadata.Aggregates{{Name: "count"}}
		}
		sql, err := f.SQL()
		require.NoError(t, err)
		require.Contains(t, sql, "`logs`.`old`")
		require.Contains(t, sql, "`logs`.`new`")
		require.Contains(t, sql, "UNION ALL")
		require.NotContains(t, sql, "1001_logical")
		require.NotContains(t, sql, "`thedate`")
		require.NotContains(t, sql, "`dtEventTime`")
		if userSQL != "" {
			require.Contains(t, strings.ToUpper(sql), "FROM_UNIXTIME")
		}
	}
}
