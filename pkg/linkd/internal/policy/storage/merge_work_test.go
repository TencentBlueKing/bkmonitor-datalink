// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License").

package storage

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestMergeWorkValueIncludesUnfinishedCleanup(t *testing.T) {
	for _, test := range []struct {
		kind, raw string
		want      bool
	}{
		{"merge_decisions", `{"progress":{"phase":"completed","window_finished":true}}`, false},
		{"merge_decisions", `{"progress":{"phase":"completed"}}`, true},
		{"merge_decisions", `{"progress":{"phase":"prepared"}}`, true},
		{"merge_relations", `{"state":"ended"}`, false},
		{"merge_relations", `{"state":"ending"}`, true},
		{"merge_relations", `{"state":"ready"}`, true},
	} {
		got, err := mergeWorkValue(test.kind, json.RawMessage(test.raw))
		if err != nil || got != test.want {
			t.Fatalf("%s: got=%v err=%v", test.raw, got, err)
		}
	}
}

func TestMergeWorkBackfillsLegacyRowsThenStopsReadingHistory(t *testing.T) {
	var mu sync.Mutex
	legacyReads, pendingReads, mappings := 0, 0, 0
	work := map[string]bool{}
	payloads := map[string]json.RawMessage{"ended": json.RawMessage(`{"state":"ended"}`), "active": json.RawMessage(`{"state":"ready"}`)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/_mapping"):
			mappings++
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPut:
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"type":"resource_already_exists_exception"}}`))
		case strings.HasSuffix(r.URL.Path, "/_search"):
			var query map[string]any
			if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
				t.Error(err)
				return
			}
			raw, _ := json.Marshal(query)
			legacy := strings.Contains(string(raw), "must_not")
			if legacy {
				legacyReads++
			} else {
				pendingReads++
				if !strings.Contains(string(raw), `"work":true`) {
					t.Error("pending query reads history")
				}
			}
			hits := []any{}
			for _, id := range []string{"active", "ended"} {
				value, indexed := work[id]
				if (legacy && indexed) || (!legacy && (!indexed || !value)) {
					continue
				}
				hits = append(hits, map[string]any{"_id": id, "_seq_no": 7, "_primary_term": 1, "_source": map[string]any{"id": id, "payload": payloads[id]}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"hits": map[string]any{"hits": hits}})
		case strings.HasSuffix(r.URL.Path, "/_bulk"):
			scanner := bufio.NewScanner(r.Body)
			items := []any{}
			for scanner.Scan() {
				var meta struct {
					Update struct {
						ID   string `json:"_id"`
						Seq  int64  `json:"if_seq_no"`
						Term int64  `json:"if_primary_term"`
					} `json:"update"`
				}
				if err := json.Unmarshal(scanner.Bytes(), &meta); err != nil {
					t.Error(err)
					return
				}
				if meta.Update.Seq != 7 || meta.Update.Term != 1 {
					t.Error("migration ignores concurrent version")
				}
				if !scanner.Scan() {
					t.Error("missing update")
					return
				}
				var value struct {
					Doc struct {
						Work bool `json:"work"`
					} `json:"doc"`
				}
				if err := json.Unmarshal(scanner.Bytes(), &value); err != nil {
					t.Error(err)
					return
				}
				work[meta.Update.ID] = value.Doc.Work
				items = append(items, map[string]any{"update": map[string]any{"status": 200}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	s, err := Open(t.Context(), esConfig(server.URL), "legacy")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for range 3 {
		rows, err := s.ListMergeWork(t.Context(), "merge_relations", "", 16)
		if err != nil || len(rows) != 1 || string(rows[0]) != `{"state":"ready"}` {
			t.Fatal("legacy active row lost or history returned", rows, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if legacyReads != 2 || pendingReads != 3 || mappings != 2 || len(payloads) != 2 {
		t.Fatalf("legacy=%d pending=%d mappings=%d", legacyReads, pendingReads, mappings)
	}
}
