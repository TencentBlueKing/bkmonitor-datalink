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

// 注入条件（时间范围等）必须约束用户 WHERE 的全部分支。此前用 AND 直接拼接，
// a = 1 OR b = 2 AND (时间) 使时间范围只约束最后一个 OR 分支。
func TestParseDorisSQLWithVisitorInjectedWhereBoundsWholeUserCondition(t *testing.T) {
	const injected = "(`dtEventTimeStamp` >= 1 AND `dtEventTimeStamp` < 2)"
	testCases := []struct {
		name  string
		q     string
		where string
		sql   string
	}{
		{
			name:  "user OR",
			q:     "SELECT COUNT(*) AS n WHERE a = 1 OR b = 2",
			where: injected,
			sql:   "SELECT COUNT(*) AS n FROM t.doris WHERE (a = 1 OR b = 2) AND " + injected + " LIMIT 100",
		},
		{
			name:  "user OR with MATCH_PHRASE",
			q:     "SELECT COUNT(*) AS n WHERE log MATCH_PHRASE 'Error' OR log MATCH_PHRASE 'Fatal'",
			where: injected,
			sql:   "SELECT COUNT(*) AS n FROM t.doris WHERE (log MATCH_PHRASE 'Error' OR log MATCH_PHRASE 'Fatal') AND " + injected + " LIMIT 100",
		},
		{
			name:  "user AND",
			q:     "SELECT COUNT(*) AS n WHERE a = 1 AND b = 2",
			where: injected,
			sql:   "SELECT COUNT(*) AS n FROM t.doris WHERE (a = 1 AND b = 2) AND " + injected + " LIMIT 100",
		},
		{
			name:  "user condition already grouped",
			q:     "SELECT COUNT(*) AS n WHERE (a = 1 OR b = 2)",
			where: injected,
			sql:   "SELECT COUNT(*) AS n FROM t.doris WHERE ( a = 1 OR b = 2 ) AND " + injected + " LIMIT 100",
		},
		{
			name:  "no user condition",
			q:     "SELECT COUNT(*) AS n",
			where: injected,
			sql:   "SELECT COUNT(*) AS n FROM t.doris WHERE " + injected + " LIMIT 100",
		},
		{
			name: "no injected condition",
			q:    "SELECT COUNT(*) AS n WHERE a = 1 OR b = 2",
			sql:  "SELECT COUNT(*) AS n FROM t.doris WHERE a = 1 OR b = 2 LIMIT 100",
		},
	}

	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			sql, err := ParseDorisSQLWithVisitor(metadata.InitHashID(context.Background()), c.q, &Option{
				Tables: []string{"t.doris"},
				Where:  c.where,
			})
			assert.Nil(t, err)
			assert.Equal(t, c.sql, sql)
		})
	}
}
