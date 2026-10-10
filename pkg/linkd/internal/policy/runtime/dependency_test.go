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
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/onemodel"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type dependencyMemory struct {
	mu      sync.Mutex
	entries map[string]redisstate.DependencyReservation
}

func (d *dependencyMemory) key(t string, p domain.PolicyVersion) string {
	return t + ":" + p.ID + ":" + p.Digest
}

func (d *dependencyMemory) GetDependencyMain(_ context.Context, t string, p domain.PolicyVersion) (redisstate.DependencyReservation, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	v, ok := d.entries[d.key(t, p)]
	return v, ok, nil
}

func (d *dependencyMemory) ClaimDependencyMain(_ context.Context, r redisstate.DependencyRequest) (redisstate.DependencyReservation, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	k := d.key(r.TenantID, r.Policy)
	if v, ok := d.entries[k]; ok {
		return v, nil
	}
	v := redisstate.DependencyReservation{Role: "pending", Main: r.Candidate}
	d.entries[k] = v
	v.Role = "candidate"
	return v, nil
}

func (d *dependencyMemory) CommitDependencyMain(_ context.Context, r redisstate.DependencyRequest) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	k := d.key(r.TenantID, r.Policy)
	v, ok := d.entries[k]
	if !ok || v.Main != r.Candidate {
		return false, nil
	}
	v.Role = "registered"
	d.entries[k] = v
	return true, nil
}

func (d *dependencyMemory) ReleaseDependencyMain(_ context.Context, r redisstate.DependencyRequest) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	k := d.key(r.TenantID, r.Policy)
	v, ok := d.entries[k]
	if !ok || v.Main != r.Candidate {
		return false, nil
	}
	delete(d.entries, k)
	return true, nil
}

func (d *dependencyMemory) ClearDependencyOwner(_ context.Context, t, id string) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for k, v := range d.entries {
		if v.Main.AlertID == id && strings.HasPrefix(k, t+":") {
			delete(d.entries, k)
			n++
		}
	}
	return n, nil
}

func dependencyRelease(t *testing.T, mode string) policy.Release {
	t.Helper()
	r := timeShieldRelease(t)
	var s policy.ShieldSpec
	if err := json.Unmarshal(r.Spec, &s); err != nil {
		t.Fatal(err)
	}
	s.Name = "dependency"
	s.ShieldType = "rely_shield"
	s.ShieldMode = mode
	s.Before = 5
	s.After = 10
	s.Times = nil
	// 目标描述限制主；子仍受业务范围约束。
	s.Policy = json.RawMessage(`{"expression":"A","A":{"condition":"term","target_key":"name","target_value":"main"}}`)
	s.RelyPolicy = json.RawMessage(`{"expression":"A","A":{"condition":"term","target_key":"source_id","target_value":"source"}}`)
	if mode == "cmdb_shield" {
		s.ModelID = "cw-Switch"
		s.Targets.ModelID = "cw-Switch"
		s.Targets.Selectors[0].Instances = []onemodel.InstanceRef{{ModelID: "cw-Switch", InstanceID: "sw1", EntityUID: "cw-Switch|sw1"}}
		s.RelyPolicy = json.RawMessage(`{"expression":"A","A":{"condition":"term","target_key":"model_id","target_value":"cw-Host","bk_obj_asst_id":"switch_connect_host"}}`)
	}
	raw, _ := json.Marshal(s)
	c, err := policy.Compile(policy.Shield, raw)
	if err != nil {
		t.Fatal(err)
	}
	r.ID = "dependency"
	r.Spec = c.Canonical
	r.Compiled = c.Summary
	return r
}

func dependencyFixture(t *testing.T, mode string) (*memory.Repository, *policyTestClock, *Shielder, *shieldHook, *shieldHook) {
	t.Helper()
	repo := memory.New()
	clock := &policyTestClock{at: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	state, action := &shieldHook{}, &shieldHook{}
	_, shielder := newShieldProcessor(t, repo, clock, &shieldDirectory{releases: []policy.Release{dependencyRelease(t, mode)}}, state, action)
	shielder.Candidates = repo
	shielder.CurrentAlert = repo.GetAlert
	shielder.Dependency = &dependencyMemory{entries: map[string]redisstate.DependencyReservation{}}
	return repo, clock, shielder, state, action
}

func dependencyProcessor(t *testing.T, repo *memory.Repository, clock *policyTestClock, s *Shielder, state, action *shieldHook, upgrades ...string) *lifecycle.Processor {
	t.Helper()
	upgrade := "update_current"
	if len(upgrades) > 0 {
		upgrade = upgrades[0]
	}
	// 通过正式选项创建同一 Shielder，不能在生成计划后替换策略依赖。
	p, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, []lifecycle.NamedFinalHook{{Name: "state", Purpose: "state", Hook: state}, {Name: "action", Purpose: "action", Hook: action}}, config.DefaultSeverityConfig(), clock, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithPolicySnapshotter(Snapshotter{Catalog: s.Loader.Catalog}), lifecycle.WithShieldEvaluator(s), lifecycle.WithSeverityUpgradePolicy(upgrade))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func dependencyEvent(id, source, title string, clock *policyTestClock) domain.Event {
	e := shieldEvent(id, clock)
	e.EventSourceID = source
	e.Fingerprint = id
	e.Title = title
	return e
}

func admittedMainFixture(t *testing.T, repo store.Repository, clock *policyTestClock, id string) domain.Alert {
	t.Helper()
	a := storetest.Alert("tenant", id, "opening-"+id, "fp-"+id, "warning")
	a.Title = "main"
	a.Labels["model_id"] = domain.NewStringScalar("cw-Host")
	a.Labels["model_inst_id"] = domain.NewStringScalar("101")
	a.BeginAt = clock.at
	a.CreateAt = clock.at
	a.UpdateAt = clock.at
	a.LastOccurredAt = clock.at
	a.Dimensions["bk_biz_id"] = domain.NewStringScalar("2")
	a.Admission = domain.AlertAdmission{AdmittedAt: &a.UpdateAt, Severity: a.Severity, CauseType: "source_event", CauseID: a.TriggerEventID}
	created, err := repo.CreateAlert(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	return created.Alert
}

func TestDependencySelectsNewestBeforeChildTimeAndKeepsBinding(t *testing.T) {
	repo, clock, s, state, action := dependencyFixture(t, "custom_shield")
	p := dependencyProcessor(t, repo, clock, s, state, action)
	old := admittedMainFixture(t, repo, clock, "a-old")
	clock.at = clock.at.Add(time.Minute)
	newest := admittedMainFixture(t, repo, clock, "z-new")
	child := mustProcessAggregation(t, repo, p, dependencyEvent("child", "other-source", "child", clock))
	id := child.Event.RelatedAlertIDs[0]
	current, _ := repo.GetAlert(t.Context(), "tenant", id)
	if !current.Alert.Shield.Active || current.Alert.Shield.Bindings[0].MainAlertID != newest.AlertID || len(action.calls) != 0 {
		t.Fatalf("wrong newest parent %+v", current.Alert)
	}
	binding := current.Alert.Shield.Bindings[0].BindingID
	clock.at = clock.at.Add(time.Minute)
	admittedMainFixture(t, repo, clock, "newer")
	if changed, err := p.CheckShield(t.Context(), "tenant", id); err != nil || changed {
		t.Fatal("timer migrated fixed parent", err)
	}
	current, _ = repo.GetAlert(t.Context(), "tenant", id)
	if current.Alert.Shield.Bindings[0].BindingID != binding {
		t.Fatal("binding identity changed")
	}
	// 最新主已超出子时间范围时，不回退到旧主 old（old 在子时间范围内）。
	e := dependencyEvent("historical", "third-source", "child", clock)
	e.OccurredAt = old.BeginAt.Add(-5 * time.Minute)
	past := mustProcessAggregation(t, repo, p, e)
	pastAlert, _ := repo.GetAlert(t.Context(), "tenant", past.Event.RelatedAlertIDs[0])
	if pastAlert.Alert.Shield.Active {
		t.Fatal("selected older main to fit child time")
	}
	// 关闭绑定主后仅解除；尽管 newer 仍活动，timer 不能自动改绑或触发处置。
	before := len(action.calls)
	_, err := p.CloseAlert(t.Context(), lifecycle.CloseAlertCommand{OperationID: "close-main", BKTenantID: "tenant", AlertID: newest.AlertID, OperatorKind: domain.OperatorKindUser, OperatorID: "tester", Reason: "done", EffectiveAt: clock.at})
	if err != nil {
		t.Fatal(err)
	}
	closedCalls := len(action.calls)
	if changed, err := p.CheckShield(t.Context(), "tenant", id); err != nil || !changed {
		t.Fatal("ended main did not release", err)
	}
	current, _ = repo.GetAlert(t.Context(), "tenant", id)
	if current.Alert.Shield.Active || current.Alert.Admission.AdmittedAt != nil || len(action.calls) != closedCalls || closedCalls < before {
		t.Fatal("release admitted or rebound child")
	}
}

func TestDependencyIgnoresUnevaluableCandidateWhenSelectingNewMain(t *testing.T) {
	for _, status := range []domain.EnrichStatus{domain.EnrichStatusPartial, domain.EnrichStatusFailed} {
		t.Run(string(status), func(t *testing.T) {
			repo, clock, s, state, action := dependencyFixture(t, "custom_shield")
			valid := admittedMainFixture(t, repo, clock, "a-valid")
			unknown := valid.Clone()
			unknown.AlertID, unknown.Fingerprint = "z-unknown", "fp-unknown"
			unknown.BeginAt = clock.at.Add(time.Minute)
			unknown.EnrichStatus = status
			unknown.Enrich = domain.JSONObject{"processors": json.RawMessage(`[{"cmdb":{"status":"` + string(status) + `","patches":[]}}]`)}
			if _, err := repo.CreateAlert(t.Context(), unknown); err != nil {
				t.Fatal(err)
			}
			clock.at = clock.at.Add(2 * time.Minute)
			p := dependencyProcessor(t, repo, clock, s, state, action)
			child := mustProcessAggregation(t, repo, p, dependencyEvent("child-known-main", "other-source", "child", clock))
			current, err := repo.GetAlert(t.Context(), "tenant", child.Event.RelatedAlertIDs[0])
			if err != nil || !current.Alert.Shield.Active || current.Alert.Shield.Bindings[0].MainAlertID != valid.AlertID {
				t.Fatalf("unrelated unevaluable candidate blocked valid main: shield=%+v err=%v", current.Alert.Shield, err)
			}
		})
	}
}

type unavailableDependencyTargets struct {
	shieldTargets
	failScope bool
}

func (s unavailableDependencyTargets) ResolveScope(ctx context.Context, tenant, space string) (onemodel.TargetScope, error) {
	if s.failScope {
		return onemodel.TargetScope{}, policy.ErrUnavailable
	}
	return s.shieldTargets.ResolveScope(ctx, tenant, space)
}

func (s unavailableDependencyTargets) Resolve(context.Context, string, string, onemodel.TargetDescriptor) (onemodel.TargetResult, error) {
	return onemodel.TargetResult{}, policy.ErrUnavailable
}

func TestDependencyCandidateFieldCompatibilityDoesNotHideTargetFailure(t *testing.T) {
	for _, failScope := range []bool{false, true} {
		t.Run(fmt.Sprint("scope=", failScope), func(t *testing.T) {
			repo, clock, s, _, _ := dependencyFixture(t, "custom_shield")
			admittedMainFixture(t, repo, clock, "known")
			release := dependencyRelease(t, "custom_shield")
			compiled, err := policy.Compile(policy.Shield, release.Spec)
			if err != nil {
				t.Fatal(err)
			}
			_, found, err := s.admittedMain(t.Context(), policy.FrozenPolicy{Release: release, Compiled: compiled}, unavailableDependencyTargets{failScope: failScope}, clock.at, func(string) (string, error) { return "2", nil })
			if found || !errors.Is(err, policy.ErrUnavailable) {
				t.Fatal("target failure became candidate exclusion", found, err)
			}
		})
	}
}

func TestDependencyReferenceBindsAndRechecksFrozenMain(t *testing.T) {
	repo, clock, s, state, action := dependencyFixture(t, "custom_shield")
	r := dependencyRelease(t, "custom_shield")
	var spec policy.ShieldSpec
	if err := json.Unmarshal(r.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	spec.RelyPolicy = json.RawMessage(`{"expression":"A","A":{"condition":"term","target_key":"object","target_value":"${model_inst_id}","is_alarm_field_referenced":true}}`)
	raw, _ := json.Marshal(spec)
	compiled, err := policy.Compile(policy.Shield, raw)
	if err != nil {
		t.Fatal(err)
	}
	r.Spec, r.Compiled = compiled.Canonical, compiled.Summary
	directory := &shieldDirectory{releases: []policy.Release{r}}
	s.Loader.Catalog = policy.NewCatalog(directory)
	s.Loader.Releases = directory
	p := dependencyProcessor(t, repo, clock, s, state, action)
	main := admittedMainFixture(t, repo, clock, "main")
	e := dependencyEvent("child", "child-source", "child", clock)
	e.ExtraData["object"] = json.RawMessage(`"101"`)
	child := mustProcessAggregation(t, repo, p, e)
	id := child.Event.RelatedAlertIDs[0]
	current, _ := repo.GetAlert(t.Context(), "tenant", id)
	if !current.Alert.Shield.Active || current.Alert.Shield.Bindings[0].MainAlertID != main.AlertID || len(action.calls) != 0 {
		t.Fatal("reference did not establish shield", current.Alert.Shield)
	}
	binding := current.Alert.Shield.Bindings[0].BindingID
	clock.at = clock.at.Add(time.Minute)
	if changed, err := p.CheckShield(t.Context(), "tenant", id); err != nil || changed {
		t.Fatal("reference recheck changed binding", err)
	}
	// 主告警读取成功但其字段不可用时保持关系，不把故障替换为空字符串后解除。
	s.CurrentAlert = func(ctx context.Context, tenant, alertID string) (store.StoredAlert, error) {
		row, err := repo.GetAlert(ctx, tenant, alertID)
		if alertID == main.AlertID {
			row.Alert.Enrich = domain.JSONObject{"processors": json.RawMessage(`[{"resource":{"status":"failed","patches":[]}}]`)}
		}
		return row, err
	}
	clock.at = clock.at.Add(time.Minute)
	if changed, err := p.CheckShield(t.Context(), "tenant", id); err != nil || changed {
		t.Fatal("unavailable origin released shield", err)
	}
	current, _ = repo.GetAlert(t.Context(), "tenant", id)
	if !current.Alert.Shield.Active || current.Alert.Shield.Bindings[0].BindingID != binding {
		t.Fatal("fixed binding lost on reference failure")
	}
}

func TestDependencyTimeBoundsAndStableTieBreak(t *testing.T) {
	for _, offset := range []time.Duration{-5 * time.Minute, -5*time.Minute - time.Nanosecond, 10 * time.Minute, 10*time.Minute + time.Nanosecond} {
		t.Run(offset.String(), func(t *testing.T) {
			repo, clock, s, state, action := dependencyFixture(t, "custom_shield")
			p := dependencyProcessor(t, repo, clock, s, state, action)
			admittedMainFixture(t, repo, clock, "z")
			admittedMainFixture(t, repo, clock, "a")
			e := dependencyEvent("child", "child-source", "child", clock)
			e.OccurredAt = e.OccurredAt.Add(offset)
			result := mustProcessAggregation(t, repo, p, e)
			a, _ := repo.GetAlert(t.Context(), "tenant", result.Event.RelatedAlertIDs[0])
			want := offset >= -5*time.Minute && offset <= 10*time.Minute
			if a.Alert.Shield.Active != want {
				t.Fatalf("bound=%v want=%v", a.Alert.Shield.Active, want)
			}
			if want && a.Alert.Shield.Bindings[0].MainAlertID != "a" {
				t.Fatal("unstable AlertID tie break")
			}
		})
	}
}

type dependencyRelations struct {
	calls  int
	err    error
	origin onemodel.InstanceRef
}

func (r *dependencyRelations) Lookup(_ context.Context, tenant string, origin onemodel.InstanceRef, relation, target string) ([]onemodel.InstanceRef, error) {
	r.calls++
	r.origin = origin
	if tenant != "tenant" || relation != "switch_connect_host" || target != "cw-Host" {
		return nil, policy.ErrAccess
	}
	return []onemodel.InstanceRef{{ModelID: "cw-Host", InstanceID: "101", EntityUID: "cw-Host|101"}}, r.err
}

func TestDependencyCMDBUsesTrustedMainAndRetainsOnRelationFailure(t *testing.T) {
	repo, clock, s, state, action := dependencyFixture(t, "cmdb_shield")
	relations := &dependencyRelations{}
	s.Relations = relations
	p := dependencyProcessor(t, repo, clock, s, state, action)
	mainEvent := dependencyEvent("switch", "switch-source", "main", clock)
	mainEvent.Labels["model_id"] = domain.NewStringScalar("cw-Switch")
	mainEvent.Labels["model_inst_id"] = domain.NewStringScalar("sw1")
	main := mustProcessAggregation(t, repo, p, mainEvent)
	child := mustProcessAggregation(t, repo, p, dependencyEvent("host", "host-source", "host", clock))
	id := child.Event.RelatedAlertIDs[0]
	a, _ := repo.GetAlert(t.Context(), "tenant", id)
	if !a.Alert.Shield.Active || a.Alert.Shield.Bindings[0].MainAlertID != main.Event.RelatedAlertIDs[0] || relations.origin.EntityUID != "cw-Switch|sw1" {
		t.Fatalf("incorrect canonical relation %+v %+v", a.Alert, relations)
	}
	relations.err = errors.New("backend unavailable")
	clock.at = clock.at.Add(time.Second)
	report, err := p.RecheckShield(t.Context(), "tenant", id, a.Alert.Revision)
	if err != nil || report.Changed || report.Outcome != "partial" || report.Decision == nil || report.Decision.Steps[0].Outcome != "skipped" || report.ResultRevision != a.Alert.Revision {
		t.Fatal("relation fault must retain binding and diagnose skipped check", report, err)
	}
	fresh := mustProcessAggregation(t, repo, p, dependencyEvent("host-2", "host-source", "host", clock))
	b, _ := repo.GetAlert(t.Context(), "tenant", fresh.Event.RelatedAlertIDs[0])
	if b.Alert.Shield.Active || fresh.Processing.PolicyDecision.Shield.Steps[0].Outcome != "skipped" {
		t.Fatal("new relation error not skipped")
	}
}

func TestRedisDependencyRegisteredBlockedMainAcrossSources(t *testing.T) {
	repo, clock, s, state, action := dependencyFixture(t, "custom_shield")
	s.Dependency = realSuppressionState(t)
	// 第一条依赖策略登记主，后一条时间屏蔽阻止其放行；登记资格不等同于处置资格。
	directory := s.Loader.Releases.(*shieldDirectory)
	directory.releases = append(directory.releases, timeShieldRelease(t))
	p := dependencyProcessor(t, repo, clock, s, state, action)
	main := mustProcessAggregation(t, repo, p, dependencyEvent("main", "source", "main", clock))
	mainID := main.Event.RelatedAlertIDs[0]
	a, _ := repo.GetAlert(t.Context(), "tenant", mainID)
	if !a.Alert.Shield.Active || a.Alert.Admission.AdmittedAt != nil {
		t.Fatal("fixture main not blocked")
	}
	child := mustProcessAggregation(t, repo, p, dependencyEvent("child", "other-source", "child", clock))
	id := child.Event.RelatedAlertIDs[0]
	b, _ := repo.GetAlert(t.Context(), "tenant", id)
	if !b.Alert.Shield.Active || b.Alert.Shield.Bindings[0].MainAlertID != mainID || b.Alert.Shield.Bindings[0].MainCandidate == nil || len(action.calls) != 0 {
		t.Fatalf("blocked registered main unused %+v", b.Alert)
	}
	end := dependencyEvent("end", "source", "main", clock)
	end.Fingerprint = "main"
	end.Evaluations[0].Action = domain.EventActionResolved
	mustProcessAggregation(t, repo, p, end)
	r := directory.releases[0]
	version := domain.PolicyVersion{ID: r.ID, Version: r.Version, Digest: r.Compiled.Digest}
	if _, exists, err := s.Dependency.GetDependencyMain(t.Context(), "tenant", version); err != nil || exists {
		t.Fatal("terminal did not clear registration", err)
	}
	clock.at = clock.at.Add(11 * time.Second)
	if changed, err := p.CheckShield(t.Context(), "tenant", id); err != nil || !changed {
		t.Fatal("timer did not release dependency", err)
	}
	b, _ = repo.GetAlert(t.Context(), "tenant", id)
	if b.Alert.Shield.Active || len(action.calls) != 0 {
		t.Fatal("timer dispatched child")
	}
}

// 同时触发的多个来源在完整生命周期中只能产生一个未屏蔽主，不能只测试 Redis 返回值。
func TestRedisDependencyConcurrentLifecycleHasOneMain(t *testing.T) {
	repo, clock, s, _, _ := dependencyFixture(t, "custom_shield")
	s.Dependency = realSuppressionState(t)
	processors := make([]*lifecycle.Processor, 16)
	for i := range processors {
		processors[i] = dependencyProcessor(t, repo, clock, s, &shieldHook{}, &shieldHook{})
	}
	type result struct {
		event store.StoredEvent
		err   error
	}
	results := make(chan result, 16)
	var wg sync.WaitGroup
	for i, p := range processors {
		wg.Go(func() {
			e := dependencyEvent(fmt.Sprintf("main-%d", i), fmt.Sprintf("source-%d", i), "main", clock)
			v, err := processAggregation(t.Context(), repo, p, e)
			results <- result{v, err}
		})
	}
	wg.Wait()
	close(results)
	mainID := ""
	bindings := []domain.ShieldBinding{}
	for v := range results {
		if v.err != nil {
			t.Fatal(v.err)
		}
		a, err := repo.GetAlert(t.Context(), "tenant", v.event.Event.RelatedAlertIDs[0])
		if err != nil {
			t.Fatal(err)
		}
		if a.Alert.Shield.Active {
			bindings = append(bindings, a.Alert.Shield.Bindings[0])
		} else {
			if mainID != "" {
				t.Fatal("more than one main admitted")
			}
			mainID = a.Alert.AlertID
		}
	}
	if mainID == "" || len(bindings) != 15 {
		t.Fatalf("main=%s bindings=%d", mainID, len(bindings))
	}
	for _, b := range bindings {
		if b.MainAlertID != mainID {
			t.Fatal("concurrent binding selected another main")
		}
	}
}

type failedCandidates struct {
	store.ActiveAlertReader
	err error
}

func (f failedCandidates) ListActiveAlerts(ctx context.Context, tenant, after string, limit int) (store.ActiveAlertPage, error) {
	return store.ActiveAlertPage{}, f.err
}

func TestDependencyDoesNotUseRegistryAfterIncompleteMainRead(t *testing.T) {
	repo, clock, s, state, action := dependencyFixture(t, "custom_shield")
	p := dependencyProcessor(t, repo, clock, s, state, action)
	s.Candidates = failedCandidates{err: errors.New("partial search")}
	first := mustProcessAggregation(t, repo, p, dependencyEvent("main", "source", "main", clock))
	if first.Processing.PolicyDecision.Shield.Steps[0].Outcome != "skipped" {
		t.Fatal("incomplete main search was used")
	}
	r := s.Loader.Releases.(*shieldDirectory).releases[0]
	v := domain.PolicyVersion{ID: r.ID, Version: r.Version, Digest: r.Compiled.Digest}
	if _, exists, err := s.Dependency.GetDependencyMain(t.Context(), "tenant", v); err != nil || exists {
		t.Fatal("partial read created registry candidate", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	e := dependencyEvent("cancel", "source", "main", clock)
	if _, err := processAggregation(ctx, repo, p, e); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation was swallowed", err)
	}
}

type relationDirectory struct {
	onemodel.TargetDirectory
	wrongTenant bool
	missing     bool
}

func (d relationDirectory) Model(_ context.Context, tenant, id string) (onemodel.ModelDefinition, bool, error) {
	if d.wrongTenant {
		tenant = "other"
	}
	return onemodel.ModelDefinition{TenantID: tenant, ModelID: id, DataSource: "cmdb", CMDBObjectID: "object"}, !d.missing, nil
}

type relationRows struct {
	onemodel.Reader
	calls       int
	wrongTenant bool
}

func (r *relationRows) Related(_ context.Context, tenant string, roots []onemodel.Instance, relation, direction string, q onemodel.Query) ([]onemodel.Instance, error) {
	r.calls++
	if len(roots) != 1 || roots[0].ModelCode != "cw-Switch" || relation != "raw_switch_connect_host" || direction != "both" || q.ModelID != "cw-Host" {
		return nil, policy.ErrInvalid
	}
	if r.wrongTenant {
		tenant = "other"
	}
	return []onemodel.Instance{{TenantID: tenant, ModelCode: "cw-Host", InstanceID: "101"}}, nil
}

func TestDependencyRelationAdapterValidatesBothModelTenantsAndRawIdentity(t *testing.T) {
	origin := onemodel.InstanceRef{ModelID: "cw-Switch", InstanceID: "s1", EntityUID: "cw-Switch|s1"}
	rows := &relationRows{}
	adapter := relationReader{reader: rows, directory: relationDirectory{}}
	result, err := adapter.Lookup(t.Context(), "tenant", origin, "raw_switch_connect_host", "cw-Host")
	if err != nil || len(result) != 1 || result[0].EntityUID != "cw-Host|101" {
		t.Fatal("raw relation mapping failed", err)
	}
	for _, d := range []onemodel.TargetDirectory{nil, relationDirectory{missing: true}, relationDirectory{wrongTenant: true}} {
		adapter.directory = d
		n := rows.calls
		if _, err := adapter.Lookup(t.Context(), "tenant", origin, "raw_switch_connect_host", "cw-Host"); err == nil || rows.calls != n {
			t.Fatal("invalid model directory reached relation backend", err)
		}
	}
	adapter.directory = relationDirectory{}
	rows.wrongTenant = true
	if _, err := adapter.Lookup(t.Context(), "tenant", origin, "raw_switch_connect_host", "cw-Host"); err == nil {
		t.Fatal("cross-tenant relation result accepted")
	}
}

func TestDependencyAuthorizationErrorDoesNotBecomePolicySkip(t *testing.T) {
	repo, clock, s, state, action := dependencyFixture(t, "cmdb_shield")
	s.Relations = &dependencyRelations{err: policy.ErrAccess}
	p := dependencyProcessor(t, repo, clock, s, state, action)
	e := dependencyEvent("switch", "switch-source", "main", clock)
	e.Labels["model_id"] = domain.NewStringScalar("cw-Switch")
	e.Labels["model_inst_id"] = domain.NewStringScalar("sw1")
	mustProcessAggregation(t, repo, p, e)
	if _, err := processAggregation(t.Context(), repo, p, dependencyEvent("host", "host-source", "host", clock)); !errors.Is(err, policy.ErrAccess) {
		t.Fatal("authorization error became ordinary skip", err)
	}
	if len(action.calls) != 1 {
		t.Fatal("denied child admitted")
	}
}

func TestDependencyRotatedUpgradeCannotDependOnItsOwnEndingLifecycle(t *testing.T) {
	repo, clock, s, state, action := dependencyFixture(t, "custom_shield")
	p := dependencyProcessor(t, repo, clock, s, state, action, "close_and_create")
	opening := mustProcessAggregation(t, repo, p, dependencyEvent("opening", "source", "main", clock))
	oldID := opening.Event.RelatedAlertIDs[0]
	clock.at = clock.at.Add(time.Second)
	upgrade := dependencyEvent("upgrade", "source", "main", clock)
	upgrade.Fingerprint = "opening"
	upgrade.Evaluations[0].Severity = "critical"
	result := mustProcessAggregation(t, repo, p, upgrade)
	if len(result.Event.RelatedAlertIDs) != 2 {
		t.Fatal("upgrade did not rotate")
	}
	old, _ := repo.GetAlert(t.Context(), "tenant", oldID)
	if !old.Alert.Status.Terminal() {
		t.Fatal("old lifecycle remained active")
	}
	for _, id := range result.Event.RelatedAlertIDs {
		if id == oldID {
			continue
		}
		current, err := repo.GetAlert(t.Context(), "tenant", id)
		if err != nil {
			t.Fatal(err)
		}
		if current.Alert.Shield.Active || current.Alert.Admission.Severity != "critical" {
			t.Fatalf("upgrade was blocked by old self %+v", current.Alert)
		}
	}
}

type shieldHintPublishFunc func(context.Context, string, string) (int64, error)

func (f shieldHintPublishFunc) PublishShieldHint(ctx context.Context, tenant, id string) (int64, error) {
	return f(ctx, tenant, id)
}

func TestDependencyTerminalHintIsBestEffortAndKeepsCancellation(t *testing.T) {
	for _, failure := range []error{nil, errors.New("private publish failure"), context.Canceled} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			called := false
			shielder := &Shielder{Dependency: &dependencyMemory{entries: map[string]redisstate.DependencyReservation{}}, Hints: shieldHintPublishFunc(func(_ context.Context, tenant, id string) (int64, error) {
				called = true
				if tenant != "tenant" || id != "main" {
					t.Error("hint identity changed")
				}
				if errors.Is(failure, context.Canceled) {
					cancel()
				}
				return 0, failure
			})}
			err := shielder.ClearDependency(ctx, "tenant", "main")
			if !called {
				t.Fatal("missing hint when main registration already gone")
			}
			if !errors.Is(failure, context.Canceled) && err != nil {
				t.Fatal("hint blocked committed terminal", err)
			}
			if errors.Is(failure, context.Canceled) && !errors.Is(err, context.Canceled) {
				t.Fatal("cancel hidden", err)
			}
		})
	}
}
