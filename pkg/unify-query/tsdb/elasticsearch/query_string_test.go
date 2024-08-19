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

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestQsToDsl(t *testing.T) {
	mock.Init()

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
			expected: `{"bool":{"must":[{"query_string":{"query":"quick"}},{"bool":{"must":[{"query_string":{"query":"brown"}},{"query_string":{"query":"fox"}}]}}]}}`,
		},
		{
			q:        `quick AND brown`,
			expected: `{"bool":{"must":[{"query_string":{"query":"quick"}},{"query_string":{"query":"brown"}}]}}`,
		},
		{
			q:        `age:[18 TO 30]`,
			expected: `{"range":{"age":{"from":"18","include_lower":true,"include_upper":true,"to":"30"}}}`,
		},
		{
			q:        `qu?ck br*wn`,
			expected: `{"bool":{"must":[{"query_string":{"query":"qu?ck"}},{"query_string":{"query":"br*wn"}}]}}`,
		},
		{
			q:        `word.key: qu?ck`,
			expected: `{"wildcard":{"word.key":{"value":"qu?ck"}}}`,
		},
		{
			q:        `quick OR brown AND fox`,
			expected: `{"bool":{"should":[{"query_string":{"query":"quick"}},{"bool":{"must":[{"query_string":{"query":"brown"}},{"query_string":{"query":"fox"}}]}}]}}`,
		},
		{
			q:        `(key: quick OR key: brown) AND demo: fox`,
			expected: `{"bool":{"must":[{"bool":{"should":[{"match_phrase":{"key":{"query":"quick"}}},{"match_phrase":{"key":{"query":"brown"}}}]}},{"match_phrase":{"demo":{"query":"fox"}}}]}}`,
		},
		{
			q:        `nested.key:quick`,
			expected: `{"nested":{"path":"nested","query":{"match_phrase":{"nested.key":{"query":"quick"}}}}}`,
		},
		{
			q:        `title:quick`,
			expected: `{"match_phrase":{"title":{"query":"quick"}}}`,
		},
		{
			q:        `log: /data/bkee/bknodeman/nodeman/apps/backend/subscription/tasks.py`,
			expected: `{"match_phrase":{"log":{"query":"/data/bkee/bknodeman/nodeman/apps/backend/subscription/tasks.py"}}}`,
		},
		{
			q:        `"1642903" AND NOT "get_proc_status_v2" AND NOT "list_service_instance_detail" AND NOT "list_hosts_without_biz"`,
			expected: `{"bool":{"must":[{"query_string":{"query":"1642903"}},{"bool":{"must":[{"bool":{"must_not":{"query_string":{"query":"get_proc_status_v2"}}}},{"bool":{"must":[{"bool":{"must_not":{"query_string":{"query":"list_service_instance_detail"}}}},{"bool":{"must_not":{"query_string":{"query":"list_hosts_without_biz"}}}}]}}]}}]}}`,
		},
		{
			q:   `"1642903" AND OR NOT "get_proc_status_v2"`,
			err: fmt.Errorf("syntax error: unexpected tOR"),
		},
		{
			q: `sync_spaces AND -keyword AND -BKLOGAPI`,
		},
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			qs := NewQueryString(c.q, func(s string) string {
				if s == "nested.key" {
					return "nested"
				}
				return ""
			})
			query, err := qs.Parser()
			if err == nil {
				body, _ := query.Source()
				bodyJson, _ := json.Marshal(body)
				bodyString := string(bodyJson)
				assert.Equal(t, c.expected, bodyString)
			} else {
				assert.Equal(t, c.err, err)
			}
		})
	}
}
