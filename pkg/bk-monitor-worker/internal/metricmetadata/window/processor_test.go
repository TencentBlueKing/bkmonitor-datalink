// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package window

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/storage"
)

type processorStore struct {
	calls     int
	failGroup int
	failError error
	routes    map[uint]storage.MetricRoute
	scopes    map[core.ScopeKey]core.ScopeState
	applied   []core.Record
	loadErr   error
}

func (s *processorStore) LoadMetricRoutes(context.Context) (map[uint]storage.MetricRoute, error) {
	if s.routes != nil {
		return s.routes, nil
	}
	return map[uint]storage.MetricRoute{
		1002: {GroupID: 9, BizID: 2},
		1003: {GroupID: 10, BizID: 3},
	}, nil
}
func (s *processorStore) GroupForDataID(_ context.Context, dataID uint) (storage.MetricRoute, bool, error) {
	routes := s.routes
	if routes == nil {
		routes = map[uint]storage.MetricRoute{
			1002: {GroupID: 9, BizID: 2, TenantID: "tenant-a"},
			1003: {GroupID: 10, BizID: 3, TenantID: "tenant-b"},
		}
	}
	route, ok := routes[dataID]
	return route, ok, nil
}
func (s *processorStore) Expire(context.Context, time.Time) (int, error) { return 0, nil }
func (s *processorStore) InvalidateReportValueCache(int)                 {}
func (s *processorStore) LoadAll(context.Context) (map[core.ScopeKey]core.ScopeState, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	states := make(map[core.ScopeKey]core.ScopeState, len(s.scopes))
	for _, state := range s.scopes {
		states[state.Key] = state.Clone()
	}
	return states, nil
}
func (s *processorStore) Apply(_ context.Context, group int, records []core.Record, now time.Time) (storage.Result, error) {
	s.calls++
	if group == s.failGroup {
		if s.failError != nil {
			return storage.Result{}, s.failError
		}
		return storage.Result{}, errors.New("injected transaction failure")
	}
	if s.scopes == nil {
		s.scopes = make(map[core.ScopeKey]core.ScopeState)
	}
	changed := make(map[core.ScopeKey]bool)
	created := make(map[core.ScopeKey]bool)
	updated := make(map[core.ScopeKey]core.ScopeState)
	for _, r := range records {
		s.applied = append(s.applied, r)
		key := r.Key().ScopeKey
		st, ok := updated[key]
		if !ok {
			st, ok = s.scopes[key]
			created[key] = !ok
			st = st.Clone()
		}
		if st.Metrics == nil {
			st.Metrics = make(map[string]core.MetricState)
		}
		st.Key = key
		st.ID = int64(group)
		st.LastSeen = now
		st.LoadedAt = now
		metric, exists := st.Metrics[r.FieldName]
		if !exists || metric.Fingerprint != r.Fingerprint {
			changed[key] = true
		}
		st.Metrics[r.FieldName] = core.MetricState{ValueID: 1, Fingerprint: r.Fingerprint}
		updated[key] = st
	}
	result := storage.Result{}
	for key, state := range updated {
		if changed[key] && !created[key] {
			state.Version++
		}
		s.scopes[key] = state
		result.Scopes = append(result.Scopes, state.Clone())
	}
	return result, nil
}

func TestProcessorParsesCollectorAndUpdatesScopeMembers(t *testing.T) {
	store := &processorStore{}
	p := newProcessor(store, core.DefaultOptions(), 42)
	p.Reset()
	ctx := context.Background()

	message := []byte(`{"data":[{"dimension":{"dataid":"1002","bk_biz_id":"2","bk_tenant_id":"tenant-a","scope_type":"APM","scope":"checkout-http","metric":"duration_sum","name":"duration","original_type":"histogram","type":"counter","temporality":"cumulative","present":"0"}}]}`)
	if n, err := p.Add(ctx, message); err != nil || n != 1 {
		t.Fatalf("Add() = (%d, %v), want (1, nil)", n, err)
	}
	if err := p.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if len(store.applied) != 1 {
		t.Fatalf("Apply() called with %d records, want 1", len(store.applied))
	}
	r := store.applied[0]
	if r.DataID != 1002 || r.GroupID != 9 || r.FieldName != "duration_sum" || r.OriginalType != "histogram" || r.MetricType != "counter" || r.Meta != `{"temporality":"cumulative"}` {
		t.Fatalf("parsed and routed record = %+v", r)
	}
	key := store.applied[0].Key().ScopeKey
	if got := store.scopes[key].Version; got != 0 {
		t.Fatalf("initial scope version = %d, want 0", got)
	}

	// 同一 scope 后续新增成员时，更新应保留原成员并加入新成员。
	message = []byte(`{"data":[{"dimension":{"dataid":"1002","bk_biz_id":"2","bk_tenant_id":"tenant-a","scope_type":"APM","scope":"checkout-http","metric":"duration_count","name":"duration","original_type":"histogram","type":"counter","temporality":"cumulative","present":"0"}}]}`)
	if n, err := p.Add(ctx, message); err != nil || n != 1 {
		t.Fatalf("Add() = (%d, %v), want (1, nil)", n, err)
	}
	if err := p.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	state, ok := p.stateWindow.Lookup(key)
	if !ok || len(state.Metrics) != 2 {
		t.Fatalf("updated scope state = %+v, found=%t; want 2 members", state, ok)
	}
	if state.Metrics["duration_sum"].Fingerprint == "" || state.Metrics["duration_count"].Fingerprint == "" {
		t.Fatalf("computed fingerprints missing from updated scope: %+v", state.Metrics)
	}
	if state.Version != 1 {
		t.Fatalf("scope version after adding a member = %d, want 1", state.Version)
	}
	if len(store.applied) != 2 || store.applied[1].FieldName != "duration_count" || store.applied[1].GroupID != 9 {
		t.Fatalf("second update was not routed and applied: %+v", store.applied)
	}

	// Collector 后续上报同一指标的新元数据时，应更新 scope version。
	message = []byte(`{"data":[{"dimension":{"dataid":"1002","bk_biz_id":"2","bk_tenant_id":"tenant-a","scope_type":"APM","scope":"checkout-http","metric":"duration_sum","name":"duration","original_type":"histogram","type":"counter","temporality":"delta","present":"0"}}]}`)
	if n, err := p.Add(ctx, message); err != nil || n != 1 {
		t.Fatalf("Add() = (%d, %v), want (1, nil)", n, err)
	}
	if err := p.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	state, ok = p.stateWindow.Lookup(key)
	if !ok || state.Version != 2 || state.Metrics["duration_sum"].Fingerprint == store.applied[0].Fingerprint {
		t.Fatalf("changed metric metadata did not update scope version: state=%+v found=%t", state, ok)
	}
	if got := store.applied[2].Meta; got != `{"temporality":"delta"}` {
		t.Fatalf("updated metric metadata = %s, want delta temporality", got)
	}
}

func processorMessage(t *testing.T, dataID int, name, unit string) []byte {
	t.Helper()
	b, err := json.Marshal([]any{map[string]any{"dataid": dataID, "__scope_type": "DEFAULT", "__scope": "", "__metric": name, "__name": name, "__original_type": "gauge", "__type": "gauge", "__unit": unit}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestProcessorCommitCacheReplacementAndRecovery(t *testing.T) {
	s := &processorStore{}
	o := core.DefaultOptions()
	p := newProcessor(s, o, 42)
	now := time.Now()
	p.now = func() time.Time { return now }
	p.Reset()
	ctx := context.Background()
	first := processorMessage(t, 1002, "cpu", "1")
	if _, err := p.Add(ctx, first); err != nil {
		t.Fatal(err)
	}
	if p.stateWindow.Len() != 0 {
		t.Fatal("uncommitted record entered cache")
	}
	s.failGroup = 9
	if err := p.Flush(ctx); err == nil {
		t.Fatal("transaction failure hidden")
	}
	if p.stateWindow.Len() != 0 {
		t.Fatal("failed write entered cache")
	}
	s.failGroup = 0
	if err := p.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	committed := s.calls
	if _, err := p.Add(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := p.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if s.calls != committed {
		t.Fatal("identical report was not skipped using the committed cache")
	}
	if _, err := p.Add(ctx, processorMessage(t, 1002, "cpu", "ms")); err != nil {
		t.Fatal(err)
	}
	if err := p.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	p.stateWindow.Range(func(st core.ScopeState) {
		if len(st.Metrics) != 1 {
			t.Fatal("replacement increased member count")
		}
	})
	p.Reset()
	if _, err := p.Add(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := p.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if s.calls != committed+2 {
		t.Fatal("new session did not restore database state")
	}
}

func TestProcessorUsesCachedStateUntilTouchInterval(t *testing.T) {
	s := &processorStore{}
	p := newProcessor(s, core.DefaultOptions(), 42)
	ctx := context.Background()
	now := time.Now()
	p.now = func() time.Time { return now }
	old := processorMessage(t, 1002, "cpu", "1")
	if _, err := p.Add(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := p.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	before := s.calls
	if _, err := p.Add(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := p.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if s.calls != before {
		t.Fatal("cached duplicate triggered a database query")
	}

	now = now.Add(p.opts.TouchInterval)
	if _, err := p.Add(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := p.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if s.calls != before+1 {
		t.Fatal("touch interval did not refresh persistent state")
	}
	for _, state := range s.scopes {
		if !state.LastSeen.Equal(now) {
			t.Fatal("touch interval persisted the wrong last-seen time")
		}
	}
}

func TestProcessorRefreshReconcilesStaleDuplicateCache(t *testing.T) {
	store := &processorStore{}
	p1 := newProcessor(store, core.DefaultOptions(), 42)
	p2 := newProcessor(store, core.DefaultOptions(), 43)
	ctx := context.Background()
	a := processorMessage(t, 1002, "cpu", "1")
	b := processorMessage(t, 1002, "cpu", "ms")
	apply := func(p *Processor, message []byte) {
		t.Helper()
		if _, err := p.Add(ctx, message); err != nil {
			t.Fatal(err)
		}
		if err := p.Flush(ctx); err != nil {
			t.Fatal(err)
		}
	}

	apply(p1, a)
	apply(p2, b)
	apply(p1, a)
	if store.calls != 2 {
		t.Fatalf("stale cache did not skip duplicate: Apply calls = %d, want 2", store.calls)
	}
	key := store.applied[0].Key().ScopeKey
	if got := store.scopes[key].Metrics["cpu"].Fingerprint; got != oldFingerprint(t, b) {
		t.Fatalf("persistent fingerprint = %s, want B", got)
	}

	// 刷新失败时保留旧快照；成功后以后续上报修正数据库状态。
	store.loadErr = errors.New("load failed")
	if err := p1.Refresh(ctx); err == nil {
		t.Fatal("expected refresh failure")
	}
	apply(p1, a)
	if store.calls != 2 {
		t.Fatalf("failed refresh changed cached behavior: Apply calls = %d, want 2", store.calls)
	}
	store.loadErr = nil
	if err := p1.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	apply(p1, a)
	if store.calls != 3 {
		t.Fatalf("report after refresh did not reach storage: Apply calls = %d, want 3", store.calls)
	}
	if got := store.scopes[key].Metrics["cpu"].Fingerprint; got != oldFingerprint(t, a) {
		t.Fatalf("persistent fingerprint = %s, want A", got)
	}
	apply(p1, a)
	if store.calls != 3 {
		t.Fatalf("duplicate after reconciliation reached storage: Apply calls = %d, want 3", store.calls)
	}
}

func TestProcessorQueriesDatabaseForMissInCompleteRouteSnapshot(t *testing.T) {
	s := &processorStore{}
	routes := newRouteCache()
	routes.snapshot.Store(&routeSnapshot{loaded: true, routes: map[uint]storage.MetricRoute{
		1002: {GroupID: 9, BizID: 2, TenantID: "tenant-a"},
	}})
	p := newProcessorWithRouteCache(s, core.DefaultOptions(), 42, routes)
	if n, err := p.Add(context.Background(), processorMessage(t, 1003, "memory", "1")); err != nil || n != 1 {
		t.Fatalf("accepted=%d err=%v", n, err)
	}
	if err := p.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.applied) != 1 || s.applied[0].GroupID != 10 {
		t.Fatalf("new database route was not persisted: %+v", s.applied)
	}
	if route, cached, loaded, _ := routes.Lookup(1003); !cached || !loaded || route.GroupID != 10 {
		t.Fatalf("positive lookup was not merged into full snapshot: route=%+v cached=%t loaded=%t", route, cached, loaded)
	}
}

func TestRouteCacheDoesNotRepublishLookupOlderThanSnapshot(t *testing.T) {
	routes := newRouteCache()
	routes.snapshot.Store(&routeSnapshot{loaded: true, generation: 1, routes: map[uint]storage.MetricRoute{
		1002: {GroupID: 9, BizID: 2},
	}})
	_, _, _, oldGeneration := routes.Lookup(1002)
	s := &processorStore{routes: map[uint]storage.MetricRoute{
		1002: {GroupID: 11, BizID: 2},
	}}
	if err := routes.Refresh(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	routes.Remember(1002, storage.MetricRoute{GroupID: 9, BizID: 2}, oldGeneration)
	if route, cached, _, _ := routes.Lookup(1002); !cached || route.GroupID != 11 {
		t.Fatalf("older point lookup replaced refreshed route: %+v", route)
	}
}

func TestProcessorRefreshesStaleGroupRouteAndContinuesOtherGroups(t *testing.T) {
	s := &processorStore{failGroup: 9, failError: storage.ErrGroupUnavailable}
	p := newProcessor(s, core.DefaultOptions(), 42)
	for _, message := range [][]byte{
		processorMessage(t, 1002, "cpu", "1"),
		processorMessage(t, 1003, "memory", "1"),
	} {
		if _, err := p.Add(context.Background(), message); err != nil {
			t.Fatal(err)
		}
	}
	// Group 9 was disabled; its DataID now routes to group 11. Group 10 stays valid.
	s.routes = map[uint]storage.MetricRoute{
		1002: {GroupID: 11, BizID: 2, TenantID: "tenant-a"},
		1003: {GroupID: 10, BizID: 3, TenantID: "tenant-b"},
	}
	if err := p.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.applied) != 2 || s.applied[0].FieldName != "memory" || s.applied[0].GroupID != 10 ||
		s.applied[1].FieldName != "cpu" || s.applied[1].GroupID != 11 {
		t.Fatalf("reports were not re-routed and committed: %+v", s.applied)
	}
}

func TestProcessorDropsDisabledGroupWithoutStoppingOtherGroups(t *testing.T) {
	s := &processorStore{failGroup: 9, failError: storage.ErrGroupUnavailable}
	p := newProcessor(s, core.DefaultOptions(), 42)
	for _, message := range [][]byte{
		processorMessage(t, 1002, "cpu", "1"),
		processorMessage(t, 1003, "memory", "1"),
	} {
		if _, err := p.Add(context.Background(), message); err != nil {
			t.Fatal(err)
		}
	}
	s.routes = map[uint]storage.MetricRoute{
		1003: {GroupID: 10, BizID: 3, TenantID: "tenant-b"},
	}
	if err := p.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.applied) != 1 || s.applied[0].GroupID != 10 || s.applied[0].FieldName != "memory" {
		t.Fatalf("disabled group blocked or contaminated valid reports: %+v", s.applied)
	}
}

func oldFingerprint(t *testing.T, message []byte) string {
	t.Helper()
	records, _, err := core.DecodeMessage(message)
	if err != nil || len(records) != 1 {
		t.Fatalf("decode message: records=%d err=%v", len(records), err)
	}
	return records[0].Fingerprint
}

func TestProcessorPartialCommitAndReplay(t *testing.T) {
	s := &processorStore{failGroup: 10}
	p := newProcessor(s, core.DefaultOptions(), 42)
	p.Reset()
	ctx := context.Background()
	for _, b := range [][]byte{processorMessage(t, 1002, "cpu", "1"), processorMessage(t, 1003, "memory", "1")} {
		if _, err := p.Add(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Flush(ctx); err == nil {
		t.Fatal("expected second group failure")
	}
	if len(s.scopes) != 1 || p.stateWindow.Len() != 0 || p.pending.Len() != 2 {
		t.Fatal("partial commit lost replay state")
	}
	s.failGroup = 0
	if err := p.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if len(s.scopes) != 2 || p.pending.Len() != 0 {
		t.Fatal("replay did not recover")
	}
}

func TestProcessorRefreshFlushesPendingReports(t *testing.T) {
	store := &processorStore{}
	p := newProcessor(store, core.DefaultOptions(), 42)
	ctx := context.Background()
	if _, err := p.Add(ctx, processorMessage(t, 1002, "cpu", "1")); err != nil {
		t.Fatal(err)
	}
	if err := p.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if store.calls != 1 || p.pending.Len() != 0 || p.stateWindow.Len() != 1 {
		t.Fatal("refresh did not persist pending reports")
	}
}

func TestProcessorRefreshReplacesWholeWindow(t *testing.T) {
	key := core.ScopeKey{GroupID: 9, Hash: "fresh"}
	state := core.ScopeState{Key: key, Metrics: map[string]core.MetricState{
		"cpu": {ValueID: 1, Fingerprint: string(make([]byte, 2048))},
	}}
	store := &processorStore{scopes: map[core.ScopeKey]core.ScopeState{key: state}}
	p := newProcessor(store, core.DefaultOptions(), 42)
	p.Reset()
	p.remember(core.ScopeState{Key: core.ScopeKey{GroupID: 9, Hash: "old"}, Metrics: map[string]core.MetricState{"old": {ValueID: 2}}})
	if err := p.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.stateWindow.Len() != 1 || p.stateWindow.Bytes() <= 0 {
		t.Fatal("full snapshot or cache byte accounting missing")
	}
	if _, ok := p.stateWindow.Lookup(key); !ok {
		t.Fatal("fresh snapshot missing")
	}
	p.remember(core.ScopeState{Key: core.ScopeKey{GroupID: 9, Hash: "later"}, Metrics: map[string]core.MetricState{"later": {ValueID: 3}}})
	if p.stateWindow.Len() != 2 {
		t.Fatal("incremental update discarded the full snapshot")
	}
	store.loadErr = errors.New("load failed")
	if err := p.Refresh(context.Background()); err == nil || p.stateWindow.Len() != 2 {
		t.Fatal("failed refresh replaced the previous snapshot")
	}
}

type blockingRefreshStore struct {
	*processorStore
	started chan struct{}
	resume  chan struct{}
	states  map[core.ScopeKey]core.ScopeState
	err     error
}

func (s *blockingRefreshStore) LoadAll(ctx context.Context) (map[core.ScopeKey]core.ScopeState, error) {
	close(s.started)
	select {
	case <-s.resume:
		return s.states, s.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestRefreshDoesNotBlockAndPreservesConcurrentChanges(t *testing.T) {
	key := core.ScopeKey{GroupID: 9, Hash: "scope"}
	old := core.ScopeState{Key: key, Metrics: map[string]core.MetricState{"metric": {Fingerprint: "old"}}}
	store := &blockingRefreshStore{processorStore: &processorStore{}, started: make(chan struct{}), resume: make(chan struct{}), states: map[core.ScopeKey]core.ScopeState{key: old}}
	p := newProcessor(store, core.DefaultOptions(), 42)
	done := make(chan error, 1)
	go func() { done <- p.Refresh(context.Background()) }()
	<-store.started
	updated := old.Clone()
	updated.Metrics["metric"] = core.MetricState{Fingerprint: "new"}
	wrote := make(chan struct{})
	go func() {
		p.mu.Lock()
		p.remember(updated)
		p.mu.Unlock()
		close(wrote)
	}()
	select {
	case <-wrote:
	case <-time.After(time.Second):
		close(store.resume)
		<-done
		t.Fatal("refresh blocked concurrent state update")
	}
	close(store.resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got, ok := p.stateWindow.Lookup(key)
	if !ok || got.Metrics["metric"].Fingerprint != "new" || p.stateWindow.Members() != 1 {
		t.Fatalf("concurrent update overwritten: %+v", got)
	}
}

func TestRefreshDiscardsSnapshotAfterInvalidation(t *testing.T) {
	for _, invalidate := range []string{"clear", "purge"} {
		t.Run(invalidate, func(t *testing.T) {
			key := core.ScopeKey{GroupID: 9, Hash: "scope"}
			old := core.ScopeState{Key: key, Metrics: map[string]core.MetricState{"metric": {Fingerprint: "old"}}}
			store := &blockingRefreshStore{processorStore: &processorStore{}, started: make(chan struct{}), resume: make(chan struct{}), states: map[core.ScopeKey]core.ScopeState{key: old}}
			p := newProcessor(store, core.DefaultOptions(), 42)
			p.stateWindow.Store(old.Clone())
			done := make(chan error, 1)
			go func() { done <- p.Refresh(context.Background()) }()
			<-store.started
			p.mu.Lock()
			if invalidate == "clear" {
				p.clearCache("test")
			} else {
				p.purgeMetricLocked(9, []string{"metric"})
			}
			p.mu.Unlock()
			close(store.resume)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			state, ok := p.stateWindow.Lookup(key)
			if (ok && len(state.Metrics) != 0) || p.stateWindow.Members() != 0 {
				t.Fatalf("deleted state restored: %+v", state)
			}
		})
	}
}

func TestRefreshFailurePreservesCurrentCache(t *testing.T) {
	key := core.ScopeKey{GroupID: 9, Hash: "scope"}
	store := &blockingRefreshStore{processorStore: &processorStore{}, started: make(chan struct{}), resume: make(chan struct{}), err: errors.New("load failed")}
	p := newProcessor(store, core.DefaultOptions(), 42)
	p.stateWindow.Store(core.ScopeState{Key: key})
	done := make(chan error, 1)
	go func() { done <- p.Refresh(context.Background()) }()
	<-store.started
	close(store.resume)
	if err := <-done; err == nil {
		t.Fatal("expected load failure")
	}
	if _, ok := p.stateWindow.Lookup(key); !ok {
		t.Fatal("cache lost on failure")
	}
}
