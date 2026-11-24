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
	"slices"
	"strings"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	"linkd/internal/runtimeconfig"
	"linkd/internal/store"
	"linkd/internal/store/memory"
)

type relationHook struct{ inputs []lifecycle.FinalHookInput }

func (h *relationHook) Execute(_ context.Context, input lifecycle.FinalHookInput) (lifecycle.FinalHookResult, error) {
	h.inputs = append(h.inputs, input)
	return lifecycle.FinalHookResult{Skipped: true}, nil
}

type relationFixture struct {
	journal       *Journal
	docs          *memoryDocuments
	repo          *memory.Repository
	processor     *lifecycle.Processor
	action, state *relationHook
	decision      Decision
	parent        store.StoredAlert
}

func newRelationFixture(t *testing.T) relationFixture {
	return newConfiguredRelationFixture(t, nil)
}

func newConfiguredRelationFixture(t *testing.T, edit func(*domain.Alert), options ...lifecycle.ProcessorOption) relationFixture {
	t.Helper()
	j, docs := newJournal(t)
	repo := memory.New()
	d, members, source, severity := renderFixture(t)
	if _, err := j.Claim(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	for _, member := range members {
		a := member.Alert
		if a.AlertID == "b" {
			a.EventSourceID = "another-source"
		}
		wait := domain.MergeWait{WindowID: d.WindowID, Policy: domain.PolicyVersion{ID: d.Policy.ID, Version: d.Policy.Version, Digest: d.Policy.Compiled.Digest}, GroupKey: d.GroupKey, MemberEventID: a.TriggerEventID, Severity: a.Severity, Groups: []int{0}, StartedAt: d.StartedAt, Deadline: d.Deadline}
		a.Merge = &domain.AlertMerge{Role: "original", State: "pending", Pending: []domain.MergeWait{wait}}
		if edit != nil {
			edit(&a)
		}
		if _, err := repo.CreateAlert(t.Context(), a); err != nil {
			t.Fatal(err)
		}
		if _, err := j.Capture(t.Context(), d.TenantID, d.ID, a); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := j.RenderAndPrepare(t.Context(), d.TenantID, d.ID, source, severity, d.FrozenAt); err != nil {
		t.Fatal(err)
	}
	publisher := Publisher{Journal: j, Events: repo, Mailboxes: &mailboxFailure{repo: repo}}
	waiting, err := publisher.Submit(t.Context(), d.TenantID, d.ID, d.FrozenAt)
	if err != nil {
		t.Fatal(err)
	}
	action, state := &relationHook{}, &relationHook{}
	p, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, []lifecycle.NamedFinalHook{{Name: "state", Purpose: "state", Hook: state}, {Name: "action", Purpose: "action", Hook: action}}, config.DefaultSeverityConfig(), journalClock{at: d.FrozenAt.Add(time.Second)}, slog.New(slog.NewTextHandler(io.Discard, nil)), append([]lifecycle.ProcessorOption{lifecycle.WithMergeRelations(j)}, options...)...)
	if err != nil {
		t.Fatal(err)
	}
	event, err := repo.GetEvent(t.Context(), d.TenantID, waiting.Decision.Progress.ParentEvent.EventID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.ProcessEvent(t.Context(), event)
	if err != nil || len(result.AlertIDs) != 1 {
		t.Fatal("parent lifecycle", err)
	}
	parent, err := repo.GetAlert(t.Context(), d.TenantID, result.AlertIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	linking, err := j.RecordParent(t.Context(), waiting, parent, d.FrozenAt)
	if err != nil {
		t.Fatal(err)
	}
	return relationFixture{j, docs, repo, p, action, state, linking.Decision, parent}
}

func prepareReferences(t *testing.T, f relationFixture) StoredRelation {
	t.Helper()
	r, err := f.journal.EnsureRelation(t.Context(), f.decision.TenantID, f.decision.ID, f.parent, f.decision.FrozenAt)
	if err != nil {
		t.Fatal(err)
	}
	for r.Relation.IndexOffset < len(r.Relation.Members)+1 {
		r, err = f.journal.IndexRelationStep(t.Context(), r, f.decision.FrozenAt)
		if err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func finishLinks(t *testing.T, f relationFixture) {
	t.Helper()
	for range 16 {
		parent, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, f.parent.Alert.AlertID)
		if err != nil {
			t.Fatal(err)
		}
		next, err := f.journal.LinkStep(t.Context(), f.decision.TenantID, f.decision.ID, parent, f.processor, f.decision.FrozenAt)
		if err != nil {
			t.Fatal(err)
		}
		if next.Decision.Progress.Phase == "completed" {
			return
		}
	}
	t.Fatal("link steps did not complete")
}

func TestRelationLinksAfterRealParentAndAdmitsOnlyAtReadiness(t *testing.T) {
	f := newRelationFixture(t)
	d := f.decision
	if len(f.action.inputs) != 0 || f.parent.Alert.Merge.RelationsReady {
		t.Fatal("parent admitted before relations")
	}
	if _, err := f.processor.ReadyMergeParent(t.Context(), d.TenantID, f.parent.Alert.AlertID, d.ID); !errors.Is(err, policy.ErrNotFound) {
		t.Fatal("missing relation accepted", err)
	}
	r, err := f.journal.EnsureRelation(t.Context(), d.TenantID, d.ID, f.parent, d.FrozenAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.journal.ReadyRelation(t.Context(), r, d.FrozenAt); err == nil {
		t.Fatal("partial relation made ready")
	}
	if _, err := f.processor.LinkMergeMember(t.Context(), d.TenantID, "a", d.ID); err == nil {
		t.Fatal("member linked before query refs")
	}
	finishLinks(t, f)
	if len(f.action.inputs) != 1 || f.action.inputs[0].Alert.AlertID != f.parent.Alert.AlertID || f.action.inputs[0].Alert.MergeChange.Kind != "parent_ready" {
		t.Fatal("member action or missing parent action")
	}
	for _, id := range d.MemberIDs {
		a, err := f.repo.GetAlert(t.Context(), d.TenantID, id)
		if err != nil || a.Alert.Merge.State != "merged" || !slices.Contains(a.Alert.Merge.RelationIDs, d.ID) || len(a.Alert.Merge.Pending) != 0 || a.Alert.MergeChange != nil || a.Alert.Admission.AdmittedAt != nil {
			t.Fatal("member relation/state incorrect", err)
		}
		page, err := f.journal.ListRelations(t.Context(), d.TenantID, id, "", 1)
		if err != nil || len(page.Relations) != 1 || page.Relations[0].State != "ready" {
			t.Fatal("member relation query", err)
		}
	}
	parent, err := f.repo.GetAlert(t.Context(), d.TenantID, f.parent.Alert.AlertID)
	if err != nil || !parent.Alert.Merge.RelationsReady || !parent.Alert.AdmittedActiveMain() {
		t.Fatal("parent relation gate not opened", err)
	}
	if _, err := f.journal.LinkStep(t.Context(), d.TenantID, d.ID, parent, f.processor, d.FrozenAt); err != nil || len(f.action.inputs) != 1 {
		t.Fatal("completed operation repeated action", err)
	}
	members, err := f.journal.ListRelationMembers(t.Context(), d.TenantID, d.ID, "", 1)
	if err != nil || len(members.Members) != 1 || members.Next == "" {
		t.Fatal("member page", err)
	}
	last, err := f.journal.ListRelationMembers(t.Context(), d.TenantID, d.ID, members.Next, 1)
	if err != nil || len(last.Members) != 1 || last.Next != "" || last.Members[0].AlertID == members.Members[0].AlertID {
		t.Fatal("member pagination repeated", err)
	}
	if _, err := f.journal.ListRelationMembers(t.Context(), "foreign", d.ID, members.Next, 1); !errors.Is(err, policy.ErrInvalid) {
		t.Fatal("foreign member cursor accepted", err)
	}
}

func TestRelationIndexAndMemberConfirmationRecoverPartialWrites(t *testing.T) {
	f := newRelationFixture(t)
	d := f.decision
	r, err := f.journal.EnsureRelation(t.Context(), d.TenantID, d.ID, f.parent, d.FrozenAt)
	if err != nil {
		t.Fatal(err)
	}
	f.docs.failKind = "merge_relations"
	if _, err := f.journal.IndexRelationStep(t.Context(), r, d.FrozenAt); err == nil {
		t.Fatal("index progress failure hidden")
	}
	page, err := f.journal.ListRelations(t.Context(), d.TenantID, f.parent.Alert.AlertID, "", 1)
	if err != nil || len(page.Relations) != 1 || page.Relations[0].State != "preparing" {
		t.Fatal("partial relation misreported", err)
	}
	r = prepareReferences(t, f)
	child, err := f.repo.GetAlert(t.Context(), d.TenantID, "a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.journal.ConfirmRelationMember(t.Context(), r, child, d.FrozenAt); err == nil {
		t.Fatal("unlinked child confirmed")
	}
	linked, err := f.processor.LinkMergeMember(t.Context(), d.TenantID, "a", d.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := len(f.state.inputs)
	f.docs.failKind = "merge_relations"
	if _, err := f.journal.ConfirmRelationMember(t.Context(), r, linked, d.FrozenAt); err == nil {
		t.Fatal("member confirm failure hidden")
	}
	replay, err := f.processor.LinkMergeMember(t.Context(), d.TenantID, "a", d.ID)
	if err != nil || replay.Version != linked.Version || len(f.state.inputs) != count {
		t.Fatal("member retry repeated mutation", err)
	}
	if _, err := f.journal.ConfirmRelationMember(t.Context(), r, replay, d.FrozenAt); err != nil {
		t.Fatal(err)
	}
	finishLinks(t, f)
	if len(f.action.inputs) != 1 {
		t.Fatal("parent not admitted once")
	}
}

func TestRelationScopeCASAndImmutableMembership(t *testing.T) {
	f := newRelationFixture(t)
	d := f.decision
	r := prepareReferences(t, f)
	if _, err := f.journal.GetRelation(t.Context(), "foreign", d.ID); !errors.Is(err, policy.ErrNotFound) {
		t.Fatal("relation tenant leak", err)
	}
	page, err := f.journal.ListRelations(t.Context(), d.TenantID, "a", "", 1)
	if err != nil || page.Next == "" {
		t.Fatal(err)
	}
	if _, err := f.journal.ListRelations(t.Context(), d.TenantID, "b", page.Next, 1); !errors.Is(err, policy.ErrInvalid) {
		t.Fatal("cross-alert cursor accepted", err)
	}
	if _, err := f.processor.LinkMergeMember(t.Context(), d.TenantID, f.parent.Alert.AlertID, d.ID); err == nil {
		t.Fatal("aggregate recursively linked")
	}
	changed := r.Relation.Clone()
	changed.ParentAlertID = "other"
	if _, err := f.journal.putRelation(t.Context(), r, changed); err == nil {
		t.Fatal("relation parent mutable")
	}
	child, err := f.processor.LinkMergeMember(t.Context(), d.TenantID, "a", d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.journal.ConfirmRelationMember(t.Context(), r, child, d.FrozenAt); err != nil {
		t.Fatal(err)
	}
	if _, err := f.journal.IndexRelationStep(t.Context(), StoredRelation{Relation: r.Relation, Version: "stale"}, d.FrozenAt); err != nil {
		t.Fatal("fully indexed read should be inert", err)
	}
	changed = r.Relation.Clone()
	changed.Members[0].State = "linked"
	if _, err := f.journal.putRelation(t.Context(), r, changed); !errors.Is(err, policy.ErrConflict) {
		t.Fatal("stale relation CAS accepted", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.processor.LinkMergeMember(ctx, d.TenantID, "b", d.ID); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled member mutation", err)
	}
}

func TestRelationWithTerminalMembersDoesNotFakeChildRecoveryOrParentTrigger(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(map[bool]string{false: "one ended", true: "all ended"}[all], func(t *testing.T) {
			f := newRelationFixture(t)
			d := f.decision
			ids := d.MemberIDs[:1]
			if all {
				ids = d.MemberIDs
			}
			for _, id := range ids {
				a, err := f.repo.GetAlert(t.Context(), d.TenantID, id)
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
				if _, err := f.repo.CompareAndSetAlert(t.Context(), d.TenantID, id, a.Version, next); err != nil {
					t.Fatal(err)
				}
			}
			var finalErr error
			for range 16 {
				parent, err := f.repo.GetAlert(t.Context(), d.TenantID, f.parent.Alert.AlertID)
				if err != nil {
					t.Fatal(err)
				}
				next, err := f.journal.LinkStep(t.Context(), d.TenantID, d.ID, parent, f.processor, d.FrozenAt)
				if err != nil {
					finalErr = err
					break
				}
				if next.Decision.Progress.Phase == "completed" {
					break
				}
			}
			if all {
				if finalErr != nil || len(f.action.inputs) != 0 {
					t.Fatal("all-ended members triggered parent", finalErr)
				}
			} else if finalErr != nil || len(f.action.inputs) != 1 {
				t.Fatal("remaining active member failed", finalErr)
			}
			for _, id := range ids {
				a, err := f.repo.GetAlert(t.Context(), d.TenantID, id)
				if err != nil || a.Alert.Status != domain.AlertStatusRecovered || len(a.Alert.Merge.RelationIDs) != 0 {
					t.Fatal("terminal member rewritten", err)
				}
			}
		})
	}
}

func TestMemberLinkRetainsOtherWaitingAndSuccessfulRelations(t *testing.T) {
	f := newRelationFixture(t)
	d := f.decision
	prepareReferences(t, f)
	a, err := f.repo.GetAlert(t.Context(), d.TenantID, "a")
	if err != nil {
		t.Fatal(err)
	}
	next := a.Alert.Clone()
	other := next.Merge.Pending[0].Clone()
	other.WindowID = strings.Repeat("e", 64)
	other.Policy.ID = "another"
	next.Merge.Pending = append(next.Merge.Pending, other)
	next.Merge.RelationIDs = []string{strings.Repeat("f", 64)}
	next.Merge.State = "merged"
	next.UpdateAt = next.UpdateAt.Add(time.Second)
	if _, err := f.repo.CompareAndSetAlert(t.Context(), d.TenantID, "a", a.Version, next); err != nil {
		t.Fatal(err)
	}
	linked, err := f.processor.LinkMergeMember(t.Context(), d.TenantID, "a", d.ID)
	if err != nil || len(linked.Alert.Merge.RelationIDs) != 2 || len(linked.Alert.Merge.Pending) != 1 || !reflect.DeepEqual(linked.Alert.Merge.Pending[0], other) || len(f.action.inputs) != 0 {
		t.Fatal("link discarded other policies", err)
	}
}

func TestMultipleParentsShareMembersAndRelationQueriesPageIndependently(t *testing.T) {
	f := newRelationFixture(t)
	first := f.decision
	second := first.Clone()
	second.WindowID = strings.Repeat("f", 64)
	var identityErr error
	second.ID, identityErr = domain.MergeDecisionID(second.TenantID, second.WindowID)
	if identityErr != nil {
		t.Fatal(identityErr)
	}
	second.Policy.ID = "merge-two"
	second.Progress = Progress{}
	if _, err := f.journal.Claim(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	for _, id := range second.MemberIDs {
		a, err := f.repo.GetAlert(t.Context(), second.TenantID, id)
		if err != nil {
			t.Fatal(err)
		}
		next := a.Alert.Clone()
		wait := next.Merge.Pending[0].Clone()
		wait.WindowID = second.WindowID
		wait.Policy.ID = second.Policy.ID
		next.Merge.Pending = append(next.Merge.Pending, wait)
		next.UpdateAt = next.UpdateAt.Add(time.Second)
		updated, err := f.repo.CompareAndSetAlert(t.Context(), second.TenantID, id, a.Version, next)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.journal.Capture(t.Context(), second.TenantID, second.ID, updated.Alert); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.journal.RenderAndPrepare(t.Context(), second.TenantID, second.ID, renderSource(), runtimeconfig.Snapshot{Severity: config.DefaultSeverityConfig()}, second.FrozenAt); err != nil {
		t.Fatal(err)
	}
	publisher := Publisher{Journal: f.journal, Events: f.repo, Mailboxes: &mailboxFailure{repo: f.repo}}
	waiting, err := publisher.Submit(t.Context(), second.TenantID, second.ID, second.FrozenAt)
	if err != nil {
		t.Fatal(err)
	}
	event, err := f.repo.GetEvent(t.Context(), second.TenantID, waiting.Decision.Progress.ParentEvent.EventID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.processor.ProcessEvent(t.Context(), event)
	if err != nil || len(result.AlertIDs) != 1 {
		t.Fatal(err)
	}
	parent, err := f.repo.GetAlert(t.Context(), second.TenantID, result.AlertIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	linking, err := f.journal.RecordParent(t.Context(), waiting, parent, second.FrozenAt)
	if err != nil {
		t.Fatal(err)
	}
	f2 := f
	f2.decision = linking.Decision
	f2.parent = parent
	finishLinks(t, f)
	finishLinks(t, f2)
	if len(f.action.inputs) != 2 || f.action.inputs[0].Alert.AlertID == f.action.inputs[1].Alert.AlertID {
		t.Fatal("shared children replaced another parent")
	}
	for _, id := range first.MemberIDs {
		child, err := f.repo.GetAlert(t.Context(), first.TenantID, id)
		if err != nil || len(child.Alert.Merge.RelationIDs) != 2 || len(child.Alert.Merge.Pending) != 0 {
			t.Fatal("shared member links incomplete", err)
		}
		page, err := f.journal.ListRelations(t.Context(), first.TenantID, id, "", 1)
		if err != nil || len(page.Relations) != 1 || page.Next == "" {
			t.Fatal("first shared page", err)
		}
		next, err := f.journal.ListRelations(t.Context(), first.TenantID, id, page.Next, 1)
		if err != nil || len(next.Relations) != 1 || next.Relations[0].ID == page.Relations[0].ID {
			t.Fatal("second shared page", err)
		}
	}
	for index, current := range []relationFixture{f, f2} {
		closeRelationParent(t, current)
		actions := len(f.action.inputs)
		finishEnding(t, current)
		for _, id := range first.MemberIDs {
			child, err := f.repo.GetAlert(t.Context(), first.TenantID, id)
			if err != nil || child.Alert.Status != domain.AlertStatusActive || len(child.Alert.Merge.RelationIDs) != 1-index || child.Alert.Admission.AdmittedAt != nil {
				t.Fatal("ending one parent damaged shared member", err)
			}
		}
		if len(f.action.inputs) != actions {
			t.Fatal("ending last shared parent dispatched member")
		}
	}
}
