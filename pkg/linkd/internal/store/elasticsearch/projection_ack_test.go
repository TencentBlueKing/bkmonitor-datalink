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
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

func TestProjectionAckUsesPartialCASAndPreservesOtherPendingTargets(t *testing.T) {
	router, err := NewStaticRouter("ack")
	if err != nil {
		t.Fatal(err)
	}
	reads, writes := 0, 0
	tr := transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet {
			reads++
			if req.URL.Query().Get("realtime") != "true" || req.URL.Query().Get("_source_includes") != "bk_tenant_id,alert_id,revision,projection" {
				t.Fatal("ACK read fetched business payload", req.URL)
			}
			value := map[string]any{"found": true, "_index": "ack-alerts", "_id": documentID("tenant", "alert"), "_seq_no": reads, "_primary_term": 1, "_source": map[string]any{"bk_tenant_id": "tenant", "alert_id": "alert", "revision": 3, "projection": domain.AlertProjection{Targets: map[string]domain.ProjectionTargetState{"kac": {SourceVersion: 1, RequiredRevision: 3}, "other": {SourceVersion: 1, RequiredRevision: 3}}}}}
			raw, _ := json.Marshal(value)
			return batchHTTPResponse(200, raw), nil
		}
		writes++
		if req.URL.Query().Get("refresh") != "false" || req.URL.Query().Get("if_seq_no") == "" {
			t.Fatal("ACK lost CAS or waited for refresh")
		}
		raw, _ := io.ReadAll(req.Body)
		var body struct {
			Doc map[string]json.RawMessage `json:"doc"`
		}
		if json.Unmarshal(raw, &body) != nil || len(body.Doc) != 2 {
			t.Fatal("ACK wrote unrelated fields", string(raw))
		}
		var watermarks domain.AlertProjection
		if json.Unmarshal(body.Doc["projection"], &watermarks) != nil || watermarks.Targets["kac"].SyncedRevision != 2 || string(body.Doc["projection_work"]) != "true" {
			t.Fatal("ACK lost a pending requirement", string(raw))
		}
		if writes == 1 {
			return batchHTTPResponse(409, []byte(`{}`)), nil
		}
		return batchHTTPResponse(200, []byte(`{"_index":"ack-alerts","_id":"`+documentID("tenant", "alert")+`","_seq_no":3,"_primary_term":1,"result":"updated"}`)), nil
	})
	repo, err := New(tr, router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	ack := store.ProjectionAcknowledgment{TenantID: "tenant", AlertID: "alert", TargetID: "kac", SourceVersion: 1, Revision: 2, At: time.Now()}
	if err := repo.AcknowledgeProjection(t.Context(), ack); err != nil {
		t.Fatal(err)
	}
	if reads != 2 || writes != 2 {
		t.Fatal("conflict did not reread metadata", reads, writes)
	}
}

func TestBatchReadPreservesSourceIncludes(t *testing.T) {
	b := newTestWriteBatch(t, transportFunc(func(req *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(req.Body)
		if req.URL.Path != "/_mget" || !strings.Contains(string(raw), `"includes":["id","bk_tenant_id"]`) {
			t.Fatal("batch read discarded field selection", string(raw))
		}
		return batchHTTPResponse(200, []byte(`{"docs":[{"found":true}]}`)), nil
	}), nil)
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/tasks/_doc/id?realtime=true&_source_includes=id,bk_tenant_id", nil)
	response, err := b.Perform(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
}
