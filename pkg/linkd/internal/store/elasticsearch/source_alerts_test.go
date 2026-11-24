// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available under the MIT License.

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

func TestSourceAlertSearchScopesAndRejectsIncompleteResults(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"success", "cross-tenant", "wrong-source", "wrong-status", "timeout", "failed-shards", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			router, err := NewStaticRouter("source-test")
			if err != nil {
				t.Fatal(err)
			}
			a := storetest.Alert("tenant", "a", "event", "fingerprint", "warning")
			a.EventSourceID, a.SourceEventID = "source", "A123"
			if mode == "cross-tenant" {
				a.BKTenantID = "other"
			}
			if mode == "wrong-source" {
				a.EventSourceID = "other"
			}
			if mode == "wrong-status" {
				a.Status = domain.AlertStatusRecovered
				at := a.UpdateAt
				a.EndAt, a.EndType, a.EndReason = &at, domain.AlertEndTypeSource, "resolved"
			}
			encoded, err := encodeAlertDocument(a)
			if err != nil {
				t.Fatal(err)
			}
			hit := map[string]any{"_index": "source-test-alerts", "_id": alertDocumentID(a), "_seq_no": 1, "_primary_term": 1, "_source": json.RawMessage(encoded)}
			hits := []any{hit}
			if mode == "oversized" {
				hits = []any{hit, hit, hit}
			}
			repository, err := New(transportFunc(func(request *http.Request) (*http.Response, error) {
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Fatal(err)
				}
				for _, field := range []string{`"bk_tenant_id":"tenant"`, `"event_source_id":"source"`, `"status":"active"`, `"collapse":{"field":"alert_id"}`} {
					if !strings.Contains(string(body), field) {
						t.Fatalf("missing scope %s: %s", field, body)
					}
				}
				failed := 0
				if mode == "failed-shards" {
					failed = 1
				}
				return jsonResponse(t, map[string]any{"timed_out": mode == "timeout", "_shards": map[string]any{"failed": failed}, "hits": map[string]any{"hits": hits}}), nil
			}), router, DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			page, err := repository.ListSourceAlerts(t.Context(), store.SourceAlertQuery{TenantID: "tenant", EventSourceID: "source", Status: domain.AlertStatusActive, Limit: 1})
			if mode == "success" {
				if err != nil || len(page.Alerts) != 1 || page.Alerts[0].Alert.SourceEventID != "A123" {
					t.Fatal(page, err)
				}
			} else if err == nil || len(page.Alerts) != 0 {
				t.Fatal("invalid/partial candidate page accepted", page, err)
			}
		})
	}
}
