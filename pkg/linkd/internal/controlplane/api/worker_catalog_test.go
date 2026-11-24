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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/policy"
)

type lockedCatalogDocuments struct {
	mu        sync.Mutex
	documents *policyDocuments
}

func (d *lockedCatalogDocuments) Get(ctx context.Context, collection, key string) (json.RawMessage, string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.documents.Get(ctx, collection, key)
}

func (d *lockedCatalogDocuments) Put(ctx context.Context, collection, key, version string, raw json.RawMessage) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.documents.Put(ctx, collection, key, version, raw)
}

func (d *lockedCatalogDocuments) List(ctx context.Context, collection, prefix, after string, limit int) ([]json.RawMessage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.documents.List(ctx, collection, prefix, after, limit)
}

type countingCatalogRecords struct {
	service *policy.Service
	calls   atomic.Int32
}

func (r *countingCatalogRecords) List(ctx context.Context, scope policy.Scope, after string, limit int) ([]policy.Record, error) {
	r.calls.Add(1)
	return r.service.List(ctx, scope, after, limit)
}

func catalogAPI(t *testing.T) (*API, *countingCatalogRecords) {
	t.Helper()
	docs := &lockedCatalogDocuments{documents: &policyDocuments{rows: map[string]policyDocument{}}}
	service := policy.NewService(docs)
	var request policy.ApplyRequest
	if err := json.Unmarshal([]byte(policyRequestBody()), &request); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	reader := &countingCatalogRecords{service: service}
	api := workerReadAPI()
	api.Policies = service
	api.PolicyCatalog = policy.NewCatalogWithTTL(reader, time.Minute)
	return api, reader
}

func TestWorkerCatalogAPISharedCacheAndMutationInvalidation(t *testing.T) {
	api, reader := catalogAPI(t)
	handler := api.Handler()
	read := func() []policy.Release {
		t.Helper()
		response := workerRequest(t, handler, http.MethodGet, "/internal/policy-catalog?task=task&bk_tenant_id=tenant", "", "w", "worker")
		if response.Code != 200 {
			t.Fatal(response.Code, response.Body.String())
		}
		var releases []policy.Release
		if err := json.Unmarshal(response.Body.Bytes(), &releases); err != nil {
			t.Fatal(err)
		}
		return releases
	}
	first := read()
	if len(first) != 1 || first[0].Version != 1 {
		t.Fatal(first)
	}
	calls := reader.calls.Load()
	_ = read()
	if reader.calls.Load() != calls {
		t.Fatal("worker bundle requests rescanned config storage")
	}
	updated := strings.Replace(policyRequestBody(), `"expected_version":0`, `"expected_version":1`, 1)
	updated = strings.Replace(updated, `"operation_id":"create"`, `"operation_id":"edit"`, 1)
	updated = strings.Replace(updated, `"name":"test"`, `"name":"updated"`, 1)
	response := policyHTTP(t, handler, http.MethodPut, "/api/v1/policies/suppression/p1", updated)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	second := read()
	if len(second) != 1 || second[0].Version != 2 || !strings.Contains(string(second[0].Spec), `"name":"updated"`) {
		t.Fatal("API update did not invalidate shared directory", second)
	}
	if reader.calls.Load() == calls {
		t.Fatal("directory not reloaded after update")
	}
	response = policyHTTP(t, handler, http.MethodDelete, "/api/v1/policies/suppression/p1", `{"schema_version":1,"bk_tenant_id":"tenant","expected_version":2,"operation_id":"delete"}`)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	if remaining := read(); len(remaining) != 0 {
		t.Fatal("deleted policy remained in worker directory", remaining)
	}
	if first[0].Version != 1 {
		t.Fatal("previous directory mutated")
	}
}

func TestWorkerCatalogAPIAuthorizationAndExplicitTenant(t *testing.T) {
	api, reader := catalogAPI(t)
	handler := api.Handler()
	tests := []struct {
		name, path, worker, token, role, phase string
		code                                   int
	}{
		{"valid", "/internal/policy-catalog?task=task&bk_tenant_id=tenant", "w", "worker", "lifecycle", "running", 200},
		{"other tenant", "/internal/policy-catalog?task=task&bk_tenant_id=other", "w", "worker", "lifecycle", "running", 403},
		{"wrong worker", "/internal/policy-catalog?task=task&bk_tenant_id=tenant", "other", "worker", "lifecycle", "running", 403},
		{"cleaner", "/internal/policy-catalog?task=task&bk_tenant_id=tenant", "w", "worker", "cleaner", "running", 403},
		{"stopped", "/internal/policy-catalog?task=task&bk_tenant_id=tenant", "w", "worker", "lifecycle", "stopped", 403},
		{"no task", "/internal/policy-catalog?bk_tenant_id=tenant", "w", "worker", "lifecycle", "running", 403},
		{"no tenant", "/internal/policy-catalog?task=task", "w", "worker", "lifecycle", "running", 400},
		{"duplicate tenant", "/internal/policy-catalog?task=task&bk_tenant_id=tenant&bk_tenant_id=other", "w", "worker", "lifecycle", "running", 400},
		{"native connection forbidden", "/internal/policy-catalog?task=task&bk_tenant_id=tenant&url=http://example.invalid", "w", "worker", "lifecycle", "running", 400},
		{"wrong token", "/internal/policy-catalog?task=task&bk_tenant_id=tenant", "w", "admin", "lifecycle", "running", 401},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api.Controller.(*workerPolicyController).task.Role = test.role
			api.Controller.(*workerPolicyController).task.Phase = test.phase
			calls := reader.calls.Load()
			response := workerRequest(t, handler, http.MethodGet, test.path, "", test.worker, test.token)
			if response.Code != test.code {
				t.Fatal(response.Code, response.Body.String())
			}
			if test.code != 200 && reader.calls.Load() != calls {
				t.Fatal("unauthorized request accessed configuration store")
			}
		})
	}
}

func TestWorkerCatalogBackgroundRefresh(t *testing.T) {
	api, reader := catalogAPI(t)
	api.PolicyCatalog = policy.NewCatalogWithTTL(reader, 200*time.Millisecond)
	if _, err := api.PolicyCatalog.Freeze(t.Context(), "tenant"); err != nil {
		t.Fatal(err)
	}
	var request policy.ApplyRequest
	if err := json.Unmarshal([]byte(policyRequestBody()), &request); err != nil {
		t.Fatal(err)
	}
	request.ExpectedVersion = 1
	request.OperationID = "background-edit"
	request.Spec = json.RawMessage(strings.Replace(string(request.Spec), `"name":"test"`, `"name":"background"`, 1))
	if _, err := api.Policies.Apply(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	// 不走 API Invalidate，验证周期补读能发现漏掉的变更。
	before, ok := api.PolicyCatalog.FindLatest(request.Scope, request.ID)
	if !ok || before.Release.Version != 1 {
		t.Fatal("test did not start with old directory")
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- api.PolicyCatalog.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	}()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("background directory refresh did not discover update")
		case <-poll.C:
			current, ok := api.PolicyCatalog.FindLatest(request.Scope, request.ID)
			if ok && current.Release.Version == 2 {
				return
			}
		}
	}
}

type catalogPreparation struct {
	calls  int
	tenant string
	err    error
}

func (p *catalogPreparation) Schedule(ctx context.Context, tenant string) error {
	p.calls++
	p.tenant = tenant
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.err
}

func (*catalogPreparation) Invalidate(string) {}

func TestWorkerCatalogRequiresStartupRetirementBeforeReturningPolicies(t *testing.T) {
	api, _ := catalogAPI(t)
	preparation := &catalogPreparation{err: policy.ErrUnavailable}
	api.PolicyPreparation = preparation
	handler := api.Handler()
	failed := workerRequest(t, handler, http.MethodGet, "/internal/policy-catalog?task=task&bk_tenant_id=tenant", "", "w", "worker")
	if failed.Code != http.StatusServiceUnavailable || strings.Contains(failed.Body.String(), `"policy_id"`) {
		t.Fatal("catalog escaped failed retirement", failed.Code, failed.Body.String())
	}
	preparation.err = nil
	ready := workerRequest(t, handler, http.MethodGet, "/internal/policy-catalog?task=task&bk_tenant_id=tenant", "", "w", "worker")
	if ready.Code != http.StatusOK || preparation.calls != 2 || preparation.tenant != "tenant" {
		t.Fatal(ready.Code, preparation)
	}
}
