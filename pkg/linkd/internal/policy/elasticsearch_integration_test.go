// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 此测试直接复制 KAC 的 analyzer/mapping 与 construct_and_dsl 字段选择，不用内存模拟器作为真值。
func TestKACElasticsearchConditionBehaviorComparison(t *testing.T) {
	endpoint := os.Getenv("LINKD_TEST_ELASTICSEARCH_URL")
	if endpoint == "" {
		t.Skip("set LINKD_TEST_ELASTICSEARCH_URL for KAC ES condition comparison")
	}
	index := "linkd-policy-comparison-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	client := &http.Client{Timeout: 10 * time.Second}
	request := func(ctx context.Context, method, path string, body any, out any) error {
		var encoded []byte
		var err error
		if body != nil {
			encoded, err = json.Marshal(body)
		}
		if err != nil {
			return err
		}
		//nolint:gosec // G704: endpoint 仅来自显式集成测试环境；请求只操作本次生成的隔离索引。
		req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(endpoint, "/")+path, bytes.NewReader(encoded))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		if key := os.Getenv("LINKD_TEST_ELASTICSEARCH_API_KEY"); key != "" {
			req.Header.Set("Authorization", "ApiKey "+key)
		}
		//nolint:gosec // G704: 复用上面的显式测试 ES 地址，不消费远端提供的 URL。
		response, err := client.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = response.Body.Close() }()
		data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
		if err != nil {
			return err
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return fmt.Errorf("comparison HTTP %d: %s", response.StatusCode, data)
		}
		if out != nil {
			return json.Unmarshal(data, out)
		}
		return nil
	}
	mapping := map[string]any{"settings": map[string]any{"number_of_shards": 1, "number_of_replicas": 0, "analysis": map[string]any{
		"char_filter": map[string]any{"kac_chars": map[string]any{"type": "pattern_replace", "pattern": "(.+?)", "replacement": "$1 "}},
		"analyzer":    map[string]any{"kac_text": map[string]any{"type": "custom", "char_filter": []string{"kac_chars"}, "tokenizer": "whitespace", "filter": []string{"lowercase"}}},
	}}, "mappings": map[string]any{"properties": map[string]any{
		"name":      map[string]any{"type": "text", "analyzer": "kac_text", "fields": map[string]any{"keyword": map[string]any{"type": "keyword", "ignore_above": 8191}}},
		"source_id": map[string]any{"type": "keyword"}, "bk_biz_id": map[string]any{"type": "integer"},
		"bk_tenant_id":  map[string]any{"type": "keyword"},
		"model_id":      map[string]any{"type": "text", "fields": map[string]any{"keyword": map[string]any{"type": "keyword"}}},
		"model_inst_id": map[string]any{"type": "text", "fields": map[string]any{"keyword": map[string]any{"type": "keyword"}}},
	}}}
	if err := request(t.Context(), http.MethodPut, "/"+index, mapping, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := request(ctx, http.MethodDelete, "/"+index, nil, nil); err != nil {
			t.Error(err)
		}
	})
	fixtures := []map[string]any{
		{"name": "CPU 使用率 过高", "source_id": "CPU", "bk_biz_id": 2},
		{"name": "C  P\tU使用率过高", "source_id": false, "bk_biz_id": 0},
		{"name": []any{"CPU", "数据库"}, "source_id": 12},
		{"name": nil}, {"name": ""}, {},
		{"name": "a\u0085b"}, {"name": "a\u00a0b"}, {"name": "a\nb"},
		{"name": "İΣẞ😀"}, {"name": strings.Repeat("😀", 4096)},
	}
	for i, doc := range fixtures {
		if err := request(t.Context(), http.MethodPut, "/"+index+"/_doc/"+strconv.Itoa(i), doc, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := request(t.Context(), http.MethodPost, "/"+index+"/_refresh", nil, nil); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		operator, field string
		value           any
	}{
		{"term", "name", "CPU 使用率 过高"}, {"term", "name", "cpu 使用率 过高"},
		{"term", "name", ""}, {"term", "name", strings.Repeat("😀", 4096)},
		{"terms", "name", []any{"CPU"}}, {"terms", "name", []any{"c", "数"}},
		{"wildcard", "name", "cpu使用率"}, {"wildcard", "name", "使用 率"}, {"wildcard", "name", "CPU数据库"}, {"wildcard", "name", " "},
		{"wildcard", "name", "a\u0085b"}, {"wildcard", "name", "a\u00a0b"}, {"wildcard", "name", "ab"}, {"wildcard", "name", "iσß😀"},
		{"regexp", "name", "CPU.*"}, {"regexp", "name", "a.b"}, {"regexp", "name", "(CPU|数据库)"}, {"regexp", "name", "[A-Z]+"},
		{"must_not_term", "name", "CPU"}, {"must_not_wildcard", "name", "数据库"},
		{"term", "bk_biz_id", "0"}, {"terms", "bk_biz_id", []any{0, 2}}, {"wildcard", "bk_biz_id", "2"},
		{"term", "source_id", false}, {"term", "source_id", 12}, {"wildcard", "source_id", "cpu"},
	}
	for _, tc := range cases {
		t.Run(tc.operator+"/"+tc.field, func(t *testing.T) {
			compiled, err := CompileExpression(expressionJSON(tc.operator, tc.field, tc.value), KACFields())
			if err != nil {
				t.Fatal(err)
			}
			field := tc.field
			operator := strings.TrimPrefix(tc.operator, "must_not_")
			value := tc.value
			if field == "name" && (operator == "term" || operator == "regexp") {
				field += ".keyword"
			}
			if operator == "term" {
				operator = "terms"
				if _, ok := value.([]any); !ok {
					value = []any{value}
				}
			}
			if operator == "wildcard" {
				operator = "match_phrase"
			}
			query := map[string]any{operator: map[string]any{field: value}}
			if strings.HasPrefix(tc.operator, "must_not_") {
				query = map[string]any{"bool": map[string]any{"must_not": query}}
			}
			var response struct {
				TimedOut bool `json:"timed_out"`
				Shards   struct {
					Failed int `json:"failed"`
				} `json:"_shards"`
				Hits struct {
					Hits []struct {
						ID string `json:"_id"`
					} `json:"hits"`
				} `json:"hits"`
			}
			if err := request(t.Context(), http.MethodPost, "/"+index+"/_search", map[string]any{"size": 100, "query": query}, &response); err != nil {
				t.Fatal(err)
			}
			if response.TimedOut || response.Shards.Failed > 0 {
				t.Fatal("comparison returned partial results")
			}
			expected := map[string]bool{}
			for _, hit := range response.Hits.Hits {
				expected[hit.ID] = true
			}
			for i, doc := range fixtures {
				result, err := compiled.Match(t.Context(), Fields{Values: doc})
				if err != nil { // 超过正则输入预算的值明确不可评估，不假装与 ES 一致。
					if tc.operator == "regexp" && i == 10 {
						continue
					}
					t.Fatalf("case=%+v doc=%d: %v", tc, i, err)
				}
				if result.Matched != expected[strconv.Itoa(i)] {
					t.Errorf("case=%+v doc=%d Go=%v ES=%v", tc, i, result.Matched, expected[strconv.Itoa(i)])
				}
			}
		})
	}
	t.Run("dependency_child_target_scope", func(t *testing.T) {
		// 复现 KAC policy2dsl 的 canonical 目标 AND 条件。这里只证明旧查询实际排除了子实例，
		// Linkd 已确认仅用该集合限制主候选，子候选按业务范围、rely_policy 与关系条件匹配。
		for _, id := range []string{"101", "102"} {
			doc := map[string]any{"bk_tenant_id": "scope-tenant", "source_id": "scope-child", "model_id": "cw-Host", "model_inst_id": id}
			if id == "101" {
				doc["source_id"] = "scope-main"
			}
			if err := request(t.Context(), http.MethodPut, "/"+index+"/_doc/scope-"+id, doc, nil); err != nil {
				t.Fatal(err)
			}
		}
		if err := request(t.Context(), http.MethodPost, "/"+index+"/_refresh", nil, nil); err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{"", "101", "102"} {
			filters := []any{map[string]any{"term": map[string]any{"bk_tenant_id": "scope-tenant"}}}
			if target != "" {
				filters = append(filters, map[string]any{"bool": map[string]any{"should": []any{map[string]any{"bool": map[string]any{"must": []any{
					map[string]any{"term": map[string]any{"model_id.keyword": "cw-Host"}},
					map[string]any{"term": map[string]any{"model_inst_id.keyword": target}},
				}}}}, "minimum_should_match": 1}})
			}
			query := map[string]any{"bool": map[string]any{"must": []any{map[string]any{"term": map[string]any{"source_id": "scope-child"}}}, "filter": filters}}
			var result struct {
				TimedOut bool `json:"timed_out"`
				Shards   struct {
					Failed int `json:"failed"`
				} `json:"_shards"`
				Hits struct {
					Hits []struct {
						ID string `json:"_id"`
					} `json:"hits"`
				} `json:"hits"`
			}
			if err := request(t.Context(), http.MethodPost, "/"+index+"/_search", map[string]any{"size": 10, "query": query}, &result); err != nil {
				t.Fatal(err)
			}
			want := 1
			if target == "101" {
				want = 0
			}
			if result.TimedOut || result.Shards.Failed != 0 || len(result.Hits.Hits) != want || (want == 1 && result.Hits.Hits[0].ID != "scope-102") {
				t.Fatalf("target=%s child matches=%+v", target, result)
			}
		}
	})
}
