// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package elasticsearchstore

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

func TestCloseCAS5000OnlySendsChangedFields(t *testing.T) {
	requests, requestBytes := 0, 0
	r, err := New(transportFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.URL.Path != "/_bulk" || req.URL.Query().Get("refresh") != "false" {
			t.Fatal(req.URL)
		}
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		requestBytes += len(raw)
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		items := []any{}
		for i := 0; i < len(lines); i += 2 {
			var head map[string]map[string]any
			var body struct {
				Script struct {
					Source string `json:"source"`
					Params struct {
						Patch map[string]any `json:"patch"`
					} `json:"params"`
				} `json:"script"`
			}
			if err := json.Unmarshal([]byte(lines[i]), &head); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(lines[i+1]), &body); err != nil {
				t.Fatal(err)
			}
			meta := head["update"]
			patch := body.Script.Params.Patch
			if meta["if_seq_no"] != float64(7) || meta["if_primary_term"] != float64(2) || body.Script.Source != "ctx._source.putAll(params.patch)" || len(patch) != 12 || patch["revision"] != float64(2) || patch["status"] != "closed" {
				t.Fatal("not a conditional close patch", meta, patch)
			}
			for _, field := range []string{"content", "enrich", "extra_data", "title", "labels"} {
				if _, ok := patch[field]; ok {
					t.Fatal("payload sent in close patch", field)
				}
			}
			if patch["shield"].(map[string]any)["active"] != false || patch["merge"] != nil {
				t.Fatal("terminal fields not replaced")
			}
			items = append(items, map[string]any{"update": map[string]any{"_id": meta["_id"], "_index": meta["_index"], "_seq_no": 8, "_primary_term": 2, "status": 200}})
		}
		return jsonResponse(t, map[string]any{"items": items}), nil
	}), mustStaticRouter(t), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	updates := make([]store.AlertSnapshotUpdate, 5000)
	content := strings.Repeat("x", 64<<10)
	for i := range updates {
		a := storetest.Alert("tenant", fmt.Sprintf("alert-%04d", i), "opening", fmt.Sprint(i), "warning")
		a.Content = content
		version, _ := encodeVersion(versionPayload{Index: "linkd-test-alerts", DocumentID: alertDocumentID(a), SeqNo: 7, PrimaryTerm: 2})
		next := a.Clone()
		at := a.UpdateAt.Add(time.Hour)
		next.UpdateAt, next.EndAt, next.Status, next.EndType, next.EndReason = at, &at, domain.AlertStatusClosed, domain.AlertEndTypeUser, "done"
		next.EndOperation = &domain.AlertEndOperation{ID: "close-batch", Source: "manual", OperatorKind: domain.OperatorKindUser, OperatorID: "tester"}
		updates[i] = store.AlertSnapshotUpdate{Current: store.StoredAlert{Alert: a, Version: version}, Replacement: next}
	}
	started := time.Now()
	results, err := r.CloseAlertsFromSnapshots(t.Context(), updates)
	if err != nil || len(results) != 5000 {
		t.Fatal(len(results), err)
	}
	for _, result := range results {
		if result.Err != nil || result.Alert.Content != content {
			t.Fatal("snapshot or result changed", result.Err)
		}
	}
	if requests != 1 || requestBytes > 8<<20 {
		t.Fatal("close patch unexpectedly large", requests, requestBytes)
	}
	full, err := encodeAlertDocument(updates[0].Replacement)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("5000 close patches: requests=%d bytes=%d old_full_document_estimate=%d elapsed=%s (local transport, not ES throughput)", requests, requestBytes, len(full)*5000, time.Since(started))
}

func TestCloseReadsExcludePayloadAtEverySelectionStage(t *testing.T) {
	a := storetest.Alert("tenant", "alert", "opening", "fp", "warning")
	full, _ := encodeAlertDocument(a)
	var source map[string]any
	if err := json.Unmarshal(full, &source); err != nil {
		t.Fatal(err)
	}
	calls := 0
	r, err := New(transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if strings.HasSuffix(req.URL.Path, "/_search") {
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if fields := body["_source"].([]any); len(fields) != 1 || fields[0] != "alert_id" || body["seq_no_primary_term"] != false {
				t.Fatal(body)
			}
			return jsonResponse(t, map[string]any{"hits": map[string]any{"hits": []any{map[string]any{"_source": map[string]string{"alert_id": "alert"}}}}}), nil
		}
		includes := req.URL.Query().Get("_source_includes")
		if includes == "" || strings.Contains(includes, "content") || strings.Contains(includes, ",enrich,") || strings.Contains(includes, "extra_data,") {
			t.Fatal("full payload requested", includes)
		}
		if calls == 2 && len(strings.Split(includes, ",")) != 5 {
			t.Fatal("identity read is not narrow", includes)
		}
		selected := map[string]any{}
		for _, name := range strings.Split(includes, ",") {
			if value, found := source[name]; found {
				selected[name] = value
			}
		}
		return jsonResponse(t, map[string]any{"docs": []any{map[string]any{"_index": "linkd-test-alerts", "_id": alertDocumentID(a), "_seq_no": 7, "_primary_term": 2, "found": true, "_source": selected}}}), nil
	}), mustStaticRouter(t), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	page, err := r.SearchAlerts(t.Context(), store.AlertSearchRequest{TenantID: "tenant", Limit: 5000, IDsOnly: true})
	if err != nil || len(page.Items) != 1 || page.Items[0].AlertID != "alert" {
		t.Fatal(page, err)
	}
	identity, err := r.GetAlertIdentities(t.Context(), "tenant", []string{"alert"})
	if err != nil || len(identity.Alerts) != 1 || identity.Alerts[0].Alert.Fingerprint != "fp" {
		t.Fatal(identity, err)
	}
	states, err := r.GetAlertCloseStates(t.Context(), "tenant", []string{"alert"})
	if err != nil || len(states.Alerts) != 1 || states.Alerts[0].Version.IsZero() || len(states.Alerts[0].Alert.Enrich) != 0 {
		t.Fatal(states, err)
	}
}
