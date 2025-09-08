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
	testMapping := func() map[string]string {
		m := make(map[string]string)
		m["log"] = "text"
		m["level"] = "keyword"
		m["loglevel"] = "keyword"
		m["word.key"] = "text"
		m["ms"] = "long"
		m["events.attributes.message.detail"] = "text"
		m["event_detail"] = "text"
		m["nested.key"] = "text"

		return m
		//schema.SetNestedField("events")
		//schema.SetNestedField("nested")
		//schema.SetNestedField("user")
		//
		//schema.SetFieldAlias("event_detail", "events.attributes.message.detail")

	}
	ctx := metadata.InitHashID(context.Background())
	for i, c := range []struct {
		q        string
		isPrefix bool
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
			expected: `{"query_string":{"analyze_wildcard":true,"fields":["*", "__*"],"lenient":true,"query":"\"message queue conflict\""}}`,
		},
		{
			q: `nested.key: test AND demo`,
			expected: `{
  "bool": {
    "must": [
      {
        "nested": {
          "path": "nested",
          "query": {
            "match_phrase": {
              "nested.key": {
                "query": "test"
              }
            }
          }
        }
      }, 
		{
        "query_string": {
          "analyze_wildcard": true,
          "fields": [
            "*",
            "__*"
          ],
          "lenient": true,
          "query": "demo"
        }
      }
    ]
  }
}`,
		},
		{
			q:        `sync_spaces AND -keyword AND -BKLOGAPI`,
			expected: `{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"sync_spaces"}},{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"keyword"}}}},{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"BKLOGAPI"}}}}]}}`,
		},
		{
			q: `*`,
		},
		{
			q:        `*`,
			isPrefix: true,
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
			isPrefix: true,
			expected: `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"demo","type":"phrase_prefix"}}`,
		},
		{
			q: ``,
		},
		{
			q:        "ms: \u003e500 AND \"/fs-server\" AND NOT \"heartbeat\"",
			expected: `{"bool":{"must":[{"term":{"ms":500}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"/fs-server\""}},{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"heartbeat\""}}}}]}}`,
		},
		{
			q:        `events.attributes.message.detail: "*66036*"`,
			expected: `{"nested":{"path":"events","query":{"wildcard":{"events.attributes.message.detail":{"value":"*66036*"}}}}}`,
		},
		// 测试别名
		{
			q:        `event_detail: "*66036*"`,
			expected: `{"nested":{"path":"events","query":{"wildcard":{"events.attributes.message.detail":{"value":"*66036*"}}}}}`,
		},
		{
			q:        `"/var/host/data/bcs/lib/docker/containers/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5-json.log" and level: "error" and "2_bklog.bkunify_query"`,
			expected: `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"/var/host/data/bcs/lib/docker/containers/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5-json.log\""}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"and"}},{"term":{"level":"error"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"and"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"2_bklog.bkunify_query\""}}]}}`,
		},
		{
			q:        `(loglevel: ("TRACE" OR "DEBUG" OR  "INFO " OR "WARN " OR "ERROR") AND log: ("friendsvr" AND ("game_app" OR "testOr") AND "testAnd" OR "test111")) AND "test111"`,
			expected: `{"bool":{"must":[{"bool":{"must":[{"terms":{"loglevel":["TRACE","DEBUG","INFO ","WARN ","ERROR"]}},{"bool":{"minimum_should_match":"1","should":[{"bool":{"must":[{"match_phrase":{"log":{"query":"friendsvr"}}},{"match_phrase":{"log":{"query":"game_app"}}},{"match_phrase":{"log":{"query":"testAnd"}}}]}},{"bool":{"must":[{"match_phrase":{"log":{"query":"friendsvr"}}},{"match_phrase":{"log":{"query":"testOr"}}},{"match_phrase":{"log":{"query":"testAnd"}}}]}},{"match_phrase":{"log":{"query":"test111"}}}]}}]}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"test111\""}}]}}`,
		},
		{
			q:        `loglevel: ("TRACE" AND "111" AND "DEBUG" AND "INFO" OR "SIMON" OR "222" AND "333" )`,
			expected: `{"bool":{"minimum_should_match":"1","should":[{"bool":{"must":[{"term":{"loglevel":"TRACE"}},{"term":{"loglevel":"111"}},{"term":{"loglevel":"DEBUG"}},{"term":{"loglevel":"INFO"}}]}},{"term":{"loglevel":"SIMON"}},{"bool":{"must":[{"term":{"loglevel":"222"}},{"term":{"loglevel":"333"}}]}}]}}`,
		},
		{
			q:        `loglevel: ("TRACE" OR ("DEBUG") OR  ("INFO ") OR "WARN " OR "ERROR") AND log: ("friendsvr" AND ("game_app" OR "testOr") AND "testAnd" OR "test111")`,
			expected: `{"bool":{"must":[{"terms":{"loglevel":["TRACE","DEBUG","INFO ","WARN ","ERROR"]}},{"bool":{"minimum_should_match":"1","should":[{"bool":{"must":[{"match_phrase":{"log":{"query":"friendsvr"}}},{"match_phrase":{"log":{"query":"game_app"}}},{"match_phrase":{"log":{"query":"testAnd"}}}]}},{"bool":{"must":[{"match_phrase":{"log":{"query":"friendsvr"}}},{"match_phrase":{"log":{"query":"testOr"}}},{"match_phrase":{"log":{"query":"testAnd"}}}]}},{"match_phrase":{"log":{"query":"test111"}}}]}}]}}`,
		},
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			encoder := func(str string) string {
				return str
			}

			decoder := func(str string) string {
				return str
			}
			parser := lucene_parser.NewParser(testMapping(), encoder, decoder)
			result, err := parser.Do(c.q, false)
			require.NoError(t, err)
			body, err := result.ES.Source()
			assert.Nil(t, err)
			require.NotNil(t, body)
			bodyJson, _ := json.Marshal(body)
			assert.JSONEq(t, c.expected, cast.ToString(bodyJson))
		})
	}
}
