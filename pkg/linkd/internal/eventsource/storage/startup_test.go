// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package storage

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"linkd/internal/config"
)

func TestExistingStoreRemainsOpenWhileSchemaPreparationFails(t *testing.T) {
	var blocked atomic.Bool
	var calls atomic.Int64
	blocked.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if blocked.Load() {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	cfg := config.StorageConfig{Repository: config.RepositoryTypeElasticsearch, Elasticsearch: &config.ElasticsearchConfig{Addresses: []string{server.URL}, IndexPrefix: "startup-test"}}
	s, err := OpenExisting(t.Context(), cfg, "startup-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if calls.Load() != 0 {
		t.Fatal("opening an existing store performed I/O")
	}
	if err := s.EnsureSchema(t.Context()); err == nil {
		t.Fatal("read-only index was reported ready")
	}
	blocked.Store(false)
	if err := s.EnsureSchema(t.Context()); err != nil {
		t.Fatal("same store failed to recover", err)
	}
}
