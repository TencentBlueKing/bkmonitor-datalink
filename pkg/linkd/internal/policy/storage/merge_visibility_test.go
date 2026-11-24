// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"linkd/internal/policy"
)

// 暂停测试专属索引刷新，验证合并步骤只依赖持久化 ACK/实时 GET，而不是每成员等待一次搜索刷新。
func TestElasticsearchMergeWritesProgressWithoutSearchRefresh(t *testing.T) {
	endpoint := os.Getenv("LINKD_TEST_ELASTICSEARCH_URL")
	if endpoint == "" {
		t.Skip("set LINKD_TEST_ELASTICSEARCH_URL")
	}
	s, err := Open(t.Context(), esConfig(endpoint), fmt.Sprintf("merge-visibility-%d-%d", os.Getpid(), time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, kind := range policyCollections() {
			if status, _, err := s.request(ctx, http.MethodDelete, "/"+s.table(kind), nil); err != nil || status != 200 {
				t.Errorf("cleanup owned %s: %d %v", kind, status, err)
			}
		}
	})
	for _, kind := range []string{"merge_decisions", "merge_members", "merge_relations", "merge_relation_refs"} {
		t.Run(kind, func(t *testing.T) {
			if status, _, err := s.request(t.Context(), http.MethodPut, "/"+s.table(kind)+"/_settings", []byte(`{"index":{"refresh_interval":"-1"}}`)); err != nil || status != 200 {
				t.Fatal(status, err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if err := s.Put(ctx, kind, "scope:a", "", json.RawMessage(`{"value":1}`)); err != nil {
				t.Fatalf("durable merge write waited for search refresh: %v", err)
			}
			_, version, err := s.Get(t.Context(), kind, "scope:a")
			if err != nil || version == "" {
				t.Fatal("write not immediately readable by identity", err)
			}
			if err := s.Put(ctx, kind, "scope:a", version, json.RawMessage(`{"value":2}`)); err != nil {
				t.Fatal(err)
			}
			raw, next, err := s.Get(t.Context(), kind, "scope:a")
			if err != nil || string(raw) != `{"value":2}` || next == version {
				t.Fatal("CAS/GET did not expose latest state", err)
			}
			if err := s.Put(ctx, kind, "scope:a", version, json.RawMessage(`{"value":3}`)); !errors.Is(err, policy.ErrConflict) {
				t.Fatal("stale CAS accepted", err)
			}
			rows, err := s.List(t.Context(), kind, "scope:", "", 16)
			if err != nil || len(rows) != 0 {
				t.Fatal("search should await index refresh", err)
			}
			if status, _, err := s.request(t.Context(), http.MethodPost, "/"+s.table(kind)+"/_refresh", nil); err != nil || status != 200 {
				t.Fatal(status, err)
			}
			rows, err = s.List(t.Context(), kind, "scope:", "", 16)
			if err != nil || len(rows) != 1 || string(rows[0]) != `{"value":2}` {
				t.Fatal("refreshed query differs from real-time state", err)
			}
		})
	}
}
