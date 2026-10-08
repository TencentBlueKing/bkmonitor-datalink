// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type catalogReader struct {
	service *Service
	mu      sync.Mutex
	calls   int
	fail    bool
}

func (r *catalogReader) List(ctx context.Context, scope Scope, after string, limit int) ([]Record, error) {
	r.mu.Lock()
	r.calls++
	fail := r.fail
	r.mu.Unlock()
	if fail {
		return nil, ErrUnavailable
	}
	return r.service.List(ctx, scope, after, limit)
}

func TestCatalogFreezesPublishedScopeAndRefreshesAtomically(t *testing.T) {
	service := NewService(newTestDocuments())
	req := testRequest()
	mustApply(t, service, req)
	other := req
	other.TenantID = "other"
	mustApply(t, service, other)
	reader := &catalogReader{service: service}
	catalog := NewCatalog(reader)
	now := time.Now()
	catalog.now = func() time.Time { return now }
	first, err := catalog.Freeze(t.Context(), "tenant")
	if err != nil || len(first) != 1 || first[0].Release.TenantID != "tenant" {
		t.Fatalf("catalog %+v %v", first, err)
	}
	changed := req
	changed.ExpectedVersion = 1
	changed.OperationID = "edit"
	changed.Spec = testSpec("updated")
	mustApply(t, service, changed)
	cached, err := catalog.Freeze(t.Context(), "tenant")
	if err != nil || cached[0].Release.Version != 1 {
		t.Fatal("cache not frozen")
	}
	now = now.Add(6 * time.Second)
	updated, err := catalog.Freeze(t.Context(), "tenant")
	if err != nil || updated[0].Release.Version != 2 || first[0].Release.Version != 1 {
		t.Fatalf("refresh %+v %v", updated, err)
	}
	reader.fail = true
	now = now.Add(6 * time.Second)
	if _, err := catalog.Freeze(t.Context(), "tenant"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("expired snapshot used after load failure")
	}
	reader.fail = false
	if _, err := service.Delete(t.Context(), req.Scope, req.ID, 2, "delete"); err != nil {
		t.Fatal(err)
	}
	empty, err := catalog.Freeze(t.Context(), "tenant")
	if err != nil || len(empty) != 0 {
		t.Fatal("tombstone remained active")
	}
}

func TestCatalogConcurrentInitialLoadsAndOrder(t *testing.T) {
	service := NewService(newTestDocuments())
	for _, id := range []string{"c", "b", "a", "d"} {
		r := testRequest()
		r.ID = id
		mustApply(t, service, r)
	}
	reader := &catalogReader{service: service}
	catalog := NewCatalog(reader)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for range 12 {
		wg.Go(func() {
			policies, err := catalog.Freeze(t.Context(), "tenant")
			if err != nil {
				errs <- err
				return
			}
			if len(policies) != 4 || policies[0].Release.ID != "a" || policies[3].Release.ID != "d" {
				errs <- fmt.Errorf("unstable policy order")
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	reader.mu.Lock()
	calls := reader.calls
	reader.mu.Unlock()
	if calls != 4 {
		t.Fatalf("duplicate refresh calls=%d", calls)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := catalog.Freeze(ctx, "tenant"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled cache read: %v", err)
	}
}

func TestCatalogRejectsInvalidPublishedConfiguration(t *testing.T) {
	docs := newTestDocuments()
	service := NewService(docs)
	request := testRequest()
	mustApply(t, service, request)
	docs.mu.Lock()
	key := "records/" + request.key(request.ID)
	saved := docs.rows[key]
	var record Record
	if err := json.Unmarshal(saved.raw, &record); err != nil {
		t.Fatal(err)
	}
	record.Compiled.CompilerVersion = 0
	saved.raw, _ = json.Marshal(record)
	docs.rows[key] = saved
	docs.mu.Unlock()
	if _, err := NewCatalog(service).Freeze(t.Context(), request.TenantID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid release became dependency skip: %v", err)
	}
}
