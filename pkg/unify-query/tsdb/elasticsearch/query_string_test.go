// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/spf13/cast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/lucene_parser"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestQsToDsl(t *testing.T) {
	mock.Init()
	testMapping := metadata.FieldsMap{
		"log": {
			FieldName:   "log",
			FieldType:   Text,
			OriginField: "log",
			IsAnalyzed:  true,
		},
		"level": {
			FieldName: "level",
			FieldType: KeyWord,
		},
		"loglevel": {
			FieldName: "loglevel",
			FieldType: KeyWord,
		},
		"word.key": {
			FieldName:   "word.key",
			OriginField: "word",
			FieldType:   Text,
		},
		"ms": {
			FieldName: "ms",
			FieldType: Long,
		},
		"events.attributes.message.detail": {
			AliasName:   "event_detail",
			OriginField: "events",
			FieldType:   Text,
			IsAnalyzed:  true,
		},
		"nested.key": {
			FieldName:   "nested.key",
			OriginField: "nested",
			IsAnalyzed:  true,
			FieldType:   Text,
		},
		"events": {
			FieldName: "events",
			FieldType: Nested,
		},
		"nested": {
			FieldType: Nested,
		},
		"user": {
			FieldType: Nested,
		},
		"group": {
			FieldType: Text,
		},
		"request_uri": {
			FieldName: "request_uri",
			FieldType: KeyWord,
		},
		"__ext.io_kubernetes_workload_name": {
			FieldName: "__ext.io_kubernetes_workload_name",
			FieldType: KeyWord,
		},
	}

	ctx := metadata.InitHashID(context.Background())
	for i, c := range []struct {
		q        string
		expected string
		err      error
	}{
		{
			q:        `log: "ERROR MSG"`,
			expected: `{"match_phrase":{"log":{"query":"ERROR MSG"}}}`,
		},
		{
			q:        `quick brown fox`,
			expected: `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"quick"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"brown"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"fox"}}]}}`,
		},
		{
			q:        `word.key: qu?ck`,
			expected: `{"wildcard":{"word.key":{"value":"qu?ck"}}}`,
		},
		{
			q:        "\"message queue conflict\"",
			expected: `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"message queue conflict\""}}`,
		},
		{
			q:        `nested.key: test AND demo`,
			expected: `{"bool":{"must":[{"nested":{"path":"nested","query":{"match_phrase":{"nested.key":{"query":"test"}}}}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"demo"}}]}}`,
		},
		{
			q:        `sync_spaces AND -keyword AND -BKLOGAPI`,
			expected: `{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"sync_spaces"}},{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"keyword"}}}},{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"BKLOGAPI"}}}}]}}`,
		},
		{
			q: `*`,
		},
		{
			q: `*`,
		},
		{
			q:        `demo*`,
			expected: `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"demo*"}}`,
		},
		{
			q:        `demo`,
			expected: `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"demo"}}`,
		},
		{
			q:        `"demo"`,
			expected: `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"demo\""}}`,
		},
		{
			q:        `demo`,
			expected: `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"demo"}}`,
		},
		{
			q: ``,
		},
		{
			q:        "ms: \u003e500 AND \"/fs-server\" AND NOT \"heartbeat\"",
			expected: `{"bool":{"must":[{"range":{"ms":{"from":500,"include_lower":false,"include_upper":true,"to":null}}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"/fs-server\""}},{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"heartbeat\""}}}}]}}`,
		},
		{
			q:        `events.attributes.message.detail: "*66036*"`,
			expected: `{"nested":{"path":"events","query":{"match_phrase":{"events.attributes.message.detail":{"query":"*66036*"}}}}}`,
		},
		// 测试别名
		{
			q:        `event_detail: "*66036*"`,
			expected: `{"nested":{"path":"events","query":{"match_phrase":{"events.attributes.message.detail":{"query":"*66036*"}}}}}`,
		},
		{
			q:        `"/var/host/data/bcs/lib/docker/containers/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5-json.log" AND level: "error" AND "2_bklog.bkunify_query"`, // lucene是大小写不敏感的
			expected: `{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"/var/host/data/bcs/lib/docker/containers/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5-json.log\""}},{"term":{"level":"error"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"2_bklog.bkunify_query\""}}]}}`,
		},
		{
			q:        `(loglevel: ("TRACE" OR "DEBUG" OR  "INFO " OR "WARN " OR "ERROR") AND log: ("friendsvr" AND ("game_app" OR "testOr") AND "testAnd" OR "test111")) AND "test111"`,
			expected: `{"bool":{"must":[{"bool":{"must":[{"bool":{"should":[{"term":{"loglevel":"TRACE"}},{"term":{"loglevel":"DEBUG"}},{"term":{"loglevel":"INFO "}},{"term":{"loglevel":"WARN "}},{"term":{"loglevel":"ERROR"}}]}},{"bool":{"must":[{"match_phrase":{"log":{"query":"friendsvr"}}},{"bool":{"should":[{"match_phrase":{"log":{"query":"game_app"}}},{"match_phrase":{"log":{"query":"testOr"}}}]}},{"match_phrase":{"log":{"query":"testAnd"}}}],"should":{"match_phrase":{"log":{"query":"test111"}}}}}]}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"test111\""}}]}}`,
		},
		{
			q:        `loglevel: ("TRACE" AND "111" AND "DEBUG" AND "INFO" OR "SIMON" OR "222" AND "333" )`,
			expected: `{"bool":{"must":[{"term":{"loglevel":"TRACE"}},{"term":{"loglevel":"111"}},{"term":{"loglevel":"DEBUG"}},{"term":{"loglevel":"INFO"}},{"term":{"loglevel":"333"}}],"should":[{"term":{"loglevel":"SIMON"}},{"term":{"loglevel":"222"}}]}}`,
		},
		{
			q:        `loglevel: ("TRACE" OR ("DEBUG") OR  ("INFO ") OR "WARN " OR "ERROR") AND log: ("friendsvr" AND ("game_app" OR "testOr") AND "testAnd" OR "test111")`,
			expected: `{"bool":{"must":[{"bool":{"should":[{"term":{"loglevel":"TRACE"}},{"term":{"loglevel":"DEBUG"}},{"term":{"loglevel":"INFO "}},{"term":{"loglevel":"WARN "}},{"term":{"loglevel":"ERROR"}}]}},{"bool":{"must":[{"match_phrase":{"log":{"query":"friendsvr"}}},{"bool":{"should":[{"match_phrase":{"log":{"query":"game_app"}}},{"match_phrase":{"log":{"query":"testOr"}}}]}},{"match_phrase":{"log":{"query":"testAnd"}}}],"should":{"match_phrase":{"log":{"query":"test111"}}}}}]}}`,
		},
		// 引号内 ? 不应被视为通配符（URL 查询参数场景）
		{
			q:        `!request_uri:"/scm/api/proxy?serviceName=test&methodName=hook"`,
			expected: `{"bool":{"must_not":{"term":{"request_uri":"/scm/api/proxy?serviceName=test\u0026methodName=hook"}}}}`,
		},
		{
			q:        `request_uri:"/scm/api/proxy?serviceName=test"`,
			expected: `{"term":{"request_uri":"/scm/api/proxy?serviceName=test"}}`,
		},
		// 引号内 * 不应生成 wildcard 查询，应生成 match_phrase（与 ES query_string 语义一致）
		{
			q:        `__ext.io_kubernetes_workload_name: "prod-roomjob-sts" AND level: "ERROR" AND NOT log:"err*" AND log:"error*"`,
			expected: `{"bool":{"must":[{"term":{"__ext.io_kubernetes_workload_name":"prod-roomjob-sts"}},{"term":{"level":"ERROR"}},{"bool":{"must_not":{"match_phrase":{"log":{"query":"err*"}}}}},{"match_phrase":{"log":{"query":"error*"}}}]}}`,
		},
		{
			q:        `_exists_:level`,
			expected: `{"exists":{"field":"level"}}`,
		},
		{
			q:        `NOT _exists_:level`,
			expected: `{"bool":{"must_not":{"exists":{"field":"level"}}}}`,
		},
		{
			q:        `_exists_: log OR _exists_: level`,
			expected: `{"bool":{"should":[{"exists":{"field":"log"}},{"exists":{"field":"level"}}]}}`,
		},
		{
			q:        `_exists_: event_detail`,
			expected: `{"exists":{"field":"events.attributes.message.detail"}}`,
		},
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			node := lucene_parser.ParseLuceneWithVisitor(ctx, c.q, lucene_parser.Option{
				FieldsMap: testMapping,
			})
			if c.err != nil {
				assert.Equal(t, c.err.Error(), node.Error().Error())
			} else if c.expected != "" {
				q := lucene_parser.MergeQuery(node.DSL())
				require.NotNil(t, q, "ES query should not be nil when expected result is provided")
				body, err := q.Source()
				assert.Nil(t, err)
				require.NotNil(t, body)
				bodyJson, _ := json.Marshal(body)
				assert.Equal(t, c.expected, cast.ToString(bodyJson))
			} else {
				t.Logf("Query: %s, ES result: %v", c.q, node != nil)
			}
		})
	}
}
