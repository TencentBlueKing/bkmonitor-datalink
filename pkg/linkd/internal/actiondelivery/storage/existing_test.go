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
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"linkd/internal/actiondelivery"
	"linkd/internal/config"
)

func TestOpenExistingDoesNotCreateMissingActionIndex(t *testing.T) {
	var requests, writes atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	store, err := OpenExisting(t.Context(), config.StorageConfig{Repository: config.RepositoryTypeElasticsearch, Elasticsearch: &config.ElasticsearchConfig{Addresses: []string{server.URL}}}, "record-only")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if requests.Load() != 0 {
		t.Fatal("opening recorder initialized schema")
	}
	if _, err := store.Get(t.Context(), "tenant", strings.Repeat("a", 64)); !errors.Is(err, actiondelivery.ErrNotFound) {
		t.Fatal("missing index not propagated", err)
	}
	if writes.Load() != 0 || requests.Load() != 1 {
		t.Fatal("record-only connection attempted initialization")
	}
}
