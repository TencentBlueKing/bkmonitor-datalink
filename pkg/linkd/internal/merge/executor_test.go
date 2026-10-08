// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package merge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/eventsource"
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	policyruntime "linkd/internal/policy/runtime"
	"linkd/internal/runtimeconfig"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type executorPolicies struct{ release policy.Release }

func (p executorPolicies) List(_ context.Context, scope policy.Scope, after string, _ int) ([]policy.Record, error) {
	if scope != p.release.Scope || after != "" {
		return nil, nil
	}
	r := p.release
	return []policy.Record{{Scope: r.Scope, ID: r.ID, Revision: r.Version, Published: r.Version, Spec: r.Spec, Compiled: r.Compiled}}, nil
}

func (p executorPolicies) GetRelease(_ context.Context, scope policy.Scope, id string, version int64) (policy.Release, error) {
	if scope != p.release.Scope || id != p.release.ID || version != p.release.Version {
		return policy.Release{}, policy.ErrAccess
	}
	return p.release, nil
}

type executorWindows struct {
	result                     policyruntime.MergeJudgment
	reads, judgments, finishes int
	finishErr                  error
}

func (w *executorWindows) ReadMergeWindow(context.Context, string, string) (redisstate.MergeWindow, bool, error) {
	w.reads++
	return w.result.Window, !w.result.Lost, nil
}

func (w *executorWindows) Evaluate(context.Context, string, string, time.Time, func(string) (string, error)) (policyruntime.MergeJudgment, error) {
	w.judgments++
	return w.result, nil
}

func (w *executorWindows) FinishMergeWindow(context.Context, string, string) error {
	w.finishes++
	err := w.finishErr
	w.finishErr = nil
	return err
}

type executorFixture struct {
	engine *Executor
	relationFixture
	state  *executorWindows
	source *eventsource.Release
}

func newExecutorFixture(t *testing.T) executorFixture {
	t.Helper()
	j, docs := newJournal(t)
	repo := memory.New()
	d, members, source, severity := renderFixture(t)
	state := &executorWindows{result: policyruntime.MergeJudgment{Frozen: true, Window: redisstate.MergeWindow{ID: d.WindowID, TenantID: d.TenantID, Policy: domain.PolicyVersion{ID: d.Policy.ID, Version: d.Policy.Version, Digest: d.Policy.Compiled.Digest}, GroupKey: d.GroupKey, StartedAtMillis: d.StartedAt.UnixMilli(), DeadlineMillis: d.Deadline.UnixMilli(), Frozen: &redisstate.MergeVerdict{Outcome: d.Outcome, OperationID: d.ID, AtMillis: d.FrozenAt.UnixMilli(), MemberIDs: d.MemberIDs}}}}
	for _, id := range d.WaitMemberIDs {
		state.result.Window.Members = append(state.result.Window.Members, redisstate.MergeMember{Main: domain.DependencyMain{AlertID: id}})
	}
	for _, m := range members {
		a := m.Alert
		a.Merge = &domain.AlertMerge{Role: "original", State: "pending", Pending: []domain.MergeWait{{WindowID: d.WindowID, Policy: state.result.Window.Policy, GroupKey: d.GroupKey, MemberEventID: a.TriggerEventID, Severity: a.Severity, Groups: []int{0}, StartedAt: d.StartedAt, Deadline: d.Deadline}}}
		saved, err := repo.CreateAlert(t.Context(), a)
		if err != nil {
			t.Fatal(err)
		}
		state.result.Members = append(state.result.Members, saved.StoredAlert)
	}
	action, output := &relationHook{}, &relationHook{}
	p, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, []lifecycle.NamedFinalHook{{Name: "action", Purpose: "action", Hook: action}, {Name: "state", Purpose: "state", Hook: output}}, config.DefaultSeverityConfig(), journalClock{at: d.FrozenAt.Add(time.Second)}, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithMergeRelations(j))
	if err != nil {
		t.Fatal(err)
	}
	e := &Executor{Journal: j, Publisher: &Publisher{Journal: j, Events: repo, Mailboxes: &mailboxFailure{repo: repo}}, Policies: executorPolicies{d.Policy}, Judge: state, Windows: state, Operations: p, CurrentAlert: repo.GetAlert, Event: repo.GetEvent, Source: func(context.Context) (eventsource.Release, error) { return source, nil }, Severity: func() runtimeconfig.Snapshot { return severity }}
	f := executorFixture{engine: e, relationFixture: relationFixture{journal: j, docs: docs, repo: repo, processor: p, action: action, state: output, decision: d}, state: state, source: &source}
	return f
}

func startExecutor(t *testing.T, f executorFixture) StoredDecision {
	t.Helper()
	a, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, "a")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.engine.CheckWork(t.Context(), store.MergeWorkItem{Alert: a, WindowID: f.decision.WindowID}, f.decision.FrozenAt); err != nil {
		t.Fatal(err)
	}
	d, err := f.journal.GetByWindow(t.Context(), f.decision.TenantID, f.decision.WindowID)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func driveExecutor(t *testing.T, f executorFixture) StoredDecision {
	t.Helper()
	for range 1024 {
		d, err := f.journal.Get(t.Context(), f.decision.TenantID, f.decision.ID)
		if err != nil {
			t.Fatal(err)
		}
		if d.Decision.Progress.Phase == "completed" {
			return d
		}
		if d.Decision.Progress.Phase == "waiting_parent" {
			event, err := f.repo.GetEvent(t.Context(), f.decision.TenantID, d.Decision.Progress.ParentEvent.EventID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.processor.ProcessEvent(t.Context(), event); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.engine.StepDecision(t.Context(), f.decision.TenantID, f.decision.ID, f.decision.FrozenAt.Add(2*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("executor did not complete")
	return StoredDecision{}
}

func TestExecutorCreatesRealParentAndRetriesFinalWindowCleanup(t *testing.T) {
	f := newExecutorFixture(t)
	initial := startExecutor(t, f)
	if initial.Decision.Progress.CaptureOffset != len(f.decision.MemberIDs) || len(f.action.inputs) != 0 {
		t.Fatal("captures or action gate incomplete")
	}
	done := driveExecutor(t, f)
	if done.Decision.Progress.ParentAlertID == "" || len(f.action.inputs) != 1 {
		t.Fatal("real parent not admitted once")
	}
	if f.state.judgments != 1 {
		t.Fatal("frozen decision reevaluated")
	}
	f.state.finishErr = errors.New("redis unavailable")
	if err := f.engine.StepDecision(t.Context(), f.decision.TenantID, f.decision.ID, f.decision.FrozenAt.Add(3*time.Second)); err == nil {
		t.Fatal("cleanup failure hidden")
	}
	work, err := f.journal.ListWork(t.Context(), "", 16)
	if err != nil || len(work.Decisions) != 1 {
		t.Fatal("completed cleanup retry lost", err)
	}
	if err := f.engine.StepDecision(t.Context(), f.decision.TenantID, f.decision.ID, f.decision.FrozenAt.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	work, err = f.journal.ListWork(t.Context(), "", 16)
	if err != nil || len(work.Decisions) != 0 || f.state.finishes != 2 || len(f.action.inputs) != 1 {
		t.Fatal("cleanup repeated business action", err)
	}
}

func TestRelationCheckMakesBoundedProgressAndResumesAfterCancellation(t *testing.T) {
	f := newExecutorFixture(t)
	for i := 0; i < 20; i++ {
		f.state.result.Window.Members = append(f.state.result.Window.Members, redisstate.MergeMember{Main: domain.DependencyMain{AlertID: fmt.Sprintf("reserved-%02d", i)}})
	}
	startExecutor(t, f)
	done := driveExecutor(t, f)
	parent, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, done.Decision.Progress.ParentAlertID)
	if err != nil {
		t.Fatal(err)
	}
	f.parent = parent
	// 活动关系复查不应改写状态；同一次调用也不能在无进展的关系上反复执行。
	before, err := f.journal.GetRelation(t.Context(), f.decision.TenantID, f.decision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.engine.CheckRelation(t.Context(), f.decision.TenantID, f.decision.ID, f.decision.FrozenAt.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	after, err := f.journal.GetRelation(t.Context(), f.decision.TenantID, f.decision.ID)
	if err != nil || after.Version != before.Version {
		t.Fatal("active relation mutated", err)
	}
	closeRelationParent(t, f.relationFixture)
	actions := len(f.action.inputs)
	if err := f.engine.CheckRelation(t.Context(), f.decision.TenantID, f.decision.ID, f.decision.FrozenAt.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	partial, err := f.journal.GetRelation(t.Context(), f.decision.TenantID, f.decision.ID)
	if err != nil || partial.Relation.EndOffset != 16 || partial.Relation.State != "ending" {
		t.Fatalf("expected one bounded batch: %+v %v", partial.Relation, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := f.engine.CheckRelation(ctx, f.decision.TenantID, f.decision.ID, f.decision.FrozenAt.Add(5*time.Second)); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
	if err := f.engine.CheckRelation(t.Context(), f.decision.TenantID, f.decision.ID, f.decision.FrozenAt.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	ended, err := f.journal.GetRelation(t.Context(), f.decision.TenantID, f.decision.ID)
	if err != nil || ended.Relation.State != "ended" || ended.Relation.EndOffset != len(ended.Relation.WaitMemberIDs) || len(f.action.inputs) != actions {
		t.Fatal("relation did not resume without child actions", err)
	}
}

func TestExecutorPreparedParentIgnoresLaterSourceChanges(t *testing.T) {
	f := newExecutorFixture(t)
	startExecutor(t, f)
	if err := f.engine.StepDecision(t.Context(), f.decision.TenantID, f.decision.ID, f.decision.FrozenAt); err != nil {
		t.Fatal(err)
	}
	f.engine.Source = func(context.Context) (eventsource.Release, error) {
		return eventsource.Release{}, errors.New("source service unavailable")
	}
	done := driveExecutor(t, f)
	if done.Decision.Progress.ParentAlertID == "" || len(f.action.inputs) != 1 {
		t.Fatal("prepared parent depended on new source")
	}
}

func TestExecutorDisabledParentSourceReleasesWithoutInventingParent(t *testing.T) {
	f := newExecutorFixture(t)
	f.source.Spec.Enabled = false
	startExecutor(t, f)
	done := driveExecutor(t, f)
	if done.Decision.Progress.ParentAlertID != "" || done.Decision.Progress.ParentEvent != nil || done.Decision.Progress.ReasonCode != "parent_template_unavailable" || len(f.action.inputs) != 2 {
		t.Fatal("disabled source created parent or lost child release")
	}
}

func TestExecutorLateReservedMemberHonorsParentCloseRule(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(map[bool]string{false: "active parent", true: "closed parent"}[closed], func(t *testing.T) {
			f := newExecutorFixture(t)
			startExecutor(t, f)
			done := driveExecutor(t, f)
			parent, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, done.Decision.Progress.ParentAlertID)
			if err != nil {
				t.Fatal(err)
			}
			f.parent = parent
			if closed {
				closeRelationParent(t, f.relationFixture)
			}
			actionCount := len(f.action.inputs)
			a := storetest.Alert(f.decision.TenantID, "uncommitted", "late-opening", "late-fp", "warning")
			a.Merge = &domain.AlertMerge{Role: "original", State: "pending", Pending: []domain.MergeWait{{WindowID: f.decision.WindowID, Policy: f.state.result.Window.Policy, GroupKey: f.decision.GroupKey, MemberEventID: a.TriggerEventID, Severity: a.Severity, Groups: []int{0}, StartedAt: f.decision.StartedAt, Deadline: f.decision.Deadline}}}
			saved, err := f.repo.CreateAlert(t.Context(), a)
			if err != nil {
				t.Fatal(err)
			}
			for range 8 {
				if err := f.engine.CheckWork(t.Context(), store.MergeWorkItem{Alert: saved.StoredAlert, WindowID: f.decision.WindowID}, f.decision.FrozenAt.Add(3*time.Second)); err != nil {
					t.Fatal(err)
				}
				got, err := f.repo.GetAlert(t.Context(), a.BKTenantID, a.AlertID)
				if err != nil {
					t.Fatal(err)
				}
				if !got.Alert.Merge.Blocking() {
					break
				}
			}
			got, err := f.repo.GetAlert(t.Context(), a.BKTenantID, a.AlertID)
			if err != nil || got.Alert.Merge.Blocking() || got.Alert.Status != domain.AlertStatusActive {
				t.Fatal("late candidate stuck", err)
			}
			expected := actionCount + 1
			if closed {
				expected = actionCount
			}
			if len(f.action.inputs) != expected || f.state.judgments != 1 {
				t.Fatal("late member reran verdict or wrong action")
			}
		})
	}
}

func TestExecutorCaptureRetriesSavedSnapshotAfterProgressFailure(t *testing.T) {
	f := newExecutorFixture(t)
	current, err := f.journal.Claim(t.Context(), f.decision)
	if err != nil {
		t.Fatal(err)
	}
	f.docs.failKind = "merge_decisions"
	if err := f.engine.capture(t.Context(), current, nil, f.decision.FrozenAt); err == nil {
		t.Fatal("capture progress failure hidden")
	}
	stored, err := f.journal.Snapshot(t.Context(), f.decision.TenantID, f.decision.ID, "a")
	if err != nil {
		t.Fatal(err)
	}
	f.engine.CurrentAlert = func(context.Context, string, string) (store.StoredAlert, error) {
		return store.StoredAlert{}, errors.New("live read must not replace first snapshot")
	}
	// 下一步先复用 a 快照推进前缀，再在读取 b 时失败；不能因为 b 的失败回退 a。
	if err := f.engine.StepDecision(t.Context(), f.decision.TenantID, f.decision.ID, f.decision.FrozenAt); err == nil {
		t.Fatal("live read failure hidden")
	}
	current, err = f.journal.Get(t.Context(), f.decision.TenantID, f.decision.ID)
	if err != nil || current.Decision.Progress.CaptureOffset != 1 {
		t.Fatal("saved prefix did not survive retry", err)
	}
	again, err := f.journal.Snapshot(t.Context(), f.decision.TenantID, f.decision.ID, "a")
	if err != nil || again.Alert.Title != stored.Alert.Title {
		t.Fatal("first snapshot overwritten", err)
	}
}

func TestExecutorCaptureBudgetResumesFromPersistedPrefix(t *testing.T) {
	f := newExecutorFixture(t)
	d := f.decision.Clone()
	d.MemberIDs = nil
	d.WaitMemberIDs = nil
	for i := range 20 {
		id := fmt.Sprintf("member-%02d", i)
		d.MemberIDs = append(d.MemberIDs, id)
		d.WaitMemberIDs = append(d.WaitMemberIDs, id)
		a := storetest.Alert(d.TenantID, id, "opening-"+id, "fp-"+id, "warning")
		if _, err := f.repo.CreateAlert(t.Context(), a); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.journal.Claim(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []int{16, 20} {
		if err := f.engine.StepDecision(t.Context(), d.TenantID, d.ID, d.FrozenAt); err != nil {
			t.Fatal(err)
		}
		current, err := f.journal.Get(t.Context(), d.TenantID, d.ID)
		if err != nil || current.Decision.Progress.CaptureOffset != expected || current.Decision.Progress.ParentEvent != nil {
			t.Fatal("capture budget/prefix lost", err)
		}
	}
}
