// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package doris_parser

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// 括号决定运算优先级，重写 SQL 时去掉括号会静默改变结果，如 (a + 0) / 60000 变成 a + 0 / 60000。
func TestParseDorisSQLWithVisitorKeepsArithmeticParentheses(t *testing.T) {
	testCases := []struct {
		name string
		q    string
		sql  string
	}{
		{
			name: "time bucket with offset",
			q:    "SELECT (CAST(FLOOR((dtEventTimeStamp + 0) / 60000) AS BIGINT) * 60000 - 0) AS ts, COUNT(*) AS n GROUP BY ts",
			sql:  "SELECT CAST(FLOOR((dtEventTimeStamp + 0) / 60000) AS BIGINT) * 60000 - 0 AS ts, COUNT(*) AS n FROM t.doris GROUP BY ts LIMIT 100",
		},
		{
			name: "nested groups inside cast",
			q:    "SELECT ((CAST((FLOOR(shard_key / 1000) + 0) / 5 AS INT) * 5 - 0) * 60 * 1000) AS ts",
			sql:  "SELECT (CAST((FLOOR(shard_key / 1000) + 0) / 5 AS INT) * 5 - 0) * 60 * 1000 AS ts FROM t.doris LIMIT 100",
		},
		{
			name: "no parentheses",
			q:    "SELECT FLOOR(dtEventTimeStamp / 60000) AS a, dtEventTimeStamp / 60000 AS b",
			sql:  "SELECT FLOOR(dtEventTimeStamp / 60000) AS a, dtEventTimeStamp / 60000 AS b FROM t.doris LIMIT 100",
		},
		{
			name: "right operand group",
			q:    "SELECT a * (b + c) AS x",
			sql:  "SELECT a * (b + c) AS x FROM t.doris LIMIT 100",
		},
		{
			name: "ratio",
			q:    "SELECT (a - b) / (a + b) AS ratio",
			sql:  "SELECT (a - b) / (a + b) AS ratio FROM t.doris LIMIT 100",
		},
		{
			name: "outer parentheses of a select item stay dropped",
			q:    "select DISTINCT(a),b",
			sql:  "SELECT DISTINCT(a), b FROM t.doris LIMIT 100",
		},
	}

	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			opt := &Option{
				DimensionTransform: func(s string) (string, string) {
					return strings.Trim(s, "`"), ""
				},
				Tables: []string{"t.doris"},
			}
			sql, err := ParseDorisSQLWithVisitor(ctx, c.q, opt)
			assert.Nil(t, err)
			assert.Equal(t, c.sql, sql)
		})
	}
}
