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
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/onemodel"
	"linkd/internal/policy"
	"linkd/internal/store/storetest"
)

type dependencyClock struct{ nanos atomic.Int64 }

func newDependencyClock(at time.Time) *dependencyClock {
	c := &dependencyClock{}
	c.nanos.Store(at.UnixNano())
	return c
}

func (c *dependencyClock) Now() time.Time { return time.Unix(0, c.nanos.Load()).UTC() }

func (c *dependencyClock) Advance(duration time.Duration) { c.nanos.Add(int64(duration)) }

func setDependencyClock(d *Dependencies, clock *dependencyClock) {
	d.mu.Lock()
	d.now = clock.Now
	d.mu.Unlock()
}

func testDependencyTarget() *onemodel.TargetDescriptor {
	return &onemodel.TargetDescriptor{SchemaVersion: 1, ModelID: "cw-Host", Selectors: []onemodel.TargetSelector{{Type: "instances", Instances: []onemodel.InstanceRef{{ModelID: "cw-Host", InstanceID: "1", EntityUID: "cw-Host|1"}}}}}
}

func dependencyValue(request DependencyRequest, at time.Time) DependencySnapshot {
	scope := onemodel.TargetScope{TenantID: request.TenantID, SpaceCode: request.SpaceCode, BusinessIDs: []int64{2}}
	value := DependencySnapshot{LoadedAt: at, Scope: scope, Targets: onemodel.TargetResult{Scope: scope}}
	if request.Target != nil {
		value.Targets.Instances = []onemodel.InstanceRef{{ModelID: request.Target.ModelID, InstanceID: "1", EntityUID: request.Target.ModelID + "|1"}}
	}
	if request.Kind == "relations" {
		value.Related = []onemodel.InstanceRef{{ModelID: request.ModelID, InstanceID: "1", EntityUID: request.ModelID + "|1"}}
	}
	return value
}

func refreshDependencyNow(t *testing.T, d *Dependencies, request DependencyRequest) {
	t.Helper()
	key, err := dependencyKey(request)
	if err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	entry := d.entries[key]
	if entry == nil || entry.loading != nil {
		d.mu.Unlock()
		t.Fatal("dependency is not ready for refresh")
	}
	entry.loading = make(chan struct{})
	d.mu.Unlock()
	d.refresh(key, entry)
}

func TestDependenciesCoalesceConcurrentAndShareAcrossDescriptors(t *testing.T) {
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	d := NewDependencies(t.Context(), func(ctx context.Context, r DependencyRequest) (DependencySnapshot, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
		case <-ctx.Done():
			return DependencySnapshot{}, ctx.Err()
		}
		return dependencyValue(r, time.Now()), nil
	})
	t.Cleanup(d.Close)
	const workers = 24
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			target := testDependencyTarget() // 不同策略各自分配 descriptor，内容相同仍共享一份源结果。
			found, err := d.ContainsTarget(t.Context(), "tenant", "bkcc__2", target, target.Selectors[0].Instances[0])
			if err != nil || !found {
				t.Error(found, err)
			}
		})
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("source not called")
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate source calls", calls.Load())
	}
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("source was read per strategy", calls.Load())
	}
	scope, err := d.ResolveBusinessScope(t.Context(), "tenant", "bkcc__2", 2)
	if err != nil || len(scope.BusinessIDs) != 1 {
		t.Fatal(scope, err)
	}
	if calls.Load() != 2 {
		t.Fatal("scope has its own shared dependency", calls.Load())
	}
	if _, err = d.ResolveBusinessScope(t.Context(), "other", "bkcc__2", 2); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatal("different tenant used cached data", calls.Load())
	}
}

func TestDependenciesFreshnessFailureAndSourceReadTracking(t *testing.T) {
	clock := newDependencyClock(time.Now().UTC())
	var fail atomic.Bool
	d := NewDependencies(t.Context(), func(_ context.Context, r DependencyRequest) (DependencySnapshot, error) {
		if fail.Load() {
			return DependencySnapshot{}, fmt.Errorf("source down")
		}
		return dependencyValue(r, clock.Now()), nil
	})
	t.Cleanup(d.Close)
	setDependencyClock(d, clock)
	request := DependencyRequest{Kind: "scope", TenantID: "tenant", SpaceCode: "bkcc__2"}
	first, err := d.Read(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(45 * time.Second)
	fail.Store(true)
	refreshDependencyNow(t, d, request)
	cached, err := d.Read(t.Context(), request)
	if err != nil || !cached.LoadedAt.Equal(first.LoadedAt) {
		t.Fatal("failed refresh replaced a still-fresh source snapshot", cached, err)
	}
	tracked, stamp := TrackDependencyReads(t.Context())
	if _, err = d.ResolveBusinessScope(tracked, "tenant", "bkcc__2", 2); err != nil {
		t.Fatal(err)
	}
	if !stamp.Time().Equal(first.ChangedAt) || !first.ChangedAt.Equal(first.LoadedAt) {
		t.Fatal("tracked request time instead of content time", stamp.Time(), first)
	}
	clock.Advance(15 * time.Second)
	if _, err = d.Read(t.Context(), request); !errors.Is(err, policy.ErrUnavailable) {
		t.Fatal("60s old dependency remained usable", err)
	}
	fail.Store(false)
	refreshDependencyNow(t, d, request)
	fresh, err := d.Read(t.Context(), request)
	if err != nil || !fresh.LoadedAt.Equal(clock.Now()) {
		t.Fatal("fresh retry unavailable", fresh, err)
	}
	if !fresh.ChangedAt.Equal(first.ChangedAt) {
		t.Fatal("unchanged retry advanced content time", fresh, first)
	}
	if got := d.DrainChangedTenants(); len(got) != 0 {
		t.Fatal("timestamp alone looked like content change", got)
	}
}

func TestDependenciesContentTimeOnlyAdvancesOnChanges(t *testing.T) {
	for _, kind := range []string{"scope", "targets", "relations"} {
		t.Run(kind, func(t *testing.T) {
			clock := newDependencyClock(time.Now().UTC())
			var changed atomic.Bool
			request := DependencyRequest{Kind: kind, TenantID: "tenant", SpaceCode: "bkcc__2", Target: testDependencyTarget(), ModelID: "cw-Host"}
			d := NewDependencies(t.Context(), func(_ context.Context, r DependencyRequest) (DependencySnapshot, error) {
				value := dependencyValue(r, clock.Now())
				if changed.Load() {
					switch kind {
					case "scope":
						value.Scope.BusinessIDs = []int64{3}
					case "targets":
						value.Targets.Instances = nil
					case "relations":
						value.Related = nil
					}
				}
				return value, nil
			})
			t.Cleanup(d.Close)
			setDependencyClock(d, clock)
			first, err := d.Read(t.Context(), request)
			if err != nil || !first.ChangedAt.Equal(first.LoadedAt) {
				t.Fatal("initial content timestamp", first, err)
			}
			clock.Advance(30 * time.Second)
			refreshDependencyNow(t, d, request)
			same, err := d.Read(t.Context(), request)
			if err != nil || !same.LoadedAt.Equal(clock.Now()) || !same.ChangedAt.Equal(first.ChangedAt) {
				t.Fatal("healthy refresh changed content version", first, same, err)
			}
			clock.Advance(time.Second)
			changed.Store(true)
			refreshDependencyNow(t, d, request)
			next, err := d.Read(t.Context(), request)
			if err != nil || !next.LoadedAt.Equal(clock.Now()) || !next.ChangedAt.Equal(next.LoadedAt) || !next.ChangedAt.After(same.ChangedAt) {
				t.Fatal("changed content retained old version", same, next, err)
			}
			clock.Advance(60 * time.Second)
			if _, err := d.Read(t.Context(), request); !errors.Is(err, policy.ErrUnavailable) {
				t.Fatal("content timestamp extended freshness", err)
			}
		})
	}
}

func TestDependenciesPreparedTargetsKeepScopeAndMembershipOnOneVersion(t *testing.T) {
	clock := newDependencyClock(time.Now().UTC())
	var removed, targetRemoved atomic.Bool
	var scopeReads, targetReads atomic.Int32
	d := NewDependencies(t.Context(), func(_ context.Context, request DependencyRequest) (DependencySnapshot, error) {
		value := dependencyValue(request, clock.Now())
		if request.Kind == "scope" {
			scopeReads.Add(1)
			if removed.Load() {
				value.Scope.BusinessIDs = nil
			}
		} else {
			targetReads.Add(1)
			// 完整展开结果的业务范围与外层 Scope 无关。
			value.Scope.BusinessIDs = []int64{99}
			if targetRemoved.Load() {
				value.Targets.Scope.BusinessIDs = nil
				value.Targets.Instances = nil
			}
		}
		return value, nil
	})
	t.Cleanup(d.Close)
	setDependencyClock(d, clock)
	target := testDependencyTarget()
	row := snapshotRecord(t, "target", "level", "warning", nil)
	var raw map[string]any
	if err := json.Unmarshal(row.Spec, &raw); err != nil {
		t.Fatal(err)
	}
	raw["model_id"], raw["target_descriptor"] = target.ModelID, target
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := policy.Compile(policy.Suppression, encoded)
	if err != nil {
		t.Fatal(err)
	}
	release := policy.Release{Scope: row.Scope, ID: row.ID, Version: 1, Spec: compiled.Canonical, Compiled: compiled.Summary}
	event := storetest.Event("tenant", "prepared-target", "fp", "warning")
	event.Dimensions["bk_biz_id"] = domain.NewStringScalar("2")
	event.Labels["model_id"], event.Labels["model_inst_id"] = domain.NewStringScalar("cw-Host"), domain.NewStringScalar("1")
	event, err = event.WithEnrichment(storetest.Enrichment(event))
	if err != nil {
		t.Fatal(err)
	}
	view, err := policy.EventView(event, "warning", nil, nil, policy.RelationContext{})
	if err != nil {
		t.Fatal(err)
	}
	qualify := func() time.Time {
		t.Helper()
		ctx, stamp := TrackDependencyReads(t.Context())
		result, err := policy.EvaluateConditions(ctx, release, compiled, view, d, event.CreateAt.Add(time.Hour), false)
		if err != nil || !result.Evaluated || !result.Matched {
			t.Fatal("complete target snapshot did not qualify", result, err)
		}
		return stamp.Time()
	}
	first := qualify()
	if scopeReads.Load() != 0 || targetReads.Load() != 1 {
		t.Fatal("target rule read a separate scope source", scopeReads.Load(), targetReads.Load())
	}
	scopeRequest := DependencyRequest{Kind: "scope", TenantID: "tenant", SpaceCode: "bkcc__2"}
	if _, err := d.Read(t.Context(), scopeRequest); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	removed.Store(true)
	refreshDependencyNow(t, d, scopeRequest)
	if stamp := qualify(); !stamp.Equal(first) {
		t.Fatal("independent scope removal advanced target rule floor", first, stamp)
	}
	clock.Advance(time.Second)
	removed.Store(false)
	refreshDependencyNow(t, d, scopeRequest)
	if stamp := qualify(); !stamp.Equal(first) || targetReads.Load() != 1 {
		t.Fatal("scope restore left target rule behind another source floor", first, stamp, targetReads.Load())
	}
	fixed, err := d.PrepareTargets(t.Context(), "tenant", "bkcc__2", target)
	if err != nil {
		t.Fatal(err)
	}
	targetRemoved.Store(true)
	clock.Advance(time.Second)
	refreshDependencyNow(t, d, DependencyRequest{Kind: "targets", TenantID: "tenant", SpaceCode: "bkcc__2", Target: target})
	ctx, stamp := TrackDependencyReads(t.Context())
	scope, err := fixed.ResolveBusinessScope(ctx, "tenant", "bkcc__2", 2)
	if err != nil || len(scope.BusinessIDs) != 1 {
		t.Fatal("prepared scope changed during evaluation", scope, err)
	}
	if found, err := fixed.(policy.TargetMembershipReader).ContainsTarget(ctx, "tenant", "bkcc__2", target, target.Selectors[0].Instances[0]); err != nil || !found || !stamp.Time().Equal(first) {
		t.Fatal("prepared membership changed during evaluation", found, stamp.Time(), err)
	}
	latest, err := d.PrepareTargets(t.Context(), "tenant", "bkcc__2", target)
	if err != nil {
		t.Fatal(err)
	}
	scope, err = latest.ResolveBusinessScope(ctx, "tenant", "bkcc__2", 2)
	if err != nil || len(scope.BusinessIDs) != 0 || !stamp.Time().After(first) {
		t.Fatal("new complete snapshot lost scope/content change", scope, stamp.Time(), err)
	}
}

func TestDependenciesChangedContentsCoalesceAndKeepReturnedSnapshot(t *testing.T) {
	clock := newDependencyClock(time.Now().UTC())
	var member atomic.Int64
	member.Store(2)
	d := NewDependencies(t.Context(), func(_ context.Context, r DependencyRequest) (DependencySnapshot, error) {
		value := dependencyValue(r, clock.Now())
		value.Scope.BusinessIDs = []int64{member.Load()}
		return value, nil
	})
	t.Cleanup(d.Close)
	setDependencyClock(d, clock)
	request := DependencyRequest{Kind: "scope", TenantID: "tenant", SpaceCode: "bkcc__2"}
	first, err := d.Read(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	member.Store(3)
	refreshDependencyNow(t, d, request)
	clock.Advance(time.Second)
	member.Store(4)
	refreshDependencyNow(t, d, request)
	if first.Scope.BusinessIDs[0] != 2 {
		t.Fatal("returned snapshot mutated")
	}
	changed := d.DrainChangedTenants()
	if len(changed) != 1 || changed[0] != "tenant" || len(d.DrainChangedTenants()) != 0 {
		t.Fatal("change notifications not coalesced", changed)
	}
	scope, err := d.ResolveBusinessScope(t.Context(), "tenant", "bkcc__2", 4)
	if err != nil || len(scope.BusinessIDs) != 1 {
		t.Fatal(scope, err)
	}
}

func TestDependenciesCancellationAndCloseCancelLoad(t *testing.T) {
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	d := NewDependencies(t.Context(), func(ctx context.Context, _ DependencyRequest) (DependencySnapshot, error) {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		return DependencySnapshot{}, ctx.Err()
	})
	request := DependencyRequest{Kind: "scope", TenantID: "tenant", SpaceCode: "bkcc__2"}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := d.Read(ctx, request); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("load did not start")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() { d.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close failed to cancel source load")
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("source context not cancelled")
	}
	if _, err := d.Read(t.Context(), request); !errors.Is(err, context.Canceled) {
		t.Fatal("closed cache accepted read", err)
	}
	d.Close()
}

func TestDependenciesConcurrentCloseAndFirstReads(t *testing.T) {
	for range 30 {
		var started, finished atomic.Int32
		d := NewDependencies(t.Context(), func(ctx context.Context, _ DependencyRequest) (DependencySnapshot, error) {
			started.Add(1)
			defer finished.Add(1)
			<-ctx.Done()
			return DependencySnapshot{}, ctx.Err()
		})
		start := make(chan struct{})
		var group sync.WaitGroup
		for range 24 {
			group.Go(func() {
				<-start
				_, err := d.Read(t.Context(), DependencyRequest{Kind: "scope", TenantID: "tenant", SpaceCode: "bkcc__2"})
				if !errors.Is(err, context.Canceled) {
					t.Error("concurrent close must terminate first read", err)
				}
			})
		}
		group.Go(func() { <-start; d.Close() })
		close(start)
		group.Wait()
		d.Close()
		if started.Load() != finished.Load() {
			t.Fatal("Close returned with source loader alive", started.Load(), finished.Load())
		}
	}
}
