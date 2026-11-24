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
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

func closeRelationParent(t *testing.T, f relationFixture) store.StoredAlert {
	t.Helper()
	_, err := f.processor.CloseAlert(t.Context(), lifecycle.CloseAlertCommand{OperationID: "manual-" + f.decision.ID, BKTenantID: f.decision.TenantID, AlertID: f.parent.Alert.AlertID, OperatorKind: domain.OperatorKindUser, OperatorID: "tester", Reason: "manual", EffectiveAt: f.decision.FrozenAt.Add(2 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, f.parent.Alert.AlertID)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func finishEnding(t *testing.T, f relationFixture) StoredRelation {
	t.Helper()
	for range 20 {
		r, err := f.journal.ReconcileRelationStep(t.Context(), f.decision.TenantID, f.decision.ID, f.processor, f.decision.FrozenAt.Add(3*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if r.Relation.State == "ended" {
			return r
		}
	}
	t.Fatal("relation did not end")
	return StoredRelation{}
}

func endChild(t *testing.T, f relationFixture, id string) store.StoredAlert {
	t.Helper()
	a, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, id)
	if err != nil {
		t.Fatal(err)
	}
	next := a.Alert.Clone()
	next.Status = domain.AlertStatusRecovered
	next.Merge = next.Merge.EndWaiting()
	next.UpdateAt = next.UpdateAt.Add(time.Second)
	next.EndAt = &next.UpdateAt
	next.EndType = domain.AlertEndTypeSource
	next.EndReason = "resolved"
	actual, err := f.repo.CompareAndSetAlert(t.Context(), f.decision.TenantID, id, a.Version, next)
	if err != nil {
		t.Fatal(err)
	}
	return actual
}

func TestParentCloseOnlyUnlinksMembersUntilNextTrigger(t *testing.T) {
	for _, stage := range []string{"before relations", "partly linked", "completed"} {
		t.Run(stage, func(t *testing.T) {
			f := newRelationFixture(t)
			d := f.decision
			if stage == "completed" {
				finishLinks(t, f)
			}
			if stage == "partly linked" {
				prepareReferences(t, f)
				if _, err := f.journal.LinkStep(t.Context(), d.TenantID, d.ID, f.parent, f.processor, d.FrozenAt); err != nil {
					t.Fatal(err)
				}
			}
			parent := closeRelationParent(t, f)
			actions := len(f.action.inputs)
			if stage != "completed" {
				for range 16 {
					next, err := f.journal.LinkStep(t.Context(), d.TenantID, d.ID, parent, f.processor, d.FrozenAt.Add(3*time.Second))
					if err != nil {
						t.Fatal(err)
					}
					if next.Decision.Progress.Phase == "completed" {
						break
					}
				}
			}
			ended := finishEnding(t, f)
			if ended.Relation.EndReason != "parent_ended" || len(f.action.inputs) != actions {
				t.Fatal("unlink dispatched child action")
			}
			gotParent, err := f.repo.GetAlert(t.Context(), d.TenantID, parent.Alert.AlertID)
			if err != nil || !reflect.DeepEqual(gotParent, parent) {
				t.Fatal("manual close was rewritten", err)
			}
			for _, id := range d.MemberIDs {
				a, err := f.repo.GetAlert(t.Context(), d.TenantID, id)
				if err != nil || a.Alert.Status != domain.AlertStatusActive || a.Alert.Merge.State != "released" || a.Alert.Merge.Blocking() || a.Alert.Admission.AdmittedAt != nil {
					t.Fatal("child lifecycle/admission altered", err)
				}
			}
			stateCount := len(f.state.inputs)
			again := finishEnding(t, f)
			if again.Version != ended.Version || len(f.state.inputs) != stateCount {
				t.Fatal("ended relation repeated mutation")
			}
			// 真实新 Event 才重新取得处置资格，解除关系本身没有生成 Event 或刷新 opening 快照。
			child, err := f.repo.GetAlert(t.Context(), d.TenantID, "a")
			if err != nil {
				t.Fatal(err)
			}
			e := storetest.Event(d.TenantID, "next-child-trigger", child.Alert.Fingerprint, child.Alert.Severity)
			e.EventSourceID = child.Alert.EventSourceID
			e.OccurredAt = d.FrozenAt.Add(4 * time.Second)
			created, err := f.repo.CreateEvent(t.Context(), e)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.processor.ProcessEvent(t.Context(), created.StoredEvent); err != nil {
				t.Fatal(err)
			}
			admitted, err := f.repo.GetAlert(t.Context(), d.TenantID, "a")
			if err != nil || len(f.action.inputs) != actions+1 || admitted.Alert.Admission.CauseID != e.EventID || admitted.Alert.TriggerEventID != child.Alert.TriggerEventID {
				t.Fatal("next event did not admit original child", err)
			}
		})
	}
}

func TestParentRecoversOnlyAfterEveryRealMemberEnds(t *testing.T) {
	f := newRelationFixture(t)
	finishLinks(t, f)
	d := f.decision
	first := endChild(t, f, "a")
	parent, err := f.processor.RecoverMergeParent(t.Context(), d.TenantID, f.parent.Alert.AlertID, d.ID)
	if err != nil || parent.Alert.Status != domain.AlertStatusActive || len(f.action.inputs) != 1 {
		t.Fatal("parent ended while member active", err)
	}
	endChild(t, f, "b")
	for _, at := range []time.Time{{}, d.FrozenAt.Add(-time.Second)} {
		if _, err := f.journal.ReconcileRelationStep(t.Context(), d.TenantID, d.ID, f.processor, at); !errors.Is(err, policy.ErrInvalid) {
			t.Fatal("invalid time accepted", err)
		}
		current, err := f.repo.GetAlert(t.Context(), d.TenantID, f.parent.Alert.AlertID)
		if err != nil || current.Alert.Status != domain.AlertStatusActive || len(f.action.inputs) != 1 {
			t.Fatal("invalid scan time caused recovery", err)
		}
	}
	ended := finishEnding(t, f)
	parent, err = f.repo.GetAlert(t.Context(), d.TenantID, f.parent.Alert.AlertID)
	if err != nil || parent.Alert.Status != domain.AlertStatusRecovered || parent.Alert.EndType != domain.AlertEndTypeSystem || parent.Alert.EndReason != "merge_members_ended" || ended.Relation.EndReason != "members_ended" || parent.Alert.MergeChange != nil || len(f.action.inputs) != 2 || f.action.inputs[1].Outcome != lifecycle.OutcomeAlertRecovered {
		t.Fatal("parent recovery incomplete", err)
	}
	a, err := f.repo.GetAlert(t.Context(), d.TenantID, "a")
	if err != nil || !reflect.DeepEqual(a, first) {
		t.Fatal("terminal member rewritten", err)
	}
	finishEnding(t, f)
	if len(f.action.inputs) != 2 {
		t.Fatal("recovery action repeated")
	}
}

func TestParentRecoveryCompletesShieldCleanupWithoutExtraAction(t *testing.T) {
	f := newRelationFixture(t)
	finishLinks(t, f)
	d := f.decision
	current, err := f.repo.GetAlert(t.Context(), d.TenantID, f.parent.Alert.AlertID)
	if err != nil {
		t.Fatal(err)
	}
	blocked := current.Alert.Clone()
	blocked.Severity = "critical"
	blocked.UpdateAt = blocked.UpdateAt.Add(time.Microsecond)
	next := blocked.UpdateAt.Add(time.Minute)
	binding := storetest.ShieldBinding(blocked.UpdateAt)
	binding.Severity = "critical"
	blocked.Shield = domain.AlertShield{Active: true, Bindings: []domain.ShieldBinding{binding}, NextCheckAt: &next}
	if _, err := f.repo.CompareAndSetAlert(t.Context(), d.TenantID, blocked.AlertID, current.Version, blocked); err != nil {
		t.Fatal(err)
	}
	endChild(t, f, "a")
	endChild(t, f, "b")
	parent, err := f.processor.RecoverMergeParent(t.Context(), d.TenantID, blocked.AlertID, d.ID)
	if err != nil || parent.Alert.Status != domain.AlertStatusRecovered || parent.Alert.Shield.Active || parent.Alert.PolicyChange != nil || parent.Alert.MergeChange != nil || len(f.action.inputs) != 2 {
		t.Fatal("combined terminal cleanup incomplete", parent, err)
	}
	logs, err := f.repo.ListAlertLogs(t.Context(), d.TenantID, blocked.AlertID, store.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, log := range logs.Logs {
		if log.OperationKind == domain.OperationKindUnshield {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("parent shield cleanup logs=%d", count)
	}
}

type endingOperator struct {
	RelationEndOperator
	unlink func(context.Context, string, string, string) (store.StoredAlert, error)
}

func (o endingOperator) UnlinkMergeMember(ctx context.Context, tenant, id, relation string) (store.StoredAlert, error) {
	return o.unlink(ctx, tenant, id, relation)
}

func TestEndingResumesAfterMemberAndDecisionProgressFailure(t *testing.T) {
	f := newRelationFixture(t)
	prepareReferences(t, f)
	parent := closeRelationParent(t, f)
	d := f.decision
	at := d.FrozenAt.Add(3 * time.Second)
	f.docs.failKind = "merge_decisions"
	if _, err := f.journal.BeginEndRelation(t.Context(), d.TenantID, d.ID, parent, at); err == nil {
		t.Fatal("decision write failure hidden")
	}
	r, err := f.journal.GetRelation(t.Context(), d.TenantID, d.ID)
	if err != nil || r.Relation.State != "ending" {
		t.Fatal("end intent lost", err)
	}
	ops := endingOperator{RelationEndOperator: f.processor, unlink: func(ctx context.Context, tenant, id, relation string) (store.StoredAlert, error) {
		a, err := f.processor.UnlinkMergeMember(ctx, tenant, id, relation)
		f.docs.failKind = "merge_relations"
		return a, err
	}}
	if _, err := f.journal.EndRelationStep(t.Context(), d.TenantID, d.ID, parent, ops, at); err == nil {
		t.Fatal("member progress failure hidden")
	}
	r, err = f.journal.GetRelation(t.Context(), d.TenantID, d.ID)
	if err != nil || r.Relation.EndOffset != 0 {
		t.Fatal("failed progress advanced", err)
	}
	child, err := f.repo.GetAlert(t.Context(), d.TenantID, "a")
	if err != nil || child.Alert.Merge.State != "released" {
		t.Fatal("child update lost", err)
	}
	stateCount := len(f.state.inputs)
	if _, err := f.journal.EndRelationStep(t.Context(), d.TenantID, d.ID, parent, f.processor, at); err != nil {
		t.Fatal(err)
	}
	if len(f.state.inputs) != stateCount {
		t.Fatal("retry repeated child output")
	}
	finishEnding(t, f)
	current, err := f.journal.Get(t.Context(), d.TenantID, d.ID)
	if err != nil || current.Decision.Progress.Phase != "completed" || current.Decision.Progress.ParentAlertID != parent.Alert.AlertID || current.Decision.Progress.ReasonCode != "parent_ended" {
		t.Fatal("ending did not finish decision", err)
	}
}

func TestEndingRejectsMissingSelectedMemberAndCoreFailures(t *testing.T) {
	for _, failure := range []error{store.ErrNotFound, context.Canceled, policy.ErrAccess, errors.New("storage failed")} {
		t.Run(failure.Error(), func(t *testing.T) {
			f := newRelationFixture(t)
			prepareReferences(t, f)
			parent := closeRelationParent(t, f)
			ops := endingOperator{RelationEndOperator: f.processor, unlink: func(context.Context, string, string, string) (store.StoredAlert, error) {
				return store.StoredAlert{}, failure
			}}
			if _, err := f.journal.EndRelationStep(t.Context(), f.decision.TenantID, f.decision.ID, parent, ops, f.decision.FrozenAt.Add(3*time.Second)); !errors.Is(err, failure) {
				t.Fatal("core failure hidden", err)
			}
			r, err := f.journal.GetRelation(t.Context(), f.decision.TenantID, f.decision.ID)
			if err != nil || r.Relation.EndOffset != 0 {
				t.Fatal("failed member skipped", err)
			}
		})
	}
}

func TestRelationWorkScanKeepsCompletedCreationUntilRelationEnds(t *testing.T) {
	f := newRelationFixture(t)
	finishLinks(t, f)
	page, err := f.journal.ListRelationWork(t.Context(), "", 1)
	if err != nil || len(page.Relations) != 1 || page.Next == "" {
		t.Fatal("completed creation lost from relation work", err)
	}
	closeRelationParent(t, f)
	finishEnding(t, f)
	page, err = f.journal.ListRelationWork(t.Context(), "", 1)
	if err != nil || len(page.Relations) != 0 || page.Next == "" {
		t.Fatal("ended row did not advance work cursor", err)
	}
	tail, err := f.journal.ListRelationWork(t.Context(), page.Next, 1)
	if err != nil || tail.Next != "" || len(tail.Relations) != 0 {
		t.Fatal("work cursor did not end", err)
	}
}

type recoveryRepository struct {
	store.Repository
	failLog bool
	missing string
}

func (r *recoveryRepository) AppendAlertLogs(ctx context.Context, logs []domain.AlertLog) ([]store.AppendAlertLogItemResult, error) {
	if r.failLog {
		r.failLog = false
		return nil, errors.New("injected log failure")
	}
	return r.Repository.AppendAlertLogs(ctx, logs)
}

func (r *recoveryRepository) GetAlert(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
	if id == r.missing {
		return store.StoredAlert{}, store.ErrNotFound
	}
	return r.Repository.GetAlert(ctx, tenant, id)
}

func recoveryProcessor(t *testing.T, f relationFixture, repo store.Repository) *lifecycle.Processor {
	t.Helper()
	p, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, []lifecycle.NamedFinalHook{{Name: "action", Purpose: "action", Hook: f.action}, {Name: "state", Purpose: "state", Hook: f.state}}, config.DefaultSeverityConfig(), journalClock{at: f.decision.FrozenAt.Add(time.Second)}, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithMergeRelations(f.journal))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParentRecoveryRetriesTerminalIntentAndNeverTreatsMissingAsEnded(t *testing.T) {
	f := newRelationFixture(t)
	finishLinks(t, f)
	endChild(t, f, "a")
	endChild(t, f, "b")
	repo := &recoveryRepository{Repository: f.repo, missing: "b"}
	p := recoveryProcessor(t, f, repo)
	d := f.decision
	if _, err := p.RecoverMergeParent(t.Context(), d.TenantID, f.parent.Alert.AlertID, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("missing member counted as ended", err)
	}
	if len(f.action.inputs) != 1 {
		t.Fatal("missing member caused recovery")
	}
	repo.missing = ""
	repo.failLog = true
	if _, err := p.RecoverMergeParent(t.Context(), d.TenantID, f.parent.Alert.AlertID, d.ID); err == nil {
		t.Fatal("log failure hidden")
	}
	pending, err := f.repo.GetAlert(t.Context(), d.TenantID, f.parent.Alert.AlertID)
	if err != nil || pending.Alert.Status != domain.AlertStatusRecovered || pending.Alert.MergeChange == nil || len(f.action.inputs) != 2 {
		t.Fatal("terminal recovery intent lost", err)
	}
	replay, err := p.RecoverMergeParent(t.Context(), d.TenantID, f.parent.Alert.AlertID, d.ID)
	if err != nil || replay.Alert.MergeChange != nil || !replay.Alert.UpdateAt.Equal(pending.Alert.UpdateAt) || len(f.action.inputs) != 3 || f.action.inputs[1].Cause != f.action.inputs[2].Cause || !reflect.DeepEqual(f.action.inputs[1].Alert, f.action.inputs[2].Alert) {
		t.Fatal("recovery retry changed identity/snapshot", err)
	}
	if _, err := p.RecoverMergeParent(t.Context(), d.TenantID, f.parent.Alert.AlertID, d.ID); err != nil || len(f.action.inputs) != 3 {
		t.Fatal("completed recovery repeated action", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.RecoverMergeParent(ctx, d.TenantID, f.parent.Alert.AlertID, d.ID); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
}
