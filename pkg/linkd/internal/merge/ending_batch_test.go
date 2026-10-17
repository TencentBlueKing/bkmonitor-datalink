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

// 并发解除会同时输出不同成员状态；仅为旧 fixture 的切片 hook 加锁，不串行化业务操作。
type endingBatchHook struct {
	mu   sync.Mutex
	hook *relationHook
}

func (h *endingBatchHook) Execute(ctx context.Context, in lifecycle.FinalHookInput) (lifecycle.FinalHookResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hook.Execute(ctx, in)
}

func endingBatchFixture(t *testing.T, reserved int) executorFixture {
	t.Helper()
	f := newExecutorFixture(t)
	f.engine.Batch = config.MergeControlConfig{MemberBatchSize: 16, Concurrency: 4}
	for i := range reserved {
		f.state.result.Window.Members = append(f.state.result.Window.Members, redisstate.MergeMember{Main: domain.DependencyMain{AlertID: fmt.Sprintf("reserved-%02d", i)}})
	}
	startExecutor(t, f)
	done := driveExecutor(t, f)
	parent, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, done.Decision.Progress.ParentAlertID)
	if err != nil {
		t.Fatal(err)
	}
	f.parent = parent
	p, err := lifecycle.NewProcessor(f.repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, []lifecycle.NamedFinalHook{{Name: "state", Purpose: "state", Hook: &endingBatchHook{hook: f.relationFixture.state}}, {Name: "action", Purpose: "action", Hook: &endingBatchHook{hook: f.action}}}, config.DefaultSeverityConfig(), journalClock{at: f.decision.FrozenAt.Add(4 * time.Second)}, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithMergeRelations(f.journal))
	if err != nil {
		t.Fatal(err)
	}
	f.processor = p
	f.engine.Operations = p
	return f
}

type endingBatchOperator struct {
	RelationEndOperator
	unlink  func(context.Context, string, string, string) (store.StoredAlert, error)
	recover func(context.Context, string, string, string) (store.StoredAlert, error)
}

func (o endingBatchOperator) UnlinkMergeMember(ctx context.Context, tenant, id, relation string) (store.StoredAlert, error) {
	if o.unlink != nil {
		return o.unlink(ctx, tenant, id, relation)
	}
	return o.RelationEndOperator.UnlinkMergeMember(ctx, tenant, id, relation)
}

func (o endingBatchOperator) RecoverMergeParent(ctx context.Context, tenant, id, relation string) (store.StoredAlert, error) {
	if o.recover != nil {
		return o.recover(ctx, tenant, id, relation)
	}
	return o.RelationEndOperator.RecoverMergeParent(ctx, tenant, id, relation)
}

func endingBatchRelation(t *testing.T, f executorFixture) StoredRelation {
	t.Helper()
	r, err := f.journal.GetRelation(t.Context(), f.decision.TenantID, f.decision.ID)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestEndRelationBatchBoundsFourConcurrentUnlinksAndSixteenMembers(t *testing.T) {
	f := endingBatchFixture(t, 20)
	parent := closeRelationParent(t, f.relationFixture)
	actions := len(f.action.inputs)
	started := make(chan string, 32)
	release := make(chan struct{})
	var unblock sync.Once
	t.Cleanup(func() { unblock.Do(func() { close(release) }) })
	var active, maxActive, calls, recoveries atomic.Int32
	ops := endingBatchOperator{RelationEndOperator: f.processor, recover: func(ctx context.Context, tenant, id, relation string) (store.StoredAlert, error) {
		recoveries.Add(1)
		return f.processor.RecoverMergeParent(ctx, tenant, id, relation)
	}, unlink: func(ctx context.Context, tenant, id, relation string) (store.StoredAlert, error) {
		n := active.Add(1)
		defer active.Add(-1)
		calls.Add(1)
		for old := maxActive.Load(); n > old && !maxActive.CompareAndSwap(old, n); old = maxActive.Load() {
		}
		started <- id
		select {
		case <-release:
		case <-ctx.Done():
			return store.StoredAlert{}, ctx.Err()
		}
		return f.processor.UnlinkMergeMember(ctx, tenant, id, relation)
	}}
	type result struct {
		r   StoredRelation
		err error
	}
	finished := make(chan result, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	go func() {
		r, err := f.journal.EndRelationBatchWithBudget(ctx, f.decision.TenantID, f.decision.ID, ops, f.decision.FrozenAt.Add(4*time.Second), 16, 4)
		finished <- result{r, err}
	}()
	for range 4 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("batch did not schedule four independent members", ctx.Err())
		}
	}
	select {
	case id := <-started:
		t.Fatal("more than four unlinks started before any completed", id)
	case <-time.After(25 * time.Millisecond):
	}
	unblock.Do(func() { close(release) })
	var got result
	select {
	case got = <-finished:
	case <-ctx.Done():
		t.Fatal("batch did not finish", ctx.Err())
	}
	if got.err != nil || maxActive.Load() != 4 || calls.Load() != 16 || recoveries.Load() != 1 || got.r.Relation.EndOffset != 16 || got.r.Relation.State != "ending" {
		t.Fatalf("active=%d calls=%d recoveries=%d offset=%d state=%s err=%v", maxActive.Load(), calls.Load(), recoveries.Load(), got.r.Relation.EndOffset, got.r.Relation.State, got.err)
	}
	if len(f.action.inputs) != actions {
		t.Fatal("batch unlink dispatched a child action")
	}
	actual, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, parent.Alert.AlertID)
	if err != nil || !reflect.DeepEqual(actual, parent) {
		t.Fatal("batch rewrote terminal parent", err)
	}
}

func TestEndRelationBatchFailureKeepsSuccessfulPrefixAndRetriesIdempotently(t *testing.T) {
	f := endingBatchFixture(t, 6)
	closeRelationParent(t, f.relationFixture)
	before := endingBatchRelation(t, f)
	failure := errors.New("second member failed")
	var callsMu sync.Mutex
	calls := map[string]int{}
	ops := endingBatchOperator{RelationEndOperator: f.processor, unlink: func(ctx context.Context, tenant, id, relation string) (store.StoredAlert, error) {
		callsMu.Lock()
		calls[id]++
		callsMu.Unlock()
		if id == "b" {
			return store.StoredAlert{}, failure
		}
		return f.processor.UnlinkMergeMember(ctx, tenant, id, relation)
	}}
	actions := len(f.action.inputs)
	if _, err := f.journal.EndRelationBatchWithBudget(t.Context(), f.decision.TenantID, f.decision.ID, ops, f.decision.FrozenAt.Add(4*time.Second), 16, 4); !errors.Is(err, failure) {
		t.Fatal("member failure was swallowed", err)
	}
	partial := endingBatchRelation(t, f)
	if partial.Relation.EndOffset != 1 || partial.Relation.State != "ending" {
		t.Fatal("progress crossed failed second member", partial.Relation.EndOffset, partial.Relation.State)
	}
	a, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, "a")
	if err != nil || a.Alert.Merge.Blocking() {
		t.Fatal("successful first member not durable", err)
	}
	stateCount := len(f.relationFixture.state.inputs)
	ended, err := f.journal.EndRelationBatchWithBudget(t.Context(), f.decision.TenantID, f.decision.ID, f.processor, f.decision.FrozenAt.Add(5*time.Second), 16, 4)
	if err != nil || ended.Relation.State != "ended" || ended.Relation.EndOffset != len(before.Relation.WaitMemberIDs) || !reflect.DeepEqual(ended.Relation.WaitMemberIDs, before.Relation.WaitMemberIDs) || !reflect.DeepEqual(ended.Relation.Members, before.Relation.Members) {
		t.Fatal("retry did not finish fixed membership", ended.Relation, err)
	}
	if len(f.action.inputs) != actions || len(f.relationFixture.state.inputs) != stateCount+1 {
		t.Fatal("retry repeated successful child output or admitted children")
	}
	if calls["reserved-00"] != 1 {
		t.Fatal("later independent member did not run after an earlier failure", calls)
	}
	stateCount = len(f.relationFixture.state.inputs)
	replay, err := f.journal.EndRelationBatchWithBudget(t.Context(), f.decision.TenantID, f.decision.ID, f.processor, f.decision.FrozenAt.Add(6*time.Second), 16, 4)
	if err != nil || replay.Version != ended.Version || len(f.relationFixture.state.inputs) != stateCount || len(f.action.inputs) != actions {
		t.Fatal("ended replay was not idempotent", err)
	}
}

// 只注入已有关系的进度 CAS 失败，避免 ensureRelation 的 create-if-absent 重试先消费故障。
type endingProgressFailDocuments struct {
	Documents
	fail    atomic.Bool
	failure error
}

func (d *endingProgressFailDocuments) Put(ctx context.Context, kind, id, expected string, raw json.RawMessage) error {
	if kind == "merge_relations" && expected != "" && d.fail.Swap(false) {
		return d.failure
	}
	return d.Documents.Put(ctx, kind, id, expected, raw)
}

func TestEndRelationBatchProgressWriteFailureDoesNotRepeatChildOutput(t *testing.T) {
	f := endingBatchFixture(t, 0)
	parent := closeRelationParent(t, f.relationFixture)
	at := f.decision.FrozenAt.Add(4 * time.Second)
	if _, err := f.journal.BeginEndRelation(t.Context(), f.decision.TenantID, f.decision.ID, parent, at); err != nil {
		t.Fatal(err)
	}
	fault := &endingProgressFailDocuments{Documents: f.journal.docs, failure: errors.New("relation progress unavailable")}
	fault.fail.Store(true)
	f.journal.docs = fault
	if _, err := f.journal.EndRelationBatchWithBudget(t.Context(), f.decision.TenantID, f.decision.ID, f.processor, at, 16, 4); !errors.Is(err, fault.failure) {
		t.Fatal("relation progress write failure was swallowed", err)
	}
	if r := endingBatchRelation(t, f); r.Relation.EndOffset != 0 {
		t.Fatal("failed progress CAS advanced offset", r.Relation.EndOffset)
	}
	for _, id := range []string{"a", "b"} {
		a, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, id)
		if err != nil || a.Alert.Merge.Blocking() {
			t.Fatal("successful child write was rolled back", id, err)
		}
	}
	states, actions := len(f.relationFixture.state.inputs), len(f.action.inputs)
	r, err := f.journal.EndRelationBatchWithBudget(t.Context(), f.decision.TenantID, f.decision.ID, f.processor, at.Add(time.Second), 16, 4)
	if err != nil || r.Relation.State != "ended" || len(f.relationFixture.state.inputs) != states || len(f.action.inputs) != actions {
		t.Fatal("retry repeated child business effects", r.Relation.State, err)
	}
}

func TestEndRelationBatchCancellationAndSelectedMemberErrors(t *testing.T) {
	for _, failure := range []error{store.ErrNotFound, policy.ErrAccess, context.Canceled, errors.New("member storage unavailable")} {
		t.Run(failure.Error(), func(t *testing.T) {
			f := endingBatchFixture(t, 0)
			closeRelationParent(t, f.relationFixture)
			ops := endingBatchOperator{RelationEndOperator: f.processor, unlink: func(context.Context, string, string, string) (store.StoredAlert, error) {
				return store.StoredAlert{}, failure
			}}
			if _, err := f.journal.EndRelationBatchWithBudget(t.Context(), f.decision.TenantID, f.decision.ID, ops, f.decision.FrozenAt.Add(4*time.Second), 16, 4); !errors.Is(err, failure) {
				t.Fatal("selected member error swallowed", err)
			}
			if r := endingBatchRelation(t, f); r.Relation.EndOffset != 0 || r.Relation.State == "ended" {
				t.Fatal("failed selected member skipped", r.Relation)
			}
		})
	}
	t.Run("canceled before start", func(t *testing.T) {
		f := endingBatchFixture(t, 0)
		closeRelationParent(t, f.relationFixture)
		before := endingBatchRelation(t, f)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := f.journal.EndRelationBatchWithBudget(ctx, f.decision.TenantID, f.decision.ID, f.processor, f.decision.FrozenAt.Add(4*time.Second), 16, 4); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation swallowed", err)
		}
		if after := endingBatchRelation(t, f); after.Version != before.Version {
			t.Fatal("canceled batch mutated progress")
		}
	})
	t.Run("cancel running workers", func(t *testing.T) {
		f := endingBatchFixture(t, 20)
		closeRelationParent(t, f.relationFixture)
		started := make(chan struct{}, 4)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		ops := endingBatchOperator{RelationEndOperator: f.processor, unlink: func(ctx context.Context, _, _, _ string) (store.StoredAlert, error) {
			started <- struct{}{}
			<-ctx.Done()
			return store.StoredAlert{}, ctx.Err()
		}}
		finished := make(chan error, 1)
		go func() {
			_, err := f.journal.EndRelationBatchWithBudget(ctx, f.decision.TenantID, f.decision.ID, ops, f.decision.FrozenAt.Add(4*time.Second), 16, 4)
			finished <- err
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
		case err := <-finished:
			if !errors.Is(err, context.Canceled) {
				t.Fatal("running cancellation swallowed", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("workers did not stop on cancellation")
		}
		if r := endingBatchRelation(t, f); r.Relation.EndOffset != 0 {
			t.Fatal("canceled unlinks advanced prefix")
		}
	})
}

func TestCheckRelationBatchMoreOnlyForBoundedEndProgress(t *testing.T) {
	f := endingBatchFixture(t, 20)
	before := endingBatchRelation(t, f)
	if more, err := f.engine.CheckRelationBatch(t.Context(), f.decision.TenantID, f.decision.ID, f.decision.FrozenAt.Add(4*time.Second)); err != nil || more {
		t.Fatal("active unchanged relation requested immediate scan", more, err)
	}
	if after := endingBatchRelation(t, f); after.Version != before.Version {
		t.Fatal("active relationship changed")
	}
	parent := closeRelationParent(t, f.relationFixture)
	actions := len(f.action.inputs)
	if _, err := f.journal.BeginEndRelation(t.Context(), f.decision.TenantID, f.decision.ID, parent, f.decision.FrozenAt.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	parentReads := 0
	f.engine.CurrentAlert = func(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
		if id == parent.Alert.AlertID {
			parentReads++
		}
		return f.repo.GetAlert(ctx, tenant, id)
	}
	more, err := f.engine.CheckRelationBatch(t.Context(), f.decision.TenantID, f.decision.ID, f.decision.FrozenAt.Add(5*time.Second))
	partial := endingBatchRelation(t, f)
	if err != nil || !more || partial.Relation.EndOffset != 16 || parentReads > 1 {
		t.Fatal("first bounded batch missing progress or repeated parent reads", more, partial.Relation.EndOffset, parentReads, err)
	}
	more, err = f.engine.CheckRelationBatch(t.Context(), f.decision.TenantID, f.decision.ID, f.decision.FrozenAt.Add(6*time.Second))
	if r := endingBatchRelation(t, f); err != nil || more || r.Relation.State != "ended" || len(f.action.inputs) != actions {
		t.Fatal("final batch requested idle recheck or child action", more, r.Relation.State, err)
	}
}

func TestCheckRelationBatchRecoversParentOnlyAfterEverySelectedMemberEnds(t *testing.T) {
	f := endingBatchFixture(t, 0)
	initialActions := len(f.action.inputs)
	first := endChild(t, f.relationFixture, "a")
	if _, err := f.engine.CheckRelationBatch(t.Context(), f.decision.TenantID, f.decision.ID, f.decision.FrozenAt.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	parent, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, f.parent.Alert.AlertID)
	if err != nil || parent.Alert.Status != domain.AlertStatusActive || len(f.action.inputs) != initialActions {
		t.Fatal("one terminal member recovered parent prematurely", parent.Alert.Status, err)
	}
	second := endChild(t, f.relationFixture, "b")
	for i := range 5 {
		more, err := f.engine.CheckRelationBatch(t.Context(), f.decision.TenantID, f.decision.ID, f.decision.FrozenAt.Add(time.Duration(5+i)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
	}
	ended := endingBatchRelation(t, f)
	parent, err = f.repo.GetAlert(t.Context(), f.decision.TenantID, f.parent.Alert.AlertID)
	if err != nil || ended.Relation.State != "ended" || ended.Relation.EndReason != "members_ended" || parent.Alert.Status != domain.AlertStatusRecovered || parent.Alert.EndReason != "merge_members_ended" || len(f.action.inputs) != initialActions+1 {
		t.Fatal("all-terminal recovery did not complete once", ended.Relation.State, parent.Alert.Status, err)
	}
	if f.action.inputs[len(f.action.inputs)-1].Alert.AlertID != parent.Alert.AlertID {
		t.Fatal("recovery dispatched a child action")
	}
	for _, original := range []store.StoredAlert{first, second} {
		actual, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, original.Alert.AlertID)
		if err != nil || !reflect.DeepEqual(actual, original) {
			t.Fatal("batch rewrote terminal child", original.Alert.AlertID, err)
		}
	}
}

func TestEndRelationBatchParentFailureLeavesMembershipAndPrefixUnchanged(t *testing.T) {
	f := endingBatchFixture(t, 0)
	closeRelationParent(t, f.relationFixture)
	before := endingBatchRelation(t, f)
	failure := errors.New("parent output unavailable")
	var unlinks atomic.Int32
	ops := endingBatchOperator{RelationEndOperator: f.processor, recover: func(context.Context, string, string, string) (store.StoredAlert, error) {
		return store.StoredAlert{}, failure
	}, unlink: func(context.Context, string, string, string) (store.StoredAlert, error) {
		unlinks.Add(1)
		return store.StoredAlert{}, errors.New("must not unlink without parent proof")
	}}
	if _, err := f.journal.EndRelationBatchWithBudget(t.Context(), f.decision.TenantID, f.decision.ID, ops, f.decision.FrozenAt.Add(4*time.Second), 16, 4); !errors.Is(err, failure) {
		t.Fatal("parent recovery error swallowed", err)
	}
	if after := endingBatchRelation(t, f); unlinks.Load() != 0 || !reflect.DeepEqual(after, before) {
		t.Fatal("failed parent proof mutated fixed relation", after.Relation.EndOffset, unlinks.Load())
	}
}

type endingBatchReadRepository struct {
	store.Repository
	mu    sync.Mutex
	reads map[string]int
}

func (r *endingBatchReadRepository) GetAlert(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
	r.mu.Lock()
	r.reads[id]++
	r.mu.Unlock()
	return r.Repository.GetAlert(ctx, tenant, id)
}

type preparedEndingBatchOperator struct {
	RelationEndOperator
	expectedParent                store.StoredAlert
	expectedRelation              domain.MergeRelation
	processor                     *lifecycle.Processor
	prepared, recovered, fallback atomic.Int32
}

func (o *preparedEndingBatchOperator) RecoverMergeParent(ctx context.Context, tenant, id, relation string) (store.StoredAlert, error) {
	o.recovered.Add(1)
	return o.RelationEndOperator.RecoverMergeParent(ctx, tenant, id, relation)
}

func (o *preparedEndingBatchOperator) UnlinkMergeMember(context.Context, string, string, string) (store.StoredAlert, error) {
	o.fallback.Add(1)
	return store.StoredAlert{}, errors.New("prepared batch must not re-read fixed facts per member")
}

func (o *preparedEndingBatchOperator) UnlinkMergeMemberPrepared(ctx context.Context, tenant, id string, relation domain.MergeRelation, parent store.StoredAlert) (store.StoredAlert, error) {
	o.prepared.Add(1)
	if !reflect.DeepEqual(parent, o.expectedParent) || relation.State != "ending" || relation.ID != o.expectedRelation.ID || relation.ParentAlertID != parent.Alert.AlertID || !reflect.DeepEqual(relation.WaitMemberIDs, o.expectedRelation.WaitMemberIDs) || !reflect.DeepEqual(relation.Members, o.expectedRelation.Members) {
		return store.StoredAlert{}, errors.New("prepared batch supplied different terminal parent or fixed membership")
	}
	return o.processor.UnlinkMergeMemberPrepared(ctx, tenant, id, relation, parent)
}

func TestEndRelationBatchPreparedFactsAvoidRepeatedParentReadsButReadCurrentChildren(t *testing.T) {
	f := endingBatchFixture(t, 0)
	parent := closeRelationParent(t, f.relationFixture)
	fixed := endingBatchRelation(t, f)
	reads := &endingBatchReadRepository{Repository: f.repo, reads: map[string]int{}}
	p, err := lifecycle.NewProcessor(reads, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, []lifecycle.NamedFinalHook{{Name: "state", Purpose: "state", Hook: &endingBatchHook{hook: f.relationFixture.state}}, {Name: "action", Purpose: "action", Hook: &endingBatchHook{hook: f.action}}}, config.DefaultSeverityConfig(), journalClock{at: f.decision.FrozenAt.Add(4 * time.Second)}, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithMergeRelations(f.journal))
	if err != nil {
		t.Fatal(err)
	}
	ops := &preparedEndingBatchOperator{RelationEndOperator: p, expectedParent: parent, expectedRelation: fixed.Relation, processor: p}
	actions := len(f.action.inputs)
	ended, err := f.journal.EndRelationBatchWithBudget(t.Context(), f.decision.TenantID, f.decision.ID, ops, f.decision.FrozenAt.Add(4*time.Second), 16, 4)
	if err != nil || ended.Relation.State != "ended" || ops.recovered.Load() != 1 || int(ops.prepared.Load()) != len(fixed.Relation.WaitMemberIDs) || ops.fallback.Load() != 0 {
		t.Fatal("prepared unlink path not used per member", ended.Relation.State, ops.recovered.Load(), ops.prepared.Load(), ops.fallback.Load(), err)
	}
	if reads.reads[parent.Alert.AlertID] != 1 {
		t.Fatal("per-member unlink re-read fixed parent", reads.reads)
	}
	for _, id := range fixed.Relation.WaitMemberIDs {
		if reads.reads[id] < 1 {
			t.Fatal("prepared facts replaced live child state read", id, reads.reads)
		}
	}
	if len(f.action.inputs) != actions {
		t.Fatal("prepared unlink dispatched child action")
	}
	for _, id := range []string{"a", "b"} {
		a, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, id)
		if err != nil || a.Alert.Merge.Blocking() || a.Alert.Status != domain.AlertStatusActive || a.Alert.Admission.AdmittedAt != nil {
			t.Fatal("prepared unlink changed child lifecycle/admission", id, err)
		}
	}
}
