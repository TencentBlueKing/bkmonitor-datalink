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
	"fmt"
	"slices"
	"testing"
	"time"
)

type batchReader func(context.Context, Query, func([]Row) error) error

func (f batchReader) ScanActiveIndex(ctx context.Context, q Query, consume func([]Row) error) error {
	return f(ctx, q, consume)
}

type batchCache struct {
	*fakeCache
	held string
	busy bool
	fail Scope
}

func (c *batchCache) Acquire(ctx context.Context, ttl time.Duration) (string, error) {
	if c.busy {
		return "", nil
	}
	token, err := c.fakeCache.Acquire(ctx, ttl)
	c.held = token
	return token, err
}
func (c *batchCache) Release(ctx context.Context, token string, scopes []Scope) error {
	if c.held == token {
		c.held = ""
	}
	return c.fakeCache.Release(ctx, token, scopes)
}
func (c *batchCache) Append(ctx context.Context, scope Scope, lease string, members []string) (int64, error) {
	if c.held != lease || scope == c.fail {
		return 0, errors.New("lease lost or staging unavailable")
	}
	return c.fakeCache.Append(ctx, scope, lease, members)
}
func (c *batchCache) Publish(ctx context.Context, p Pending, lease string, count int64) error {
	if c.held != lease || p.Scope == c.fail {
		return errors.New("lease lost or publish unavailable")
	}
	return c.fakeCache.Publish(ctx, p, lease, count)
}
func newBatchCache(scopes ...Scope) *batchCache {
	c := &batchCache{fakeCache: &fakeCache{values: map[Scope][]string{}, pending: map[Scope]string{}}}
	for _, scope := range scopes {
		c.pending[scope] = "old"
		c.values[scope] = []string{"stale"}
	}
	return c
}

func TestLargeScanSharedAcrossStrategiesAndTenants(t *testing.T) {
	const total = 200000
	scopes := make([]Scope, 128)
	for i := range scopes {
		scopes[i] = Scope{fmt.Sprintf("tenant-%d", i%4), fmt.Sprint(i)}
	}
	closed := Scope{"tenant-0", "closed"}
	cache := newBatchCache(append(scopes, closed)...)
	calls, pages := 0, 0
	reader := batchReader(func(ctx context.Context, q Query, consume func([]Row) error) error {
		calls++
		if cache.held == "" || q.Scope != nil || q.BKTenantID != "" {
			t.Fatal("scan did not hold target lease or share all tenants")
		}
		for start := 0; start < total; start += q.PageSize {
			page := make([]Row, 0, q.PageSize)
			for i := start; i < min(start+q.PageSize, total); i++ {
				scope := scopes[i%len(scopes)]
				page = append(page, activeRow("source-a", scope.BKTenantID, scope.StrategyID, fmt.Sprint(i)))
			}
			pages++
			if err := consume(page); err != nil {
				return err
			}
			// 正式集合在最后一页返回之前保持旧值。
			if !slices.Equal(cache.values[scopes[0]], []string{"stale"}) {
				t.Fatal("partial scan published")
			}
		}
		for _, scope := range scopes {
			cache.pending[scope] = "new"
		}
		return ctx.Err()
	})
	manager := testManager(t, reader, cache)
	manager.settings.OperationTimeout = time.Minute
	manager.settings.PageSize = 2000
	if err := manager.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || pages != 100 || cache.held != "" || len(cache.values[closed]) != 0 {
		t.Fatalf("calls=%d pages=%d lease=%s", calls, pages, cache.held)
	}
	members := 0
	for _, scope := range scopes {
		members += len(cache.values[scope])
		if cache.pending[scope] != "new" {
			t.Fatal("new hint acknowledged")
		}
	}
	if members != total {
		t.Fatalf("members=%d", members)
	}
}

func TestScanFailuresPreserveSnapshotsAndIsolateStrategies(t *testing.T) {
	for _, mode := range []string{"later-page", "invalid", "staging", "publish", "lease-lost"} {
		t.Run(mode, func(t *testing.T) {
			a, b := Scope{"t", "a"}, Scope{"other", "b"}
			cache := newBatchCache(a, b)
			reader := batchReader(func(_ context.Context, _ Query, consume func([]Row) error) error {
				if mode == "staging" {
					cache.fail = a
				}
				if err := consume([]Row{activeRow("source-a", "t", "a", "fp-a"), activeRow("source-b", "other", "b", "fp-b")}); err != nil {
					return err
				}
				switch mode {
				case "later-page":
					return errors.New("second page failed")
				case "invalid":
					return consume([]Row{activeRow("source-a", "t", "a", "")})
				case "publish":
					cache.fail = a
				case "lease-lost":
					cache.held = "successor"
				}
				return nil
			})
			manager := testManager(t, reader, cache)
			manager.nextDiscovery = time.Now().Add(time.Hour)
			err := manager.Step(t.Context())
			if (err != nil) != (mode == "later-page" || mode == "invalid") {
				t.Fatalf("scan error=%v", err)
			}
			if !slices.Equal(cache.values[a], []string{"stale"}) || cache.pending[a] == "" {
				t.Fatal("failure lost old snapshot or pending hint")
			}
			isolated := mode == "staging" || mode == "publish"
			if isolated {
				if !slices.Equal(cache.values[b], []string{"fp-b"}) || cache.failures != 1 {
					t.Fatal("one strategy blocked another")
				}
			} else if !slices.Equal(cache.values[b], []string{"stale"}) || cache.failures != 2 {
				t.Fatal("partial or unleased scan published")
			}
			if mode == "lease-lost" && cache.held != "successor" {
				t.Fatal("released successor")
			}
			if cache.staged != nil {
				t.Fatal("temporary sets leaked")
			}
		})
	}
}

func TestTargetLeaseAndIdleRoundsAvoidScanning(t *testing.T) {
	cache := newBatchCache(Scope{"t", "a"})
	reader := &fakeReader{}
	manager := testManager(t, reader, cache)
	cache.busy = true
	if err := manager.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reader.calls != 0 {
		t.Fatal("concurrent owner scanned")
	}
	cache.busy = false
	if err := manager.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reader.calls != 1 {
		t.Fatal("discovery and refresh scanned separately")
	}
	if err := manager.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reader.calls != 1 {
		t.Fatal("idle round scanned")
	}
}

func TestScanCancellationReleasesLeaseAndStaging(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cache := newBatchCache(Scope{"t", "a"})
	reader := batchReader(func(_ context.Context, _ Query, consume func([]Row) error) error {
		if err := consume([]Row{activeRow("source-a", "t", "a", "new")}); err != nil {
			return err
		}
		cancel()
		return ctx.Err()
	})
	manager := testManager(t, reader, cache)
	if err := manager.Step(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	if cache.held != "" || cache.staged != nil || len(cache.pending) != 1 || cache.values[Scope{"t", "a"}][0] != "stale" {
		t.Fatal("cancel lost work or leaked lease")
	}
}

func TestTenantQueryScopeValidation(t *testing.T) {
	query := Query{Sources: []string{"source"}, BKTenantID: "tenant", PageSize: 10, MaxBytes: 1024}
	if err := query.Validate(); err != nil || query.TenantID() != "tenant" {
		t.Fatalf("tenant query=%v", err)
	}
	query.Scope = &Scope{"other", "strategy"}
	if err := query.Validate(); err == nil {
		t.Fatal("conflicting tenant and strategy accepted")
	}
	query.Scope, query.BKTenantID = nil, "tenant:invalid"
	if err := query.Validate(); err == nil {
		t.Fatal("invalid tenant accepted")
	}
}
