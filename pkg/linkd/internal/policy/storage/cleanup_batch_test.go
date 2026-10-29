// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"linkd/internal/policy"
	"linkd/internal/suppressioncleanup"
)

func TestCleanup5000UsesOneMultiGetAndOneBulk(t *testing.T) {
	for _, scenario := range []string{"normal", "partial", "http_failure"} {
		t.Run(scenario, func(t *testing.T) {
			reads, writes := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/_mget":
					reads++
					var b struct {
						Docs []map[string]string `json:"docs"`
					}
					if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
						t.Fatal(err)
					}
					if len(b.Docs) != 5000 {
						t.Fatal("not a full batch", len(b.Docs))
					}
					docs := []any{}
					for _, d := range b.Docs {
						docs = append(docs, map[string]any{"_id": d["_id"], "found": false})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"docs": docs})
				case "/_bulk":
					writes++
					if r.URL.Query().Get("refresh") != "false" {
						t.Fatal(r.URL)
					}
					if scenario == "http_failure" {
						w.WriteHeader(503)
						_, _ = w.Write([]byte(`{}`))
						return
					}
					raw, err := io.ReadAll(r.Body)
					if err != nil {
						t.Fatal(err)
					}
					lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
					if len(lines) != 10000 {
						t.Fatal("per-item bulk", len(lines))
					}
					items := []any{}
					for i := 0; i < len(lines); i += 2 {
						var head map[string]map[string]any
						if json.Unmarshal([]byte(lines[i]), &head) != nil {
							t.Fatal("invalid bulk")
						}
						op := "create"
						if i >= 5000 {
							op = "index"
						}
						meta := head[op]
						if meta == nil {
							t.Fatal(head)
						}
						if op == "index" && (meta["if_seq_no"] != float64(7) || meta["if_primary_term"] != float64(2)) {
							t.Fatal(meta)
						}
						status := 201
						if scenario == "partial" && i == 2 {
							status = 409
						}
						items = append(items, map[string]any{op: map[string]any{"status": status, "_seq_no": 8, "_primary_term": 2}})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
				default:
					t.Errorf("unexpected individual request: %s", r.URL)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			s, err := OpenExisting(t.Context(), esConfig(server.URL), "batch-cleanup")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			ids := make([]string, 5000)
			docs := make([]suppressioncleanup.Document, 5000)
			for i := range docs {
				ids[i] = fmt.Sprintf("tenant:cleanup-%04d", i)
				docs[i] = suppressioncleanup.Document{ID: ids[i], Payload: json.RawMessage(`{"state":"pending"}`)}
				if i >= 2500 {
					docs[i].Version = "7:2"
				}
			}
			for _, r := range s.ReadCleanupBatch(t.Context(), ids) {
				if !errors.Is(r.Err, policy.ErrNotFound) {
					t.Fatal(r.Err)
				}
			}
			out := s.WriteCleanupBatch(t.Context(), docs)
			for i, r := range out {
				if scenario == "http_failure" {
					if r.Err == nil {
						t.Fatal("HTTP failure accepted")
					}
				} else if scenario == "partial" && i == 1 {
					if !errors.Is(r.Err, policy.ErrConflict) {
						t.Fatal(r.Err)
					}
				} else if r.Err != nil || r.Version != "8:2" {
					t.Fatal(r.Err, r.Version)
				}
			}
			if reads != 1 || writes != 1 {
				t.Fatal("not explicit bulk", reads, writes)
			}
		})
	}
}
