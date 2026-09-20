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

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql/sql_expr"
)

func TestQueryClusterName(t *testing.T) {
	tests := []struct {
		name string
		q    *metadata.Query
		want string
	}{
		{
			name: "查询为空时返回空集群名",
		},
		{
			name: "使用存储集群名",
			q:    &metadata.Query{StorageName: "storage_cluster"},
			want: "storage_cluster",
		},
		{
			name: "路由集群名优先于存储集群名",
			q: &metadata.Query{
				ClusterName: "route_cluster",
				StorageName: "storage_cluster",
			},
			want: "route_cluster",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, queryClusterName(tt.q))
		})
	}
}

func TestShouldDisableShardKeyTimeBucket(t *testing.T) {
	query := &metadata.Query{
		Measurement: sql_expr.Doris,
		DBs:         []string{"db_current", "db_history"},
	}
	fieldsMap := metadata.FieldsMap{
		"dtEventTimeStamp": {FieldType: sql_expr.DorisTypeBigInt},
		sql_expr.ShardKey:  {FieldType: sql_expr.DorisTypeBigInt},
	}

	type opt struct {
		shardKeyTimeBucketEnabled bool
	}

	tests := []struct {
		name                              string
		query                             *metadata.Query
		fieldsMap                         metadata.FieldsMap
		tableFieldsMap                    TableFieldsMap
		inputTimeField                    string
		opt                               opt
		expectedDisableShardKeyTimeBucket bool
		expectedTimeBucketField           string
	}{
		{
			name:      "所有 Doris 物理表都有 __shard_key__ 时使用分片键时间桶",
			query:     query,
			fieldsMap: fieldsMap,
			tableFieldsMap: TableFieldsMap{
				"`db_current`.doris": {
					"dtEventTimeStamp": {FieldType: sql_expr.DorisTypeBigInt},
					sql_expr.ShardKey:  {FieldType: sql_expr.DorisTypeBigInt},
				},
				"`db_history`.doris": {
					"dtEventTimeStamp": {FieldType: sql_expr.DorisTypeBigInt},
					sql_expr.ShardKey:  {FieldType: sql_expr.DorisTypeBigInt},
				},
			},
			inputTimeField: "dtEventTimeStamp",
			opt: opt{
				shardKeyTimeBucketEnabled: true,
			},
			expectedTimeBucketField: sql_expr.ShardKey,
		},
		{
			name:      "任意 Doris 物理表缺少 __shard_key__ 时回退时间字段时间桶",
			query:     query,
			fieldsMap: fieldsMap,
			tableFieldsMap: TableFieldsMap{
				"`db_current`.doris": {
					"dtEventTimeStamp": {FieldType: sql_expr.DorisTypeBigInt},
					sql_expr.ShardKey:  {FieldType: sql_expr.DorisTypeBigInt},
				},
				"`db_history`.doris": {
					"dtEventTimeStamp": {FieldType: sql_expr.DorisTypeBigInt},
				},
			},
			inputTimeField: "dtEventTimeStamp",
			opt: opt{
				shardKeyTimeBucketEnabled: false,
			},
			expectedTimeBucketField:           "dtEventTimeStamp",
			expectedDisableShardKeyTimeBucket: true,
		},
		{
			name:      "任意 Doris 物理表缺少字段映射时回退时间字段时间桶",
			query:     query,
			fieldsMap: fieldsMap,
			tableFieldsMap: TableFieldsMap{
				"`db_current`.doris": {
					"dtEventTimeStamp": {FieldType: sql_expr.DorisTypeBigInt},
					sql_expr.ShardKey:  {FieldType: sql_expr.DorisTypeBigInt},
				},
			},
			inputTimeField:                    "dtEventTimeStamp",
			expectedDisableShardKeyTimeBucket: true,
		},
		{
			name: "非 Doris 查询不处理分片键时间桶开关",
			query: &metadata.Query{
				Measurement: sql_expr.TSpider,
			},
			fieldsMap: metadata.FieldsMap{
				"dtEventTimeStamp": {FieldType: sql_expr.DorisTypeBigInt},
			},
			inputTimeField: "dtEventTimeStamp",
		},
		{
			name:  "仅有合并字段映射且包含 __shard_key__ 时使用分片键时间桶",
			query: &metadata.Query{Measurement: sql_expr.Doris},
			fieldsMap: metadata.FieldsMap{
				"dtEventTimeStamp": {FieldType: sql_expr.DorisTypeBigInt},
				sql_expr.ShardKey:  {FieldType: sql_expr.DorisTypeBigInt},
			},
			inputTimeField: "dtEventTimeStamp",
			opt: opt{
				shardKeyTimeBucketEnabled: true,
			},
			expectedTimeBucketField: sql_expr.ShardKey,
		},
		{
			name:  "仅有合并字段映射且缺少 __shard_key__ 时回退时间字段时间桶",
			query: &metadata.Query{Measurement: sql_expr.Doris},
			fieldsMap: metadata.FieldsMap{
				"dtEventTimeStamp": {FieldType: sql_expr.DorisTypeBigInt},
			},
			inputTimeField: "dtEventTimeStamp",
			opt: opt{
				shardKeyTimeBucketEnabled: false,
			},
			expectedTimeBucketField:           "dtEventTimeStamp",
			expectedDisableShardKeyTimeBucket: true,
		},
		{
			name:  "仅有合并字段映射但缺少时间字段时不关闭分片键时间桶",
			query: &metadata.Query{Measurement: sql_expr.Doris},
			fieldsMap: metadata.FieldsMap{
				sql_expr.ShardKey: {FieldType: sql_expr.DorisTypeBigInt},
			},
			inputTimeField: "dtEventTimeStamp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldDisableShardKeyTimeBucket(tt.query, tt.fieldsMap, tt.tableFieldsMap, tt.inputTimeField)
			assert.Equal(t, tt.expectedDisableShardKeyTimeBucket, got)

			if tt.expectedTimeBucketField != "" {
				assert.Equal(t, !got, tt.opt.shardKeyTimeBucketEnabled)

				sql := buildShardKeyTimeBucketSQL(t, tt.query, tt.fieldsMap, tt.tableFieldsMap, tt.inputTimeField, tt.opt.shardKeyTimeBucketEnabled)
				switch tt.expectedTimeBucketField {
				case sql_expr.ShardKey:
					assert.Contains(t, sql, "FLOOR(__shard_key__ / 1000)")
					assert.NotContains(t, sql, "FLOOR(dtEventTimeStamp + 0)")
				case "dtEventTimeStamp":
					assert.NotContains(t, sql, "__shard_key__")
					assert.Contains(t, sql, "FLOOR(dtEventTimeStamp + 0)")
				default:
					t.Fatalf("未配置预期时间桶字段: %q", tt.expectedTimeBucketField)
				}
			}
		})
	}
}

func buildShardKeyTimeBucketSQL(t *testing.T, query *metadata.Query, fieldsMap metadata.FieldsMap, tableFieldsMap TableFieldsMap, inputTimeField string, shardKeyTimeBucketEnabled bool) string {
	t.Helper()

	q := *query
	q.Field = inputTimeField
	q.Aggregates = metadata.Aggregates{{
		Name:   "count",
		Window: time.Minute,
	}}

	ctx := metadata.InitHashID(context.Background())
	f := NewQueryFactory(ctx, &q).
		WithFieldsMap(fieldsMap).
		WithTableFieldsMap(tableFieldsMap).
		WithRangeTime(time.UnixMilli(1718189940000), time.UnixMilli(1718193555000)).
		WithShardKeyTimeBucket(shardKeyTimeBucketEnabled)
	sql, err := f.SQL()
	assert.NoError(t, err)

	return strings.Split(sql, " FROM ")[0]
}
