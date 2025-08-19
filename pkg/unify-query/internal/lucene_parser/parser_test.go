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
		es  string
		err error
	}{
		{
			name: "基础查询 - 单字段",
			q:    `status:200`,
			sql:  `"status" = '200'`,
			es:   `{"term":{"status":"200"}}`,
		},
		{
			name: "基础查询 - 多字段",
			q:    `host:server1 AND port:80 AND protocol:http`,
			sql:  `"host" = 'server1' AND "port" = '80' AND "protocol" = 'http'`,
			es:   `{"bool":{"must":[{"term":{"host":"server1"}},{"term":{"port":"80"}},{"term":{"protocol":"http"}}]}}`,
		},
		{
			name: "基础查询 - 无字段名",
			q:    `error`,
			sql:  `"_all" like '%error%'`,
			es:   `{"query_string":{"query":"error"}}`,
		},

		// 布尔运算符优先级测试（AND > OR）
		{
			name: "优先级测试 - AND优先于OR",
			q:    `a:1 OR b:2 AND c:3 OR d:4`,
			sql:  `"a" = '1' OR "b" = '2' AND "c" = '3' OR "d" = '4'`,
			es:   `{"bool":{"should":[{"term":{"a":"1"}},{"bool":{"must":[{"term":{"b":"2"}},{"term":{"c":"3"}}]}},{"term":{"d":"4"}}]}}`,
		},
		{
			name: "优先级测试 - 多个AND和OR混合",
			q:    `host:web AND status:500 OR host:db AND status:503`,
			sql:  `"host" = 'web' AND "status" = '500' OR "host" = 'db' AND "status" = '503'`,
			es:   `{"bool":{"should":[{"bool":{"must":[{"term":{"host":"web"}},{"term":{"status":"500"}}]}},{"bool":{"must":[{"term":{"host":"db"}},{"term":{"status":"503"}}]}}]}}`,
		},

		// 修饰符测试（+ - NOT）
		{
			name: "修饰符测试 - 必须包含",
			q:    `+status:200 +method:GET`,
			sql:  `"status" = '200' AND "method" = 'GET'`,
			es:   `{"bool":{"must":[{"term":{"status":"200"}},{"term":{"method":"GET"}}]}}`,
		},
		{
			name: "修饰符测试 - 必须排除",
			q:    `status:200 -error:true`,
			sql:  `"status" = '200' AND "error" != 'true'`,
			es:   `{"bool":{"must":[{"term":{"status":"200"}},{"bool":{"must_not":{"term":{"error":"true"}}}}]}}`,
		},
		{
			name: "修饰符测试 - NOT运算符",
			q:    `NOT status:404 AND host:web`,
			sql:  `"status" != '404' AND "host" = 'web'`,
			es:   `{"bool":{"must":[{"bool":{"must_not":{"term":{"status":"404"}}}},{"term":{"host":"web"}}]}}`,
		},

		// 分组测试（括号优先级）
		{
			name: "分组测试 - 简单括号",
			q:    `(a:1 OR b:2) AND c:3`,
			sql:  `"a" = '1' OR "b" = '2' AND "c" = '3'`,
			es:   `{"bool":{"must":[{"bool":{"should":[{"term":{"a":"1"}},{"term":{"b":"2"}}]}},{"term":{"c":"3"}}]}}`,
		},
		{
			name: "分组测试 - 多层括号",
			q:    `(a:1 AND (b:2 OR c:3)) OR d:4`,
			sql:  `"a" = '1' AND "b" = '2' OR "c" = '3' OR "d" = '4'`,
			es:   `{"bool":{"should":[{"bool":{"must":[{"term":{"a":"1"}},{"bool":{"should":[{"term":{"b":"2"}},{"term":{"c":"3"}}]}}]}},{"term":{"d":"4"}}]}}`,
		},

		// 范围查询测试（数字、字符串、日期）
		{
			name: "范围查询 - 数字闭区间",
			q:    `price:[100 TO 500]`,
			sql:  `"price" BETWEEN 100 AND 500`,
			es:   `{"range":{"price":{"gte":100,"lte":500}}}`,
		},
		{
			name: "范围查询 - 字符串范围",
			q:    `name:[Alice TO Bob]`,
			sql:  `"name" BETWEEN 'Alice' AND 'Bob'`,
			es:   `{"range":{"name":{"gte":"Alice","lte":"Bob"}}}`,
		},

		// 特殊查询测试（正则、模糊、权重）
		{
			name: "特殊查询 - 正则表达式",
			q:    `path:/.*\.log/`,
			sql:  `"path" REGEXP '.*\.log'`,
			es:   `{"regexp":{"path":".*\.log"}}`,
		},
		{
			name: "特殊查询 - 权重查询",
			q:    `title:important`,
			sql:  `"title" = 'important'`,
			es:   `{"term":{"title":{"value":"important"}}}`,
		},
		{
			name: "特殊查询 - 引号短语",
			q:    `message:"user login failed"`,
			sql:  `"message" = 'user login failed'`,
			es:   `{"match_phrase":{"message":"user login failed"}}`,
		},

		// 函数查询测试（fn:func）
		{
			name: "复杂组合 - 短语查询",
			q:    `content:"quick brown fox"`,
			sql:  `"content" = 'quick brown fox'`,
			es:   `{"match_phrase":{"content":"quick brown fox"}}`,
		},

		// 复杂组合测试
		{
			name: "复杂组合 - 所有特性混合",
			q:    `host:web AND status:[200 TO 299]`,
			sql:  `"host" = 'web' AND "status" BETWEEN 200 AND 299`,
			es:   `{"bool":{"must":[{"term":{"host":"web"}},{"range":{"status":{"gte":200,"lte":299}}}]}}`,
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
