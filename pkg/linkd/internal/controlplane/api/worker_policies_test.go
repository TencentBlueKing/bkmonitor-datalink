// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"linkd/internal/config"
	"linkd/internal/eventsource"
	"linkd/internal/policy"
	"linkd/internal/taskdispatch"
)

type workerPolicyController struct{ task taskdispatch.Task }

func (c *workerPolicyController) Snapshot(context.Context) (taskdispatch.State, error) {
	return taskdispatch.State{Tasks: map[string]taskdispatch.Task{"task": c.task}}, nil
}

func (*workerPolicyController) Beat(context.Context, taskdispatch.Heartbeat) ([]taskdispatch.Task, error) {
	return nil, nil
}

func TestWorkerPolicyReadsAssignedTenantAndOnlyPublishedSpec(t *testing.T) {
	docs := &policyDocuments{rows: map[string]policyDocument{}}
	service := policy.NewService(docs)
	var request policy.ApplyRequest
	if err := json.Unmarshal([]byte(policyRequestBody()), &request); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	for key, doc := range docs.rows {
		if strings.HasPrefix(key, "records/") {
			var record policy.Record
			if err := json.Unmarshal(doc.raw, &record); err != nil {
				t.Fatal(err)
			}
			record.Pending = &policy.Release{Spec: json.RawMessage(`{"name":"unpublished-secret"}`)}
			record.Revision = 2
			doc.raw, _ = json.Marshal(record)
			docs.rows[key] = doc
		}
	}
	source := eventsource.Release{ID: "source", Version: 1, Spec: config.EventSource{EventSourceID: "source", RelatedTenantID: "tenant"}}
	sources := eventsource.New(publishedDocuments{release: source}, config.DefaultSeverityConfig())
	controller := &workerPolicyController{task: taskdispatch.Task{Source: "source", Version: 1, Worker: "w", Role: "lifecycle", Phase: "running"}}
	handler := (&API{Policies: service, Sources: sources, Controller: controller, Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}, WorkerToken: "worker"}}).Handler()
	for _, tc := range []struct {
		name, path, worker, token, role, phase string
		code                                   int
	}{
		{"assigned", "/internal/policies?task=task&bk_tenant_id=tenant&type=suppression", "w", "worker", "lifecycle", "running", 200},
		{"release", "/internal/policies/suppression/p1/releases/1?task=task&bk_tenant_id=tenant", "w", "worker", "lifecycle", "running", 200},
		{"other tenant", "/internal/policies?task=task&bk_tenant_id=other&type=suppression", "w", "worker", "lifecycle", "running", 403},
		{"wrong worker", "/internal/policies?task=task&bk_tenant_id=tenant&type=suppression", "other", "worker", "lifecycle", "running", 403},
		{"cleaner", "/internal/policies?task=task&bk_tenant_id=tenant&type=suppression", "w", "worker", "cleaner", "running", 403},
		{"stopped", "/internal/policies?task=task&bk_tenant_id=tenant&type=suppression", "w", "worker", "lifecycle", "stopped", 403},
		{"missing assignment", "/internal/policies?bk_tenant_id=tenant&type=suppression", "w", "worker", "lifecycle", "running", 403},
		{"missing tenant", "/internal/policies?task=task&type=suppression", "w", "worker", "lifecycle", "running", 400},
		{"large page", "/internal/policies?task=task&bk_tenant_id=tenant&type=suppression&limit=4", "w", "worker", "lifecycle", "running", 400},
		{"wrong token", "/internal/policies?task=task&bk_tenant_id=tenant&type=suppression", "w", "other", "lifecycle", "running", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			controller.task.Role = tc.role
			controller.task.Phase = tc.phase
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.path, nil)
			r.Header.Set("Authorization", "Bearer "+tc.token)
			r.Header.Set("X-Worker-ID", tc.worker)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.code {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "unpublished-secret") {
				t.Fatal("unpublished spec leaked")
			}
			if tc.code == 200 && !strings.Contains(w.Body.String(), `"compiler_version":1`) {
				t.Fatal("published compile identity missing")
			}
		})
	}
}
