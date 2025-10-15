// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sql_expr

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestDefaultSQLExpr_buildCondition_HDFS_RegexpLike(t *testing.T) {
	hdfsExpr := &DefaultSQLExpr{key: HDFS}
	defaultExpr := &DefaultSQLExpr{key: "default"}

	testCases := []struct {
		name      string
		expr      *DefaultSQLExpr
		condition metadata.ConditionField
		expected  string
	}{
		{
			name: "HDFS正则匹配单个值",
			expr: hdfsExpr,
			condition: metadata.ConditionField{
				DimensionName: "projectId",
				Operator:      metadata.ConditionRegEqual,
				Value:         []string{"tdstore"},
			},
			expected: "regexp_like(`projectId`, 'tdstore')",
		},
		{
			name: "HDFS正则匹配多个值",
			expr: hdfsExpr,
			condition: metadata.ConditionField{
				DimensionName: "bytes",
				Operator:      metadata.ConditionRegEqual,
				Value:         []string{"1[0-9]{8,}", "2[0-9]{7,}"},
			},
			expected: "regexp_like(`bytes`, '1[0-9]{8,}|2[0-9]{7,}')",
		},
		{
			name: "HDFS正则不匹配",
			expr: hdfsExpr,
			condition: metadata.ConditionField{
				DimensionName: "projectId",
				Operator:      metadata.ConditionNotRegEqual,
				Value:         []string{"test"},
			},
			expected: "NOT regexp_like(`projectId`, 'test')",
		},
		{
			name: "默认数据库正则匹配",
			expr: defaultExpr,
			condition: metadata.ConditionField{
				DimensionName: "projectId",
				Operator:      metadata.ConditionRegEqual,
				Value:         []string{"tdstore"},
			},
			expected: "`projectId` REGEXP 'tdstore'",
		},
		{
			name: "HDFS等于条件",
			expr: hdfsExpr,
			condition: metadata.ConditionField{
				DimensionName: "type",
				Operator:      metadata.ConditionEqual,
				Value:         []string{"RECEIVE"},
			},
			expected: "`type` = 'RECEIVE'",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.expr.buildCondition(tc.condition)
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestNewSQLExpr_HDFS(t *testing.T) {
	expr := NewSQLExpr(HDFS)
	defaultExpr, ok := expr.(*DefaultSQLExpr)
	assert.True(t, ok)
	assert.Equal(t, HDFS, defaultExpr.key)
}

func TestNewSQLExpr_Default(t *testing.T) {
	expr := NewSQLExpr("other")
	defaultExpr, ok := expr.(*DefaultSQLExpr)
	assert.True(t, ok)
	assert.Equal(t, "other", defaultExpr.key)
}

func TestHDFS_vs_Default_RegexComparison(t *testing.T) {
	condition := metadata.ConditionField{
		DimensionName: "projectId",
		Operator:      metadata.ConditionRegEqual,
		Value:         []string{"tdstore"},
	}

	hdfsExpr := NewSQLExpr(HDFS).(*DefaultSQLExpr)
	hdfsResult, err := hdfsExpr.buildCondition(condition)
	assert.NoError(t, err)
	assert.Equal(t, "regexp_like(`projectId`, 'tdstore')", hdfsResult)

	defaultExpr := NewSQLExpr("default").(*DefaultSQLExpr)
	defaultResult, err := defaultExpr.buildCondition(condition)
	assert.NoError(t, err)
	assert.Equal(t, "`projectId` REGEXP 'tdstore'", defaultResult)

	assert.NotEqual(t, hdfsResult, defaultResult)
}
