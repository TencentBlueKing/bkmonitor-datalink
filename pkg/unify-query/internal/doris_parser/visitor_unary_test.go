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
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// 一元正负号此前被丢弃：SELECT -a 渲染成 a（静默丢符号），WHERE -a > 1 渲染成非法的 a - > 1。
func TestParseDorisSQLWithVisitorKeepsUnaryOperators(t *testing.T) {
	fieldAlias := map[string]string{"m": "__ext.m"}
	testCases := []struct {
		name string
		q    string
		sql  string
	}{
		{name: "select minus", q: "SELECT -a AS x", sql: "SELECT -a AS x FROM t.doris LIMIT 100"},
		{name: "select plus", q: "SELECT +a AS x", sql: "SELECT +a AS x FROM t.doris LIMIT 100"},
		{name: "minus of group", q: "SELECT -(a + b) AS x", sql: "SELECT -(a + b) AS x FROM t.doris LIMIT 100"},
		{name: "right operand", q: "SELECT a * -b AS x", sql: "SELECT a * -b AS x FROM t.doris LIMIT 100"},
		{name: "left operand", q: "SELECT -a * b AS x", sql: "SELECT -a * b AS x FROM t.doris LIMIT 100"},
		{name: "function argument", q: "SELECT ABS(-a) AS x", sql: "SELECT ABS(-a) AS x FROM t.doris LIMIT 100"},
		{name: "aggregate argument", q: "SELECT SUM(-a) AS x", sql: "SELECT SUM(-a) AS x FROM t.doris LIMIT 100"},
		{name: "cast value", q: "SELECT CAST(-a AS INT) AS x", sql: "SELECT CAST(-a AS INT) AS x FROM t.doris LIMIT 100"},
		{name: "minus of function", q: "SELECT -FLOOR(a) AS x", sql: "SELECT -(FLOOR(a)) AS x FROM t.doris LIMIT 100"},
		{name: "case value", q: "SELECT CASE WHEN a > 0 THEN -a ELSE a END AS x", sql: "SELECT CASE WHEN a > 0 THEN -a ELSE a END AS x FROM t.doris LIMIT 100"},
		{name: "mapped field", q: "SELECT -m AS x", sql: "SELECT -__ext.m AS x FROM t.doris LIMIT 100"},
		{name: "where left", q: "SELECT COUNT(*) AS n WHERE -a > 1", sql: "SELECT COUNT(*) AS n FROM t.doris WHERE -a > 1 LIMIT 100"},
		{name: "where right", q: "SELECT COUNT(*) AS n WHERE 1 < -a", sql: "SELECT COUNT(*) AS n FROM t.doris WHERE 1 < -a LIMIT 100"},
		{name: "where mapped", q: "SELECT COUNT(*) AS n WHERE -m > 1", sql: "SELECT COUNT(*) AS n FROM t.doris WHERE -__ext.m > 1 LIMIT 100"},
		// 两个负号相邻会变成注释起始 --，必须加括号
		{name: "double minus", q: "SELECT - -a AS x", sql: "SELECT -(-a) AS x FROM t.doris LIMIT 100"},
		{name: "order by", q: "SELECT a, -SUM(b) AS x GROUP BY a ORDER BY -SUM(b)", sql: "SELECT a, -(SUM(b)) AS x FROM t.doris GROUP BY a ORDER BY -(SUM(b)) LIMIT 100"},
		{name: "negative literal unchanged", q: "SELECT a * -1 AS x", sql: "SELECT a * -1 AS x FROM t.doris LIMIT 100"},
		{name: "negative literal in where unchanged", q: "SELECT COUNT(*) AS n WHERE a > -1", sql: "SELECT COUNT(*) AS n FROM t.doris WHERE a > -1 LIMIT 100"},
	}

	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			sql, err := ParseDorisSQLWithVisitor(metadata.InitHashID(context.Background()), c.q, &Option{
				DimensionTransform: func(s string) (string, string) {
					if v, ok := fieldAlias[s]; ok {
						return v, s
					}
					return s, ""
				},
				Tables: []string{"t.doris"},
			})
			assert.Nil(t, err)
			assert.Equal(t, c.sql, sql)
		})
	}
}
