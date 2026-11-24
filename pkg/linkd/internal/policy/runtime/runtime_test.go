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
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/onemodel"
	"linkd/internal/policy"
	"linkd/internal/taskdispatch"
)

func TestOpenDoesNotRequireUnusedResources(t *testing.T) {
	r, err := Open(config.ResourcesConfig{}, config.BluekingConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if _, err := r.Targets.ResolveScope(t.Context(), "tenant", "bkcc__2"); !errors.Is(err, onemodel.ErrTargetUnavailable) {
		t.Fatal(err)
	}
}

type runtimeRecords struct {
	err     error
	foreign bool
}

func (r runtimeRecords) List(context.Context, policy.Scope, string, int) ([]policy.Record, error) {
	if r.foreign {
		return []policy.Record{{Scope: policy.Scope{TenantID: "other", Kind: policy.Suppression}, ID: "p", Published: 1, Revision: 1}}, nil
	}
	return []policy.Record{}, r.err
}

func TestSnapshotDependencySkipDoesNotSwallowAuthorizationOrCancellation(t *testing.T) {
	event := domain.Event{BKTenantID: "tenant"}
	at := time.Now().UTC()
	for _, test := range []struct {
		name   string
		reader runtimeRecords
		hard   bool
	}{{"dependency", runtimeRecords{err: errors.New("dependency")}, false}, {"access", runtimeRecords{err: policy.ErrAccess}, true}, {"foreign scope", runtimeRecords{foreign: true}, true}} {
		t.Run(test.name, func(t *testing.T) {
			snapshot, err := (Snapshotter{Catalog: policy.NewCatalog(test.reader)}).Snapshot(t.Context(), event, at)
			if test.hard {
				if !errors.Is(err, policy.ErrAccess) || snapshot != nil {
					t.Fatalf("auth became skip %+v %v", snapshot, err)
				}
			} else if err != nil || snapshot.ReasonCode != "policy_load_failed" || !snapshot.EvaluatedAt.Equal(at) {
				t.Fatalf("skip %+v %v", snapshot, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := (Snapshotter{Catalog: policy.NewCatalog(runtimeRecords{})}).Snapshot(ctx, event, at); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestWorkerPolicyReaderClassifiesAuthenticationAndVerifiesRelease(t *testing.T) {
	scope := policy.Scope{TenantID: "tenant", Kind: policy.Suppression}
	status := 200
	foreign := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer worker" || r.Header.Get("X-Worker-ID") != "w" || r.URL.Query().Get("bk_tenant_id") != "tenant" || r.URL.Query().Get("task") != "task" {
			t.Error("worker scope/header missing")
		}
		if status != 200 {
			http.Error(w, "secret backend detail", status)
			return
		}
		release := policy.Release{Scope: scope, ID: "p", Version: 1}
		if foreign {
			release.TenantID = "other"
		}
		if r.URL.Path == "/internal/policies" {
			_ = json.NewEncoder(w).Encode([]policy.Record{})
			return
		}
		_ = json.NewEncoder(w).Encode(release)
	}))
	defer server.Close()
	reader := WorkerReader{Client: taskdispatch.Client{URL: server.URL, Token: "worker", WorkerID: "w"}, TaskID: "task"}
	if _, err := reader.List(t.Context(), scope, "", 3); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.GetRelease(t.Context(), scope, "p", 1); err != nil {
		t.Fatal(err)
	}
	foreign = true
	if _, err := reader.GetRelease(t.Context(), scope, "p", 1); !errors.Is(err, policy.ErrAccess) {
		t.Fatal("foreign response accepted")
	}
	for _, code := range []int{401, 403, 500} {
		status = code
		_, err := reader.List(t.Context(), scope, "", 3)
		if err == nil || errors.Is(err, policy.ErrAccess) != (code == 401 || code == 403) {
			t.Fatalf("HTTP %d classification %v", code, err)
		}
	}
}
