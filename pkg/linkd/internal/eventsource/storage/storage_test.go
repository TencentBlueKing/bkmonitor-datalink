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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/eventsource"
)

func esConfig(endpoint string) config.StorageConfig {
	return config.StorageConfig{Repository: config.RepositoryTypeElasticsearch, Elasticsearch: &config.ElasticsearchConfig{Addresses: []string{endpoint}}}
}

func TestSourceListRejectsIncompleteSearch(t *testing.T) {
	for _, body := range []string{`{"timed_out":true,"hits":{"hits":[]}}`, `{"_shards":{"failed":1},"hits":{"hits":[]}}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodPut {
				_, _ = w.Write([]byte(`{}`))
				return
			}
			_, _ = w.Write([]byte(body))
		}))
		s, err := Open(t.Context(), esConfig(server.URL), "partial-test")
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		_, err = s.List(t.Context(), "records", "", 10)
		_ = s.Close()
		server.Close()
		if err == nil {
			t.Fatal("partial search accepted")
		}
	}
}

// TestElasticsearchSourceContract 仅在显式配置测试 ES 后创建独立部署作用域的索引。
func TestElasticsearchSourceContract(t *testing.T) {
	endpoint := os.Getenv("LINKD_TEST_ELASTICSEARCH_URL")
	if endpoint == "" {
		t.Skip("set LINKD_TEST_ELASTICSEARCH_URL to run source storage contract")
	}
	s, err := Open(t.Context(), esConfig(endpoint), fmt.Sprintf("compat-%d-%d", os.Getpid(), time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	defer func() {
		for _, kind := range []string{"records", "releases"} {
			if _, _, err := s.request(t.Context(), http.MethodDelete, "/"+s.table(kind), nil); err != nil {
				t.Error(err)
			}
		}
	}()
	for _, kind := range []string{"records", "releases"} {
		if err := s.Put(t.Context(), kind, "a", "", json.RawMessage(`{"value":1}`)); err != nil {
			t.Fatal(err)
		}
		if err := s.Put(t.Context(), kind, "a", "", json.RawMessage(`{"value":1}`)); !errors.Is(err, eventsource.ErrConflict) {
			t.Fatalf("create conflict=%v", err)
		}
		_, version, err := s.Get(t.Context(), kind, "a")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Put(t.Context(), kind, "a", version, json.RawMessage(`{"value":2}`)); err != nil {
			t.Fatal(err)
		}
		if err := s.Put(t.Context(), kind, "a", version, json.RawMessage(`{"value":3}`)); !errors.Is(err, eventsource.ErrConflict) {
			t.Fatalf("CAS conflict=%v", err)
		}
		rows, err := s.List(t.Context(), kind, "", 1)
		if err != nil || len(rows) != 1 {
			t.Fatalf("rows=%v err=%v", rows, err)
		}
		rows, err = s.List(t.Context(), kind, "a", 1)
		if err != nil || len(rows) != 0 {
			t.Fatalf("next rows=%v err=%v", rows, err)
		}
		if _, _, err := s.Get(t.Context(), kind, "missing"); !errors.Is(err, eventsource.ErrNotFound) {
			t.Fatalf("missing=%v", err)
		}
	}
}
