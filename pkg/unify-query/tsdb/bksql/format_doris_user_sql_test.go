// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bksql_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql/sql_expr"
)

// TestNewQueryFactory_Doris_UserSQL_ThroughQueryPath renders user SQL for Doris through the query path
// (NewQueryFactory -> SQL -> ParserSQL -> ParseDorisSQLWithVisitor) and checks the rendered statement:
//   - a query over several physical tables becomes one UNION ALL sub-query per table, each bounded by the
//     time range, with the user's outer clauses applied once to the combined rows;
//   - a FROM sub-query keeps its outer WHERE, and the time range is injected into the inner query on the
//     physical table;
//   - a sub-query alias referenced by the outer query is kept.
func TestNewQueryFactory_Doris_UserSQL_ThroughQueryPath(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())
	start := time.Unix(1741795260, 0)
	end := time.Unix(1741796260, 0)

	const timeRange = "(`dtEventTimeStamp` >= 1741795260000 AND `dtEventTimeStamp` <= 1741796260000 AND `dtEventTime` >= '2025-03-13 00:01:00' AND `dtEventTime` <= '2025-03-13 00:17:41' AND `thedate` = '20250313')"

	fieldsMap := metadata.FieldsMap{
		"dtEventTimeStamp": {FieldType: sql_expr.DorisTypeBigInt},
		"level":            {FieldType: sql_expr.DorisTypeString},
		"log":              {FieldType: sql_expr.DorisTypeText},
		"serverIp":         {FieldType: sql_expr.DorisTypeString},
	}

	for _, c := range []struct {
		name    string
		dbs     []string
		userSQL string
		wantSQL string
	}{
		{
			name:    "union_of_physical_tables",
			dbs:     []string{"db_a", "db_b"},
			userSQL: "SELECT level, COUNT(*) AS n FROM t WHERE level = 'ERROR' OR serverIp = '127.0.0.1' GROUP BY level ORDER BY n DESC LIMIT 10",
			// every table is bounded by the time range as a whole; aggregation, order and limit run once on the combined rows
			wantSQL: "SELECT `level`, COUNT(*) AS n FROM (" +
				"SELECT `level` FROM `db_b`.doris WHERE (`level` = 'ERROR' OR `serverIp` = '127.0.0.1') AND (" + timeRange + ")" +
				" UNION ALL " +
				"SELECT `level` FROM `db_a`.doris WHERE (`level` = 'ERROR' OR `serverIp` = '127.0.0.1') AND (" + timeRange + ")" +
				") AS combined_data GROUP BY `level` ORDER BY `n` DESC LIMIT 10",
		},
		{
			name:    "from_subquery_outer_where",
			dbs:     []string{"db_a"},
			userSQL: "SELECT level, cnt FROM (SELECT level, COUNT(*) AS cnt FROM t GROUP BY level) AS sub WHERE cnt > 1",
			// the time range goes into the inner query on the physical table; the outer WHERE stays outside
			wantSQL: "SELECT `level`, `cnt` FROM (SELECT `level`, COUNT(*) AS cnt FROM `db_a`.doris WHERE " + timeRange + " GROUP BY `level`) sub WHERE `cnt` > 1 LIMIT 100",
		},
		{
			name:    "subquery_alias",
			dbs:     []string{"db_a"},
			userSQL: "SELECT COUNT(DISTINCT val) AS n FROM (SELECT serverIp AS val FROM t) t WHERE val != ''",
			wantSQL: "SELECT COUNT(DISTINCT(`val`)) AS n FROM (SELECT `serverIp` AS val FROM `db_a`.doris WHERE " + timeRange + ") t WHERE `val` != '' LIMIT 100",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			q := &metadata.Query{
				StorageType: metadata.BkSqlStorageType,
				DB:          c.dbs[0],
				DBs:         c.dbs,
				Measurement: sql_expr.Doris,
				Field:       "log",
				SQL:         c.userSQL,
			}
			got, err := bksql.NewQueryFactory(ctx, q).WithRangeTime(start, end).WithFieldsMap(fieldsMap).SQL()
			require.NoError(t, err)
			assert.Equal(t, c.wantSQL, got)
		})
	}
}
