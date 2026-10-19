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

// 谓词此前按"左操作数 运算符 常量列表"建模：右侧的列被丢掉（a = b 渲染成 a =），BETWEEN 渲染成
// BETWEEN AND 1, 5，NOT IN 丢了括号，LIKE ... ESCAPE 渲染成 LIKE ESCAPE 'x%', '!'。
func TestParseDorisSQLWithVisitorKeepsPredicateOperands(t *testing.T) {
	fieldAlias := map[string]string{"m": "__ext.m"}
	testCases := []struct {
		name  string
		where string
		sql   string
	}{
		{name: "column on the right", where: "a = b", sql: "a = b"},
		{name: "literal on the left", where: "'x' = m", sql: "'x' = __ext.m"},
		{name: "number on the left", where: "2 < m", sql: "2 < __ext.m"},
		{name: "mapped column on the right", where: "a = m", sql: "a = __ext.m"},
		{name: "function on the right", where: "a = UPPER(b)", sql: "a = UPPER(b)"},
		{name: "literal on the right unchanged", where: "a = 'x'", sql: "a = 'x'"},
		{name: "between", where: "a BETWEEN 1 AND 5", sql: "a BETWEEN 1 AND 5"},
		{name: "not between", where: "a NOT BETWEEN 1 AND 5", sql: "a NOT BETWEEN 1 AND 5"},
		{name: "between then AND", where: "m BETWEEN 1 AND 5 AND b = 1", sql: "__ext.m BETWEEN 1 AND 5 AND b = 1"},
		{name: "between strings", where: "dt BETWEEN '2026-10-01' AND '2026-10-02'", sql: "dt BETWEEN '2026-10-01' AND '2026-10-02'"},
		{name: "in", where: "a IN ('x', 'y')", sql: "a IN ('x', 'y')"},
		{name: "not in", where: "a NOT IN ('x', 'y')", sql: "a NOT IN ('x', 'y')"},
		{name: "in and not in", where: "a IN (1, 2) AND b NOT IN (3)", sql: "a IN (1, 2) AND b NOT IN (3)"},
		{name: "like escape", where: "a LIKE 'x%' ESCAPE '!'", sql: "a LIKE 'x%' ESCAPE '!'"},
		{name: "not like", where: "a NOT LIKE 'x%'", sql: "a NOT LIKE 'x%'"},
		{name: "is null", where: "a IS NULL", sql: "a IS NULL"},
		{name: "is not null mapped", where: "m IS NOT NULL", sql: "__ext.m IS NOT NULL"},
		{name: "match phrase", where: "log MATCH_PHRASE 'Error' AND log MATCH_ALL 'a b'", sql: "log MATCH_PHRASE 'Error' AND log MATCH_ALL 'a b'"},
		{name: "regexp", where: "a REGEXP '^x'", sql: "a REGEXP '^x'"},
		{name: "keyword case kept", where: "log like 'test*'", sql: "log like 'test*'"},
	}

	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			sql, err := ParseDorisSQLWithVisitor(metadata.InitHashID(context.Background()), "SELECT COUNT(*) AS n WHERE "+c.where, &Option{
				DimensionTransform: func(s string) (string, string) {
					if v, ok := fieldAlias[s]; ok {
						return v, s
					}
					return s, ""
				},
				Tables: []string{"t.doris"},
			})
			assert.Nil(t, err)
			assert.Equal(t, "SELECT COUNT(*) AS n FROM t.doris WHERE "+c.sql+" LIMIT 100", sql)
		})
	}
}
