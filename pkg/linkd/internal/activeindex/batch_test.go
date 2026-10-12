// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package activeindex

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

type batchReader func(context.Context, Query) ([]Row, error)

func (f batchReader) ReadActiveIndex(ctx context.Context, q Query) ([]Row, error) { return f(ctx, q) }

type batchCache struct {
	*fakeCache
	held map[Scope]string
	busy Scope
	fail Scope
}

func (c *batchCache) Acquire(_ context.Context, scope Scope, _ time.Duration) (string, error) {
	if scope == c.busy {
		return "", nil
	}
	c.held[scope] = "lease"
	return "lease", nil
}

func (c *batchCache) Release(_ context.Context, scope Scope, token string) error {
	if c.held[scope] == token {
		delete(c.held, scope)
	}
	return nil
}

func (c *batchCache) Publish(ctx context.Context, p Pending, lease string, members []string) error {
	if c.held[p.Scope] != lease || p.Scope == c.fail {
		return errors.New("lease lost or publish unavailable")
	}
	return c.fakeCache.Publish(ctx, p, lease, members)
}

func newBatchCache(scopes ...Scope) *batchCache {
	c := &batchCache{fakeCache: &fakeCache{values: map[Scope][]string{}, pending: map[Scope]string{}}, held: map[Scope]string{}}
	for _, scope := range scopes {
		c.pending[scope] = "old"
		c.values[scope] = []string{"stale"}
	}
	return c
}

func TestBatchReadsOncePerTenantAfterAcquiringAllLeases(t *testing.T) {
	a, b, closed, other := Scope{"tenant", "a"}, Scope{"tenant", "b"}, Scope{"tenant", "closed"}, Scope{"other", "a"}
	cache := newBatchCache(a, b, closed, other)
	reads := map[string]int{}
	reader := batchReader(func(_ context.Context, q Query) ([]Row, error) {
		if q.Validate() != nil || q.Scope != nil || q.BKTenantID == "" {
			t.Fatal("batch did not use a bounded tenant query")
		}
		reads[q.BKTenantID]++
		for _, scope := range []Scope{a, b, closed, other} {
			if scope.BKTenantID == q.BKTenantID {
				if cache.held[scope] == "" {
					t.Fatal("snapshot read before acquiring all publishing leases")
				}
				cache.pending[scope] = "new"
			}
		}
		return []Row{activeRow("source-a", q.BKTenantID, "a", "fp-a"), activeRow("source-b", q.BKTenantID, "b", "fp-b")}, nil
	})
	manager := testManager(t, reader, cache)
	manager.nextDiscovery = time.Now().Add(time.Hour)
	if err := manager.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reads["tenant"] != 1 || reads["other"] != 1 || len(cache.held) != 0 {
		t.Fatalf("reads=%v leases=%v", reads, cache.held)
	}
	if !slices.Equal(cache.values[a], []string{"fp-a"}) || !slices.Equal(cache.values[b], []string{"fp-b"}) || len(cache.values[closed]) != 0 || !slices.Equal(cache.values[other], []string{"fp-a"}) {
		t.Fatal("batch mixed or lost strategy membership")
	}
	if _, published := cache.values[Scope{"other", "b"}]; published {
		t.Fatal("published an unleased strategy from the shared snapshot")
	}
	for _, scope := range []Scope{a, b, closed, other} {
		if cache.pending[scope] != "new" {
			t.Fatal("hint arriving during shared scan was acknowledged")
		}
	}
}

func TestBatchBusyOrLostLeaseCannotPublish(t *testing.T) {
	busy, lost, ready := Scope{"t", "busy"}, Scope{"t", "lost"}, Scope{"t", "ready"}
	cache := newBatchCache(busy, lost, ready)
	cache.busy = busy
	reads := 0
	reader := batchReader(func(context.Context, Query) ([]Row, error) {
		reads++
		cache.held[lost] = "successor"
		return nil, nil
	})
	manager := testManager(t, reader, cache)
	manager.nextDiscovery = time.Now().Add(time.Hour)
	if err := manager.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reads != 1 || cache.failures != 1 || cache.held[lost] != "successor" || len(cache.values[ready]) != 0 {
		t.Fatal("lease loss affected independent strategy or released successor")
	}
	for _, scope := range []Scope{busy, lost} {
		if !slices.Equal(cache.values[scope], []string{"stale"}) || cache.pending[scope] == "" {
			t.Fatal("unleased strategy changed or its hint was lost")
		}
	}
}

func TestBatchReadFailureAndPublishFailureIsolation(t *testing.T) {
	for _, mode := range []string{"partial", "wrong-tenant", "publish"} {
		t.Run(mode, func(t *testing.T) {
			a, b := Scope{"t", "a"}, Scope{"t", "b"}
			cache := newBatchCache(a, b)
			reader := batchReader(func(context.Context, Query) ([]Row, error) {
				if mode == "partial" {
					return []Row{activeRow("source-a", "t", "a", "fp")}, errors.New("partial snapshot")
				}
				if mode == "wrong-tenant" {
					return []Row{activeRow("source-a", "other", "", "fp")}, nil
				}
				cache.fail = a
				return nil, nil
			})
			manager := testManager(t, reader, cache)
			manager.nextDiscovery = time.Now().Add(time.Hour)
			if err := manager.Step(t.Context()); err != nil {
				t.Fatal(err)
			}
			wantFailed := 2
			if mode == "publish" {
				wantFailed = 1
				if len(cache.values[b]) != 0 || cache.pending[b] != "" {
					t.Fatal("one publish failure blocked the other strategy")
				}
			} else if !slices.Equal(cache.values[b], []string{"stale"}) {
				t.Fatal("incomplete snapshot erased a strategy")
			}
			if cache.failures != wantFailed || !slices.Equal(cache.values[a], []string{"stale"}) || cache.pending[a] == "" || len(cache.held) != 0 {
				t.Fatal("failure lost snapshot, hint, or lease cleanup")
			}
		})
	}
}

func TestBatchCancellationReleasesLeases(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cache := newBatchCache(Scope{"t", "a"}, Scope{"t", "b"})
	reader := batchReader(func(context.Context, Query) ([]Row, error) { cancel(); return nil, ctx.Err() })
	manager := testManager(t, reader, cache)
	manager.nextDiscovery = time.Now().Add(time.Hour)
	if err := manager.Step(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	if len(cache.held) != 0 || len(cache.pending) != 2 {
		t.Fatal("cancellation leaked leases or acknowledged pending work")
	}
}

func TestTenantQueryScopeValidation(t *testing.T) {
	query := Query{Sources: []string{"source"}, BKTenantID: "tenant", MaxRows: 10, MaxBytes: 1024}
	if err := query.Validate(); err != nil || query.TenantID() != "tenant" {
		t.Fatalf("tenant query=%v", err)
	}
	query.Scope = &Scope{"other", "strategy"}
	if err := query.Validate(); err == nil {
		t.Fatal("conflicting tenant and strategy scopes accepted")
	}
	query.Scope, query.BKTenantID = nil, "tenant:invalid"
	if err := query.Validate(); err == nil {
		t.Fatal("invalid tenant accepted")
	}
}
