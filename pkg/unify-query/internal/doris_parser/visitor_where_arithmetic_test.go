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

// 比较两侧的算术子式要整体作为操作数输出；此前 a * 2 > 2 会渲染成非法的 a * > 2, 2。
func TestParseDorisSQLWithVisitorKeepsArithmeticComparisonOperands(t *testing.T) {
	fieldAlias := map[string]string{"pod_namespace": "__ext.io_kubernetes_pod_namespace"}
	testCases := []struct {
		name  string
		where string
		sql   string
	}{
		{name: "left multiply", where: "a * 2 > 2", sql: "a * 2 > 2"},
		{name: "left add", where: "a + 1 > 2", sql: "a + 1 > 2"},
		{name: "right multiply", where: "2 < a * 2", sql: "2 < a * 2"},
		{name: "both sides", where: "a * 2 > b + 1", sql: "a * 2 > b + 1"},
		{name: "with logic", where: "a / 2 > 1 AND b = 1", sql: "a / 2 > 1 AND b = 1"},
		{name: "in list", where: "a + 1 IN (2, 3)", sql: "a + 1 IN (2, 3)"},
		{name: "time offset", where: "dtEventTimeStamp - 1000 >= 1700000000000", sql: "dtEventTimeStamp - 1000 >= 1700000000000"},
		{name: "mapped field", where: "pod_namespace + 1 > 2", sql: "__ext.io_kubernetes_pod_namespace + 1 > 2"},
		{name: "plain comparison unchanged", where: "a = 'x' AND b > 1", sql: "a = 'x' AND b > 1"},
	}

	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			opt := &Option{
				DimensionTransform: func(s string) (string, string) {
					if v, ok := fieldAlias[s]; ok {
						return v, s
					}
					return s, ""
				},
				Tables: []string{"t.doris"},
			}
			sql, err := ParseDorisSQLWithVisitor(metadata.InitHashID(context.Background()), "SELECT COUNT(*) AS n WHERE "+c.where, opt)
			assert.Nil(t, err)
			assert.Equal(t, "SELECT COUNT(*) AS n FROM t.doris WHERE "+c.sql+" LIMIT 100", sql)
		})
	}
}
