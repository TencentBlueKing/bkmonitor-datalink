// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/onemodel"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	"linkd/internal/processingerror"
	"linkd/internal/store"
)

type indexedTestMain struct {
	candidate redisstate.DependencyCandidate
	at        time.Time
}

type memoryDependencyIndex struct {
	mu        sync.Mutex
	ready     map[string]bool
	entries   map[string]map[string]indexedTestMain
	revisions map[string]int64
}

func newMemoryDependencyIndex() *memoryDependencyIndex {
	return &memoryDependencyIndex{ready: map[string]bool{}, entries: map[string]map[string]indexedTestMain{}, revisions: map[string]int64{}}
}

func dependencyTestKey(t string, p redisstate.DependencyIndexRef) string {
	return fmt.Sprintf("%s/%s/%d/%s/%s", t, p.ID, p.Version, p.Digest, p.ConfigDigest)
}

func (m *memoryDependencyIndex) ApplyDependencyCandidates(ctx context.Context, u redisstate.DependencyCandidateUpdate) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	owner := u.TenantID + "/" + u.AlertID
	if m.revisions[owner] > u.Revision {
		return nil
	}
	if m.revisions[owner] < u.Revision || u.Clear {
		for key, items := range m.entries {
			if len(key) > len(u.TenantID) && key[:len(u.TenantID)+1] == u.TenantID+"/" {
				delete(items, u.AlertID)
			}
		}
	}
	m.revisions[owner] = u.Revision
	if u.Clear {
		return nil
	}
	for _, v := range u.Memberships {
		key := dependencyTestKey(u.TenantID, redisstate.DependencyIndexRef{PolicyVersion: v.Policy, ConfigDigest: u.ConfigDigest})
		if m.entries[key] == nil {
			m.entries[key] = map[string]indexedTestMain{}
		}
		delete(m.entries[key], u.AlertID)
		if v.Matched {
			m.entries[key][u.AlertID] = indexedTestMain{candidate: redisstate.DependencyCandidate{AlertID: u.AlertID, Revision: u.Revision}, at: u.BeginAt}
		}
	}
	return nil
}

func (m *memoryDependencyIndex) TopDependencyCandidate(ctx context.Context, t string, p redisstate.DependencyIndexRef) (redisstate.DependencyCandidate, bool, error) {
	if err := ctx.Err(); err != nil {
		return redisstate.DependencyCandidate{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := dependencyTestKey(t, p)
	var chosen indexedTestMain
	for _, item := range m.entries[key] {
		if chosen.candidate.AlertID == "" || item.at.After(chosen.at) || (item.at.Equal(chosen.at) && item.candidate.AlertID < chosen.candidate.AlertID) {
			chosen = item
		}
	}
	return chosen.candidate, m.ready[key], nil
}

func (m *memoryDependencyIndex) MarkDependencyIndexReady(ctx context.Context, t string, p redisstate.DependencyIndexRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ready[dependencyTestKey(t, p)] = true
	return nil
}

func (m *memoryDependencyIndex) RemoveDependencyCandidate(ctx context.Context, t string, p redisstate.DependencyIndexRef, c redisstate.DependencyCandidate) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	items := m.entries[dependencyTestKey(t, p)]
	if items[c.AlertID].candidate.Revision == c.Revision {
		delete(items, c.AlertID)
	}
	return nil
}

func TestDependencyUnreadyIndexDoesNotAdmitOrScan(t *testing.T) {
	repo, clock, s, state, action := dependencyFixture(t, "custom_shield")
	p := dependencyProcessor(t, repo, clock, s, state, action)
	s.Index = newMemoryDependencyIndex()
	s.Candidates = failedCandidates{err: errors.New("event must not scan")}
	_, err := processAggregation(t.Context(), repo, p, dependencyEvent("unready", "source", "main", clock))
	if !errors.Is(err, ErrDependencyIndexNotReady) || !processingerror.Retryable(err) {
		t.Fatalf("unready index was not retryable: %v", err)
	}
	if len(action.calls) != 0 {
		t.Fatal("unready index admitted an action")
	}
	var pending interface{ PolicyPreparationPending() bool }
	if !errors.As(err, &pending) || !pending.PolicyPreparationPending() {
		t.Fatal("unready index lost mailbox pending marker", err)
	}
}

func TestDependencyMainIndexUsesOneCurrentReadAndCleansTerminal(t *testing.T) {
	repo, clock, s, _, _ := dependencyFixture(t, "custom_shield")
	a := admittedMainFixture(t, repo, clock, "old", s)
	clock.at = clock.at.Add(time.Minute)
	b := admittedMainFixture(t, repo, clock, "new", s)
	r := dependencyRelease(t, "custom_shield")
	compiled, err := policy.Compile(r.Kind, r.Spec)
	if err != nil {
		t.Fatal(err)
	}
	f := policy.FrozenPolicy{Release: r, Compiled: compiled}
	s.Candidates = failedCandidates{err: errors.New("hot path must not scan")}
	reads := 0
	s.CurrentAlert = func(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
		reads++
		return repo.GetAlert(ctx, tenant, id)
	}
	main, found, err := s.admittedMain(t.Context(), f, s.Loader.Targets, clock.at, nil)
	if err != nil || !found || main.alert.AlertID != b.AlertID || reads != 1 {
		t.Fatalf("indexed read main=%s found=%t reads=%d err=%v", main.alert.AlertID, found, reads, err)
	}
	// 模拟终态已经落库而增量观察尚未来得及执行；实时状态不能继续选择旧主。
	current, _ := repo.GetAlert(t.Context(), "tenant", b.AlertID)
	ended := current.Alert.Clone()
	ended.Status = domain.AlertStatusClosed
	ended.EndAt = &clock.at
	ended.EndType = domain.AlertEndTypeSystem
	ended.UpdateAt = clock.at.Add(time.Second)
	if _, err := repo.CompareAndSetAlert(t.Context(), "tenant", b.AlertID, current.Version, ended); err != nil {
		t.Fatal(err)
	}
	main, found, err = s.admittedMain(t.Context(), f, s.Loader.Targets, clock.at, nil)
	if err != nil || !found || main.alert.AlertID != a.AlertID {
		t.Fatalf("terminal candidate blocked earlier main: %s %t %v", main.alert.AlertID, found, err)
	}
}

type changingDependencyTargets struct {
	shieldTargets
	included bool
}

func (r *changingDependencyTargets) Resolve(ctx context.Context, tenant, space string, d onemodel.TargetDescriptor) (onemodel.TargetResult, error) {
	result, err := r.shieldTargets.Resolve(ctx, tenant, space, d)
	if !r.included {
		result.Instances = nil
	}
	return result, err
}

func TestDependencyTargetChangeRebuildAddsAndRemovesUnchangedAlert(t *testing.T) {
	repo, clock, s, _, _ := dependencyFixture(t, "custom_shield")
	targets := &changingDependencyTargets{}
	s.Loader.Targets = targets
	a := admittedMainFixture(t, repo, clock, "main", s)
	r := dependencyRelease(t, "custom_shield")
	compiled, err := policy.Compile(r.Kind, r.Spec)
	if err != nil {
		t.Fatal(err)
	}
	f := policy.FrozenPolicy{Release: r, Compiled: compiled}
	if _, found, err := s.admittedMain(t.Context(), f, targets, clock.at, nil); err != nil || found {
		t.Fatal("outside target main indexed", found, err)
	}
	targets.included = true
	if err := s.RebuildDependencyIndex(t.Context(), "tenant", clock.at, nil); err != nil {
		t.Fatal(err)
	}
	if main, found, err := s.admittedMain(t.Context(), f, targets, clock.at, nil); err != nil || !found || main.alert.AlertID != a.AlertID {
		t.Fatal("new target membership not indexed", found, err)
	}
	targets.included = false
	if err := s.RebuildDependencyIndex(t.Context(), "tenant", clock.at, nil); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.admittedMain(t.Context(), f, targets, clock.at, nil); err != nil || found {
		t.Fatal("removed target membership retained", found, err)
	}
}

func TestDependencyReadyRefreshDoesNotRepeatActiveScan(t *testing.T) {
	_, clock, s, _, _ := dependencyFixture(t, "custom_shield")
	s.Candidates = failedCandidates{err: errors.New("ready rule must not rescan")}
	if err := s.RefreshDependencyIndex(t.Context(), "tenant", clock.at, nil); err != nil {
		t.Fatal(err)
	}
}

type preparedDependencyTargets struct {
	shieldTargets
	at       time.Time
	contains int
	found    bool
}

func (r *preparedDependencyTargets) ResolveBusinessScope(ctx context.Context, tenant, space string, id int64) (onemodel.TargetScope, error) {
	recordDependencyRead(ctx, r.at.Add(-time.Second))
	return r.shieldTargets.ResolveBusinessScope(ctx, tenant, space, id)
}

func (r *preparedDependencyTargets) ContainsTarget(ctx context.Context, _, _ string, _ *onemodel.TargetDescriptor, _ onemodel.InstanceRef) (bool, error) {
	r.contains++
	recordDependencyRead(ctx, r.at)
	return r.found, nil
}

func (*preparedDependencyTargets) Resolve(context.Context, string, string, onemodel.TargetDescriptor) (onemodel.TargetResult, error) {
	return onemodel.TargetResult{}, errors.New("prepared membership must not expand targets")
}

func TestDependencyCandidateTracksSourceTimeAndKeepsPreparedMembership(t *testing.T) {
	repo, clock, s, _, _ := dependencyFixture(t, "custom_shield")
	a := admittedMainFixture(t, repo, clock, "main")
	targets := &preparedDependencyTargets{at: time.Now().UTC(), found: true}
	s.Loader.Targets = targets
	r := dependencyRelease(t, "custom_shield")
	compiled, err := policy.Compile(r.Kind, r.Spec)
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.dependencyCandidateUpdate(t.Context(), a, []policy.FrozenPolicy{{Release: r, Compiled: compiled}}, clock.at, nil)
	if err != nil || len(u.Memberships) != 1 || !u.Memberships[0].Matched || !u.Memberships[0].ObservedAt.Equal(targets.at) || targets.contains != 1 {
		t.Fatalf("prepared evaluation lost source time: %+v calls=%d err=%v", u.Memberships, targets.contains, err)
	}
}

func TestRedisDependencyOldWorkerCannotRemoveNewlyEligibleMain(t *testing.T) {
	repo, clock, s, _, _ := dependencyFixture(t, "custom_shield")
	a := admittedMainFixture(t, repo, clock, "main")
	r := dependencyRelease(t, "custom_shield")
	compiled, err := policy.Compile(r.Kind, r.Spec)
	if err != nil {
		t.Fatal(err)
	}
	f := policy.FrozenPolicy{Release: r, Compiled: compiled}
	shared := realSuppressionState(t)
	s.Index = shared
	newer := time.Now().UTC()
	u := redisstate.DependencyCandidateUpdate{TenantID: a.BKTenantID, AlertID: a.AlertID, Revision: a.Revision, BeginAt: a.BeginAt, Memberships: []redisstate.DependencyMembership{{Policy: dependencyVersion(f), Matched: true, ObservedAt: newer}}}
	if err := shared.ApplyDependencyCandidates(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	if err := shared.MarkDependencyIndexReady(t.Context(), a.BKTenantID, s.dependencyIndexRef(f)); err != nil {
		t.Fatal(err)
	}
	oldWorker := &preparedDependencyTargets{at: newer.Add(-30 * time.Second), found: false}
	_, found, err := s.admittedMain(t.Context(), f, &evaluationTargets{reader: oldWorker}, clock.at, nil)
	if found || !errors.Is(err, ErrDependencyIndexNotReady) || !processingerror.Retryable(err) {
		t.Fatalf("old Worker did not wait for fresh source: %t %v", found, err)
	}
	row, ready, err := shared.TopDependencyCandidate(t.Context(), a.BKTenantID, s.dependencyIndexRef(f))
	if err != nil || !ready || row.AlertID != a.AlertID {
		t.Fatal("old Worker removed current eligible main", row, err)
	}
}

func TestDependencyNewPolicyBackfillPreservesReadyPolicyAtNewAlertRevision(t *testing.T) {
	repo, clock, s, _, _ := dependencyFixture(t, "custom_shield")
	s.Index = realSuppressionState(t)
	a := admittedMainFixture(t, repo, clock, "main")
	r := dependencyRelease(t, "custom_shield")
	compiled, err := policy.Compile(r.Kind, r.Spec)
	if err != nil {
		t.Fatal(err)
	}
	first := policy.FrozenPolicy{Release: r, Compiled: compiled}
	all := []policy.FrozenPolicy{first}
	if err := s.RefreshDependencyIndexSnapshot(t.Context(), "tenant", clock.at, nil, all, false); err != nil {
		t.Fatal(err)
	}
	current, err := repo.GetAlert(t.Context(), "tenant", a.AlertID)
	if err != nil {
		t.Fatal(err)
	}
	replacement := current.Alert.Clone()
	replacement.UpdateAt = clock.at.Add(time.Second)
	current, err = repo.CompareAndSetAlert(t.Context(), "tenant", a.AlertID, current.Version, replacement)
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second.Release.ID = "new-policy"
	all = append(all, second)
	// 新规则回填先观察到已 CAS 但尚未增量登记的 revision，旧 Ready 规则也必须补齐。
	if err := s.RefreshDependencyIndexSnapshot(t.Context(), "tenant", clock.at, nil, all, false); err != nil {
		t.Fatal(err)
	}
	for _, f := range all {
		row, ready, err := s.Index.TopDependencyCandidate(t.Context(), "tenant", s.dependencyIndexRef(f))
		if err != nil || !ready || row.AlertID != a.AlertID || row.Revision != current.Alert.Revision {
			t.Fatal("new version backfill erased existing Ready main", f.Release.ID, row, ready, err)
		}
	}
}

func TestDependencyRetiredNamespaceDoesNotTreatOldReadyAsUsable(t *testing.T) {
	repo, clock, s, _, _ := dependencyFixture(t, "custom_shield")
	s.Index = realSuppressionState(t)
	s = s.WithConfigDigest("old-config").(*Shielder)
	a := admittedMainFixture(t, repo, clock, "main")
	r := dependencyRelease(t, "custom_shield")
	compiled, err := policy.Compile(r.Kind, r.Spec)
	if err != nil {
		t.Fatal(err)
	}
	f := policy.FrozenPolicy{Release: r, Compiled: compiled}
	if err := s.RefreshDependencyIndexSnapshot(t.Context(), "tenant", clock.at, nil, []policy.FrozenPolicy{f}, false); err != nil {
		t.Fatal(err)
	}
	if err := s.RetireDependencyNamespace(t.Context(), "tenant", s.ConfigDigest); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.admittedMain(t.Context(), f, s.Loader.Targets, clock.at, nil); found || !errors.Is(err, ErrDependencyIndexNotReady) {
		t.Fatal("retired config admitted main", found, err)
	}
	if err := s.RefreshDependencyIndexSnapshot(t.Context(), "tenant", clock.at, nil, []policy.FrozenPolicy{f}, true); err != nil {
		t.Fatal(err)
	}
	if err := s.ActivateDependencyNamespace(t.Context(), "tenant", s.ConfigDigest); err != nil {
		t.Fatal(err)
	}
	if main, found, err := s.admittedMain(t.Context(), f, s.Loader.Targets, clock.at, nil); err != nil || !found || main.alert.AlertID != a.AlertID {
		t.Fatal("rebuilt rollback config unavailable", found, err)
	}
}

func TestDependencyHotPathRepairsAllPoliciesWhenCurrentRevisionLeadsIndex(t *testing.T) {
	repo, clock, s, _, _ := dependencyFixture(t, "custom_shield")
	s.Index = realSuppressionState(t)
	a := admittedMainFixture(t, repo, clock, "main")
	r := dependencyRelease(t, "custom_shield")
	compiled, err := policy.Compile(r.Kind, r.Spec)
	if err != nil {
		t.Fatal(err)
	}
	first := policy.FrozenPolicy{Release: r, Compiled: compiled}
	second := first
	second.Release.ID = "other-policy"
	// 两条规则均已 Ready，模拟 CAS 完成后 Redis 观察前的读取窗口。
	policies := s.Loader.Releases.(*shieldDirectory)
	policies.releases = append(policies.releases, second.Release)
	s.Loader.Catalog = policy.NewCatalog(policies)
	if err := s.RefreshDependencyIndexSnapshot(t.Context(), "tenant", clock.at, nil, []policy.FrozenPolicy{first, second}, false); err != nil {
		t.Fatal(err)
	}
	current, err := repo.GetAlert(t.Context(), "tenant", a.AlertID)
	if err != nil {
		t.Fatal(err)
	}
	replacement := current.Alert.Clone()
	replacement.UpdateAt = clock.at.Add(time.Second)
	current, err = repo.CompareAndSetAlert(t.Context(), "tenant", a.AlertID, current.Version, replacement)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.admittedMain(t.Context(), first, s.Loader.Targets, clock.at, nil); err != nil || !found {
		t.Fatal("failed to repair indexed current main", found, err)
	}
	for _, f := range []policy.FrozenPolicy{first, second} {
		row, ready, err := s.Index.TopDependencyCandidate(t.Context(), "tenant", s.dependencyIndexRef(f))
		if err != nil || !ready || row.AlertID != a.AlertID || row.Revision != current.Alert.Revision {
			t.Fatal("repair omitted another Ready policy", f.Release.ID, row, ready, err)
		}
	}
}

func TestRedisDependencyBusinessTransitionsAdvanceEligibilityRevision(t *testing.T) {
	for _, mode := range []string{"admitted_create", "time_shield_release", "merge_release"} {
		t.Run(mode, func(t *testing.T) {
			repo, clock, s, state, action := dependencyFixture(t, "custom_shield")
			s.Index = realSuppressionState(t)
			if mode == "time_shield_release" {
				directory := s.Loader.Releases.(*shieldDirectory)
				directory.releases = append(directory.releases, timeShieldRelease(t))
			}
			p := dependencyProcessor(t, repo, clock, s, state, action)
			r := dependencyRelease(t, "custom_shield")
			compiled, err := policy.Compile(r.Kind, r.Spec)
			if err != nil {
				t.Fatal(err)
			}
			ref := s.dependencyIndexRef(policy.FrozenPolicy{Release: r, Compiled: compiled})
			opening := dependencyEvent("business-main", "source", "main", clock)
			result := mustProcessAggregation(t, repo, p, opening)
			id := result.Event.RelatedAlertIDs[0]
			current, err := repo.GetAlert(t.Context(), "tenant", id)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "admitted_create" {
				row, ready, err := s.Index.TopDependencyCandidate(t.Context(), "tenant", ref)
				if err != nil || !ready || row.AlertID != id || row.Revision != current.Alert.Revision || !current.Alert.AdmittedActiveMain() {
					t.Fatal("real admission at create was rejected by clear tombstone", row, current.Alert.Admission, err)
				}
				return
			}
			if mode == "time_shield_release" {
				if !current.Alert.Shield.Active {
					t.Fatal("expected maintenance shield")
				}
				before := current.Alert.Revision
				clock.at = clock.at.Add(11 * time.Second)
				if changed, err := p.CheckShield(t.Context(), "tenant", id); err != nil || !changed {
					t.Fatal("timer did not release", changed, err)
				}
				current, err = repo.GetAlert(t.Context(), "tenant", id)
				if err != nil {
					t.Fatal(err)
				}
				if current.Alert.Revision <= before || current.Alert.AdmittedActiveMain() {
					t.Fatal("timer release did not advance business revision or admitted early", before, current.Alert.Revision)
				}
				if row, _, err := s.Index.TopDependencyCandidate(t.Context(), "tenant", ref); err != nil || row.AlertID != "" {
					t.Fatal("timer release admitted an unadmitted main", row, err)
				}
				next := dependencyEvent("business-retrigger", "source", "main", clock)
				next.Fingerprint = opening.Fingerprint
				mustProcessAggregation(t, repo, p, next)
				admitted, err := repo.GetAlert(t.Context(), "tenant", id)
				if err != nil {
					t.Fatal(err)
				}
				row, _, err := s.Index.TopDependencyCandidate(t.Context(), "tenant", ref)
				if err != nil || row.AlertID != id || admitted.Alert.Revision <= current.Alert.Revision || !admitted.Alert.AdmittedActiveMain() {
					t.Fatal("next trigger failed after clear tombstone", row, admitted.Alert.Admission, err)
				}
				return
			}
			// 合并等待清资格后，通过真实 ReleaseMergeWindow 解除并恢复主候选；不手工指定 revision。
			next := current.Alert.Clone()
			window := fmt.Sprintf("%064d", 1)
			next.Merge = &domain.AlertMerge{Role: "original", State: "pending", Pending: []domain.MergeWait{{WindowID: window, Policy: domain.PolicyVersion{ID: "merge", Version: 1, Digest: fmt.Sprintf("%064d", 2)}, GroupKey: fmt.Sprintf("%064d", 3), MemberEventID: next.TriggerEventID, Severity: next.Severity, Groups: []int{0}, StartedAt: next.UpdateAt, Deadline: next.UpdateAt.Add(time.Minute)}}}
			next.UpdateAt = next.UpdateAt.Add(time.Second)
			blocked, err := repo.CompareAndSetAlert(t.Context(), "tenant", id, current.Version, next)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.ObserveDependencyAlert(t.Context(), blocked.Alert, nil); err != nil {
				t.Fatal(err)
			}
			if row, _, err := s.Index.TopDependencyCandidate(t.Context(), "tenant", ref); err != nil || row.AlertID != "" {
				t.Fatal("merge waiting retained main", row, err)
			}
			released, err := p.ReleaseMergeWindow(t.Context(), "tenant", id, window)
			if err != nil {
				t.Fatal(err)
			}
			row, _, err := s.Index.TopDependencyCandidate(t.Context(), "tenant", ref)
			if err != nil || row.AlertID != id || released.Alert.Revision <= blocked.Alert.Revision || !released.Alert.AdmittedActiveMain() {
				t.Fatal("real merge release failed to restore eligibility", row, released.Alert.Admission, err)
			}
		})
	}
}

func TestDependencyBackfillUsesOneCurrentBatchPerPage(t *testing.T) {
	for _, failure := range []string{"", "missing", "foreign"} {
		t.Run(failure, func(t *testing.T) {
			repo, clock, s, _, _ := dependencyFixture(t, "custom_shield")
			a := admittedMainFixture(t, repo, clock, "template")
			template, err := repo.GetAlert(t.Context(), a.BKTenantID, a.AlertID)
			if err != nil {
				t.Fatal(err)
			}
			s.Candidates = &capacityCandidates{template: template, count: 65}
			s.Index = newMemoryDependencyIndex()
			batches, individuals := 0, 0
			s.CurrentAlert = func(context.Context, string, string) (store.StoredAlert, error) {
				individuals++
				return store.StoredAlert{}, errors.New("background must use batch")
			}
			s.CurrentAlerts = func(_ context.Context, tenant string, ids []string) (store.AlertBatch, error) {
				batches++
				batch := store.AlertBatch{}
				for _, id := range ids {
					if failure == "missing" {
						batch.NotFound = append(batch.NotFound, id)
						continue
					}
					row := template
					row.Alert = template.Alert.Clone()
					row.Alert.AlertID = id
					if failure == "foreign" {
						row.Alert.BKTenantID = "other-tenant"
					}
					batch.Alerts = append(batch.Alerts, row)
				}
				return batch, nil
			}
			err = s.RefreshDependencyIndex(t.Context(), "tenant", clock.at, nil)
			switch failure {
			case "":
				if err != nil || batches != 3 || individuals != 0 {
					t.Fatalf("65 rows batches=%d individuals=%d err=%v", batches, individuals, err)
				}
			case "missing":
				if !errors.Is(err, store.ErrNotFound) {
					t.Fatal("missing current row became empty ready index", err)
				}
			case "foreign":
				if !errors.Is(err, policy.ErrAccess) {
					t.Fatal("batch crossed tenant", err)
				}
			}
		})
	}
}

type forwardingPreparedTargets struct {
	shieldTargets
	result        policy.TargetReader
	err           error
	calls         int
	tenant, space string
	descriptor    *onemodel.TargetDescriptor
}

func (s *forwardingPreparedTargets) PrepareTargets(_ context.Context, tenant, space string, target *onemodel.TargetDescriptor) (policy.TargetReader, error) {
	s.calls++
	s.tenant = tenant
	s.space = space
	s.descriptor = target
	return s.result, s.err
}

func TestDependencyEvaluationTargetsForwardsPreparedSnapshot(t *testing.T) {
	descriptor := &onemodel.TargetDescriptor{ModelID: "cw-Host"}
	snapshot := &shieldTargets{}
	source := &forwardingPreparedTargets{result: snapshot}
	wrapped := &evaluationTargets{reader: source}
	prepared, err := wrapped.PrepareTargets(t.Context(), "tenant", "bkcc__2", descriptor)
	if err != nil || prepared != snapshot || source.calls != 1 || source.tenant != "tenant" || source.space != "bkcc__2" || source.descriptor != descriptor {
		t.Fatal("wrapper hid fixed target snapshot", prepared, source, err)
	}
	unavailable := errors.New("prepare failed")
	source.err = unavailable
	if _, err := wrapped.PrepareTargets(t.Context(), "tenant", "bkcc__2", descriptor); !errors.Is(err, unavailable) {
		t.Fatal("wrapper swallowed preparation failure", err)
	}
	fallback := &evaluationTargets{reader: shieldTargets{}}
	if prepared, err := fallback.PrepareTargets(t.Context(), "tenant", "bkcc__2", descriptor); err != nil || prepared != fallback {
		t.Fatal("legacy target reader lost wrapper", prepared, err)
	}
}
