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

// WHERE 以外的条件此前被拆散：SELECT a = 1 渲染成 a[1]，IF(a > 1, 1, 0) 渲染成 IF(a, 1, 1, 0)，
// CASE WHEN a IN (1, 2) 渲染成 CASE WHEN a IN 1 (2, 1) 0 END，CASE a WHEN 1 ... 渲染成 a[1]['x']['y']。
func TestParseDorisSQLWithVisitorKeepsConditionsAsValues(t *testing.T) {
	fieldAlias := map[string]string{"m": "__ext.m"}
	testCases := []struct {
		name string
		q    string
		sql  string
	}{
		{name: "comparison as a value", q: "SELECT a = 1 AS x", sql: "SELECT a = 1 AS x FROM t.doris LIMIT 100"},
		{name: "AND as a value", q: "SELECT a > 1 AND b < 2 AS x", sql: "SELECT a > 1 AND b < 2 AS x FROM t.doris LIMIT 100"},
		{name: "IS NULL as a value", q: "SELECT m IS NULL AS x", sql: "SELECT __ext.m IS NULL AS x FROM t.doris LIMIT 100"},
		{name: "IN as a value", q: "SELECT a IN (1, 2) AS x", sql: "SELECT a IN (1, 2) AS x FROM t.doris LIMIT 100"},
		{name: "IF condition", q: "SELECT IF(a > 1, 1, 0) AS x", sql: "SELECT IF(a > 1, 1, 0) AS x FROM t.doris LIMIT 100"},
		{name: "IF inside an aggregate", q: "SELECT SUM(IF(a = 1, 1, 0)) AS x", sql: "SELECT SUM(IF(a = 1, 1, 0)) AS x FROM t.doris LIMIT 100"},
		{name: "COUNT_IF", q: "SELECT COUNT_IF(a > 1) AS x", sql: "SELECT COUNT_IF(a > 1) AS x FROM t.doris LIMIT 100"},
		{name: "IF with OR", q: "SELECT IF(m IN ('a') OR b IS NULL, 1, 0) AS x", sql: "SELECT IF(__ext.m IN ('a') OR b IS NULL, 1, 0) AS x FROM t.doris LIMIT 100"},
		{name: "CASE WHEN IN", q: "SELECT CASE WHEN a IN (1, 2) THEN 1 ELSE 0 END AS x", sql: "SELECT CASE WHEN a IN (1, 2) THEN 1 ELSE 0 END AS x FROM t.doris LIMIT 100"},
		{name: "CASE WHEN IS NULL", q: "SELECT CASE WHEN a IS NULL THEN 1 ELSE 0 END AS x", sql: "SELECT CASE WHEN a IS NULL THEN 1 ELSE 0 END AS x FROM t.doris LIMIT 100"},
		{name: "CASE WHEN AND", q: "SELECT CASE WHEN a > 1 AND b < 2 THEN 'x' ELSE 'y' END AS x", sql: "SELECT CASE WHEN a > 1 AND b < 2 THEN 'x' ELSE 'y' END AS x FROM t.doris LIMIT 100"},
		{name: "CASE WHEN comparison unchanged", q: "SELECT CASE WHEN a > 1 THEN 'x' ELSE 'y' END AS x", sql: "SELECT CASE WHEN a > 1 THEN 'x' ELSE 'y' END AS x FROM t.doris LIMIT 100"},
		{name: "CASE WHEN mapped", q: "SELECT CASE WHEN m = 'e' THEN 1 WHEN m = 'w' THEN 2 END AS x", sql: "SELECT CASE WHEN __ext.m = 'e' THEN 1 WHEN __ext.m = 'w' THEN 2 END AS x FROM t.doris LIMIT 100"},
		{name: "simple CASE", q: "SELECT CASE a WHEN 1 THEN 'x' ELSE 'y' END AS x", sql: "SELECT CASE a WHEN 1 THEN 'x' ELSE 'y' END AS x FROM t.doris LIMIT 100"},
		{name: "simple CASE mapped", q: "SELECT CASE m WHEN 'a' THEN 1 WHEN 'b' THEN 2 END AS x", sql: "SELECT CASE __ext.m WHEN 'a' THEN 1 WHEN 'b' THEN 2 END AS x FROM t.doris LIMIT 100"},
		{name: "simple CASE inside an aggregate", q: "SELECT SUM(CASE a WHEN 1 THEN 1 ELSE 0 END) AS x", sql: "SELECT SUM(CASE a WHEN 1 THEN 1 ELSE 0 END) AS x FROM t.doris LIMIT 100"},
		{name: "simple CASE nested in CASE", q: "SELECT CASE WHEN b > 0 THEN CASE a WHEN 1 THEN 'x' END ELSE 'z' END AS x", sql: "SELECT CASE WHEN b > 0 THEN CASE a WHEN 1 THEN 'x' END ELSE 'z' END AS x FROM t.doris LIMIT 100"},
		{name: "simple CASE in WHERE", q: "SELECT COUNT(*) AS n WHERE CASE a WHEN 1 THEN 1 ELSE 0 END = 1", sql: "SELECT COUNT(*) AS n FROM t.doris WHERE CASE a WHEN 1 THEN 1 ELSE 0 END = 1 LIMIT 100"},
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
