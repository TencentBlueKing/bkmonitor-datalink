// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License").

package storage

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestMergeSnapshotBatchPreservesFirstValuesAndReportsPartialFailure(t *testing.T) {
	for _, kind := range []string{"merge_members", "merge_relation_refs"} {
		t.Run(kind, func(t *testing.T) { runMergeBatchPartialFailure(t, kind, false, false) })
	}
}

func TestReferenceBatchUpsertRetriesWithoutReadback(t *testing.T) {
	t.Run("partial write", func(t *testing.T) { runMergeBatchPartialFailure(t, "merge_relation_refs", true, false) })
	t.Run("response lost after write", func(t *testing.T) { runMergeBatchPartialFailure(t, "merge_relation_refs", true, true) })
}

func runMergeBatchPartialFailure(t *testing.T, kind string, replace, loseResponse bool) {
	t.Helper()
	var mu sync.Mutex
	documents := map[string]json.RawMessage{}
	reads, writes := 0, 0
	fail := true
	action := "create"
	if replace {
		action = "index"
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPut:
			_, _ = w.Write([]byte(`{}`))
		case strings.HasSuffix(r.URL.Path, "/_bulk"):
			writes++
			if r.URL.Query().Get("refresh") != "false" {
				t.Error("snapshot batch waits for refresh")
			}
			scanner := bufio.NewScanner(r.Body)
			items := []any{}
			for scanner.Scan() {
				var meta map[string]struct {
					ID string `json:"_id"`
				}
				if err := json.Unmarshal(scanner.Bytes(), &meta); err != nil {
					t.Error(err)
					return
				}
				if !scanner.Scan() {
					t.Error("missing document")
					return
				}
				var doc struct {
					Payload json.RawMessage `json:"payload"`
				}
				if err := json.Unmarshal(scanner.Bytes(), &doc); err != nil {
					t.Error(err)
					return
				}
				status := 201
				id := meta[action].ID
				if id == "" {
					t.Error("wrong bulk action")
					w.WriteHeader(500)
					return
				}
				if _, exists := documents[id]; exists && !replace {
					status = 409
				} else if fail && !loseResponse && id == "b" {
					status = 503
				} else {
					if _, exists := documents[id]; exists {
						status = 200
					}
					documents[id] = append(json.RawMessage(nil), doc.Payload...)
				}
				items = append(items, map[string]any{action: map[string]any{"_id": id, "status": status}})
			}
			if err := scanner.Err(); err != nil {
				t.Error(err)
			}
			if fail && loseResponse {
				w.WriteHeader(503)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		case strings.HasSuffix(r.URL.Path, "/_mget"):
			reads++
			if r.URL.Query().Get("realtime") != "true" {
				t.Error("snapshot batch is not realtime")
			}
			var request struct {
				IDs []string `json:"ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			items := []any{}
			for _, id := range request.IDs {
				raw, found := documents[id]
				items = append(items, map[string]any{"_id": id, "found": found, "_source": map[string]any{"payload": raw}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"docs": items})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	s, err := Open(t.Context(), esConfig(server.URL), "batch")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	write := func(input map[string]json.RawMessage) error {
		if replace {
			return s.PutReferences(t.Context(), input)
		}
		return s.CreateMany(t.Context(), kind, input)
	}
	input := map[string]json.RawMessage{"a": json.RawMessage(`{"title":"first"}`), "b": json.RawMessage(`{"title":"second"}`)}
	if err := write(input); err == nil {
		t.Fatal("partial failure hidden")
	}
	mu.Lock()
	fail = false
	mu.Unlock()
	input["a"] = json.RawMessage(`{"title":"changed"}`)
	if err := write(input); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMany(t.Context(), kind, []string{"b", "a", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]json.RawMessage{"a": json.RawMessage(`{"title":"first"}`), "b": json.RawMessage(`{"title":"second"}`)}
	if replace {
		wanted["a"] = input["a"]
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(got, wanted) || reads != 1 || writes != 2 {
		t.Fatalf("got %s reads=%d writes=%d", got, reads, writes)
	}
}

func TestMergeSnapshotBatchRejectsIncompleteAndFailedReads(t *testing.T) {
	for _, body := range []string{
		`{"docs":[]}`,
		`{"docs":[{"_id":"other","found":false}]}`,
		`{"docs":[{"_id":"a","error":{"type":"unavailable_shards_exception"}}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					_, _ = w.Write([]byte(`{}`))
					return
				}
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			s, err := Open(t.Context(), esConfig(server.URL), "batch-errors")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			if _, err := s.GetMany(t.Context(), "merge_members", []string{"a"}); err == nil {
				t.Fatal("failed/incomplete batch accepted")
			}
		})
	}
}
