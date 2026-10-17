// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/onemodel"
	"linkd/internal/policy"
	"linkd/internal/taskdispatch"
)

func TestWorkerDependenciesShareSourceTimeAndAssignedTask(t *testing.T) {
	loaded := time.Now().UTC().Add(-40 * time.Second)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/internal/policy-dependencies" || r.Header.Get("Authorization") != "Bearer worker" || r.Header.Get("X-Worker-ID") != "w" || r.URL.Query().Get("task") != "task" || r.URL.Query().Get("bk_tenant_id") != "tenant" {
			t.Error("worker assignment or token missing", r.URL)
		}
		var request DependencyRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "invalid", 400)
			return
		}
		if err := json.NewEncoder(w).Encode(dependencyValue(request, loaded)); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	worker := NewWorkerDependencies(t.Context(), taskdispatch.Client{URL: server.URL, Token: "worker"})
	t.Cleanup(worker.Close)
	unregister := worker.Register(taskdispatch.Task{ID: "task", Worker: "w"}, "tenant")
	defer unregister()
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			value, err := worker.Read(t.Context(), DependencyRequest{Kind: "scope", TenantID: "tenant", SpaceCode: "bkcc__2"})
			if err != nil || !value.LoadedAt.Equal(loaded) {
				t.Error("source time reset", value.LoadedAt, loaded, err)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("source task did not share load", calls.Load())
	}
	clock := newDependencyClock(loaded.Add(59 * time.Second))
	setDependencyClock(worker.Dependencies, clock)
	tracked, stamp := TrackDependencyReads(t.Context())
	if _, err := worker.ResolveBusinessScope(tracked, "tenant", "bkcc__2", 2); err != nil || !stamp.Time().Equal(loaded) {
		t.Fatal("worker renewed source time", stamp.Time(), err)
	}
	clock.Advance(2 * time.Second)
	if _, err := worker.Read(t.Context(), DependencyRequest{Kind: "scope", TenantID: "tenant", SpaceCode: "bkcc__2"}); !errors.Is(err, policy.ErrUnavailable) {
		t.Fatal("worker extended CP snapshot beyond 60s", err)
	}
	if calls.Load() != 1 {
		t.Fatal("expired result unexpectedly used an extra source response", calls.Load())
	}
}

func TestWorkerDependenciesSameContentHasSameQualificationTime(t *testing.T) {
	clock := newDependencyClock(time.Now().UTC())
	var changed atomic.Bool
	cp := NewDependencies(t.Context(), func(_ context.Context, request DependencyRequest) (DependencySnapshot, error) {
		value := dependencyValue(request, clock.Now())
		if changed.Load() {
			value.Targets.Instances = append(value.Targets.Instances, onemodel.InstanceRef{ModelID: "cw-Host", InstanceID: "2", EntityUID: "cw-Host|2"})
		}
		return value, nil
	})
	t.Cleanup(cp.Close)
	setDependencyClock(cp, clock)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request DependencyRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		value, err := cp.Read(r.Context(), request)
		if err != nil {
			t.Error(err)
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := json.NewEncoder(w).Encode(value); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	workers := make([]*WorkerDependencies, 2)
	for i := range workers {
		workers[i] = NewWorkerDependencies(t.Context(), taskdispatch.Client{URL: server.URL, Token: "worker"})
		t.Cleanup(workers[i].Close)
		setDependencyClock(workers[i].Dependencies, clock)
		t.Cleanup(workers[i].Register(taskdispatch.Task{ID: "task", Worker: "w"}, "tenant"))
	}
	target := testDependencyTarget()
	request := DependencyRequest{Kind: "targets", TenantID: "tenant", SpaceCode: "bkcc__2", Target: target}
	read := func(worker *WorkerDependencies) DependencySnapshot {
		t.Helper()
		value, err := worker.Read(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		ctx, stamp := TrackDependencyReads(t.Context())
		if found, err := worker.ContainsTarget(ctx, "tenant", "bkcc__2", target, target.Selectors[0].Instances[0]); err != nil || !found || !stamp.Time().Equal(value.ChangedAt) {
			t.Fatal("qualification did not use CP content time", found, stamp.Time(), value, err)
		}
		return value
	}
	first := read(workers[0])
	clock.Advance(30 * time.Second)
	refreshDependencyNow(t, cp, request)
	second := read(workers[1])
	if !second.LoadedAt.After(first.LoadedAt) || !second.ChangedAt.Equal(first.ChangedAt) {
		t.Fatal("equal content from different refreshes would make old worker wait", first, second)
	}
	if old := read(workers[0]); !old.ChangedAt.Equal(second.ChangedAt) || !old.LoadedAt.Equal(first.LoadedAt) {
		t.Fatal("unchanged old worker cannot reuse qualification", old, second)
	}
	clock.Advance(time.Second)
	changed.Store(true)
	refreshDependencyNow(t, cp, request)
	refreshDependencyNow(t, workers[1].Dependencies, request)
	next := read(workers[1])
	if !next.ChangedAt.Equal(clock.Now()) || !next.ChangedAt.After(first.ChangedAt) {
		t.Fatal("HTTP transport lost actual content change", first, next)
	}
	refreshDependencyNow(t, workers[0].Dependencies, request)
	if current := read(workers[0]); !current.ChangedAt.Equal(next.ChangedAt) {
		t.Fatal("worker replaced CP content time with local load time", current, next)
	}
}

func TestWorkerDependenciesRejectForeignScopeAndMalformedMembers(t *testing.T) {
	tests := []struct {
		name    string
		request DependencyRequest
		mutate  func(*DependencySnapshot)
		want    error
	}{
		{"foreign scope", DependencyRequest{Kind: "scope", TenantID: "tenant", SpaceCode: "bkcc__2"}, func(v *DependencySnapshot) { v.Scope.TenantID = "other" }, policy.ErrAccess},
		{"different space", DependencyRequest{Kind: "scope", TenantID: "tenant", SpaceCode: "bkcc__2"}, func(v *DependencySnapshot) { v.Scope.SpaceCode = "bkcc__3" }, policy.ErrAccess},
		{"foreign target scope", DependencyRequest{Kind: "targets", TenantID: "tenant", SpaceCode: "bkcc__2", Target: testDependencyTarget()}, func(v *DependencySnapshot) { v.Targets.Scope.TenantID = "other" }, policy.ErrAccess},
		{"target model", DependencyRequest{Kind: "targets", TenantID: "tenant", SpaceCode: "bkcc__2", Target: testDependencyTarget()}, func(v *DependencySnapshot) { v.Targets.Instances[0].ModelID = "other" }, policy.ErrTargetIdentity},
		{"target entity UID", DependencyRequest{Kind: "targets", TenantID: "tenant", SpaceCode: "bkcc__2", Target: testDependencyTarget()}, func(v *DependencySnapshot) { v.Targets.Instances[0].EntityUID = "cw-Host|2" }, policy.ErrTargetIdentity},
		{"relation model", DependencyRequest{Kind: "relations", TenantID: "tenant", Origin: onemodel.InstanceRef{ModelID: "cw-biz", InstanceID: "2", EntityUID: "cw-biz|2"}, Relation: "rel", ModelID: "cw-Host"}, func(v *DependencySnapshot) { v.Related[0].ModelID = "other" }, policy.ErrAccess},
		{"relation entity UID", DependencyRequest{Kind: "relations", TenantID: "tenant", Origin: onemodel.InstanceRef{ModelID: "cw-biz", InstanceID: "2", EntityUID: "cw-biz|2"}, Relation: "rel", ModelID: "cw-Host"}, func(v *DependencySnapshot) { v.Related[0].EntityUID = "bad" }, policy.ErrTargetIdentity},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				value := dependencyValue(test.request, time.Now())
				test.mutate(&value)
				if err := json.NewEncoder(w).Encode(value); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			worker := NewWorkerDependencies(t.Context(), taskdispatch.Client{URL: server.URL, Token: "worker"})
			defer worker.Close()
			unregister := worker.Register(taskdispatch.Task{ID: "task", Worker: "w"}, "tenant")
			defer unregister()
			if _, err := worker.Read(t.Context(), test.request); !errors.Is(err, test.want) {
				t.Fatalf("scope/member error became successful cached result: %v", err)
			}
		})
	}
}

func TestWorkerDependenciesRegistrationScopeAndUnregister(t *testing.T) {
	worker := NewWorkerDependencies(t.Context(), taskdispatch.Client{})
	defer worker.Close()
	a := worker.Register(taskdispatch.Task{ID: "first", Worker: "one"}, "tenant")
	b := worker.Register(taskdispatch.Task{ID: "second", Worker: "two"}, "tenant")
	if _, err := worker.reader("other"); !errors.Is(err, policy.ErrUnavailable) {
		t.Fatal("task authorized foreign tenant", err)
	}
	a()
	reader, err := worker.reader("tenant")
	if err != nil || reader.TaskID != "second" || reader.Client.WorkerID != "two" {
		t.Fatal("removed task remained selected", reader, err)
	}
	b()
	if _, err := worker.reader("tenant"); !errors.Is(err, policy.ErrUnavailable) {
		t.Fatal("reader survived last task", err)
	}
	global := worker.Register(taskdispatch.Task{ID: "global", Worker: "g"}, "")
	defer global()
	if reader, err = worker.reader("explicit-tenant"); err != nil || reader.TaskID != "global" {
		t.Fatal("multi-tenant task rejected explicit tenant", reader, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := worker.Read(ctx, DependencyRequest{Kind: "scope", TenantID: "tenant", SpaceCode: "bkcc__2"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestWorkerDependenciesHTTPAuthorizationErrorRemainsHard(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "denied", http.StatusForbidden) }))
	defer server.Close()
	worker := NewWorkerDependencies(t.Context(), taskdispatch.Client{URL: server.URL, Token: "worker"})
	defer worker.Close()
	unregister := worker.Register(taskdispatch.Task{ID: "task", Worker: "w"}, "tenant")
	defer unregister()
	if _, err := worker.Read(t.Context(), DependencyRequest{Kind: "scope", TenantID: "tenant", SpaceCode: "bkcc__2"}); !errors.Is(err, policy.ErrAccess) {
		t.Fatal("HTTP authorization became empty dependency", err)
	}
}

func TestWorkerDependenciesLargeSnapshotUsesExistingSourceByteBudget(t *testing.T) {
	target := &onemodel.TargetDescriptor{SchemaVersion: 1, ModelID: "cw-Host", Selectors: []onemodel.TargetSelector{{Type: "dynamic_group", Provider: "kingeye", DynamicGroupID: "1"}}}
	request := DependencyRequest{Kind: "targets", TenantID: "tenant", SpaceCode: "bkcc__2", Target: target}
	value := dependencyValue(request, time.Now())
	value.Targets.Instances = make([]onemodel.InstanceRef, 10000)
	for i := range value.Targets.Instances {
		id := strings.Repeat("x", 450) + strconv.Itoa(i)
		value.Targets.Instances[i] = onemodel.InstanceRef{ModelID: "cw-Host", InstanceID: id, EntityUID: "cw-Host|" + id}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) <= 8<<20 || len(raw) > 32<<20 {
		t.Fatalf("test response size=%d", len(raw))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(raw); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	worker := NewWorkerDependencies(t.Context(), taskdispatch.Client{URL: server.URL, Token: "worker"})
	defer worker.Close()
	unregister := worker.Register(taskdispatch.Task{ID: "task", Worker: "w"}, "tenant")
	defer unregister()
	found, err := worker.ContainsTarget(t.Context(), "tenant", "bkcc__2", target, value.Targets.Instances[len(value.Targets.Instances)-1])
	if err != nil || !found {
		t.Fatal("valid 32MiB-budget source snapshot was truncated at 8MiB", found, err)
	}
}
