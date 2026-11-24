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
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/eventsource"
	"linkd/internal/onemodel"
	"linkd/internal/policy"
	policyruntime "linkd/internal/policy/runtime"
	"linkd/internal/taskdispatch"
)

type workerDependencyReader struct {
	calls atomic.Int32
	at    time.Time
	err   error
}

func (d *workerDependencyReader) Read(_ context.Context, request policyruntime.DependencyRequest) (policyruntime.DependencySnapshot, error) {
	d.calls.Add(1)
	return policyruntime.DependencySnapshot{LoadedAt: d.at, Scope: onemodel.TargetScope{TenantID: request.TenantID, SpaceCode: request.SpaceCode, BusinessIDs: []int64{2}}}, d.err
}

func workerReadAPI() *API {
	source := eventsource.Release{ID: "source", Version: 1, Spec: config.EventSource{EventSourceID: "source", RelatedTenantID: "tenant"}}
	sources := eventsource.New(publishedDocuments{release: source}, config.DefaultSeverityConfig())
	controller := &workerPolicyController{task: taskdispatch.Task{Source: "source", Version: 1, Worker: "w", Role: "lifecycle", Phase: "running"}}
	return &API{Sources: sources, Controller: controller, Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}, WorkerToken: "worker"}}
}

func workerRequest(t *testing.T, handler http.Handler, method, path, body, worker, token string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Worker-ID", worker)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestWorkerDependenciesAPIAuthorizationAndScope(t *testing.T) {
	loaded := time.Now().UTC().Add(-35 * time.Second)
	reader := &workerDependencyReader{at: loaded}
	api := workerReadAPI()
	api.PolicyDependencies = reader
	handler := api.Handler()
	tests := []struct {
		name, path, body, worker, token, role, phase string
		code                                         int
	}{
		{"scope", "/internal/policy-dependencies?task=task&bk_tenant_id=tenant", `{"kind":"scope","bk_tenant_id":"tenant","space_code":"bkcc__2"}`, "w", "worker", "lifecycle", "running", 200},
		{"non-BKCC scope", "/internal/policy-dependencies?task=task&bk_tenant_id=tenant", `{"kind":"scope","bk_tenant_id":"tenant","space_code":"bkci__project-a"}`, "w", "worker", "lifecycle", "running", 200},
		{"non-BKCC targets", "/internal/policy-dependencies?task=task&bk_tenant_id=tenant", `{"kind":"targets","bk_tenant_id":"tenant","space_code":"default__custom","target":{"schema_version":1,"model_id":"cw-Host","selectors":[{"type":"instances","instances":[{"model_id":"cw-Host","model_inst_id":"1","entity_uid":"cw-Host|1"}]}]}}`, "w", "worker", "lifecycle", "running", 200},
		{"empty non-BKCC space identity", "/internal/policy-dependencies?task=task&bk_tenant_id=tenant", `{"kind":"scope","bk_tenant_id":"tenant","space_code":"bkci__"}`, "w", "worker", "lifecycle", "running", 400},
		{"zero BKCC space identity", "/internal/policy-dependencies?task=task&bk_tenant_id=tenant", `{"kind":"scope","bk_tenant_id":"tenant","space_code":"bkcc__0"}`, "w", "worker", "lifecycle", "running", 400},
		{"targets", "/internal/policy-dependencies?task=task&bk_tenant_id=tenant", `{"kind":"targets","bk_tenant_id":"tenant","space_code":"bkcc__2","target":{"schema_version":1,"model_id":"cw-Host","selectors":[{"type":"instances","instances":[{"model_id":"cw-Host","model_inst_id":"1","entity_uid":"cw-Host|1"}]}]}}`, "w", "worker", "lifecycle", "running", 200},
		{"relations", "/internal/policy-dependencies?task=task&bk_tenant_id=tenant", `{"kind":"relations","bk_tenant_id":"tenant","origin":{"model_id":"cw-biz","model_inst_id":"2","entity_uid":"cw-biz|2"},"relation":"rel","model_id":"cw-Host"}`, "w", "worker", "lifecycle", "running", 200},
		{"foreign query tenant", "/internal/policy-dependencies?task=task&bk_tenant_id=other", `{"kind":"scope","bk_tenant_id":"other","space_code":"bkcc__2"}`, "w", "worker", "lifecycle", "running", 403},
		{"body query mismatch", "/internal/policy-dependencies?task=task&bk_tenant_id=tenant", `{"kind":"scope","bk_tenant_id":"other","space_code":"bkcc__2"}`, "w", "worker", "lifecycle", "running", 403},
		{"wrong worker", "/internal/policy-dependencies?task=task&bk_tenant_id=tenant", `{"kind":"scope","bk_tenant_id":"tenant","space_code":"bkcc__2"}`, "other", "worker", "lifecycle", "running", 403},
		{"cleaner", "/internal/policy-dependencies?task=task&bk_tenant_id=tenant", `{"kind":"scope","bk_tenant_id":"tenant","space_code":"bkcc__2"}`, "w", "worker", "cleaner", "running", 403},
		{"stopped", "/internal/policy-dependencies?task=task&bk_tenant_id=tenant", `{"kind":"scope","bk_tenant_id":"tenant","space_code":"bkcc__2"}`, "w", "worker", "lifecycle", "stopped", 403},
		{"missing task", "/internal/policy-dependencies?bk_tenant_id=tenant", `{"kind":"scope","bk_tenant_id":"tenant","space_code":"bkcc__2"}`, "w", "worker", "lifecycle", "running", 403},
		{"missing tenant", "/internal/policy-dependencies?task=task", `{"kind":"scope","bk_tenant_id":"tenant","space_code":"bkcc__2"}`, "w", "worker", "lifecycle", "running", 400},
		{"bad space", "/internal/policy-dependencies?task=task&bk_tenant_id=tenant", `{"kind":"scope","bk_tenant_id":"tenant","space_code":"any"}`, "w", "worker", "lifecycle", "running", 400},
		{"unknown kind", "/internal/policy-dependencies?task=task&bk_tenant_id=tenant", `{"kind":"raw-query","bk_tenant_id":"tenant"}`, "w", "worker", "lifecycle", "running", 400},
		{"bad target identity", "/internal/policy-dependencies?task=task&bk_tenant_id=tenant", `{"kind":"targets","bk_tenant_id":"tenant","space_code":"bkcc__2","target":{"schema_version":1,"model_id":"cw-Host","selectors":[{"type":"instances","instances":[{"model_id":"cw-Host","model_inst_id":"1","entity_uid":"other|1"}]}]}}`, "w", "worker", "lifecycle", "running", 400},
		{"raw native query rejected", "/internal/policy-dependencies?task=task&bk_tenant_id=tenant", `{"kind":"scope","bk_tenant_id":"tenant","space_code":"bkcc__2","query":{"match_all":{}}}`, "w", "worker", "lifecycle", "running", 400},
		{"duplicate query", "/internal/policy-dependencies?task=task&bk_tenant_id=tenant&bk_tenant_id=other", `{"kind":"scope","bk_tenant_id":"tenant","space_code":"bkcc__2"}`, "w", "worker", "lifecycle", "running", 400},
		{"wrong token", "/internal/policy-dependencies?task=task&bk_tenant_id=tenant", `{"kind":"scope","bk_tenant_id":"tenant","space_code":"bkcc__2"}`, "w", "admin", "lifecycle", "running", 401},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api.Controller.(*workerPolicyController).task.Role = test.role
			api.Controller.(*workerPolicyController).task.Phase = test.phase
			before := reader.calls.Load()
			response := workerRequest(t, handler, http.MethodPost, test.path, test.body, test.worker, test.token)
			if response.Code != test.code {
				t.Fatalf("code=%d body=%s", response.Code, response.Body.String())
			}
			if test.code != 200 && reader.calls.Load() != before {
				t.Fatal("rejected request accessed source")
			}
			if test.code == 200 {
				var value policyruntime.DependencySnapshot
				if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil || !value.LoadedAt.Equal(loaded) {
					t.Fatal("source age reset", value.LoadedAt, err)
				}
			}
		})
	}
}

func TestWorkerDependenciesAPIFailureIsNotEmptySuccess(t *testing.T) {
	api := workerReadAPI()
	reader := &workerDependencyReader{err: policy.ErrUnavailable}
	api.PolicyDependencies = reader
	response := workerRequest(t, api.Handler(), http.MethodPost, "/internal/policy-dependencies?task=task&bk_tenant_id=tenant", `{"kind":"scope","bk_tenant_id":"tenant","space_code":"bkcc__2"}`, "w", "worker")
	if response.Code != 503 {
		t.Fatal(response.Code, response.Body.String())
	}
	api.PolicyDependencies = nil
	response = workerRequest(t, api.Handler(), http.MethodPost, "/internal/policy-dependencies?task=task&bk_tenant_id=tenant", `{"kind":"scope","bk_tenant_id":"tenant","space_code":"bkcc__2"}`, "w", "worker")
	if response.Code != 503 {
		t.Fatal(response.Code, response.Body.String())
	}
}

func TestWorkerDependenciesAPIAcceptsPolicySizedDescriptor(t *testing.T) {
	reader := &workerDependencyReader{at: time.Now()}
	api := workerReadAPI()
	api.PolicyDependencies = reader
	instances := make([]onemodel.InstanceRef, 1000)
	for i := range instances {
		id := strings.Repeat("a", 900) + strconv.Itoa(i)
		instances[i] = onemodel.InstanceRef{ModelID: "cw-Host", InstanceID: id, EntityUID: "cw-Host|" + id}
	}
	request := policyruntime.DependencyRequest{Kind: "targets", TenantID: "tenant", SpaceCode: "bkcc__2", Target: &onemodel.TargetDescriptor{SchemaVersion: 1, ModelID: "cw-Host", Selectors: []onemodel.TargetSelector{{Type: "instances", Instances: instances}}}}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) <= 1<<20 || len(raw) > 2<<20 {
		t.Fatal("descriptor test size", len(raw))
	}
	response := workerRequest(t, api.Handler(), http.MethodPost, "/internal/policy-dependencies?task=task&bk_tenant_id=tenant", string(raw), "w", "worker")
	if response.Code != 200 || reader.calls.Load() != 1 {
		t.Fatal("valid policy-sized target descriptor rejected", response.Code, response.Body.String())
	}
}

func TestWorkerDependencyQueryDistinguishesMissingTargetAndReadFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"not found", errors.Join(policy.ErrUnavailable, policy.ErrNotFound), 404},
		{"unresolvable target", errors.Join(policy.ErrUnavailable, onemodel.ErrTargetUnavailable), 422},
		{"invalid target identity", errors.Join(policy.ErrUnavailable, policy.ErrTargetIdentity), 422},
		{"query failed", errors.Join(policy.ErrUnavailable, errors.New("database unavailable")), 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := workerReadAPI()
			api.PolicyDependencies = &workerDependencyReader{err: tc.err}
			response := workerRequest(t, api.Handler(), http.MethodPost, "/internal/policy-dependencies?task=task&bk_tenant_id=tenant", `{"kind":"scope","bk_tenant_id":"tenant","space_code":"bkcc__2"}`, "w", "worker")
			if response.Code != tc.status {
				t.Fatal(response.Code, response.Body.String())
			}
		})
	}
}
