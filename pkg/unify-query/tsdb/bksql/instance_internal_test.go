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
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql/sql_expr"
)

func TestQueryClusterName(t *testing.T) {
	assert.Empty(t, queryClusterName(nil))
	assert.Equal(t, "storage_cluster", queryClusterName(&metadata.Query{StorageName: "storage_cluster"}))
	assert.Equal(t, "route_cluster", queryClusterName(&metadata.Query{
		ClusterName: "route_cluster",
		StorageName: "storage_cluster",
	}))
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

	assert.False(t, shouldDisableShardKeyTimeBucket(query, fieldsMap, TableFieldsMap{
		"`db_current`.doris": {
			"dtEventTimeStamp": {FieldType: sql_expr.DorisTypeBigInt},
			sql_expr.ShardKey:  {FieldType: sql_expr.DorisTypeBigInt},
		},
		"`db_history`.doris": {
			"dtEventTimeStamp": {FieldType: sql_expr.DorisTypeBigInt},
			sql_expr.ShardKey:  {FieldType: sql_expr.DorisTypeBigInt},
		},
	}, "dtEventTimeStamp"))

	assert.True(t, shouldDisableShardKeyTimeBucket(query, fieldsMap, TableFieldsMap{
		"`db_current`.doris": {
			"dtEventTimeStamp": {FieldType: sql_expr.DorisTypeBigInt},
			sql_expr.ShardKey:  {FieldType: sql_expr.DorisTypeBigInt},
		},
		"`db_history`.doris": {
			"dtEventTimeStamp": {FieldType: sql_expr.DorisTypeBigInt},
		},
	}, "dtEventTimeStamp"))

	assert.True(t, shouldDisableShardKeyTimeBucket(query, fieldsMap, TableFieldsMap{
		"`db_current`.doris": {
			"dtEventTimeStamp": {FieldType: sql_expr.DorisTypeBigInt},
			sql_expr.ShardKey:  {FieldType: sql_expr.DorisTypeBigInt},
		},
	}, "dtEventTimeStamp"))

	assert.False(t, shouldDisableShardKeyTimeBucket(&metadata.Query{Measurement: sql_expr.TSpider}, metadata.FieldsMap{
		"dtEventTimeStamp": {FieldType: sql_expr.DorisTypeBigInt},
	}, nil, "dtEventTimeStamp"))
}

func TestShouldDisableShardKeyTimeBucketFallbackFieldsMap(t *testing.T) {
	query := &metadata.Query{Measurement: sql_expr.Doris}

	assert.False(t, shouldDisableShardKeyTimeBucket(query, metadata.FieldsMap{
		"dtEventTimeStamp": {FieldType: sql_expr.DorisTypeBigInt},
		sql_expr.ShardKey:  {FieldType: sql_expr.DorisTypeBigInt},
	}, nil, "dtEventTimeStamp"))

	assert.True(t, shouldDisableShardKeyTimeBucket(query, metadata.FieldsMap{
		"dtEventTimeStamp": {FieldType: sql_expr.DorisTypeBigInt},
	}, nil, "dtEventTimeStamp"))

	assert.False(t, shouldDisableShardKeyTimeBucket(query, metadata.FieldsMap{
		sql_expr.ShardKey: {FieldType: sql_expr.DorisTypeBigInt},
	}, nil, "dtEventTimeStamp"))
}
