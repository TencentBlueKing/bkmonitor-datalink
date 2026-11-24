// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package merge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	"linkd/internal/store"
)

func linkBatchFixture(t *testing.T, extraSelected, reserved int) (executorFixture, StoredDecision) {
	t.Helper()
	f := newExecutorFixture(t)
	template := f.state.result.Members[1].Alert
	for i := range extraSelected {
		id := fmt.Sprintf("c-%02d", i)
		a := template.Clone()
		a.AlertID = id
		a.Fingerprint = "fp-" + id
		a.TriggerEventID = "event-" + id
		a.LatestEventID = a.TriggerEventID
		a.Merge.Pending[0].MemberEventID = a.TriggerEventID
		saved, err := f.repo.CreateAlert(t.Context(), a)
		if err != nil {
			t.Fatal(err)
		}
		f.state.result.Members = append(f.state.result.Members, saved.StoredAlert)
		f.state.result.Window.Members = append(f.state.result.Window.Members, redisstate.MergeMember{Main: domain.DependencyMain{AlertID: id}})
		f.state.result.Window.Frozen.MemberIDs = append(f.state.result.Window.Frozen.MemberIDs, id)
	}
	slices.Sort(f.state.result.Window.Frozen.MemberIDs)
	for i := range reserved {
		f.state.result.Window.Members = append(f.state.result.Window.Members, redisstate.MergeMember{Main: domain.DependencyMain{AlertID: fmt.Sprintf("reserved-%02d", i)}})
	}
	startExecutor(t, f)
	for range 32 {
		current, err := f.journal.Get(t.Context(), f.decision.TenantID, f.decision.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Decision.Progress.Phase == "linking" {
			f.decision = current.Decision
			f.parent, err = f.repo.GetAlert(t.Context(), f.decision.TenantID, f.decision.Progress.ParentAlertID)
			if err != nil {
				t.Fatal(err)
			}
			p, err := lifecycle.NewProcessor(f.repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, []lifecycle.NamedFinalHook{{Name: "state", Purpose: "state", Hook: &endingBatchHook{hook: f.relationFixture.state}}, {Name: "action", Purpose: "action", Hook: &endingBatchHook{hook: f.action}}}, config.DefaultSeverityConfig(), journalClock{at: f.decision.FrozenAt.Add(2 * time.Second)}, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithMergeRelations(f.journal))
			if err != nil {
				t.Fatal(err)
			}
			f.processor = p
			f.engine.Operations = p
			r, err := f.journal.EnsureRelation(t.Context(), f.decision.TenantID, f.decision.ID, f.parent, f.decision.FrozenAt.Add(2*time.Second))
			if err != nil {
				t.Fatal("prepare fixed relation", err)
			}
			for r.Relation.IndexOffset < len(r.Relation.Members)+1 {
				r, err = f.journal.IndexRelationStep(t.Context(), r, f.decision.FrozenAt.Add(2*time.Second))
				if err != nil {
					t.Fatal("prepare references", err)
				}
			}
			return f, current
		}
		if current.Decision.Progress.Phase == "waiting_parent" {
			e, err := f.repo.GetEvent(t.Context(), f.decision.TenantID, current.Decision.Progress.ParentEvent.EventID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.processor.ProcessEvent(t.Context(), e); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.engine.StepDecision(t.Context(), f.decision.TenantID, f.decision.ID, f.decision.FrozenAt.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("fixture did not reach member linking")
	return f, StoredDecision{}
}

type linkBatchOperator struct {
	RelationOperator
	link    func(context.Context, string, string, string) (store.StoredAlert, error)
	release func(context.Context, string, string, string, string) (store.StoredAlert, error)
}

type explicitLinkBatchOperator struct{ linkBatchOperator }

func (o explicitLinkBatchOperator) LinkMergeMembersPrepared(ctx context.Context, tenant string, ids []string, r domain.MergeRelation) []store.AlertUpdateResult {
	items := make([]store.AlertUpdateResult, len(ids))
	for i, id := range ids {
		items[i].StoredAlert, items[i].Err = o.LinkMergeMember(ctx, tenant, id, r.ID)
	}
	return items
}

func (o linkBatchOperator) LinkMergeMember(ctx context.Context, tenant, id, relation string) (store.StoredAlert, error) {
	if o.link != nil {
		return o.link(ctx, tenant, id, relation)
	}
	return o.RelationOperator.LinkMergeMember(ctx, tenant, id, relation)
}

func (o linkBatchOperator) ReleaseMergeWindowWithReason(ctx context.Context, tenant, id, window, reason string) (store.StoredAlert, error) {
	if o.release != nil {
		return o.release(ctx, tenant, id, window, reason)
	}
	return o.RelationOperator.ReleaseMergeWindowWithReason(ctx, tenant, id, window, reason)
}

type linkBatchDocuments struct {
	Documents
	mu       sync.Mutex
	writes   map[string]int
	failKind string
	failure  error
}

func (d *linkBatchDocuments) Put(ctx context.Context, kind, id, expected string, raw json.RawMessage) error {
	d.mu.Lock()
	if expected != "" && kind == d.failKind {
		d.failKind = ""
		d.mu.Unlock()
		return d.failure
	}
	d.mu.Unlock()
	if err := d.Documents.Put(ctx, kind, id, expected, raw); err != nil {
		return err
	}
	if expected != "" {
		d.mu.Lock()
		d.writes[kind]++
		d.mu.Unlock()
	}
	return nil
}

func (tdocs *linkBatchDocuments) counts() (int, int) {
	tdocs.mu.Lock()
	defer tdocs.mu.Unlock()
	return tdocs.writes["merge_relations"], tdocs.writes["merge_decisions"]
}

func TestLinkMembersBatchUsesConfiguredConcurrencyAndTwoProgressWrites(t *testing.T) {
	for _, tc := range []struct{ size, concurrency int }{{4, 4}, {64, 8}, {int(^uint(0) >> 1), int(^uint(0) >> 1)}} {
		t.Run(fmt.Sprintf("%d/%d", tc.size, tc.concurrency), func(t *testing.T) {
			f, current := linkBatchFixture(t, 8, 6)
			docs := &linkBatchDocuments{Documents: f.journal.docs, writes: map[string]int{}}
			f.journal.docs = docs
			want := min(tc.size, len(current.Decision.WaitMemberIDs))
			parallel := min(tc.concurrency, want)
			started := make(chan struct{}, want)
			release := make(chan struct{})
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			var active, maximum, calls atomic.Int32
			gate := func(ctx context.Context) error {
				n := active.Add(1)
				calls.Add(1)
				for previous := maximum.Load(); n > previous && !maximum.CompareAndSwap(previous, n); previous = maximum.Load() {
				}
				started <- struct{}{}
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			ops := linkBatchOperator{RelationOperator: f.processor, link: func(ctx context.Context, tenant, id, relation string) (store.StoredAlert, error) {
				if err := gate(ctx); err != nil {
					active.Add(-1)
					return store.StoredAlert{}, err
				}
				defer active.Add(-1)
				return f.processor.LinkMergeMember(ctx, tenant, id, relation)
			}, release: func(ctx context.Context, tenant, id, window, reason string) (store.StoredAlert, error) {
				if err := gate(ctx); err != nil {
					active.Add(-1)
					return store.StoredAlert{}, err
				}
				defer active.Add(-1)
				return f.processor.ReleaseMergeWindowWithReason(ctx, tenant, id, window, reason)
			}}
			ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
			defer cancel()
			type result struct {
				current StoredDecision
				err     error
			}
			finished := make(chan result, 1)
			go func() {
				next, err := f.journal.LinkMembersBatch(ctx, current, f.parent, ops, f.decision.FrozenAt.Add(3*time.Second), tc.size, tc.concurrency)
				finished <- result{next, err}
			}()
			for range parallel {
				select {
				case <-started:
				case <-ctx.Done():
					t.Fatal("configured concurrency was not reached", ctx.Err())
				}
			}
			select {
			case <-started:
				t.Fatal("concurrency budget exceeded")
			case <-time.After(20 * time.Millisecond):
			}
			once.Do(func() { close(release) })
			var got result
			select {
			case got = <-finished:
			case <-ctx.Done():
				t.Fatal("batch did not finish", ctx.Err())
			}
			relationWrites, decisionWrites := docs.counts()
			if got.err != nil || int(maximum.Load()) != parallel || int(calls.Load()) != want || got.current.Decision.Progress.MemberOffset != want || relationWrites != 1 || decisionWrites != 1 {
				t.Fatalf("parallel=%d calls=%d offset=%d writes=%d/%d err=%v", maximum.Load(), calls.Load(), got.current.Decision.Progress.MemberOffset, relationWrites, decisionWrites, got.err)
			}
			parent, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, f.parent.Alert.AlertID)
			if err != nil || parent.Alert.Merge.RelationsReady || len(f.action.inputs) != 0 {
				t.Fatal("member batch admitted parent before Ready", err)
			}
		})
	}
}

func TestLinkMembersBatchFailurePersistsOnlySuccessfulPrefixAndReplaysLaterSuccess(t *testing.T) {
	f, current := linkBatchFixture(t, 1, 1)
	failure := errors.New("member b unavailable")
	ops := linkBatchOperator{RelationOperator: f.processor, link: func(ctx context.Context, tenant, id, relation string) (store.StoredAlert, error) {
		if id == "b" {
			return store.StoredAlert{}, failure
		}
		return f.processor.LinkMergeMember(ctx, tenant, id, relation)
	}}
	next, err := f.journal.LinkMembersBatch(t.Context(), current, f.parent, explicitLinkBatchOperator{ops}, f.decision.FrozenAt.Add(3*time.Second), 64, 4)
	if !errors.Is(err, failure) || next.Decision.Progress.MemberOffset != 1 {
		t.Fatal("failed member was skipped", next.Decision.Progress.MemberOffset, err)
	}
	r := endingBatchRelation(t, f)
	a, _ := r.Relation.Member("a")
	b, _ := r.Relation.Member("b")
	c, _ := r.Relation.Member("c-00")
	if a.State != "linked" || b.State != "pending" || c.State != "pending" {
		t.Fatal("relation confirmation crossed failed prefix", r.Relation.Members)
	}
	later, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, "c-00")
	if err != nil || !slices.Contains(later.Alert.Merge.RelationIDs, f.decision.ID) {
		t.Fatal("later successful child write was lost", err)
	}
	states := len(f.relationFixture.state.inputs)
	repaired, err := f.journal.LinkMembersBatch(t.Context(), next, f.parent, f.processor, f.decision.FrozenAt.Add(4*time.Second), 64, 4)
	if err != nil || repaired.Decision.Progress.MemberOffset != len(current.Decision.WaitMemberIDs) || len(f.relationFixture.state.inputs) != states+1 || len(f.action.inputs) != 0 {
		t.Fatal("retry repeated successful member output", err)
	}
	parent, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, f.parent.Alert.AlertID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.journal.LinkStep(t.Context(), f.decision.TenantID, f.decision.ID, parent, f.processor, f.decision.FrozenAt.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(f.action.inputs) != 1 || f.action.inputs[0].Alert.AlertID != parent.Alert.AlertID {
		t.Fatal("Ready did not admit only real parent once")
	}
}

func TestLinkMembersBatchProgressFailuresPreserveChildEffectsAndRetry(t *testing.T) {
	for _, kind := range []string{"merge_relations", "merge_decisions"} {
		t.Run(kind, func(t *testing.T) {
			f, current := linkBatchFixture(t, 1, 0)
			failure := errors.New("progress CAS unavailable")
			docs := &linkBatchDocuments{Documents: f.journal.docs, writes: map[string]int{}, failKind: kind, failure: failure}
			f.journal.docs = docs
			if _, err := f.journal.LinkMembersBatch(t.Context(), current, f.parent, f.processor, f.decision.FrozenAt.Add(3*time.Second), 64, 4); !errors.Is(err, failure) {
				t.Fatal("CAS failure swallowed", err)
			}
			saved, err := f.journal.Get(t.Context(), f.decision.TenantID, f.decision.ID)
			if err != nil || saved.Decision.Progress.MemberOffset != 0 {
				t.Fatal("failed decision advanced prefix", err)
			}
			states := len(f.relationFixture.state.inputs)
			for _, id := range current.Decision.MemberIDs {
				a, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, id)
				if err != nil || !slices.Contains(a.Alert.Merge.RelationIDs, f.decision.ID) {
					t.Fatal("child effects lost on progress failure", id, err)
				}
			}
			next, err := f.journal.LinkMembersBatch(t.Context(), saved, f.parent, f.processor, f.decision.FrozenAt.Add(4*time.Second), 64, 4)
			if err != nil || next.Decision.Progress.MemberOffset != len(current.Decision.WaitMemberIDs) || len(f.relationFixture.state.inputs) != states || len(f.action.inputs) != 0 {
				t.Fatal("CAS retry repeated business effects", err)
			}
		})
	}
}

func TestLinkMembersBatchCancellationAndInvalidResultKeepValidPrefix(t *testing.T) {
	t.Run("canceled before start", func(t *testing.T) {
		f, current := linkBatchFixture(t, 0, 0)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := f.journal.LinkMembersBatch(ctx, current, f.parent, f.processor, f.decision.FrozenAt.Add(3*time.Second), 16, 4); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation swallowed", err)
		}
		actual, err := f.journal.Get(t.Context(), f.decision.TenantID, f.decision.ID)
		if err != nil || actual.Version != current.Version {
			t.Fatal("canceled batch advanced", err)
		}
	})
	t.Run("cancel running workers", func(t *testing.T) {
		f, current := linkBatchFixture(t, 4, 0)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		started := make(chan struct{}, 4)
		ops := linkBatchOperator{RelationOperator: f.processor, link: func(ctx context.Context, _, _, _ string) (store.StoredAlert, error) {
			started <- struct{}{}
			<-ctx.Done()
			return store.StoredAlert{}, ctx.Err()
		}}
		done := make(chan error, 1)
		go func() {
			_, err := f.journal.LinkMembersBatch(ctx, current, f.parent, ops, f.decision.FrozenAt.Add(3*time.Second), 16, 4)
			done <- err
		}()
		for range 4 {
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("workers did not start")
			}
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal("running cancellation swallowed", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("workers did not stop")
		}
		actual, err := f.journal.Get(t.Context(), f.decision.TenantID, f.decision.ID)
		if err != nil || actual.Decision.Progress.MemberOffset != 0 {
			t.Fatal("canceled members advanced", err)
		}
	})
	t.Run("invalid success after first member", func(t *testing.T) {
		f, current := linkBatchFixture(t, 1, 0)
		ops := linkBatchOperator{RelationOperator: f.processor, link: func(ctx context.Context, tenant, id, relation string) (store.StoredAlert, error) {
			a, err := f.processor.LinkMergeMember(ctx, tenant, id, relation)
			if id == "b" {
				a.Version = store.VersionToken{}
			}
			return a, err
		}}
		next, err := f.journal.LinkMembersBatch(t.Context(), current, f.parent, ops, f.decision.FrozenAt.Add(3*time.Second), 16, 4)
		if !errors.Is(err, policy.ErrInvalid) || next.Decision.Progress.MemberOffset != 1 {
			t.Fatal("invalid nil-error result crossed prefix", next.Decision.Progress.MemberOffset, err)
		}
	})
}

func TestLinkMembersBatchParentCloseDuringBatchNeverReadiesParent(t *testing.T) {
	f, current := linkBatchFixture(t, 0, 0)
	ops := linkBatchOperator{RelationOperator: f.processor, link: func(ctx context.Context, tenant, id, relation string) (store.StoredAlert, error) {
		linked, err := f.processor.LinkMergeMember(ctx, tenant, id, relation)
		if err == nil && id == "a" {
			_, err = f.processor.CloseAlert(ctx, lifecycle.CloseAlertCommand{OperationID: "during-link-batch", BKTenantID: tenant, AlertID: f.parent.Alert.AlertID, OperatorKind: domain.OperatorKindUser, OperatorID: "tester", Reason: "manual", EffectiveAt: f.decision.FrozenAt.Add(3 * time.Second)})
		}
		return linked, err
	}}
	next, err := f.journal.LinkMembersBatch(t.Context(), current, f.parent, ops, f.decision.FrozenAt.Add(3*time.Second), 16, 1)
	if !errors.Is(err, store.ErrInvalidTransition) || next.Decision.Progress.MemberOffset != 1 {
		t.Fatal("stale active parent snapshot bypassed per-child read", next.Decision.Progress.MemberOffset, err)
	}
	parent, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, f.parent.Alert.AlertID)
	if err != nil || parent.Alert.Status != domain.AlertStatusClosed || parent.Alert.Merge.RelationsReady || parent.Alert.Admission.AdmittedAt != nil || len(f.action.inputs) != 0 {
		t.Fatal("closed parent became Ready/admitted", err)
	}
	if _, err := f.journal.LinkMembersBatch(t.Context(), next, parent, f.processor, f.decision.FrozenAt.Add(4*time.Second), 16, 4); err != nil {
		t.Fatal(err)
	}
	ended := finishLinkBatchEnding(t, f)
	if ended.Relation.State != "ended" || len(f.action.inputs) != 0 {
		t.Fatal("parent close did not unwind without child action")
	}
}

func TestLinkMembersBatchTerminalChildrenKeepOriginalNoAdmissionSemantics(t *testing.T) {
	f, current := linkBatchFixture(t, 0, 0)
	before := []store.StoredAlert{endChild(t, f.relationFixture, "a"), endChild(t, f.relationFixture, "b")}
	next, err := f.journal.LinkMembersBatch(t.Context(), current, f.parent, f.processor, f.decision.FrozenAt.Add(3*time.Second), 16, 4)
	if err != nil || next.Decision.Progress.MemberOffset != len(current.Decision.WaitMemberIDs) {
		t.Fatal("terminal members were not confirmed", err)
	}
	parent, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, f.parent.Alert.AlertID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.journal.LinkStep(t.Context(), f.decision.TenantID, f.decision.ID, parent, f.processor, f.decision.FrozenAt.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	finishLinkBatchEnding(t, f)
	actual, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, parent.Alert.AlertID)
	if err != nil || actual.Alert.Status != domain.AlertStatusRecovered || actual.Alert.Admission.AdmittedAt != nil || len(f.action.inputs) != 0 {
		t.Fatal("all-terminal batch briefly admitted parent", err)
	}
	for _, original := range before {
		now, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, original.Alert.AlertID)
		if err != nil || !reflect.DeepEqual(now, original) {
			t.Fatal("terminal child rewritten", err)
		}
	}
}

func finishLinkBatchEnding(t *testing.T, f executorFixture) StoredRelation {
	t.Helper()
	for i := range 32 {
		r, err := f.journal.ReconcileRelationStep(t.Context(), f.decision.TenantID, f.decision.ID, f.processor, f.decision.FrozenAt.Add(time.Duration(5+i)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if r.Relation.State == "ended" {
			return r
		}
	}
	t.Fatal("batch parent ending did not complete")
	return StoredRelation{}
}

type preparedLinkBatchOperator struct {
	RelationOperator
	prepared                     func(context.Context, string, string, domain.MergeRelation) (store.StoredAlert, error)
	preparedCalls, fallbackCalls atomic.Int32
}

func (o *preparedLinkBatchOperator) LinkMergeMember(context.Context, string, string, string) (store.StoredAlert, error) {
	o.fallbackCalls.Add(1)
	return store.StoredAlert{}, errors.New("must reuse fixed relation")
}

func (o *preparedLinkBatchOperator) LinkMergeMemberPrepared(ctx context.Context, tenant, id string, r domain.MergeRelation) (store.StoredAlert, error) {
	o.preparedCalls.Add(1)
	return o.prepared(ctx, tenant, id, r)
}

func TestLinkMembersBatchPreparedRelationStillReadsLiveParentPerMember(t *testing.T) {
	for _, closeDuring := range []bool{false, true} {
		t.Run(fmt.Sprintf("close=%t", closeDuring), func(t *testing.T) {
			f, current := linkBatchFixture(t, 1, 0)
			reads := &endingBatchReadRepository{Repository: f.repo, reads: map[string]int{}}
			p, err := lifecycle.NewProcessor(reads, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, []lifecycle.NamedFinalHook{{Name: "state", Purpose: "state", Hook: &endingBatchHook{hook: f.relationFixture.state}}, {Name: "action", Purpose: "action", Hook: &endingBatchHook{hook: f.action}}}, config.DefaultSeverityConfig(), journalClock{at: f.decision.FrozenAt.Add(2 * time.Second)}, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithMergeRelations(f.journal))
			if err != nil {
				t.Fatal(err)
			}
			ops := &preparedLinkBatchOperator{RelationOperator: p, prepared: func(ctx context.Context, tenant, id string, r domain.MergeRelation) (store.StoredAlert, error) {
				if r.ID != f.decision.ID || r.State != "preparing" || r.IndexOffset != len(r.Members)+1 {
					return store.StoredAlert{}, errors.New("different prepared relation")
				}
				linked, err := p.LinkMergeMemberPrepared(ctx, tenant, id, r)
				if closeDuring && id == "a" && err == nil {
					_, err = p.CloseAlert(ctx, lifecycle.CloseAlertCommand{OperationID: "close-during-prepared-link", BKTenantID: tenant, AlertID: r.ParentAlertID, OperatorKind: domain.OperatorKindUser, OperatorID: "tester", Reason: "manual", EffectiveAt: f.decision.FrozenAt.Add(3 * time.Second)})
				}
				return linked, err
			}}
			concurrency := 4
			if closeDuring {
				concurrency = 1
			}
			next, err := f.journal.LinkMembersBatch(t.Context(), current, f.parent, ops, f.decision.FrozenAt.Add(3*time.Second), 16, concurrency)
			if ops.fallbackCalls.Load() != 0 || int(ops.preparedCalls.Load()) != len(current.Decision.MemberIDs) {
				t.Fatal("prepared relation port hidden", ops.preparedCalls.Load(), ops.fallbackCalls.Load())
			}
			if closeDuring {
				if !errors.Is(err, store.ErrInvalidTransition) || next.Decision.Progress.MemberOffset != 1 {
					t.Fatal("prepared path used stale active parent", next.Decision.Progress.MemberOffset, err)
				}
			} else if err != nil || reads.reads[f.parent.Alert.AlertID] != len(current.Decision.MemberIDs) || next.Decision.Progress.MemberOffset != len(current.Decision.WaitMemberIDs) {
				t.Fatal("prepared relation bypassed live parent reads", reads.reads, err)
			}
			parent, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, f.parent.Alert.AlertID)
			if err != nil || parent.Alert.Merge.RelationsReady || parent.Alert.Admission.AdmittedAt != nil || len(f.action.inputs) != 0 {
				t.Fatal("prepared member batch prematurely readied/admitted parent", err)
			}
		})
	}
}

func TestLinkMembersBatchUnselectedReleaseKeepsSingleOriginalAdmissionOnRetry(t *testing.T) {
	f, current := linkBatchFixture(t, 0, 0)
	template, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, "a")
	if err != nil {
		t.Fatal(err)
	}
	unselected := template.Alert.Clone()
	unselected.AlertID = "uncommitted"
	unselected.Fingerprint = "unselected-fp"
	unselected.TriggerEventID = "unselected-event"
	unselected.LatestEventID = unselected.TriggerEventID
	unselected.Merge.Pending[0].MemberEventID = unselected.TriggerEventID
	if _, err := f.repo.CreateAlert(t.Context(), unselected); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("decision offset write unavailable")
	docs := &linkBatchDocuments{Documents: f.journal.docs, writes: map[string]int{}, failKind: "merge_decisions", failure: failure}
	f.journal.docs = docs
	if _, err := f.journal.LinkMembersBatch(t.Context(), current, f.parent, f.processor, f.decision.FrozenAt.Add(3*time.Second), 16, 4); !errors.Is(err, failure) {
		t.Fatal("offset failure swallowed", err)
	}
	if len(f.action.inputs) != 1 || f.action.inputs[0].Alert.AlertID != "uncommitted" {
		t.Fatal("selected link dispatched action or unselected lost original admission")
	}
	admitted, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, "uncommitted")
	if err != nil || admitted.Alert.Admission.AdmittedAt == nil || admitted.Alert.Merge.Blocking() {
		t.Fatal("unselected original release missing", err)
	}
	saved, err := f.journal.Get(t.Context(), f.decision.TenantID, f.decision.ID)
	if err != nil {
		t.Fatal(err)
	}
	next, err := f.journal.LinkMembersBatch(t.Context(), saved, f.parent, f.processor, f.decision.FrozenAt.Add(4*time.Second), 16, 4)
	if err != nil || next.Decision.Progress.MemberOffset != len(current.Decision.WaitMemberIDs) || len(f.action.inputs) != 1 {
		t.Fatal("replay repeated unselected admission", err)
	}
	after, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, "uncommitted")
	if err != nil || !reflect.DeepEqual(after, admitted) {
		t.Fatal("replay rewrote unselected child", err)
	}
}
