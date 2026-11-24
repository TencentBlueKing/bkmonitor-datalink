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
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/dynamicconfig"
	"linkd/internal/runtimeconfig"
)

func testRecord() dynamicconfig.Record {
	snapshot, _ := (runtimeconfig.Snapshot{Enabled: true, NativeNames: true, Severity: config.DefaultSeverityConfig()}).Normalize()
	return dynamicconfig.Record{SchemaVersion: 1, Deployment: "test", Item: "severity", SourceIdentity: "test-identity", PersistedAt: time.Now().UTC(), Snapshot: snapshot}
}

func TestElasticsearchSnapshotCASAndRealtimeRead(t *testing.T) {
	var document json.RawMessage
	version := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !strings.Contains(r.URL.Path, "/_doc/") {
			_, _ = w.Write([]byte(`{"acknowledged":true}`))
			return
		}
		if r.Method == http.MethodGet {
			if version == 0 {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"_seq_no": version, "_primary_term": 1, "_source": json.RawMessage(document)})
			return
		}
		if (r.URL.Query().Get("op_type") == "create" && version > 0) || (r.URL.Query().Has("if_seq_no") && r.URL.Query().Get("if_seq_no") != fmt.Sprint(version)) {
			w.WriteHeader(409)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&document); err != nil {
			t.Error(err)
		}
		version++
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	s, err := New(config.StorageConfig{Repository: config.RepositoryTypeElasticsearch, Elasticsearch: &config.ElasticsearchConfig{Addresses: []string{server.URL}, IndexPrefix: "unit"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	if _, _, err = s.Load(ctx, "key"); !errors.Is(err, dynamicconfig.ErrNotFound) {
		t.Fatal("missing record not distinguished")
	}
	r := testRecord()
	if err = s.Save(ctx, "key", "", r); err != nil {
		t.Fatal(err)
	}
	saved, token, err := s.Load(ctx, "key")
	if err != nil || saved.Snapshot.Digest != r.Snapshot.Digest || token != "1:1" {
		t.Fatalf("load=%+v token=%s err=%v", saved, token, err)
	}
	if err = s.Save(ctx, "key", "", r); !errors.Is(err, dynamicconfig.ErrConflict) {
		t.Fatal("create overwrote snapshot")
	}
	if err = s.Save(ctx, "key", token, r); err != nil {
		t.Fatal(err)
	}
	if err = s.Save(ctx, "key", token, r); !errors.Is(err, dynamicconfig.ErrConflict) {
		t.Fatal("stale CAS overwrote snapshot")
	}
}
