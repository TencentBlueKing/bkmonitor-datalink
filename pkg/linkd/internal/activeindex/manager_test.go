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
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"linkd/internal/domain"
)

type fakeReader struct {
	rows          []Row
	fail          bool
	failDiscovery bool
	block         bool
	calls         int
}

func (r *fakeReader) ReadActiveIndex(ctx context.Context, q Query) ([]Row, error) {
	r.calls++
	if r.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if r.fail || (q.Scope == nil && r.failDiscovery) {
		return nil, errors.New("partial snapshot")
	}
	result := []Row{}
	for _, row := range r.rows {
		if slices.Contains(q.Sources, row.EventSourceID) && (q.Scope == nil || q.Scope.BKTenantID == row.BKTenantID) {
			result = append(result, row)
		}
	}
	return result, nil
}

func TestFailedGlobalDiscoveryStillRepairsKnownStrategies(t *testing.T) {
	s := Scope{"tenant", "123"}
	c := &fakeCache{values: map[Scope][]string{s: {"stale"}}, pending: map[Scope]string{}}
	m := testManager(t, &fakeReader{failDiscovery: true}, c)
	if err := m.Step(t.Context()); err == nil {
		t.Fatal("global discovery failure not reported")
	}
	if len(c.values[s]) != 0 {
		t.Fatal("global discovery failure blocked complete per-strategy repair")
	}
}

type fakeCache struct {
	values   map[Scope][]string
	pending  map[Scope]string
	failure  bool
	during   func()
	failures int
}

func (c *fakeCache) ReportDiscovery(context.Context, bool) error { return nil }

func (c *fakeCache) Discover(context.Context) ([]Scope, error) {
	out := []Scope{}
	for s := range c.values {
		out = append(out, s)
	}
	return out, nil
}

func (c *fakeCache) Enqueue(_ context.Context, s Scope) error { c.pending[s] = "token"; return nil }

func (c *fakeCache) Pending(_ context.Context, limit int) ([]Pending, error) {
	out := []Pending{}
	for s, v := range c.pending {
		out = append(out, Pending{s, v})
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (c *fakeCache) Acquire(context.Context, Scope, time.Duration) (string, error) {
	return "lease", nil
}

func (c *fakeCache) Release(context.Context, Scope, string) error { return nil }

func (c *fakeCache) Failed(context.Context, Pending, string, string) error { c.failures++; return nil }

func (c *fakeCache) Publish(_ context.Context, p Pending, _ string, members []string) error {
	if c.failure {
		return errors.New("unavailable")
	}
	if c.during != nil {
		c.during()
	}
	c.values[p.Scope] = slices.Clone(members)
	if c.pending[p.Scope] == p.Token {
		delete(c.pending, p.Scope)
	}
	return nil
}

func testManager(t *testing.T, r Reader, c Cache) *Manager {
	t.Helper()
	m, err := NewManager(r, c, []string{"source-a", "source-b"}, Settings{PollInterval: time.Millisecond, ReconcileInterval: time.Minute, OperationTimeout: 20 * time.Millisecond, BatchSize: 16, MaxRows: 1000, MaxBytes: 1 << 20}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func activeRow(source, tenant, strategy, fp string) Row {
	return Row{BKTenantID: tenant, EventSourceID: source, Fingerprint: fp, Labels: domain.DimensionMap{"strategy_id": domain.NewStringScalar(strategy)}}
}

func TestUnionAndRecoveryWithoutHints(t *testing.T) {
	s := Scope{"tenant", "123"}
	stale := Scope{"tenant", "closed"}
	c := &fakeCache{values: map[Scope][]string{stale: {"old"}}, pending: map[Scope]string{}}
	r := &fakeReader{rows: []Row{activeRow("source-a", "tenant", "123", "fp"), activeRow("source-b", "tenant", "123", "fp"), activeRow("source-b", "other", "123", "other-fp")}}
	m := testManager(t, r, c)
	if err := m.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.values[s], []string{"fp"}) || len(c.values[stale]) != 0 || !slices.Equal(c.values[Scope{"other", "123"}], []string{"other-fp"}) {
		t.Fatalf("snapshot=%v", c.values)
	}
	r.rows = r.rows[1:]
	_ = c.Enqueue(t.Context(), s)
	if err := m.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.values[s], []string{"fp"}) {
		t.Fatal("closing one source removed shared member")
	}
	r.rows = nil
	// 模拟重启且最后一个关闭提示丢失：周期发现正式集合，最终发布空结果。
	m = testManager(t, r, c)
	if err := m.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(c.values[s]) != 0 {
		t.Fatal("orphan not removed after restart")
	}
}

func TestFailuresPreserveSnapshotAndRetry(t *testing.T) {
	for _, failure := range []string{"read", "publish", "invalid"} {
		t.Run(failure, func(t *testing.T) {
			s := Scope{"tenant", "123"}
			c := &fakeCache{values: map[Scope][]string{s: {"old"}}, pending: map[Scope]string{s: "token"}}
			r := &fakeReader{rows: []Row{activeRow("source-a", "tenant", "123", "new")}, fail: failure == "read"}
			c.failure = failure == "publish"
			if failure == "invalid" {
				r.rows[0].Fingerprint = ""
			}
			m := testManager(t, r, c)
			_ = m.Step(t.Context())
			if !slices.Equal(c.values[s], []string{"old"}) || len(c.pending) != 1 || c.failures != 1 {
				t.Fatal("failed refresh damaged current snapshot")
			}
			r.fail = false
			c.failure = false
			r.rows[0].Fingerprint = "new"
			if err := m.Step(t.Context()); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(c.values[s], []string{"new"}) || len(c.pending) != 0 {
				t.Fatal("retry did not recover")
			}
		})
	}
}

func TestHintDuringRefreshIsNotAcknowledged(t *testing.T) {
	s := Scope{"tenant", "123"}
	c := &fakeCache{values: map[Scope][]string{}, pending: map[Scope]string{s: "old"}}
	c.during = func() { c.pending[s] = "new" }
	m := testManager(t, &fakeReader{}, c)
	m.nextDiscovery = time.Now().Add(time.Hour)
	if err := m.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	if c.pending[s] != "new" {
		t.Fatal("new hint lost")
	}
}

func TestManagerCancellation(t *testing.T) {
	c := &fakeCache{values: map[Scope][]string{}, pending: map[Scope]string{}}
	m := testManager(t, &fakeReader{block: true}, c)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("manager did not stop")
	}
}

func TestScopeAndNumericBoundaries(t *testing.T) {
	for _, prefix := range []string{"linkd", "linkd:active-index", "linkd:active-index:custom"} {
		if ValidatePrefix(prefix) == nil {
			t.Fatalf("reserved namespace accepted: %s", prefix)
		}
	}
	for _, value := range []string{"00123", "1e3", "NaN", "+1"} {
		if _, ok := NumericStrategy(value); ok {
			t.Fatalf("accepted %s", value)
		}
	}
	if n, ok := NumericStrategy("123"); !ok || n != 123 {
		t.Fatal("numeric strategy rejected")
	}
	for _, s := range []Scope{{"a", "s:x"}, {"a", "123"}} {
		got, err := ParseKey("a:b", s.Key("a:b"))
		if err != nil || got != s {
			t.Fatal("scope roundtrip")
		}
	}
}

func TestGroupUsesEnrichedStrategyInsteadOfRawLabel(t *testing.T) {
	row := Row{BKTenantID: "t", EventSourceID: "host", Fingerprint: "fp", Labels: domain.DimensionMap{"strategy_id": domain.NewStringScalar("old")}, Enrich: domain.JSONObject{"processors": json.RawMessage(`[{"fields":{"status":"succeeded","patches":[{"op":"set","path":"$.extra_data.items[0].name","value":"renamed"},{"op":"set","path":"$.labels.strategy_id","value":"new"}]}}]`)}}
	q := Query{Sources: []string{"host"}, Scope: &Scope{BKTenantID: "t", StrategyID: "new"}, MaxRows: 10, MaxBytes: 1024}
	groups, err := Group(q, []Row{row})
	if err != nil || len(groups[*q.Scope]) != 1 {
		t.Fatalf("groups=%v err=%v", groups, err)
	}
	if id, _, _ := StrategyID(row.Labels); id != "old" {
		t.Fatal("original label changed")
	}
}

func TestRunObserverIncludesIsolatedStrategyFailures(t *testing.T) {
	scope := Scope{"tenant", "123"}
	cache := &fakeCache{values: map[Scope][]string{scope: {"old"}}, pending: map[Scope]string{scope: "token"}, failure: true}
	manager := testManager(t, &fakeReader{rows: []Row{activeRow("source-a", "tenant", "123", "new")}}, cache)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	observed := false
	err := manager.Run(ctx, func(_ context.Context, _ time.Duration, work, failed int, err error) {
		observed = true
		if work != 1 || failed != 1 || err != nil {
			t.Errorf("work=%d failed=%d error=%v", work, failed, err)
		}
		cancel()
	})
	if err != nil || !observed {
		t.Fatal("missing full round observation", err)
	}
	if cache.values[scope][0] != "old" {
		t.Fatal("instrumentation changed failure isolation")
	}
}
