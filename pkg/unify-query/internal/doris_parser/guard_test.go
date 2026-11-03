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

func parseForGuard(q string) (string, error) {
	return ParseDorisSQLWithVisitor(metadata.InitHashID(context.Background()), q, &Option{Tables: []string{"t.doris"}})
}

// 访问器没有建模的语法必须显式拒绝，并在错误里指出是哪一种写法；此前这些写法会被静默丢弃或改写。
func TestParseDorisSQLWithVisitorRejectsUnmodelledSyntax(t *testing.T) {
	testCases := []struct {
		q   string
		err string
	}{
		{"SELECT a UNION ALL SELECT b", "unsupported Doris SQL: UNION / EXCEPT / INTERSECT in QUERY clause: SELECT a UNION ALL SELECT b"},
		{"SELECT a FROM t.doris JOIN u.doris ON t.a = u.a", "unsupported Doris SQL: JOIN in FROM clause: JOIN u.doris ON t.a = u.a"},
		{"SELECT a WHERE EXISTS (SELECT 1 FROM u.doris)", "unsupported Doris SQL: EXISTS in WHERE clause: EXISTS (SELECT 1 FROM u.doris)"},
		{"SELECT a WHERE a IN (SELECT a FROM u.doris)", "unsupported Doris SQL: subquery in WHERE clause: SELECT a FROM u.doris"},
		{"SELECT a GROUP BY a WITH ROLLUP", "unsupported Doris SQL: ROLLUP in GROUP BY"},
		{"SELECT a QUALIFY ROW_NUMBER() OVER (ORDER BY a) = 1", "unsupported Doris SQL: QUALIFY in QUERY clause: QUALIFY ROW_NUMBER() OVER (ORDER BY a) = 1"},
		{"SELECT a, ROW_NUMBER() OVER (PARTITION BY a ORDER BY b) AS r", "unsupported Doris SQL: window function (OVER) in SELECT clause: (PARTITION BY a ORDER BY b)"},
		{"WITH x AS (SELECT a FROM t.doris) SELECT a FROM x", "unsupported Doris SQL: WITH (common table expression) in QUERY clause: WITH x AS (SELECT a FROM t.doris); rewrite it as a FROM subquery"},
		{"SELECT a FROM", "doris sql syntax error: line 1:13 mismatched input '<EOF>'"},
		// input the query rule stops before was dropped from the rewrite: a lowercase exponent lexes as 1.5, e, -, 3
		{"SELECT a WHERE a = 1.5e-3", `doris sql syntax error: unexpected input after the query at "e"`},
		{"SELECT a FROM t.doris LIMIT 5 junk", `doris sql syntax error: unexpected input after the query at "junk"`},
		// the visitor drops a NULL grouping or sort key
		{"SELECT COUNT(*) AS n GROUP BY NULL", "unsupported Doris SQL: NullLiteral in GROUP clause: NULL"},
	}
	for _, c := range testCases {
		t.Run(c.q, func(t *testing.T) {
			sql, err := parseForGuard(c.q)
			assert.Empty(t, sql)
			assert.ErrorContains(t, err, c.err)
		})
	}
}

// 已建模的写法照常渲染，守卫不影响。
func TestParseDorisSQLWithVisitorGuardKeepsModelledSyntax(t *testing.T) {
	testCases := []struct {
		q   string
		sql string
	}{
		{"SELECT a FROM (SELECT a FROM t.doris) t WHERE a > 1", "SELECT a FROM (SELECT a FROM t.doris) t WHERE a > 1 LIMIT 100"},
		{"SELECT DISTINCT a, b", "SELECT DISTINCT(a), b FROM t.doris LIMIT 100"},
		{"SELECT a ORDER BY a LIMIT 10 OFFSET 5", "SELECT a FROM t.doris ORDER BY a LIMIT 10 OFFSET 5"},
		{"SELECT a WHERE a REGEXP 'x.*' AND b LIKE 'y%' AND d IN (1, 2)", "SELECT a FROM t.doris WHERE a REGEXP 'x.*' AND b LIKE 'y%' AND d IN (1, 2) LIMIT 100"},
		{"SELECT a WHERE a MATCH_ANY 'x y'", "SELECT a FROM t.doris WHERE a MATCH_ANY 'x y' LIMIT 100"},
		{"SELECT COUNT(DISTINCT a) AS n", "SELECT COUNT(DISTINCT(a)) AS n FROM t.doris LIMIT 100"},
		{"SELECT minute1, COUNT(*) AS log_count GROUP BY minute1 ORDER BY minute1 DESC LIMIT 100", "SELECT minute1, COUNT(*) AS log_count FROM t.doris GROUP BY minute1 ORDER BY minute1 DESC LIMIT 100"},
		// modelled by the root-cause fixes that land before the guard
		{"SELECT a, COUNT(*) AS n GROUP BY a HAVING COUNT(*) > 1", "SELECT a, COUNT(*) AS n FROM t.doris GROUP BY a HAVING COUNT(*) > 1 LIMIT 100"},
		{"SELECT COUNT(*) AS n WHERE NOT a = 1", "SELECT COUNT(*) AS n FROM t.doris WHERE NOT a = 1 LIMIT 100"},
		{"SELECT -a AS x", "SELECT -a AS x FROM t.doris LIMIT 100"},
		{"SELECT CASE a WHEN 1 THEN 'x' ELSE 'y' END AS x", "SELECT CASE a WHEN 1 THEN 'x' ELSE 'y' END AS x FROM t.doris LIMIT 100"},
		// literals of every kind, in every clause that takes expressions
		{"SELECT a WHERE a > 1.5 AND b = true", "SELECT a FROM t.doris WHERE a > 1.5 AND b = true LIMIT 100"},
		{"SELECT a WHERE a = 1.5E-3", "SELECT a FROM t.doris WHERE a = 1.5E-3 LIMIT 100"},
		// trailing semicolons are the only input allowed after the query
		{"SELECT a FROM t.doris;", "SELECT a FROM t.doris LIMIT 100"},
	}
	for _, c := range testCases {
		t.Run(c.q, func(t *testing.T) {
			sql, err := parseForGuard(c.q)
			assert.Nil(t, err)
			assert.Equal(t, c.sql, sql)
		})
	}
}
