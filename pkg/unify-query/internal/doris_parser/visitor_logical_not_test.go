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

// WHERE 中的 NOT 此前被丢弃，NOT a = 1 渲染成 a = 1，过滤条件被静默反转。
func TestParseDorisSQLWithVisitorKeepsLogicalNot(t *testing.T) {
	testCases := []struct {
		name  string
		where string
		sql   string
	}{
		{name: "single predicate", where: "NOT a = 1", sql: "NOT a = 1"},
		{name: "grouped", where: "NOT (a = 1 OR b = 2)", sql: "NOT ( a = 1 OR b = 2 )"},
		{name: "before AND", where: "NOT a = 1 AND b = 2", sql: "NOT a = 1 AND b = 2"},
		{name: "after AND", where: "b = 2 AND NOT a = 1", sql: "b = 2 AND NOT a = 1"},
		{name: "inside group", where: "(a = 1 OR NOT b = 2) AND c = 3", sql: "( a = 1 OR NOT b = 2 ) AND c = 3"},
		{name: "nested", where: "a = 1 AND NOT (b = 2 AND NOT c = 3)", sql: "a = 1 AND NOT ( b = 2 AND NOT c = 3 )"},
		{name: "double", where: "NOT NOT a = 1", sql: "NOT NOT a = 1"},
		{name: "bang", where: "! (a = 1)", sql: "NOT ( a = 1 )"},
		{name: "NOT LIKE unchanged", where: "a NOT LIKE 'x%'", sql: "a NOT LIKE 'x%'"},
		{name: "no NOT unchanged", where: "a = 1 AND b = 2 OR c = 3", sql: "a = 1 AND b = 2 OR c = 3"},
	}

	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			sql, err := ParseDorisSQLWithVisitor(metadata.InitHashID(context.Background()),
				"SELECT COUNT(*) AS n WHERE "+c.where, &Option{Tables: []string{"t.doris"}})
			assert.Nil(t, err)
			assert.Equal(t, "SELECT COUNT(*) AS n FROM t.doris WHERE "+c.sql+" LIMIT 100", sql)
		})
	}
}
