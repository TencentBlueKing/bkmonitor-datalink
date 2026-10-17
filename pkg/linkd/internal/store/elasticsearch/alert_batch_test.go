// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package elasticsearchstore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

func TestExplicitAlertBulkPreservesItemCASAndPartialCompletion(t *testing.T) {
	requests := 0
	took := time.Duration(0)
	ctx := store.WithBatchPhaseObserver(t.Context(), func(_ context.Context, phase string, d time.Duration) {
		if phase == "es.bulk_took" {
			took += d
		}
	})
	transport := transportFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.Method != "POST" || req.URL.Path != "/_bulk" {
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL)
		}
		raw, _ := io.ReadAll(req.Body)
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		if len(lines) != 4 {
			t.Fatalf("explicit batch not in one request: %s", raw)
		}
		items := []any{}
		for i := 0; i < len(lines); i += 2 {
			var header map[string]map[string]any
			if err := json.Unmarshal([]byte(lines[i]), &header); err != nil {
				t.Fatal(err)
			}
			for action, meta := range header {
				if meta["if_primary_term"] != float64(2) {
					t.Fatal("missing primary term")
				}
				status := 200
				if meta["if_seq_no"] != float64(4) {
					status = 409
				}
				if action == "update" {
					var body struct {
						Doc map[string]any `json:"doc"`
					}
					if err := json.Unmarshal([]byte(lines[i+1]), &body); err != nil {
						t.Fatal(err)
					}
					if len(body.Doc) != 2 {
						t.Fatal("completion sends full document")
					}
				}
				items = append(items, map[string]any{action: map[string]any{"_id": meta["_id"], "_index": meta["_index"], "_seq_no": 5, "_primary_term": 2, "status": status}})
			}
		}
		body, _ := json.Marshal(map[string]any{"items": items, "took": 13})
		return managerJSONResponse(200, string(body)), nil
	})
	r, err := New(transport, mustStaticRouter(t), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	updates := []store.AlertSnapshotUpdate{}
	for i, id := range []string{"a", "b"} {
		a := storetest.Alert("tenant", id, "opening", id, "warning")
		version, _ := encodeVersion(versionPayload{Index: "linkd-test-alerts", DocumentID: alertDocumentID(a), SeqNo: int64(4 - i), PrimaryTerm: 2})
		next := a.Clone()
		next.UpdateAt = next.UpdateAt.Add(1)
		updates = append(updates, store.AlertSnapshotUpdate{Current: store.StoredAlert{Alert: a, Version: version}, Replacement: next})
	}
	items, err := r.CompareAndSetAlertsFromSnapshots(ctx, updates)
	if err != nil || items[0].Err != nil || !errors.Is(items[1].Err, store.ErrVersionConflict) || items[0].Alert.Revision != 2 {
		t.Fatalf("partial CAS: %+v %v", items, err)
	}
	items, err = r.CompleteAlertChanges(ctx, []store.StoredAlert{updates[0].Current, updates[1].Current}, store.MergeChangeField)
	if err != nil || items[0].Err != nil || !errors.Is(items[1].Err, store.ErrVersionConflict) || requests != 2 {
		t.Fatalf("partial completion: %+v %v", items, err)
	}
	if took != 26*time.Millisecond {
		t.Fatal("lost explicit Bulk service timing", took)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := r.CompareAndSetAlertsFromSnapshots(ctx, updates); !errors.Is(err, context.Canceled) || requests != 2 {
		t.Fatal("canceled bulk sent", err)
	}
}
