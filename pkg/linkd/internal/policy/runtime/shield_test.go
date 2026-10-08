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
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/onemodel"
	"linkd/internal/policy"
	"linkd/internal/shieldcheck"
	"linkd/internal/store"
	"linkd/internal/store/memory"
)

type shieldDirectory struct {
	releases []policy.Release
	err      error
}

func (d *shieldDirectory) GetRelease(_ context.Context, scope policy.Scope, id string, version int64) (policy.Release, error) {
	if d.err != nil {
		return policy.Release{}, d.err
	}
	for _, r := range d.releases {
		if r.Scope == scope && r.ID == id && r.Version == version {
			return r, nil
		}
	}
	return policy.Release{}, policy.ErrNotFound
}

func (d *shieldDirectory) List(_ context.Context, scope policy.Scope, after string, limit int) ([]policy.Record, error) {
	if d.err != nil {
		return nil, d.err
	}
	latest := map[string]policy.Release{}
	for _, r := range d.releases {
		if r.Scope == scope && r.ID > after && r.Version > latest[r.ID].Version {
			latest[r.ID] = r
		}
	}
	rows := []policy.Record{}
	for _, r := range latest {
		rows = append(rows, policy.Record{Scope: r.Scope, ID: r.ID, Revision: r.Version, Published: r.Version, Spec: r.Spec, Compiled: r.Compiled, Deleted: r.Deleted})
	}
	slices.SortFunc(rows, func(a, b policy.Record) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

type shieldTargets struct{ err error }

func (s shieldTargets) ResolveScope(_ context.Context, tenant, space string) (onemodel.TargetScope, error) {
	return onemodel.TargetScope{TenantID: tenant, SpaceCode: space, BusinessIDs: []int64{2}}, s.err
}

func (s shieldTargets) Resolve(ctx context.Context, tenant, space string, d onemodel.TargetDescriptor) (onemodel.TargetResult, error) {
	scope, err := s.ResolveScope(ctx, tenant, space)
	return onemodel.TargetResult{Scope: scope, Instances: d.Selectors[0].Instances}, err
}

type shieldHook struct{ calls []lifecycle.FinalHookInput }

func (h *shieldHook) Execute(_ context.Context, input lifecycle.FinalHookInput) (lifecycle.FinalHookResult, error) {
	h.calls = append(h.calls, input)
	return lifecycle.FinalHookResult{Skipped: true}, nil
}

type shieldLogFailure struct {
	store.Repository
	fail bool
}

func (r *shieldLogFailure) AppendAlertLogs(ctx context.Context, logs []domain.AlertLog) ([]store.AppendAlertLogItemResult, error) {
	if r.fail {
		r.fail = false
		return nil, errors.New("logs unavailable")
	}
	return r.Repository.AppendAlertLogs(ctx, logs)
}

func timeShieldRelease(t *testing.T) policy.Release {
	t.Helper()
	raw := json.RawMessage(`{"name":"maintenance","is_enable":true,"updated_at":"2026-09-30T00:00:00Z","space_code":"bkcc__2","model_id":"cw-Host","target_descriptor":{"schema_version":1,"model_id":"cw-Host","selectors":[{"type":"instances","instances":[{"model_id":"cw-Host","model_inst_id":"101","entity_uid":"cw-Host|101"}]}]},"activate_times":[{"period":"once","open_datetime_once":"2026-09-30T00:00:00Z","close_datetime_once":"2026-09-30T00:00:10Z"}],"policy":{"expression":"A","A":{"condition":"term","target_key":"source_id","target_value":"source"}},"shield_type":"time_shield","reason":"maintenance","alarm_tags":[12]}`)
	c, err := policy.Compile(policy.Shield, raw)
	if err != nil {
		t.Fatal(err)
	}
	return policy.Release{Scope: policy.Scope{TenantID: "tenant", Kind: policy.Shield}, ID: "maintenance", Version: 1, Spec: c.Canonical, Compiled: c.Summary}
}

func shieldEvent(id string, clock *policyTestClock) domain.Event {
	e := aggregationEvent(id, "source", "fp", clock.at)
	e.Labels["model_id"] = domain.NewStringScalar("cw-Host")
	e.Labels["model_inst_id"] = domain.NewStringScalar("101")
	return e
}

func newShieldProcessor(t *testing.T, repo store.Repository, clock *policyTestClock, d *shieldDirectory, state, action *shieldHook) (*lifecycle.Processor, *Shielder) {
	t.Helper()
	catalog := policy.NewCatalog(d)
	loader := &Suppressor{Releases: d, Catalog: catalog, Targets: shieldTargets{}}
	shielder := &Shielder{Loader: loader, Events: repo.GetEvent}
	p, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, []lifecycle.NamedFinalHook{{Name: "state", Purpose: "state", Hook: state}, {Name: "action", Purpose: "action", Hook: action}}, config.DefaultSeverityConfig(), clock, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithPolicySnapshotter(Snapshotter{Catalog: catalog}), lifecycle.WithShieldEvaluator(shielder), lifecycle.WithSeverityUpgradePolicy("update_current"))
	if err != nil {
		t.Fatal(err)
	}
	return p, shielder
}

func TestTimeShieldTimerDoesNotAdmitUntilNextTrigger(t *testing.T) {
	repo := memory.New()
	clock := &policyTestClock{at: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	directory := &shieldDirectory{releases: []policy.Release{timeShieldRelease(t)}}
	state, action := &shieldHook{}, &shieldHook{}
	p, _ := newShieldProcessor(t, repo, clock, directory, state, action)
	opening := mustProcessAggregation(t, repo, p, shieldEvent("opening", clock))
	id := opening.Event.RelatedAlertIDs[0]
	current, err := repo.GetAlert(t.Context(), "tenant", id)
	if err != nil {
		t.Fatal(err)
	}
	if !current.Alert.Shield.Active || current.Alert.Status != domain.AlertStatusActive || current.Alert.Admission.AdmittedAt != nil || len(action.calls) != 0 || len(state.calls) != 1 || !slices.Equal(current.Alert.PolicyTags, []int64{12}) {
		t.Fatalf("shield did not stop action %+v", current.Alert)
	}
	clock.at = clock.at.Add(5 * time.Second)
	next := shieldEvent("repeat-shielded", clock)
	next.Title = "new title must not replace opening"
	mustProcessAggregation(t, repo, p, next)
	if len(action.calls) != 0 {
		t.Fatal("shielded repeat admitted")
	}
	clock.at = clock.at.Add(6 * time.Second)
	changed, err := p.CheckShield(t.Context(), "tenant", id)
	if err != nil || !changed {
		t.Fatal("timer did not release", err)
	}
	current, err = repo.GetAlert(t.Context(), "tenant", id)
	if err != nil {
		t.Fatal(err)
	}
	if current.Alert.Shield.Active || current.Alert.Status != domain.AlertStatusActive || current.Alert.Admission.AdmittedAt != nil || current.Alert.PolicyChange != nil || len(action.calls) != 0 || current.Alert.LatestEventID != next.EventID || current.Alert.Title != opening.Event.Title {
		t.Fatalf("timer created admission or changed facts %+v", current.Alert)
	}
	if changed, err := p.CheckShield(t.Context(), "tenant", id); err != nil || changed {
		t.Fatal("empty timer repeated release", err)
	}
	clock.at = clock.at.Add(time.Second)
	admitted := mustProcessAggregation(t, repo, p, shieldEvent("after-release", clock))
	if admitted.Processing.State != domain.EventProcessStateAccepted || len(action.calls) != 1 {
		t.Fatal("next trigger was blocked by duplicate suppression")
	}
	repeated := mustProcessAggregation(t, repo, p, shieldEvent("after-admission", clock))
	if repeated.Processing.ReasonCode != "duplicate_trigger" || len(action.calls) != 1 {
		t.Fatal("admitted repeat emitted another action")
	}
	for _, call := range state.calls {
		if call.Alert.PolicyChange != nil {
			t.Fatal("internal output intent leaked through hook")
		}
	}
	logs, err := repo.ListAlertLogs(t.Context(), "tenant", id, store.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	shields, unshields := 0, 0
	for _, log := range logs.Logs {
		if log.OperationKind == domain.OperationKindShield {
			shields++
		}
		if log.OperationKind == domain.OperationKindUnshield {
			unshields++
		}
	}
	if shields != 1 || unshields != 1 {
		t.Fatalf("incorrect transition logs %d %d", shields, unshields)
	}
}

func TestPartialUnshieldHistoryKeepsRetainedBindingAndCompleteTransition(t *testing.T) {
	repo := memory.New()
	clock := &policyTestClock{at: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	first, second := timeShieldRelease(t), timeShieldRelease(t)
	second.ID = "longer-maintenance"
	var spec policy.ShieldSpec
	if err := json.Unmarshal(second.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	spec.Times[0].CloseOnce = "2026-09-30T00:00:20Z"
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := policy.Compile(policy.Shield, raw)
	if err != nil {
		t.Fatal(err)
	}
	second.Spec, second.Compiled = compiled.Canonical, compiled.Summary
	state, action := &shieldHook{}, &shieldHook{}
	p, _ := newShieldProcessor(t, repo, clock, &shieldDirectory{releases: []policy.Release{first, second}}, state, action)
	opening := mustProcessAggregation(t, repo, p, shieldEvent("opening-multiple", clock))
	id := opening.Event.RelatedAlertIDs[0]
	// 新匹配仍按 KAC 取首条命中；这里构造领域模型允许的多绑定持久快照，专门验证部分解除历史。
	stored, err := repo.GetAlert(t.Context(), "tenant", id)
	if err != nil {
		t.Fatal(err)
	}
	multiple := stored.Alert.Clone()
	multiple.Shield.Bindings = nil
	for _, release := range []policy.Release{first, second} {
		c, err := policy.Compile(policy.Shield, release.Spec)
		if err != nil {
			t.Fatal(err)
		}
		activation, err := c.Schedule.Occurrence(clock.at)
		if err != nil {
			t.Fatal(err)
		}
		b := domain.ShieldBinding{Policy: domain.PolicyVersion{ID: release.ID, Version: release.Version, Digest: release.Compiled.Digest}, Type: "time_shield", SourceEventID: opening.Event.EventID, Severity: "warning", BoundAt: clock.at, Reason: c.Shield.Reason, ActivationID: activation}
		b.BindingID = shieldBindingID("tenant", id, b)
		multiple.Shield.Bindings = append(multiple.Shield.Bindings, b)
	}
	multiple.UpdateAt = multiple.UpdateAt.Add(time.Microsecond)
	if _, err := repo.CompareAndSetAlert(t.Context(), "tenant", id, stored.Version, multiple); err != nil {
		t.Fatal(err)
	}
	clock.at = clock.at.Add(11 * time.Second)
	if changed, err := p.CheckShield(t.Context(), "tenant", id); err != nil || !changed {
		t.Fatal(changed, err)
	}
	a, err := repo.GetAlert(t.Context(), "tenant", id)
	if err != nil || len(a.Alert.Shield.Bindings) != 1 || a.Alert.Shield.Bindings[0].Policy.ID != second.ID || len(action.calls) != 0 {
		t.Fatal("retained binding changed", a, err)
	}
	logs, err := repo.ListAlertLogs(t.Context(), "tenant", id, store.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	shieldCount, unshieldCount := 0, 0
	for _, log := range logs.Logs {
		switch log.OperationKind {
		case domain.OperationKindShield:
			shieldCount++
			var after []domain.ShieldBinding
			if err := json.Unmarshal(log.Params["after_bindings"], &after); err != nil || len(after) != 1 {
				t.Fatal("initial transition lost bindings", err)
			}
		case domain.OperationKindUnshield:
			unshieldCount++
			var removed, before, after []domain.ShieldBinding
			for key, target := range map[string]*[]domain.ShieldBinding{"bindings": &removed, "before_bindings": &before, "after_bindings": &after} {
				if err := json.Unmarshal(log.Params[key], target); err != nil {
					t.Fatal(err)
				}
			}
			if len(removed) != 1 || removed[0].Policy.ID != first.ID || len(before) != 2 || len(after) != 1 || after[0].Policy.ID != second.ID {
				t.Fatal("partial release reported retained relation as removed")
			}
		}
	}
	if shieldCount != 1 || unshieldCount != 1 {
		t.Fatalf("retained binding was re-created in history: %d/%d", shieldCount, unshieldCount)
	}
}

func TestTimerStateCASRetainsIntentAfterLogFailure(t *testing.T) {
	base := memory.New()
	repo := &shieldLogFailure{Repository: base}
	clock := &policyTestClock{at: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	state, action := &shieldHook{}, &shieldHook{}
	p, _ := newShieldProcessor(t, repo, clock, &shieldDirectory{releases: []policy.Release{timeShieldRelease(t)}}, state, action)
	opening := mustProcessAggregation(t, repo, p, shieldEvent("opening", clock))
	id := opening.Event.RelatedAlertIDs[0]
	clock.at = clock.at.Add(11 * time.Second)
	repo.fail = true
	report, checkErr := p.RecheckShield(t.Context(), "tenant", id, 0)
	if checkErr == nil || report.Outcome != "failed" || !report.Changed || report.ResultRevision != report.ObservedRevision+1 || report.RemainingBindings != 0 {
		t.Fatal("post-CAS interruption lost confirmed state change", report, checkErr)
	}
	pending, err := repo.GetAlert(t.Context(), "tenant", id)
	if err != nil || pending.Alert.Shield.Active || pending.Alert.PolicyChange == nil {
		t.Fatal("pending output intent lost", err)
	}
	page, err := base.ListShieldWork(t.Context(), store.ShieldWorkCursor{}, clock.at, 16)
	if err != nil || len(page.Alerts) != 1 {
		t.Fatal("released pending intent was not discoverable", err)
	}
	operation := pending.Alert.PolicyChange.OperationID
	if _, err := p.CheckShield(t.Context(), "tenant", id); err != nil {
		t.Fatal(err)
	}
	final, err := repo.GetAlert(t.Context(), "tenant", id)
	if err != nil || final.Alert.PolicyChange != nil || final.Alert.UpdateAt != pending.Alert.UpdateAt || len(action.calls) != 0 {
		t.Fatal("metadata completion changed business state", err)
	}
	if len(state.calls) != 3 || state.calls[1].Cause.ID != operation || state.calls[2].Cause.ID != operation {
		t.Fatal("repair changed stable operation identity")
	}
	page, err = base.ListShieldWork(t.Context(), store.ShieldWorkCursor{}, clock.at, 16)
	if err != nil || len(page.Alerts) != 0 {
		t.Fatal("completed intent remained queued", err)
	}
}

func TestShieldFailureSkipsNewButDoesNotReleaseExisting(t *testing.T) {
	repo := memory.New()
	clock := &policyTestClock{at: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	state, action := &shieldHook{}, &shieldHook{}
	p, shielder := newShieldProcessor(t, repo, clock, &shieldDirectory{releases: []policy.Release{timeShieldRelease(t)}}, state, action)
	opening := mustProcessAggregation(t, repo, p, shieldEvent("opening", clock))
	shielder.Loader.Targets = shieldTargets{err: errors.New("private metadata failure")}
	clock.at = clock.at.Add(5 * time.Second)
	if changed, err := p.CheckShield(t.Context(), "tenant", opening.Event.RelatedAlertIDs[0]); err != nil || changed {
		t.Fatal("dependency failure released existing binding", err)
	}
	current, _ := repo.GetAlert(t.Context(), "tenant", opening.Event.RelatedAlertIDs[0])
	if !current.Alert.Shield.Active {
		t.Fatal("existing shield vanished")
	}
	other := shieldEvent("other", clock)
	other.Fingerprint = "other-fp"
	result := mustProcessAggregation(t, repo, p, other)
	if result.Processing.PolicyDecision.Shield.Steps[0].Outcome != "skipped" || len(action.calls) != 1 {
		t.Fatal("new policy failure did not record skip")
	}
}

func TestShieldedUpgradeRetainsNewLevelAdmissionOpportunity(t *testing.T) {
	repo := memory.New()
	clock := &policyTestClock{at: time.Date(2026, 9, 29, 23, 59, 59, 0, time.UTC)}
	state, action := &shieldHook{}, &shieldHook{}
	p, _ := newShieldProcessor(t, repo, clock, &shieldDirectory{releases: []policy.Release{timeShieldRelease(t)}}, state, action)
	first := mustProcessAggregation(t, repo, p, shieldEvent("warning", clock))
	id := first.Event.RelatedAlertIDs[0]
	clock.at = clock.at.Add(time.Second)
	upgrade := shieldEvent("critical", clock)
	upgrade.Evaluations[0].Severity = "critical"
	mustProcessAggregation(t, repo, p, upgrade)
	current, err := repo.GetAlert(t.Context(), "tenant", id)
	if err != nil {
		t.Fatal(err)
	}
	if current.Alert.Severity != "critical" || !current.Alert.Shield.Active || current.Alert.Admission.Severity != "warning" || len(action.calls) != 1 {
		t.Fatalf("upgrade state and admission conflated %+v", current.Alert)
	}
	clock.at = clock.at.Add(11 * time.Second)
	if _, err := p.CheckShield(t.Context(), "tenant", id); err != nil {
		t.Fatal(err)
	}
	if len(action.calls) != 1 {
		t.Fatal("timer admitted previously blocked upgrade")
	}
	next := shieldEvent("critical-after-release", clock)
	next.Evaluations[0].Severity = "critical"
	mustProcessAggregation(t, repo, p, next)
	current, err = repo.GetAlert(t.Context(), "tenant", id)
	if err != nil || current.Alert.Admission.Severity != "critical" || current.Alert.Admission.CauseID != next.EventID || len(action.calls) != 2 {
		t.Fatal("upgrade opportunity lost after unshield", err)
	}
}

func TestSourceTerminalClearsShieldWithoutCreatingAction(t *testing.T) {
	repo := memory.New()
	clock := &policyTestClock{at: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	state, action := &shieldHook{}, &shieldHook{}
	p, _ := newShieldProcessor(t, repo, clock, &shieldDirectory{releases: []policy.Release{timeShieldRelease(t)}}, state, action)
	first := mustProcessAggregation(t, repo, p, shieldEvent("opening", clock))
	id := first.Event.RelatedAlertIDs[0]
	terminal := shieldEvent("recover", clock)
	terminal.Evaluations[0].Action = domain.EventActionResolved
	mustProcessAggregation(t, repo, p, terminal)
	current, err := repo.GetAlert(t.Context(), "tenant", id)
	if err != nil || current.Alert.Status != domain.AlertStatusRecovered || current.Alert.Shield.Active || len(action.calls) != 0 {
		t.Fatal("terminal blocked by shield", err)
	}
	page, err := repo.ListShieldWork(t.Context(), store.ShieldWorkCursor{}, clock.at.Add(time.Minute), 16)
	if err != nil || len(page.Alerts) != 0 {
		t.Fatal("terminal retained active work", err)
	}
	assertTerminalShieldHistory(t, repo, id)
}

func assertTerminalShieldHistory(t *testing.T, repo store.Repository, id string) {
	t.Helper()
	page, err := repo.ListAlertLogs(t.Context(), "tenant", id, store.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, log := range page.Logs {
		if log.OperationKind != domain.OperationKindUnshield {
			continue
		}
		count++
		var before, after []domain.ShieldBinding
		if json.Unmarshal(log.Params["before_bindings"], &before) != nil || json.Unmarshal(log.Params["after_bindings"], &after) != nil || len(before) != 1 || len(after) != 0 {
			t.Fatal("terminal cleanup lost binding history")
		}
	}
	if count != 1 {
		t.Fatalf("terminal unshield logs=%d", count)
	}
}

func TestManualCloseShieldCleanupSurvivesLogFailure(t *testing.T) {
	base := memory.New()
	repo := &shieldLogFailure{Repository: base}
	clock := &policyTestClock{at: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	state, action := &shieldHook{}, &shieldHook{}
	p, _ := newShieldProcessor(t, repo, clock, &shieldDirectory{releases: []policy.Release{timeShieldRelease(t)}}, state, action)
	first := mustProcessAggregation(t, repo, p, shieldEvent("manual-opening", clock))
	id := first.Event.RelatedAlertIDs[0]
	clock.at = clock.at.Add(time.Second)
	command := lifecycle.CloseAlertCommand{BKTenantID: "tenant", AlertID: id, OperationID: "close-shield", OperatorID: "operator", OperatorKind: domain.OperatorKindUser, Reason: "manual", EffectiveAt: clock.at}
	repo.fail = true
	if _, err := p.CloseAlert(t.Context(), command); err == nil {
		t.Fatal("expected log failure")
	}
	pending, err := base.GetAlert(t.Context(), "tenant", id)
	if err != nil || pending.Alert.Status != domain.AlertStatusClosed || pending.Alert.Shield.Active || pending.Alert.PolicyChange == nil {
		t.Fatal("close lost cleanup intent", pending, err)
	}
	beforeRevision, operation := pending.Alert.Revision, pending.Alert.PolicyChange.OperationID
	closed, err := p.CloseAlert(t.Context(), command)
	if err != nil || !closed.AlreadyClosed || closed.Alert.PolicyChange != nil || closed.Alert.Revision != beforeRevision || len(action.calls) != 0 {
		t.Fatal("retry changed terminal business state or emitted action", closed, err)
	}
	if len(state.calls) < 3 || state.calls[1].Cause.ID != operation || state.calls[2].Cause.ID != operation {
		t.Fatal("cleanup retry changed operation")
	}
	assertTerminalShieldHistory(t, base, id)
}

func TestManualShieldRevisionFencePrecedesEvaluationAndOutput(t *testing.T) {
	repo := memory.New()
	clock := &policyTestClock{at: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	directory := &shieldDirectory{releases: []policy.Release{timeShieldRelease(t)}}
	state, action := &shieldHook{}, &shieldHook{}
	p, _ := newShieldProcessor(t, repo, clock, directory, state, action)
	opening := mustProcessAggregation(t, repo, p, shieldEvent("manual-opening", clock))
	id := opening.Event.RelatedAlertIDs[0]
	before, err := repo.GetAlert(t.Context(), "tenant", id)
	if err != nil {
		t.Fatal(err)
	}
	clock.at = clock.at.Add(11 * time.Second)
	r, err := p.RecheckShield(t.Context(), "tenant", id, before.Alert.Revision+1)
	if !errors.Is(err, lifecycle.ErrShieldCheckStale) || r.Outcome != "superseded" || r.Changed || r.Decision != nil || len(state.calls) != 1 || len(action.calls) != 0 {
		t.Fatal("stale request side effects", r, err)
	}
	unchanged, err := repo.GetAlert(t.Context(), "tenant", id)
	if err != nil || unchanged.Version != before.Version {
		t.Fatal("stale request wrote Alert", err)
	}
	r, err = p.RecheckShield(t.Context(), "tenant", id, before.Alert.Revision)
	if err != nil || r.Outcome != "changed" || !r.Changed || r.ResultRevision != before.Alert.Revision+1 || r.RemainingBindings != 0 || len(action.calls) != 0 {
		t.Fatal("manual check admitted or report lost change", r, err)
	}
	r, err = p.RecheckShield(t.Context(), "tenant", id, r.ResultRevision)
	if err != nil || r.Outcome != "inactive" || r.Changed || len(action.calls) != 0 {
		t.Fatal(r, err)
	}
}

func TestRealRecheckReportIncludesCandidateStepsAndValidatesAsDiagnostic(t *testing.T) {
	repo := memory.New()
	clock := &policyTestClock{at: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	state, action := &shieldHook{}, &shieldHook{}
	p, _ := newShieldProcessor(t, repo, clock, &shieldDirectory{releases: []policy.Release{timeShieldRelease(t)}}, state, action)
	opening := mustProcessAggregation(t, repo, p, shieldEvent("opening", clock))
	id := opening.Event.RelatedAlertIDs[0]
	clock.at = clock.at.Add(11 * time.Second)
	started := time.Now().UTC()
	report, err := p.RecheckShield(t.Context(), "tenant", id, 0)
	if err != nil || !report.Changed || report.Decision == nil || len(report.Decision.Steps) != 2 || !report.Decision.Steps[0].FromBinding || report.Decision.Steps[1].FromBinding {
		t.Fatal("expected release and candidate rule steps", report, err)
	}
	diagnostic := shieldcheck.Check{TenantID: "tenant", AlertID: id, Trigger: "hint", StartedAt: started, FinishedAt: time.Now().UTC(), Report: report}
	if err := diagnostic.Validate(); err != nil {
		t.Fatal("actual recheck cannot be persisted", err)
	}
	if len(action.calls) != 0 {
		t.Fatal("diagnostic changed admission behavior")
	}
}
