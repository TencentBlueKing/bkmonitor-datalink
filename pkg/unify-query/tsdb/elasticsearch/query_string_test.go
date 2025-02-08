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
			expected: `{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"query":"\"quick\""}},{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"query":"\"brown\""}},{"query_string":{"analyze_wildcard":true,"query":"\"fox\""}}]}}]}}`,
		},
		{
			q:        `word.key: qu?ck`,
			expected: `{"wildcard":{"word.key":{"value":"qu?ck"}}}`,
		},
		{
			q:        "\"message queue conflict\"",
			expected: `{"query_string":{"analyze_wildcard":true,"query":"\"message queue conflict\""}}`,
		},
		{
			q:        `nested.key: test AND demo`,
			expected: `{"nested":{"path":"nested.key","query":{"bool":{"must":[{"match_phrase":{"nested.key":{"query":"test"}}},{"query_string":{"analyze_wildcard":true,"query":"\"demo\""}}]}}}}`,
		},
		{
			q:        `sync_spaces AND -keyword AND -BKLOGAPI`,
			expected: `{"query_string":{"analyze_wildcard":true,"query":"sync_spaces AND -keyword AND -BKLOGAPI"}}`,
		},
		{
			q: `*`,
		},
		{
			q: ``,
		},
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			qs := NewQueryString(c.q, func(s string) string {
				if s == "nested.key" {
					return s
				}
				return ""
			})
			query, err := qs.Parser()
			if err == nil {
				if query != nil {
					body, _ := query.Source()
					bodyJson, _ := json.Marshal(body)
					bodyString := string(bodyJson)
					assert.Equal(t, c.expected, bodyString)
				}
			} else {
				assert.Equal(t, c.err, err)
			}
		})
	}
}
