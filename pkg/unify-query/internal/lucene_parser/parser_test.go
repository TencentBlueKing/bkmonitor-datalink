// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lucene_parser

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestParseWithVisitor(t *testing.T) {
	testCases := []struct {
		name string
		q    string

		sql string
		err error
	}{
		// 用法验证
		{
			name: "test - 1",
			q:    `"/var/host/data/bcs/lib/docker/containers/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5-json.log" and level: "error" and "2_bklog.bkunify_query"`,
			sql:  "",
		},
		{
			name: "test - 2",
			q:    `loglevel: ("TRACE" OR "DEBUG" OR  "INFO " OR "WARN " OR "ERROR") AND log: ("friendsvr" AND ("game_app" OR "testOr") AND "testAnd" OR "test111")`,
			sql:  "",
		},
		{
			name: "test - 3",
			q:    `"test" AND`,
			sql:  "",
		},
		{
			name: "test - 3",
			q:    `test`,
			sql:  "",
		},
		{
			name: "test - 4",
			q:    `log:test AND message:"bm"`,
			sql:  `log:test AND message:"bm"`,
		},
		{
			name: "test - 5",
			q:    `log:test AND message:"bm" OR "test"`,
			sql:  `log:test AND message:"bm" OR "test"`,
		},
	}

	mock.Init()
	fieldAlias := map[string]string{
		"pod_namespace": "__ext.io_kubernetes_pod_namespace",
		"serverIp":      "test_server_ip",
	}

	ctx := context.Background()
	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)

			// antlr4 and visitor
			opt := &Option{
				DimensionTransform: func(s string) (string, bool) {
					if _, ok := fieldAlias[s]; ok {
						return fieldAlias[s], true
					}
					return s, false
				},
			}
			sql, err := ParseLuceneWithVisitor(ctx, c.q, opt)
			if c.err != nil {
				assert.Equal(t, c.err, err)
			} else {
				assert.Nil(t, err)
				assert.NotEmpty(t, sql)
				assert.Equal(t, c.sql, sql)
			}
		})
	}
}
