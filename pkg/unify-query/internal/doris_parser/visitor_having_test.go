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

// HAVING 此前没有实现，整个子句被丢弃：GROUP BY a HAVING COUNT(*) > 1 返回全部分组且不报错。
func TestParseDorisSQLWithVisitorKeepsHaving(t *testing.T) {
	fieldAlias := map[string]string{"m": "__ext.m"}
	const injected = "(`dtEventTimeStamp` >= 1)"
	testCases := []struct {
		name   string
		q      string
		tables []string
		where  string
		sql    string
	}{
		{
			name: "aggregate condition",
			q:    "SELECT a, COUNT(*) AS n GROUP BY a HAVING COUNT(*) > 1",
			sql:  "SELECT a, COUNT(*) AS n FROM t.doris GROUP BY a HAVING COUNT(*) > 1 LIMIT 100",
		},
		{
			name: "select alias, before ORDER BY and LIMIT",
			q:    "SELECT a, COUNT(*) AS n GROUP BY a HAVING n > 1 ORDER BY n DESC LIMIT 10",
			sql:  "SELECT a, COUNT(*) AS n FROM t.doris GROUP BY a HAVING n > 1 ORDER BY n DESC LIMIT 10",
		},
		{
			name: "mapped field inside aggregate",
			q:    "SELECT m, COUNT(*) AS n GROUP BY m HAVING COUNT(*) > 1 AND MAX(m) != 'x'",
			sql:  "SELECT __ext.m AS m, COUNT(*) AS n FROM t.doris GROUP BY __ext.m HAVING COUNT(*) > 1 AND MAX(__ext.m) != 'x' LIMIT 100",
		},
		{
			name:  "OR is not joined to the injected WHERE",
			q:     "SELECT a, COUNT(*) AS n GROUP BY a HAVING COUNT(*) > 1 OR SUM(b) < 2",
			where: injected,
			sql:   "SELECT a, COUNT(*) AS n FROM t.doris WHERE " + injected + " GROUP BY a HAVING COUNT(*) > 1 OR SUM(b) < 2 LIMIT 100",
		},
		{
			name: "grouped condition",
			q:    "SELECT a, COUNT(*) AS n GROUP BY a HAVING (n > 1 OR n < 0)",
			sql:  "SELECT a, COUNT(*) AS n FROM t.doris GROUP BY a HAVING ( n > 1 OR n < 0 ) LIMIT 100",
		},
		{
			name:   "multi-table: on the outer query, its fields projected by every branch",
			q:      "SELECT a, COUNT(*) AS n GROUP BY a HAVING MAX(b) > 1",
			tables: []string{"t1.doris", "t2.doris"},
			where:  injected,
			sql: "SELECT a, COUNT(*) AS n FROM (SELECT `a`, `b` FROM t1.doris WHERE " + injected + " UNION ALL " +
				"SELECT `a`, `b` FROM t2.doris WHERE " + injected + ") AS combined_data GROUP BY a HAVING MAX(b) > 1 LIMIT 100",
		},
		{
			name:   "multi-table: a select alias is not projected",
			q:      "SELECT a, COUNT(*) AS n GROUP BY a HAVING n > 1",
			tables: []string{"t1.doris", "t2.doris"},
			where:  injected,
			sql: "SELECT a, COUNT(*) AS n FROM (SELECT `a` FROM t1.doris WHERE " + injected + " UNION ALL " +
				"SELECT `a` FROM t2.doris WHERE " + injected + ") AS combined_data GROUP BY a HAVING n > 1 LIMIT 100",
		},
	}

	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			tables := c.tables
			if tables == nil {
				tables = []string{"t.doris"}
			}
			ignored := map[string]bool{}
			sql, err := ParseDorisSQLWithVisitor(metadata.InitHashID(context.Background()), c.q, &Option{
				DimensionTransform: func(s string) (string, string) {
					if ignored[s] {
						return s, ""
					}
					if v, ok := fieldAlias[s]; ok {
						return v, s
					}
					return s, ""
				},
				AddIgnoreField: func(s string) { ignored[s] = true },
				Tables:         tables,
				Where:          c.where,
			})
			assert.Nil(t, err)
			assert.Equal(t, c.sql, sql)
		})
	}
}
