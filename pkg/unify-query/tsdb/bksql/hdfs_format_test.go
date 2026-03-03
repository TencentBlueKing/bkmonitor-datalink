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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql/sql_expr"
)

func TestHDFS_SQL_RegexpIntField(t *testing.T) {
	mock.Init()

	start := time.UnixMilli(1730118589181)
	end := time.UnixMilli(1730118889181)

	fieldsMap := metadata.FieldsMap{
		"opType":           {FieldType: "INT"},
		"name":             {FieldType: "STRING"},
		"dtEventTimeStamp": {FieldType: "BIGINT"},
	}

	for name, c := range map[string]struct {
		query    *metadata.Query
		expected string
	}{
		"hdfs_regexp_int_field_with_cast": {
			query: &metadata.Query{
				DB:          "100_hdfs_test_table",
				Measurement: sql_expr.HDFS,
				Field:       "dtEventTimeStamp",
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "opType",
							Operator:      metadata.ConditionRegEqual,
							Value:         []string{"2", "5"},
						},
					},
				},
				Size: 10,
			},
			expected: "SELECT *, `dtEventTimeStamp` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `100_hdfs_test_table`.hdfs WHERE `dtEventTimeStamp` >= 1730118589181 AND `dtEventTimeStamp` < 1730118889181 AND `dtEventTime` >= '2024-10-28 20:29:49' AND `dtEventTime` <= '2024-10-28 20:34:50' AND `thedate` = '20241028' AND regexp_like(CAST(`opType` AS VARCHAR), '2|5') LIMIT 10",
		},
		"hdfs_regexp_string_field_no_cast": {
			query: &metadata.Query{
				DB:          "100_hdfs_test_table",
				Measurement: sql_expr.HDFS,
				Field:       "dtEventTimeStamp",
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "name",
							Operator:      metadata.ConditionRegEqual,
							Value:         []string{"test.*"},
						},
					},
				},
				Size: 10,
			},
			expected: "SELECT *, `dtEventTimeStamp` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `100_hdfs_test_table`.hdfs WHERE `dtEventTimeStamp` >= 1730118589181 AND `dtEventTimeStamp` < 1730118889181 AND `dtEventTime` >= '2024-10-28 20:29:49' AND `dtEventTime` <= '2024-10-28 20:34:50' AND `thedate` = '20241028' AND regexp_like(`name`, 'test.*') LIMIT 10",
		},
		"hdfs_not_regexp_int_field_with_cast": {
			query: &metadata.Query{
				DB:          "100_hdfs_test_table",
				Measurement: sql_expr.HDFS,
				Field:       "dtEventTimeStamp",
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "opType",
							Operator:      metadata.ConditionNotRegEqual,
							Value:         []string{"3"},
						},
					},
				},
				Size: 10,
			},
			expected: "SELECT *, `dtEventTimeStamp` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `100_hdfs_test_table`.hdfs WHERE `dtEventTimeStamp` >= 1730118589181 AND `dtEventTimeStamp` < 1730118889181 AND `dtEventTime` >= '2024-10-28 20:29:49' AND `dtEventTime` <= '2024-10-28 20:34:50' AND `thedate` = '20241028' AND NOT regexp_like(CAST(`opType` AS VARCHAR), '3') LIMIT 10",
		},
		"hdfs_no_regexp_no_cast": {
			query: &metadata.Query{
				DB:          "100_hdfs_test_table",
				Measurement: sql_expr.HDFS,
				Field:       "dtEventTimeStamp",
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "opType",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"2"},
						},
					},
				},
				Size: 10,
			},
			expected: "SELECT *, `dtEventTimeStamp` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `100_hdfs_test_table`.hdfs WHERE `dtEventTimeStamp` >= 1730118589181 AND `dtEventTimeStamp` < 1730118889181 AND `dtEventTime` >= '2024-10-28 20:29:49' AND `dtEventTime` <= '2024-10-28 20:34:50' AND `thedate` = '20241028' AND `opType` = '2' LIMIT 10",
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			fact := bksql.NewQueryFactory(ctx, c.query).
				WithRangeTime(start, end).
				WithFieldsMap(fieldsMap)
			sql, err := fact.SQL()
			assert.Nil(t, err)
			assert.Equal(t, c.expected, sql)
		})
	}
}

func TestHDFS_Instance_RegexpIntField(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())
	ins := createTestInstance(ctx)

	mock.BkSQL.Set(map[string]any{
		"SHOW CREATE TABLE `100_hdfs_test_table`.hdfs": `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":{},"cluster":"hdfs-test","totalRecords":3,"external_api_call_time_mills":{"bkbase_auth_api":30,"bkbase_meta_api":10,"bkbase_apigw_api":0},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"Field":"dtEventTimeStamp","Type":"bigint","Null":"NO","Key":"NO","Default":null,"Extra":""},{"Field":"dtEventTime","Type":"varchar(32)","Null":"YES","Key":"NO","Default":null,"Extra":""},{"Field":"thedate","Type":"int","Null":"NO","Key":"YES","Default":null,"Extra":""},{"Field":"opType","Type":"int","Null":"YES","Key":"NO","Default":null,"Extra":""},{"Field":"name","Type":"varchar(256)","Null":"YES","Key":"NO","Default":null,"Extra":""}],"select_fields_order":["Field","Type","Null","Key","Default","Extra"],"sql":"SHOW COLUMNS FROM hdfs_test.100_hdfs_test_table","total_record_size":1024,"timetaken":0.05,"result_schema":[{"field_type":"string","field_name":"Field","field_alias":"Field","field_index":0},{"field_type":"string","field_name":"Type","field_alias":"Type","field_index":1},{"field_type":"string","field_name":"Null","field_alias":"Null","field_index":2},{"field_type":"string","field_name":"Key","field_alias":"Key","field_index":3},{"field_type":"string","field_name":"Default","field_alias":"Default","field_index":4},{"field_type":"string","field_name":"Extra","field_alias":"Extra","field_index":5}],"bksql_call_elapsed_time":0,"device":"hdfs","result_table_ids":["100_hdfs_test_table"]},"errors":null,"trace_id":"00000000000000000000000000000000","span_id":"0000000000000000"}`,
		"SELECT *, `dtEventTimeStamp` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `100_hdfs_test_table`.hdfs WHERE `dtEventTimeStamp` >= 1730118589181 AND `dtEventTimeStamp` < 1730118889181 AND `dtEventTime` >= '2024-10-28 20:29:49' AND `dtEventTime` <= '2024-10-28 20:34:50' AND `thedate` = '20241028' AND regexp_like(CAST(`opType` AS VARCHAR), '2|5') LIMIT 10": `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":{"100_hdfs_test_table":{"start":"2024102800","end":"2024102823"}},"cluster":"hdfs-test","totalRecords":2,"external_api_call_time_mills":{"bkbase_auth_api":30,"bkbase_meta_api":0,"bkbase_apigw_api":0},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"dtEventTimeStamp":1730118600000,"dtEventTime":"2024-10-28 20:30:00","thedate":20241028,"opType":2,"name":"test1","_value_":1730118600000,"_timestamp_":1730118600000},{"dtEventTimeStamp":1730118700000,"dtEventTime":"2024-10-28 20:31:40","thedate":20241028,"opType":5,"name":"test2","_value_":1730118700000,"_timestamp_":1730118700000}],"select_fields_order":["dtEventTimeStamp","dtEventTime","thedate","opType","name","_value_","_timestamp_"],"sql":"SELECT ...","total_record_size":512,"timetaken":0.1,"result_schema":[],"bksql_call_elapsed_time":0,"device":"hdfs","result_table_ids":["100_hdfs_test_table"]},"errors":null,"trace_id":"00000000000000000000000000000000","span_id":"0000000000000000"}`,
	})

	end := time.UnixMilli(1730118889181)
	start := time.UnixMilli(1730118589181)

	query := &metadata.Query{
		DB:          "100_hdfs_test_table",
		Measurement: sql_expr.HDFS,
		Field:       "dtEventTimeStamp",
		AllConditions: metadata.AllConditions{
			{
				{
					DimensionName: "opType",
					Operator:      metadata.ConditionRegEqual,
					Value:         []string{"2", "5"},
				},
			},
		},
		Size: 10,
	}

	fact, err := ins.InitQueryFactory(ctx, query, start, end)
	assert.Nil(t, err)
	assert.NotNil(t, fact)

	fieldMap := fact.FieldMap()
	assert.NotNil(t, fieldMap, "HDFS should have fieldMap populated via QueryFieldMap")
	assert.Equal(t, "int", fieldMap["opType"].FieldType, "opType should be int type from SHOW CREATE TABLE")

	sql, err := fact.SQL()
	assert.Nil(t, err)
	assert.Contains(t, sql, "regexp_like(CAST(`opType` AS VARCHAR), '2|5')")
}
