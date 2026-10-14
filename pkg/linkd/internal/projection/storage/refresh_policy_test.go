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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"linkd/internal/projection"
	es "linkd/internal/store/elasticsearch"
)

func TestProgressCASDoesNotWaitForRefresh(t *testing.T) {
	task := testTask(t, "tenant")
	var doc esDocument
	seq := int64(0)
	creates := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			if r.URL.Query().Get("op_type") == "create" {
				creates++
				expected := "false"
				if r.URL.Query().Get("refresh") != expected {
					t.Error("wrong create visibility boundary")
				}
			} else if r.URL.Query().Get("refresh") != "" {
				t.Error("progress write waited for refresh")
			}
			if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
				t.Error(err)
			}
			seq++
		}
		if r.Method == http.MethodPost {
			if r.URL.Query().Get("refresh") != "false" {
				t.Error("progress write waited for refresh")
			}
			var patch struct {
				Script struct {
					Params struct {
						Progress projection.Progress `json:"progress"`
						Work     bool                `json:"work"`
					} `json:"params"`
				} `json:"script"`
			}
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				t.Error(err)
			}
			var payload projection.Task
			if err := json.Unmarshal(doc.Payload, &payload); err != nil {
				t.Error(err)
			}
			frozen, restoreErr := projection.RestoreSnapshot(payload.Request.Alert)
			if restoreErr != nil || string(frozen) != string(task.Request.Alert) {
				t.Error("progress update modified frozen snapshot")
			}
			payload.Progress = patch.Script.Params.Progress
			doc.Work = patch.Script.Params.Work
			doc.Payload, _ = json.Marshal(payload)
			seq++
		}
		_ = json.NewEncoder(w).Encode(esHit{Index: "tasks", ID: task.Request.TenantID + "-" + task.ID, Seq: seq, Term: 1, Source: doc})
	}))
	defer server.Close()
	transport, err := es.NewHTTPTransport(es.HTTPTransportConfig{Addresses: []string{server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	s := &Store{transport: transport, index: "tasks"}
	row, err := s.Put(t.Context(), task, "")
	if err != nil {
		t.Fatal(err)
	}
	next := row.Task.Clone()
	next.Progress.State = "sending"
	next.Progress.Attempts = 1
	next.Progress.TotalAttempts = 1
	next.Progress.DueAt = nil
	until := next.Progress.UpdatedAt.Add(30 * time.Second)
	next.Progress.LeaseUntil = &until
	if _, err = s.Put(t.Context(), next, row.Version); err != nil {
		t.Fatal(err)
	}
	if creates != 1 {
		t.Fatal("create-only identity lost")
	}
}

func TestTaskReadDoesNotRecomputeSnapshotDigest(t *testing.T) {
	for _, storedAsString := range []bool{false, true} {
		task := testTask(t, "tenant")
		task.Request.ContentHash = strings.Repeat("0", 64)
		raw, err := json.Marshal(task)
		if err != nil {
			t.Fatal(err)
		}
		if storedAsString {
			raw, err = projection.PreserveSnapshot(raw, "request")
			if err != nil {
				t.Fatal(err)
			}
		}
		row, err := decode(raw, task.Request.TenantID, task.ID, "1:1", true)
		if err != nil || row.Task.Request.ContentHash != task.Request.ContentHash {
			t.Fatal("stored hash rejected or replaced", err)
		}
		if _, err := decode(raw, "other", task.ID, "1:1", true); err == nil {
			t.Fatal("tenant isolation lost")
		}
		if _, err := decode(raw, task.Request.TenantID, task.ID, "1:1", false); err == nil {
			t.Fatal("work/state consistency lost")
		}
	}
}
