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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql/sql_expr"
)

func TestNewSqlFactory(t *testing.T) {
	start := time.Unix(1741795260, 0)
	end := time.Unix(1741796260, 0)

	for name, c := range map[string]struct {
		query    *metadata.Query
		expected string

		start time.Time
		end   time.Time
	}{
		"doris sum-count_over_time-with-promql-1": {
			query: &metadata.Query{
				DB:          "5000140_bklog_container_log_demo_analysis",
				Measurement: "doris",
				Field:       "gseIndex",
				Aggregates: metadata.Aggregates{
					{
						Name: "count",
						Dimensions: []string{
							"level",
						},
						Window: time.Minute,
					},
				},
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "gseIndex",
							Operator:      metadata.ConditionGt,
							Value:         []string{"0"},
						},
						{
							DimensionName: "level",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"ERROR"},
						},
					},
				},
				From: 0,
				Size: 0,
				Orders: metadata.Orders{
					{
						Name: "level",
						Ast:  true,
					},
				},
			},
			expected: "SELECT `level`, COUNT(`gseIndex`) AS `_value_`, ((CAST((FLOOR(__shard_key__ / 1000) + 0) / 1 AS INT) * 1 - 0) * 60 * 1000) AS `_timestamp_` FROM `5000140_bklog_container_log_demo_analysis`.doris WHERE `dtEventTimeStamp` >= 1741795260000 AND `dtEventTimeStamp` <= 1741796260000 AND `dtEventTime` >= '2025-03-13 00:01:00' AND `dtEventTime` <= '2025-03-13 00:17:41' AND `thedate` = '20250313' AND `gseIndex` > 0 AND `level` = 'ERROR' GROUP BY `level`, _timestamp_ ORDER BY `level` ASC",
		},
		"doris sum-count_over_time-with-promql-seconds": {
			query: &metadata.Query{
				DB:          "5000140_bklog_container_log_demo_analysis",
				Measurement: "doris",
				Field:       "gseIndex",
				Aggregates: metadata.Aggregates{
					{
						Name: "count",
						Dimensions: []string{
							"level",
						},
						Window: time.Minute + time.Second*15,
					},
				},
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "gseIndex",
							Operator:      metadata.ConditionGt,
							Value:         []string{"0"},
						},
						{
							DimensionName: "level",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"ERROR"},
						},
					},
				},
				From: 0,
				Size: 0,
				Orders: metadata.Orders{
					{
						Name: "level",
						Ast:  true,
					},
				},
			},
			expected: "SELECT `level`, COUNT(`gseIndex`) AS `_value_`, (CAST((FLOOR(dtEventTimeStamp + 0) / 75000) AS INT) * 75000 - 0) AS `_timestamp_` FROM `5000140_bklog_container_log_demo_analysis`.doris WHERE `dtEventTimeStamp` >= 1741795260000 AND `dtEventTimeStamp` <= 1741796260000 AND `dtEventTime` >= '2025-03-13 00:01:00' AND `dtEventTime` <= '2025-03-13 00:17:41' AND `thedate` = '20250313' AND `gseIndex` > 0 AND `level` = 'ERROR' GROUP BY `level`, _timestamp_ ORDER BY `level` ASC",
		},
		"doris sum-with-promql-1": {
			query: &metadata.Query{
				DB:          "100133_ieod_logsearch4_errorlog_p",
				Measurement: "doris",
				Field:       "gseIndex",
				Aggregates: metadata.Aggregates{
					{
						Name: "sum",
						Dimensions: []string{
							"ip",
						},
					},
				},
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "gseIndex",
							Operator:      metadata.ConditionGt,
							Value:         []string{"0"},
						},
					},
				},
				From:   0,
				Size:   10,
				Orders: nil,
			},
			expected: "SELECT `ip`, SUM(`gseIndex`) AS `_value_` FROM `100133_ieod_logsearch4_errorlog_p`.doris WHERE `dtEventTimeStamp` >= 1741795260000 AND `dtEventTimeStamp` <= 1741796260000 AND `dtEventTime` >= '2025-03-13 00:01:00' AND `dtEventTime` <= '2025-03-13 00:17:41' AND `thedate` = '20250313' AND `gseIndex` > 0 GROUP BY `ip` LIMIT 10",
		},
		"doris count-with-count-promql-1": {
			query: &metadata.Query{
				DB:          "100133_ieod_logsearch4_errorlog_p",
				Measurement: "doris",
				Field:       "gseIndex",
				Aggregates: metadata.Aggregates{
					{
						Name: "count",
						Dimensions: []string{
							"ip",
						},
						Window: time.Minute,
					},
				},
			},
			expected: "SELECT `ip`, COUNT(`gseIndex`) AS `_value_`, ((CAST((FLOOR(__shard_key__ / 1000) + 0) / 1 AS INT) * 1 - 0) * 60 * 1000) AS `_timestamp_` FROM `100133_ieod_logsearch4_errorlog_p`.doris WHERE `dtEventTimeStamp` >= 1741795260000 AND `dtEventTimeStamp` <= 1741796260000 AND `dtEventTime` >= '2025-03-13 00:01:00' AND `dtEventTime` <= '2025-03-13 00:17:41' AND `thedate` = '20250313' GROUP BY `ip`, _timestamp_",
		},
		"doris count-with-count-promql-2": {
			// 2024-12-07 21:36:40	UTC
			// 2024-12-08 05:36:40  Asia/Shanghai
			start: time.Unix(1733607400, 0),
			// 2024-12-11 17:49:35 	UTC
			// 2024-12-12 01:49:35  Asia/Shanghai
			end: time.Unix(1733939375, 0),
			query: &metadata.Query{
				DB:          "100133_ieod_logsearch4_errorlog_p",
				Measurement: "doris",
				Field:       "gseIndex",
				Aggregates: metadata.Aggregates{
					{
						Name: "count",
						Dimensions: []string{
							"ip",
						},
						Window: time.Minute,
					},
				},
				AllConditions: metadata.AllConditions{
					[]metadata.ConditionField{
						{
							DimensionName: "gseIndex",
							Value:         []string{"0"},
							Operator:      metadata.ConditionGt,
						},
					},
				},
			},
			expected: "SELECT `ip`, COUNT(`gseIndex`) AS `_value_`, ((CAST((FLOOR(__shard_key__ / 1000) + 0) / 1 AS INT) * 1 - 0) * 60 * 1000) AS `_timestamp_` FROM `100133_ieod_logsearch4_errorlog_p`.doris WHERE `dtEventTimeStamp` >= 1733607400000 AND `dtEventTimeStamp` <= 1733939375000 AND `dtEventTime` >= '2024-12-08 05:36:40' AND `dtEventTime` <= '2024-12-12 01:49:36' AND `thedate` >= '20241208' AND `thedate` <= '20241212' AND `gseIndex` > 0 GROUP BY `ip`, _timestamp_",
		},
		"doris count by day with UTC": {
			// 2025-03-14 15:05:45  Asia/Shanghai
			start: time.UnixMilli(1741935945000),
			// 2025-03-20 15:35:46 Asia/Shanghai
			end: time.UnixMilli(1742456145000),
			query: &metadata.Query{
				DB:          "5000140_bklog_container_log_demo_analysis",
				Measurement: "doris",
				Field:       "__ext.container_id",
				Aggregates: metadata.Aggregates{
					{
						Name:   "count",
						Window: time.Hour * 24,
					},
				},
			},
			expected: "SELECT COUNT(CAST(__ext['container_id'] AS STRING)) AS `_value_`, ((CAST((FLOOR(__shard_key__ / 1000) + 0) / 1440 AS INT) * 1440 - 0) * 60 * 1000) AS `_timestamp_` FROM `5000140_bklog_container_log_demo_analysis`.doris WHERE `dtEventTimeStamp` >= 1741935945000 AND `dtEventTimeStamp` <= 1742456145000 AND `dtEventTime` >= '2025-03-14 15:05:45' AND `dtEventTime` <= '2025-03-20 15:35:46' AND `thedate` >= '20250314' AND `thedate` <= '20250320' GROUP BY _timestamp_",
		},
		"doris count by day with Asia/Shanghai": {
			// 2025-03-14 15:05:45  Asia/Shanghai
			start: time.UnixMilli(1741935945000),
			// 2025-03-20 15:35:46 Asia/Shanghai
			end: time.UnixMilli(1742456145000),
			query: &metadata.Query{
				DB:          "5000140_bklog_container_log_demo_analysis",
				Measurement: "doris",
				Field:       "__ext.container_id",
				Aggregates: metadata.Aggregates{
					{
						Name:           "count",
						Window:         time.Hour * 24,
						TimeZone:       "Asia/Shanghai",
						TimeZoneOffset: (time.Hour * -8).Milliseconds(),
					},
				},
			},
			expected: "SELECT COUNT(CAST(__ext['container_id'] AS STRING)) AS `_value_`, ((CAST((FLOOR(__shard_key__ / 1000) + 480) / 1440 AS INT) * 1440 - 480) * 60 * 1000) AS `_timestamp_` FROM `5000140_bklog_container_log_demo_analysis`.doris WHERE `dtEventTimeStamp` >= 1741935945000 AND `dtEventTimeStamp` <= 1742456145000 AND `dtEventTime` >= '2025-03-14 15:05:45' AND `dtEventTime` <= '2025-03-20 15:35:46' AND `thedate` >= '20250314' AND `thedate` <= '20250320' GROUP BY _timestamp_",
		},
		"doris count by dimension with object": {
			// 2025-03-14 15:05:45  Asia/Shanghai
			start: time.UnixMilli(1741935945000),
			// 2025-03-20 15:35:46 Asia/Shanghai
			end: time.UnixMilli(1742456145000),
			query: &metadata.Query{
				DB:          "5000140_bklog_container_log_demo_analysis",
				Measurement: "doris",
				Field:       "__ext.container_id",
				Aggregates: metadata.Aggregates{
					{
						Name:       "count",
						Window:     time.Hour * 24,
						Dimensions: []string{"__ext.container_id"},
					},
				},
			},
			expected: "SELECT CAST(__ext['container_id'] AS STRING) AS `__ext__bk_46__container_id`, COUNT(CAST(__ext['container_id'] AS STRING)) AS `_value_`, ((CAST((FLOOR(__shard_key__ / 1000) + 0) / 1440 AS INT) * 1440 - 0) * 60 * 1000) AS `_timestamp_` FROM `5000140_bklog_container_log_demo_analysis`.doris WHERE `dtEventTimeStamp` >= 1741935945000 AND `dtEventTimeStamp` <= 1742456145000 AND `dtEventTime` >= '2025-03-14 15:05:45' AND `dtEventTime` <= '2025-03-20 15:35:46' AND `thedate` >= '20250314' AND `thedate` <= '20250320' GROUP BY __ext__bk_46__container_id, _timestamp_",
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			if c.start.Unix() <= 0 {
				c.start = start
			}
			if c.end.Unix() <= 0 {
				c.end = end
			}

			log.Infof(ctx, "start: %s, end: %s", c.start, c.end)
			fact := bksql.NewQueryFactory(ctx, c.query).
				WithRangeTime(c.start, c.end).
				WithFieldsMap(map[string]metadata.FieldOption{
					"level": {
						FieldType: sql_expr.DorisTypeString,
					},
					"ip": {
						FieldType: sql_expr.DorisTypeString,
					},
					"__ext.container_id": {
						FieldType: sql_expr.DorisTypeString,
					},
					"gseIndex": {
						FieldType: sql_expr.DorisTypeInt,
					},
				})
			sql, err := fact.SQL()
			assert.Nil(t, err)
			assert.Equal(t, c.expected, sql)
		})
	}
}

// TestNewQueryFactory_BkSql_TSpider_UserSQL 单段 table_id（Measurement 空）走用户 SQL 时，应选用 TSpider 表达式并完成 ParserSQL，表名仍为 `db` 无 .suffix。
// wantSQL 为完整 golden（start=1741795260,end=1741796260 时 BuildWhere 追加的区间固定）；解析器改写子句时同步更新本期望值。
func TestNewQueryFactory_BkSql_TSpider_UserSQL(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())
	start := time.Unix(1741795260, 0)
	end := time.Unix(1741796260, 0)

	const whereTime = "(`dtEventTimeStamp` >= 1741795260000 AND `dtEventTimeStamp` <= 1741796260000 AND `dtEventTime` >= '2025-03-13 00:01:00' AND `dtEventTime` <= '2025-03-13 00:17:41' AND `thedate` = '20250313')"

	tests := []struct {
		name    string
		db      string
		dbs     []string
		field   string
		userSQL string
		size    int
		wantSQL string
	}{
		{
			name:    "basic_dtEventTimeStamp_limit10",
			db:      "132_lol_new_login_queue_login_1min",
			field:   "login_rate",
			userSQL: "SELECT dtEventTimeStamp FROM tbl WHERE dtEventTimeStamp > 0 LIMIT 10",
			wantSQL: "SELECT NULL AS dtEventTimeStamp FROM `132_lol_new_login_queue_login_1min` WHERE NULL > 0 AND " + whereTime + " LIMIT 10",
		},
		{
			name:    "count_star_alias",
			db:      "132_lol_new_login_queue_login_1min",
			field:   "login_rate",
			userSQL: "SELECT COUNT(*) AS cnt FROM tbl WHERE id > 0",
			wantSQL: "SELECT COUNT(*) AS cnt FROM `132_lol_new_login_queue_login_1min` WHERE NULL > 0 AND " + whereTime + " LIMIT 100",
		},
		{
			name:    "select_with_explicit_limit",
			db:      "132_lol_new_login_queue_login_1min",
			field:   "login_rate",
			userSQL: "SELECT serverIp FROM tbl WHERE 1 = 1 LIMIT 5",
			wantSQL: "SELECT NULL AS serverIp FROM `132_lol_new_login_queue_login_1min` WHERE 1 = 1 AND " + whereTime + " LIMIT 5",
		},
		{
			name:    "different_table_id_db",
			db:      "5000140_bklog_demo",
			field:   "cnt",
			userSQL: "SELECT dtEventTimeStamp FROM tbl WHERE 1 = 1 LIMIT 3",
			wantSQL: "SELECT NULL AS dtEventTimeStamp FROM `5000140_bklog_demo` WHERE 1 = 1 AND " + whereTime + " LIMIT 3",
		},
		{
			name:    "multi_db_union_combined",
			db:      "unused_when_dbs_set",
			dbs:     []string{"db_a", "db_b"},
			field:   "login_rate",
			userSQL: "SELECT 1 FROM tbl",
			wantSQL: "SELECT 1 FROM (SELECT * FROM `db_b` WHERE " + whereTime + " UNION ALL SELECT * FROM `db_a` WHERE " + whereTime + ") AS combined_data LIMIT 100",
		},
		{
			name:    "parenthesized_or_and_is_not_null_limit_offset",
			db:      "132_lol_new_login_queue_login_1min",
			field:   "login_rate",
			userSQL: "SELECT a, b FROM tbl WHERE (dim1 = 'x' OR dim2 > 1) AND dim3 IS NOT NULL ORDER BY a DESC, b ASC LIMIT 20 OFFSET 2",
			wantSQL: "SELECT NULL AS a, NULL AS b FROM `132_lol_new_login_queue_login_1min` WHERE ( NULL = 'x' OR NULL > 1 ) AND NULL IS NOT NULL  AND " + whereTime + " LIMIT 20 OFFSET 2",
		},
		{
			name:    "match_phrase_count_order_by",
			db:      "132_lol_new_login_queue_login_1min",
			field:   "login_rate",
			userSQL: "SELECT serverIp, COUNT(*) AS c FROM tbl WHERE log MATCH_PHRASE 'Error' GROUP BY serverIp ORDER BY c DESC LIMIT 30",
			wantSQL: "SELECT NULL AS serverIp, COUNT(*) AS c FROM `132_lol_new_login_queue_login_1min` WHERE NULL MATCH_PHRASE 'Error' AND " + whereTime + " ORDER BY `c` DESC LIMIT 30",
		},
		{
			name:    "case_when_in_select",
			db:      "132_lol_new_login_queue_login_1min",
			field:   "login_rate",
			userSQL: "SELECT CASE WHEN level = 'error' THEN 1 ELSE 0 END AS err_flag, dtEventTimeStamp FROM tbl WHERE 1=1",
			wantSQL: "SELECT CASE WHEN NULL = 'error' THEN 1 ELSE 0 END AS err_flag, NULL AS dtEventTimeStamp FROM `132_lol_new_login_queue_login_1min` WHERE 1 = 1 AND " + whereTime + " LIMIT 100",
		},
		{
			name:    "inner_join_qualifiers",
			db:      "132_lol_new_login_queue_login_1min",
			field:   "login_rate",
			userSQL: "SELECT t1.a FROM tbl t1 INNER JOIN tbl t2 ON t1.id = t2.parent_id WHERE t1.x > 0",
			wantSQL: "SELECT NULL AS t1__bk_46__a FROM `132_lol_new_login_queue_login_1min` WHERE NULL > 0 AND " + whereTime + " LIMIT 100",
		},
		{
			name:    "in_list_count_order_by",
			db:      "132_lol_new_login_queue_login_1min",
			field:   "login_rate",
			userSQL: "SELECT city, COUNT(*) AS c FROM tbl WHERE status IN ('ok', 'fail') GROUP BY city HAVING c > 10 ORDER BY c DESC LIMIT 50",
			wantSQL: "SELECT NULL AS city, COUNT(*) AS c FROM `132_lol_new_login_queue_login_1min` WHERE NULL IN ('ok', 'fail') AND " + whereTime + " ORDER BY `c` DESC LIMIT 50",
		},
		{
			name:    "subquery_flattened_outer_where",
			db:      "132_lol_new_login_queue_login_1min",
			field:   "login_rate",
			userSQL: "SELECT * FROM (SELECT id, cnt FROM tbl WHERE cnt > 0) AS sub WHERE id < 100",
			wantSQL: "SELECT * FROM `132_lol_new_login_queue_login_1min` WHERE NULL < 100 AND " + whereTime + " LIMIT 100",
		},
		{
			name:    "json_extract_split_part_match_all",
			db:      "132_lol_new_login_queue_login_1min",
			field:   "login_rate",
			userSQL: "SELECT JSON_EXTRACT_STRING(__ext, '$.pod') AS pod, split_part(log, '|', 2) AS seg FROM tbl WHERE log MATCH_ALL 'timeout' LIMIT 99",
			wantSQL: "SELECT JSON_EXTRACT_STRING(NULL, '$.pod') AS pod, split_part(NULL, '|', 2) AS seg FROM `132_lol_new_login_queue_login_1min` WHERE NULL MATCH_ALL 'timeout' AND " + whereTime + " LIMIT 99",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := &metadata.Query{
				StorageType: metadata.BkSqlStorageType,
				DB:          tt.db,
				DBs:         tt.dbs,
				Measurement: "",
				Field:       tt.field,
				SQL:         tt.userSQL,
				Size:        tt.size,
			}
			got, err := bksql.NewQueryFactory(ctx, q).WithRangeTime(start, end).SQL()
			require.NoError(t, err)
			require.NotEmpty(t, got)
			assert.Equal(t, tt.wantSQL, got)
			assert.NotContains(t, got, ".tspider")
		})
	}
}

func TestWindowWithTimezone(t *testing.T) {
	mock.Init()

	testCases := []struct {
		name     string
		start    time.Time
		timezone string
		window   time.Duration

		expected time.Time
	}{
		{
			name:     "test window 1m - 1",
			start:    time.UnixMilli(1742267704000),
			timezone: "Asia/Shanghai",
			window:   time.Minute,

			expected: time.UnixMilli(1742267700000),
		},
		{
			name:   "test window 1d +8 - 1",
			start:  time.UnixMilli(1742267704000),
			window: time.Hour * 24,

			expected: time.UnixMilli(1742256000000),
		},
		{
			name:     "test window 1d +6 - 1",
			start:    time.UnixMilli(1742267704000),
			timezone: "Asia/Urumqi",
			window:   time.Hour * 24,

			expected: time.UnixMilli(1742234400000),
		},
		{
			name:     "test window 1d -6 - 1",
			start:    time.UnixMilli(1742267704000),
			timezone: "America/Guatemala",
			window:   time.Hour * 24,

			expected: time.UnixMilli(1742191200000),
		},
		{
			name:     "test window 1d +8 - 1",
			start:    time.UnixMilli(1742267704000),
			timezone: "Asia/Shanghai",
			window:   time.Hour * 24,

			expected: time.UnixMilli(1742227200000),
		},
		{
			name:     "test window 1d +8 - 2",
			start:    time.UnixMilli(1741885200000),
			timezone: "Asia/Shanghai",
			window:   time.Hour * 24,

			expected: time.UnixMilli(1741881600000),
		},
		{
			name:     "test window 26h +8 - 1",
			start:    time.UnixMilli(1742267704000),
			timezone: "Asia/Shanghai",
			window:   time.Hour*24 + time.Hour*2,

			expected: time.UnixMilli(1742176800000),
		},
		{
			name:     "test window 3d +8 - 1",
			start:    time.UnixMilli(1742267704000),
			timezone: "Asia/Shanghai",
			window:   time.Hour * 24 * 3,

			expected: time.UnixMilli(1742054400000),
		},
		{
			name:     "test window 1m +8 - 2",
			start:    time.UnixMilli(1742266099000),
			timezone: "Asia/Shanghai",
			window:   time.Minute,

			expected: time.UnixMilli(1742266080000),
		},
		{
			name:     "test window 6h +8 - 2",
			start:    time.UnixMilli(1742266099000), // 2025-03-18 10:48:19 +0800
			timezone: "Asia/Shanghai",
			window:   time.Hour * 6,

			expected: time.UnixMilli(1742256000000), // 2025-03-18 08:00:00 +0800 CST
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cTime := tc.start.UnixMilli()
			window := tc.window.Milliseconds()

			var offset int64
			var loc *time.Location
			var err error

			if tc.timezone != "" {
				loc, err = time.LoadLocation(tc.timezone)
				if err != nil {
					t.Fatalf("Failed to load timezone %s: %v", tc.timezone, err)
				}
			} else {
				loc = time.UTC
			}

			if window%(time.Hour*24).Milliseconds() == 0 {
				_, z := time.Now().In(loc).Zone()
				offset = int64(z) * 1000
			}

			nTime1 := (cTime + offset) - (cTime+offset)%window - offset
			nTime2 := (cTime+offset)/window*window - offset

			milliUnixToString := func(milliUnix int64) string {
				return time.UnixMilli(milliUnix).In(loc).String()
			}

			log.Debugf(context.TODO(), "window: %d, nTime: %s, nTime1: %s nTime2: %s", window/1000, milliUnixToString(cTime), milliUnixToString(nTime1), milliUnixToString(nTime2))
			log.Debugf(context.TODO(), "nTime1: (%d + %d) - (%d + %d) %% %d - %d", cTime, offset, cTime, offset, window, offset)
			log.Debugf(context.TODO(), "nTime2: (%d + %d) / %d * %d - %d", cTime, offset, window, window, offset)

			assert.Equal(t, nTime1, nTime2)
			assert.Equal(t, tc.expected.UnixMilli(), nTime1)
		})
	}
}

// TestTimeZone 时区聚合测试
func TestTimeZone(t *testing.T) {
	timezones := []string{
		"Asia/Shanghai",
		"UTC",
	}

	var s strings.Builder

	for _, tz := range timezones {
		s.WriteString(tz + "\n")
		loc, _ := time.LoadLocation(tz)
		_, z := time.Now().In(loc).Zone()
		offset := int64(z) * 1000
		window := (time.Hour * 24).Milliseconds()

		st := time.Date(2025, 3, 18, 0, 0, 0, 0, time.UTC)
		for i := 0; i < 50; i++ {
			// milli := (st.UnixMilli() + offset) - (st.UnixMilli()+offset)%window - offset
			milli := (st.UnixMilli())/window*window - offset
			ct := st.In(loc).String()
			ot := time.UnixMilli(milli).In(loc).String()

			s.WriteString(fmt.Sprintf("%s => %s => %s\n", st, ct, ot))

			st = st.Add(time.Hour)
		}
	}

	assert.Equal(t, s.String(), `Asia/Shanghai
2025-03-18 00:00:00 +0000 UTC => 2025-03-18 08:00:00 +0800 CST => 2025-03-18 00:00:00 +0800 CST
2025-03-18 01:00:00 +0000 UTC => 2025-03-18 09:00:00 +0800 CST => 2025-03-18 00:00:00 +0800 CST
2025-03-18 02:00:00 +0000 UTC => 2025-03-18 10:00:00 +0800 CST => 2025-03-18 00:00:00 +0800 CST
2025-03-18 03:00:00 +0000 UTC => 2025-03-18 11:00:00 +0800 CST => 2025-03-18 00:00:00 +0800 CST
2025-03-18 04:00:00 +0000 UTC => 2025-03-18 12:00:00 +0800 CST => 2025-03-18 00:00:00 +0800 CST
2025-03-18 05:00:00 +0000 UTC => 2025-03-18 13:00:00 +0800 CST => 2025-03-18 00:00:00 +0800 CST
2025-03-18 06:00:00 +0000 UTC => 2025-03-18 14:00:00 +0800 CST => 2025-03-18 00:00:00 +0800 CST
2025-03-18 07:00:00 +0000 UTC => 2025-03-18 15:00:00 +0800 CST => 2025-03-18 00:00:00 +0800 CST
2025-03-18 08:00:00 +0000 UTC => 2025-03-18 16:00:00 +0800 CST => 2025-03-18 00:00:00 +0800 CST
2025-03-18 09:00:00 +0000 UTC => 2025-03-18 17:00:00 +0800 CST => 2025-03-18 00:00:00 +0800 CST
2025-03-18 10:00:00 +0000 UTC => 2025-03-18 18:00:00 +0800 CST => 2025-03-18 00:00:00 +0800 CST
2025-03-18 11:00:00 +0000 UTC => 2025-03-18 19:00:00 +0800 CST => 2025-03-18 00:00:00 +0800 CST
2025-03-18 12:00:00 +0000 UTC => 2025-03-18 20:00:00 +0800 CST => 2025-03-18 00:00:00 +0800 CST
2025-03-18 13:00:00 +0000 UTC => 2025-03-18 21:00:00 +0800 CST => 2025-03-18 00:00:00 +0800 CST
2025-03-18 14:00:00 +0000 UTC => 2025-03-18 22:00:00 +0800 CST => 2025-03-18 00:00:00 +0800 CST
2025-03-18 15:00:00 +0000 UTC => 2025-03-18 23:00:00 +0800 CST => 2025-03-18 00:00:00 +0800 CST
2025-03-18 16:00:00 +0000 UTC => 2025-03-19 00:00:00 +0800 CST => 2025-03-18 00:00:00 +0800 CST
2025-03-18 17:00:00 +0000 UTC => 2025-03-19 01:00:00 +0800 CST => 2025-03-18 00:00:00 +0800 CST
2025-03-18 18:00:00 +0000 UTC => 2025-03-19 02:00:00 +0800 CST => 2025-03-18 00:00:00 +0800 CST
2025-03-18 19:00:00 +0000 UTC => 2025-03-19 03:00:00 +0800 CST => 2025-03-18 00:00:00 +0800 CST
2025-03-18 20:00:00 +0000 UTC => 2025-03-19 04:00:00 +0800 CST => 2025-03-18 00:00:00 +0800 CST
2025-03-18 21:00:00 +0000 UTC => 2025-03-19 05:00:00 +0800 CST => 2025-03-18 00:00:00 +0800 CST
2025-03-18 22:00:00 +0000 UTC => 2025-03-19 06:00:00 +0800 CST => 2025-03-18 00:00:00 +0800 CST
2025-03-18 23:00:00 +0000 UTC => 2025-03-19 07:00:00 +0800 CST => 2025-03-18 00:00:00 +0800 CST
2025-03-19 00:00:00 +0000 UTC => 2025-03-19 08:00:00 +0800 CST => 2025-03-19 00:00:00 +0800 CST
2025-03-19 01:00:00 +0000 UTC => 2025-03-19 09:00:00 +0800 CST => 2025-03-19 00:00:00 +0800 CST
2025-03-19 02:00:00 +0000 UTC => 2025-03-19 10:00:00 +0800 CST => 2025-03-19 00:00:00 +0800 CST
2025-03-19 03:00:00 +0000 UTC => 2025-03-19 11:00:00 +0800 CST => 2025-03-19 00:00:00 +0800 CST
2025-03-19 04:00:00 +0000 UTC => 2025-03-19 12:00:00 +0800 CST => 2025-03-19 00:00:00 +0800 CST
2025-03-19 05:00:00 +0000 UTC => 2025-03-19 13:00:00 +0800 CST => 2025-03-19 00:00:00 +0800 CST
2025-03-19 06:00:00 +0000 UTC => 2025-03-19 14:00:00 +0800 CST => 2025-03-19 00:00:00 +0800 CST
2025-03-19 07:00:00 +0000 UTC => 2025-03-19 15:00:00 +0800 CST => 2025-03-19 00:00:00 +0800 CST
2025-03-19 08:00:00 +0000 UTC => 2025-03-19 16:00:00 +0800 CST => 2025-03-19 00:00:00 +0800 CST
2025-03-19 09:00:00 +0000 UTC => 2025-03-19 17:00:00 +0800 CST => 2025-03-19 00:00:00 +0800 CST
2025-03-19 10:00:00 +0000 UTC => 2025-03-19 18:00:00 +0800 CST => 2025-03-19 00:00:00 +0800 CST
2025-03-19 11:00:00 +0000 UTC => 2025-03-19 19:00:00 +0800 CST => 2025-03-19 00:00:00 +0800 CST
2025-03-19 12:00:00 +0000 UTC => 2025-03-19 20:00:00 +0800 CST => 2025-03-19 00:00:00 +0800 CST
2025-03-19 13:00:00 +0000 UTC => 2025-03-19 21:00:00 +0800 CST => 2025-03-19 00:00:00 +0800 CST
2025-03-19 14:00:00 +0000 UTC => 2025-03-19 22:00:00 +0800 CST => 2025-03-19 00:00:00 +0800 CST
2025-03-19 15:00:00 +0000 UTC => 2025-03-19 23:00:00 +0800 CST => 2025-03-19 00:00:00 +0800 CST
2025-03-19 16:00:00 +0000 UTC => 2025-03-20 00:00:00 +0800 CST => 2025-03-19 00:00:00 +0800 CST
2025-03-19 17:00:00 +0000 UTC => 2025-03-20 01:00:00 +0800 CST => 2025-03-19 00:00:00 +0800 CST
2025-03-19 18:00:00 +0000 UTC => 2025-03-20 02:00:00 +0800 CST => 2025-03-19 00:00:00 +0800 CST
2025-03-19 19:00:00 +0000 UTC => 2025-03-20 03:00:00 +0800 CST => 2025-03-19 00:00:00 +0800 CST
2025-03-19 20:00:00 +0000 UTC => 2025-03-20 04:00:00 +0800 CST => 2025-03-19 00:00:00 +0800 CST
2025-03-19 21:00:00 +0000 UTC => 2025-03-20 05:00:00 +0800 CST => 2025-03-19 00:00:00 +0800 CST
2025-03-19 22:00:00 +0000 UTC => 2025-03-20 06:00:00 +0800 CST => 2025-03-19 00:00:00 +0800 CST
2025-03-19 23:00:00 +0000 UTC => 2025-03-20 07:00:00 +0800 CST => 2025-03-19 00:00:00 +0800 CST
2025-03-20 00:00:00 +0000 UTC => 2025-03-20 08:00:00 +0800 CST => 2025-03-20 00:00:00 +0800 CST
2025-03-20 01:00:00 +0000 UTC => 2025-03-20 09:00:00 +0800 CST => 2025-03-20 00:00:00 +0800 CST
UTC
2025-03-18 00:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC
2025-03-18 01:00:00 +0000 UTC => 2025-03-18 01:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC
2025-03-18 02:00:00 +0000 UTC => 2025-03-18 02:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC
2025-03-18 03:00:00 +0000 UTC => 2025-03-18 03:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC
2025-03-18 04:00:00 +0000 UTC => 2025-03-18 04:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC
2025-03-18 05:00:00 +0000 UTC => 2025-03-18 05:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC
2025-03-18 06:00:00 +0000 UTC => 2025-03-18 06:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC
2025-03-18 07:00:00 +0000 UTC => 2025-03-18 07:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC
2025-03-18 08:00:00 +0000 UTC => 2025-03-18 08:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC
2025-03-18 09:00:00 +0000 UTC => 2025-03-18 09:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC
2025-03-18 10:00:00 +0000 UTC => 2025-03-18 10:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC
2025-03-18 11:00:00 +0000 UTC => 2025-03-18 11:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC
2025-03-18 12:00:00 +0000 UTC => 2025-03-18 12:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC
2025-03-18 13:00:00 +0000 UTC => 2025-03-18 13:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC
2025-03-18 14:00:00 +0000 UTC => 2025-03-18 14:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC
2025-03-18 15:00:00 +0000 UTC => 2025-03-18 15:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC
2025-03-18 16:00:00 +0000 UTC => 2025-03-18 16:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC
2025-03-18 17:00:00 +0000 UTC => 2025-03-18 17:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC
2025-03-18 18:00:00 +0000 UTC => 2025-03-18 18:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC
2025-03-18 19:00:00 +0000 UTC => 2025-03-18 19:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC
2025-03-18 20:00:00 +0000 UTC => 2025-03-18 20:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC
2025-03-18 21:00:00 +0000 UTC => 2025-03-18 21:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC
2025-03-18 22:00:00 +0000 UTC => 2025-03-18 22:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC
2025-03-18 23:00:00 +0000 UTC => 2025-03-18 23:00:00 +0000 UTC => 2025-03-18 00:00:00 +0000 UTC
2025-03-19 00:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC
2025-03-19 01:00:00 +0000 UTC => 2025-03-19 01:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC
2025-03-19 02:00:00 +0000 UTC => 2025-03-19 02:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC
2025-03-19 03:00:00 +0000 UTC => 2025-03-19 03:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC
2025-03-19 04:00:00 +0000 UTC => 2025-03-19 04:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC
2025-03-19 05:00:00 +0000 UTC => 2025-03-19 05:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC
2025-03-19 06:00:00 +0000 UTC => 2025-03-19 06:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC
2025-03-19 07:00:00 +0000 UTC => 2025-03-19 07:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC
2025-03-19 08:00:00 +0000 UTC => 2025-03-19 08:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC
2025-03-19 09:00:00 +0000 UTC => 2025-03-19 09:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC
2025-03-19 10:00:00 +0000 UTC => 2025-03-19 10:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC
2025-03-19 11:00:00 +0000 UTC => 2025-03-19 11:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC
2025-03-19 12:00:00 +0000 UTC => 2025-03-19 12:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC
2025-03-19 13:00:00 +0000 UTC => 2025-03-19 13:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC
2025-03-19 14:00:00 +0000 UTC => 2025-03-19 14:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC
2025-03-19 15:00:00 +0000 UTC => 2025-03-19 15:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC
2025-03-19 16:00:00 +0000 UTC => 2025-03-19 16:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC
2025-03-19 17:00:00 +0000 UTC => 2025-03-19 17:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC
2025-03-19 18:00:00 +0000 UTC => 2025-03-19 18:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC
2025-03-19 19:00:00 +0000 UTC => 2025-03-19 19:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC
2025-03-19 20:00:00 +0000 UTC => 2025-03-19 20:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC
2025-03-19 21:00:00 +0000 UTC => 2025-03-19 21:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC
2025-03-19 22:00:00 +0000 UTC => 2025-03-19 22:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC
2025-03-19 23:00:00 +0000 UTC => 2025-03-19 23:00:00 +0000 UTC => 2025-03-19 00:00:00 +0000 UTC
2025-03-20 00:00:00 +0000 UTC => 2025-03-20 00:00:00 +0000 UTC => 2025-03-20 00:00:00 +0000 UTC
2025-03-20 01:00:00 +0000 UTC => 2025-03-20 01:00:00 +0000 UTC => 2025-03-20 00:00:00 +0000 UTC
`)
}
