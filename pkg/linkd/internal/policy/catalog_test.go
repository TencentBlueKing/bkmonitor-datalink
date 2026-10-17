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
	"slices"
	"sort"
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
	if calls != 3 {
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

func TestCatalogCandidatesAndExactVersionsAcrossRefresh(t *testing.T) {
	service := NewService(newTestDocuments())
	req := testRequest()
	first := mustApply(t, service, req)
	catalog := NewCatalog(service)
	now := time.Now()
	catalog.now = func() time.Time { return now }
	got, err := catalog.Candidates(t.Context(), "tenant", candidateFields(Fields{Values: map[string]any{"source_id": "unrelated"}}))
	if err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
	latest, ok := catalog.FindLatest(req.Scope, req.ID)
	if !ok || latest.Release.Version != 1 {
		t.Fatal("candidate omission hid latest version")
	}
	edit := req
	edit.OperationID = "edit"
	edit.ExpectedVersion = 1
	edit.Spec = testSpec("second")
	second := mustApply(t, service, edit)
	now = now.Add(6 * time.Second)
	if _, err = catalog.Candidates(t.Context(), "tenant", nil); err != nil {
		t.Fatal(err)
	}
	for _, release := range []Release{first, second} {
		cached, found := catalog.FindFrozen(req.Scope, req.ID, release.Version, release.Compiled.Digest)
		if !found || cached.Release.Version != release.Version {
			t.Fatal("in-flight version lost", release.Version)
		}
	}
	if _, ok = catalog.FindLatest(Scope{TenantID: "other", Kind: Suppression}, req.ID); ok {
		t.Fatal("cross-tenant latest")
	}
	if _, ok = catalog.FindFrozen(req.Scope, req.ID, second.Version, first.Compiled.Digest); ok {
		t.Fatal("different digest hit")
	}
}

type largeCatalogReader struct {
	records   []Record
	calls     int
	failAfter int
}

func (r *largeCatalogReader) List(_ context.Context, scope Scope, after string, limit int) ([]Record, error) {
	r.calls++
	if r.failAfter > 0 && r.calls > r.failAfter {
		return nil, ErrUnavailable
	}
	if scope.Kind != Suppression {
		return nil, nil
	}
	start := sort.Search(len(r.records), func(i int) bool { return r.records[i].ID > after })
	return r.records[start:min(start+limit, len(r.records))], nil
}

func TestCatalogLoadsTenThousandPolicies(t *testing.T) {
	spec := testSpec("many")
	compiled, err := Compile(Suppression, spec)
	if err != nil {
		t.Fatal(err)
	}
	reader := &largeCatalogReader{records: make([]Record, 10000)}
	for i := range reader.records {
		reader.records[i] = Record{Scope: Scope{TenantID: "tenant", Kind: Suppression}, ID: fmt.Sprintf("p%05d", i), Revision: 1, Published: 1, Spec: compiled.Canonical, Compiled: compiled.Summary}
	}
	catalog := NewCatalog(reader)
	got, err := catalog.Candidates(t.Context(), "tenant", candidateFields(Fields{Values: map[string]any{"source_id": "none"}}))
	if err != nil || len(got) != 0 {
		t.Fatal("large catalog became unavailable", len(got), err)
	}
	if item, ok := catalog.FindLatest(Scope{TenantID: "tenant", Kind: Suppression}, "p09999"); !ok || item.Release.ID != "p09999" {
		t.Fatal("large catalog truncated")
	}
	if reader.calls != 10000/MaxPageSize+3 {
		t.Fatalf("page requests=%d", reader.calls)
	}
}

func TestCatalogRunCancellation(t *testing.T) {
	catalog := NewCatalog(NewService(newTestDocuments()))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := catalog.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCatalogRefreshReusesCompilationAndConcurrentCandidates(t *testing.T) {
	service := NewService(newTestDocuments())
	req := testRequest()
	mustApply(t, service, req)
	catalog := NewCatalog(service)
	first, err := catalog.Freeze(t.Context(), req.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = catalog.loadEntry(t.Context(), req.TenantID, true); err != nil {
		t.Fatal(err)
	}
	same, ok := catalog.FindLatest(req.Scope, req.ID)
	if !ok || same.Compiled != first[0].Compiled {
		t.Fatal("unchanged publication recompiled")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 30 {
				got, err := catalog.Candidates(t.Context(), req.TenantID, candidateFields(Fields{Values: map[string]any{"source_id": "source"}}))
				if err != nil || len(got) != 1 || got[0].Release.ID != req.ID {
					t.Error("concurrent candidate changed", err)
				}
				if _, ok := catalog.FindFrozen(req.Scope, req.ID, 1, first[0].Compiled.Summary.Digest); !ok {
					t.Error("current version lost")
				}
			}
		})
	}
	wg.Go(func() {
		for range 6 {
			if _, err := catalog.loadEntry(t.Context(), req.TenantID, true); err != nil {
				t.Error(err)
			}
		}
	})
	wg.Wait()
}

func TestCatalogPartialRefreshDoesNotReplaceCompleteIndex(t *testing.T) {
	compiled, err := Compile(Suppression, testSpec("first"))
	if err != nil {
		t.Fatal(err)
	}
	reader := &largeCatalogReader{records: make([]Record, MaxPageSize+1)}
	for i := range reader.records {
		reader.records[i] = Record{Scope: Scope{TenantID: "tenant", Kind: Suppression}, ID: fmt.Sprintf("p%05d", i), Revision: 1, Published: 1, Spec: compiled.Canonical, Compiled: compiled.Summary}
	}
	catalog := NewCatalog(reader)
	if _, err = catalog.Freeze(t.Context(), "tenant"); err != nil {
		t.Fatal(err)
	}
	reader.records[0].Revision = 2
	reader.records[0].Published = 2
	reader.calls = 0
	reader.failAfter = 1
	if _, err = catalog.loadEntry(t.Context(), "tenant", true); !errors.Is(err, ErrUnavailable) {
		t.Fatal("incomplete refresh accepted", err)
	}
	latest, ok := catalog.FindLatest(Scope{TenantID: "tenant", Kind: Suppression}, "p00000")
	if !ok || latest.Release.Version != 1 {
		t.Fatal("partial snapshot replaced complete version", latest)
	}
	got, err := catalog.Candidates(t.Context(), "tenant", candidateFields(Fields{Values: map[string]any{"source_id": "source"}}))
	if err != nil || len(got) != MaxPageSize+1 {
		t.Fatal("complete index lost", len(got), err)
	}
}

type publishedCatalogReader struct {
	releases  []Release
	calls     int
	afterRead func()
}

func (r *publishedCatalogReader) List(context.Context, Scope, string, int) ([]Record, error) {
	return nil, fmt.Errorf("bundle reader must not make page requests")
}

func (r *publishedCatalogReader) PublishedPolicies(context.Context, string) ([]Release, error) {
	r.calls++
	if r.afterRead != nil {
		r.afterRead()
	}
	return r.releases, nil
}

func TestCatalogPublishedBundleAndInvalidation(t *testing.T) {
	compiled, err := Compile(Suppression, testSpec("published"))
	if err != nil {
		t.Fatal(err)
	}
	reader := &publishedCatalogReader{releases: []Release{{Scope: Scope{TenantID: "tenant", Kind: Suppression}, ID: "b", Version: 1, Spec: compiled.Canonical, Compiled: compiled.Summary}, {Scope: Scope{TenantID: "tenant", Kind: Suppression}, ID: "a", Version: 1, Spec: compiled.Canonical, Compiled: compiled.Summary}}}
	catalog := NewCatalogWithTTL(reader, time.Minute)
	now := time.Now()
	catalog.now = func() time.Time { return now }
	first, err := catalog.Freeze(t.Context(), "tenant")
	if err != nil || !slices.Equal(candidateIDs(first), []string{"a", "b"}) || reader.calls != 1 {
		t.Fatal(first, reader.calls, err)
	}
	now = now.Add(10 * time.Second)
	if _, err = catalog.Freeze(t.Context(), "tenant"); err != nil || reader.calls != 1 {
		t.Fatal("custom TTL ignored", reader.calls, err)
	}
	reader.releases[0].Version = 2
	catalog.Invalidate("tenant")
	second, err := catalog.Freeze(t.Context(), "tenant")
	if err != nil || reader.calls != 2 || second[1].Release.Version != 2 {
		t.Fatal("invalidated bundle stayed cached", second, reader.calls, err)
	}
	// 读完旧数据后发生新的变更通知，不能由本次加载把失效覆盖掉。
	catalog.Invalidate("tenant")
	reader.afterRead = func() { catalog.Invalidate("tenant") }
	if _, err = catalog.Freeze(t.Context(), "tenant"); err != nil {
		t.Fatal(err)
	}
	reader.afterRead = nil
	if _, err = catalog.Freeze(t.Context(), "tenant"); err != nil || reader.calls != 4 {
		t.Fatal("in-flight invalidation lost", reader.calls, err)
	}
	if first[1].Release.Version != 1 {
		t.Fatal("published snapshot was mutated")
	}
}

func TestCatalogPublishedBundleRejectsIncompleteScope(t *testing.T) {
	compiled, err := Compile(Suppression, testSpec("published"))
	if err != nil {
		t.Fatal(err)
	}
	release := Release{Scope: Scope{TenantID: "tenant", Kind: Suppression}, ID: "a", Version: 1, Spec: compiled.Canonical, Compiled: compiled.Summary}
	for _, test := range []struct {
		name     string
		releases []Release
		want     error
	}{
		{"foreign tenant", []Release{func() Release { r := release; r.TenantID = "other"; return r }()}, ErrAccess},
		{"duplicate", []Release{release, release}, ErrInvalid},
		{"unpublished", []Release{func() Release { r := release; r.Version = 0; return r }()}, ErrInvalid},
		{"different compiler", []Release{func() Release { r := release; r.Compiled.CompilerVersion = 0; return r }()}, ErrInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &publishedCatalogReader{releases: test.releases}
			if _, err := NewCatalog(reader).Freeze(t.Context(), "tenant"); !errors.Is(err, test.want) {
				t.Fatal(err)
			}
		})
	}
}

func TestCatalogTenThousandPoliciesUseOnePublishedBundle(t *testing.T) {
	compiled, err := Compile(Suppression, testSpec("bulk"))
	if err != nil {
		t.Fatal(err)
	}
	reader := &publishedCatalogReader{releases: make([]Release, 10000)}
	for i := range reader.releases {
		reader.releases[i] = Release{Scope: Scope{TenantID: "tenant", Kind: Suppression}, ID: fmt.Sprintf("p%05d", i), Version: 1, Spec: compiled.Canonical, Compiled: compiled.Summary}
	}
	catalog := NewCatalog(reader)
	got, err := catalog.Candidates(t.Context(), "tenant", candidateFields(Fields{Values: map[string]any{"source_id": "none"}}))
	if err != nil || len(got) != 0 || reader.calls != 1 {
		t.Fatalf("10000 policies: candidates=%d bundles=%d err=%v", len(got), reader.calls, err)
	}
	if _, ok := catalog.FindLatest(Scope{TenantID: "tenant", Kind: Suppression}, "p09999"); !ok {
		t.Fatal("bundle truncated")
	}
}

type canceledCatalogReader struct{ cancel context.CancelFunc }

func (r canceledCatalogReader) List(context.Context, Scope, string, int) ([]Record, error) {
	return nil, nil
}

func (r canceledCatalogReader) PublishedPolicies(context.Context, string) ([]Release, error) {
	r.cancel()
	return nil, nil
}

func TestCatalogDoesNotPublishCanceledSuccessfulRead(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := NewCatalog(canceledCatalogReader{cancel: cancel})
	if _, err := c.Freeze(ctx, "tenant"); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled read was published", err)
	}
	if len(c.entries) != 0 {
		t.Fatal("canceled read cached")
	}
}

func TestCatalogChecksCancellationAgainBeforePublishingCompiledEntry(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := NewCatalog(prepublicationRecords{})
	calls := 0
	c.now = func() time.Time {
		calls++
		// First two calls check the cache; the third assigns the newly built entry expiry.
		if calls == 3 {
			cancel()
		}
		return time.Now()
	}
	if _, err := c.Freeze(ctx, "tenant"); !errors.Is(err, context.Canceled) {
		t.Fatal("entry published after compilation deadline", err)
	}
	if len(c.entries) != 0 {
		t.Fatal("canceled compiled entry cached")
	}
}

type prepublicationRecords struct{}

func (prepublicationRecords) List(context.Context, Scope, string, int) ([]Record, error) {
	return nil, nil
}
