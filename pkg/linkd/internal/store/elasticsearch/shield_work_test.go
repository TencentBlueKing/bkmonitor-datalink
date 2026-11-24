// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package elasticsearchstore

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"linkd/internal/store"
)

func TestShieldWorkQueryFiltersDueBeforePaging(t *testing.T) {
	at := time.Date(2026, 10, 14, 1, 2, 3, 123456789, time.UTC)
	calls := 0
	repo, err := New(transportFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["size"] != float64(17) {
			t.Fatal("unexpected page limit", body["size"])
		}
		filters := body["query"].(map[string]any)["bool"].(map[string]any)["filter"].([]any)
		branches := filters[0].(map[string]any)["bool"].(map[string]any)["should"].([]any)
		if len(branches) != 2 {
			t.Fatal("pending changes and due shields must both be queried")
		}
		pending := branches[0].(map[string]any)["exists"].(map[string]any)["field"]
		if pending != "policy_change.operation_id" {
			t.Fatal("pending output was excluded")
		}
		due := branches[1].(map[string]any)["bool"].(map[string]any)["filter"].([]any)
		if due[0].(map[string]any)["term"].(map[string]any)["shield.active"] != true {
			t.Fatal("inactive shield included")
		}
		if due[1].(map[string]any)["range"].(map[string]any)["shield.next_check_at"].(map[string]any)["lte"] != at.Format(time.RFC3339Nano) {
			t.Fatal("due cutoff lost nanoseconds")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"timed_out":false,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"hits":[]}}`))}, nil
	}), mustStaticRouter(t), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	page, err := repo.ListShieldWork(t.Context(), store.ShieldWorkCursor{}, at, 16)
	if err != nil || len(page.Alerts) != 0 || calls != 1 {
		t.Fatal("query failed", page, calls, err)
	}
}
