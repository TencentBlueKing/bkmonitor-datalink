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
	"net/http"
	"net/http/httptest"
	"testing"

	"linkd/internal/config"
)

func TestCapacityRejectsIncompleteCounts(t *testing.T) {
	for _, body := range []string{`{}`, `{"hits":{"total":{"value":-1,"relation":"eq"}}}`, `{"hits":{"total":{"value":0,"relation":"unknown"}}}`, `{"timed_out":true,"hits":{"total":{"value":0,"relation":"eq"}}}`, `{"_shards":{"failed":1},"hits":{"total":{"value":0,"relation":"eq"}}}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					_, _ = w.Write([]byte(`{}`))
					return
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			s, err := Open(t.Context(), config.StorageConfig{Repository: config.RepositoryTypeElasticsearch, Elasticsearch: &config.ElasticsearchConfig{Addresses: []string{server.URL}}}, "counts")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			if _, err := s.CountWork(t.Context(), "tenant", 1024); err == nil {
				t.Fatal("incomplete count admitted work")
			}
		})
	}
}
