// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package merge

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

type poolTestOperations struct {
	Operations
	mu      sync.Mutex
	fail    string
	calls   []string
	gate    chan struct{}
	entered chan struct{}
}

func (o *poolTestOperations) ProcessMergeMemberBatch(ctx context.Context, facts domain.MergeMemberContext, ids []string, unlink bool) []store.AlertUpdateResult {
	if o.gate != nil {
		o.entered <- struct{}{}
		<-o.gate
	}
	out := make([]store.AlertUpdateResult, len(ids))
	for i, id := range ids {
		o.mu.Lock()
		o.calls = append(o.calls, id)
		fail := o.fail == id
		o.mu.Unlock()
		if fail {
			out[i].Err = store.ErrVersionConflict
			continue
		}
		if unlink {
			out[i].StoredAlert, out[i].Err = o.UnlinkMergeMember(ctx, facts.TenantID, id, facts.ID)
		} else {
			out[i].StoredAlert, out[i].Err = o.LinkMergeMember(ctx, facts.TenantID, id, facts.ID)
		}
	}
	return out
}

func poolFixture(t *testing.T, members int) (executorFixture, StoredDecision, *poolTestOperations) {
	t.Helper()
	f, current := linkBatchFixture(t, members-2, 0)
	ops := &poolTestOperations{Operations: f.processor}
	f.engine.Operations, f.engine.Members = ops, NewMemberPool()
	f.engine.Batch.MemberBatchSize = 2
	r, err := f.journal.GetRelation(t.Context(), f.decision.TenantID, f.decision.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.engine.memberPool(r.Relation, "link")
	return f, current, ops
}

func finishPoolBatch(ctx context.Context, e *Executor, b MemberBatch) (bool, error) {
	result, err := e.ProcessMemberBatch(ctx, b)
	return e.Members.Finish(result, err, time.Now()), err
}

func TestMemberPoolEndedRelationRetriesDecisionOnly(t *testing.T) {
	f, _, ops := poolFixture(t, 4)
	closeRelationParent(t, f.relationFixture)
	at := f.decision.FrozenAt.Add(5 * time.Second)
	for range 2 {
		if _, err := f.engine.CheckRelationBatch(t.Context(), f.decision.TenantID, f.decision.ID, at); err != nil {
			t.Fatal(err)
		}
	}
	for _, b := range f.engine.Members.Take(100, time.Now()) {
		if _, err := finishPoolBatch(t.Context(), f.engine, b); err != nil {
			t.Fatal(err)
		}
	}
	f.docs.failKind = "merge_decisions"
	if _, err := f.engine.CheckRelationBatch(t.Context(), f.decision.TenantID, f.decision.ID, at); err == nil {
		t.Fatal("decision failure not injected")
	}
	ended, err := f.journal.GetRelation(t.Context(), f.decision.TenantID, f.decision.ID)
	if err != nil || ended.Relation.State != "ended" {
		t.Fatal("relation was not committed", err)
	}
	calls := len(ops.calls)
	read := f.engine.CurrentAlert
	f.engine.CurrentAlert = func(context.Context, string, string) (store.StoredAlert, error) {
		return store.StoredAlert{}, errors.New("temporary read failure")
	}
	if _, err := f.engine.StepDecisionBatch(t.Context(), f.decision.TenantID, f.decision.ID, at.Add(time.Second)); err == nil {
		t.Fatal("read failure not injected")
	}
	f.engine.CurrentAlert = read
	if _, err := f.engine.StepDecisionBatch(t.Context(), f.decision.TenantID, f.decision.ID, at.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	decision, err := f.journal.Get(t.Context(), f.decision.TenantID, f.decision.ID)
	if err != nil || decision.Decision.Progress.Phase != "completed" {
		t.Fatal("ended relation did not complete decision", decision.Decision.Progress.Phase, err)
	}
	after, err := f.journal.GetRelation(t.Context(), f.decision.TenantID, f.decision.ID)
	if err != nil || after.Version != ended.Version || len(ops.calls) != calls || len(f.engine.Members.Take(100, time.Now())) != 0 {
		t.Fatal("ended relation was re-executed", err)
	}
}

func TestMemberPoolRetriesOnlyFailedMembers(t *testing.T) {
	f, _, ops := poolFixture(t, 4)
	first := f.engine.Members.Take(1, time.Now())[0]
	ops.fail = first.Members[1].AlertID
	if ready, err := finishPoolBatch(t.Context(), f.engine, first); ready || !errors.Is(err, store.ErrVersionConflict) {
		t.Fatal(ready, err)
	}
	ops.fail = ""
	retry := f.engine.Members.Take(1, time.Now().Add(2*time.Second))[0]
	if retry.Key != first.Key || retry.Members[0].State != "linked" {
		t.Fatal("lost successful member", retry)
	}
	if _, err := finishPoolBatch(t.Context(), f.engine, retry); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ops.calls, []string{first.Members[0].AlertID, first.Members[1].AlertID, first.Members[1].AlertID}) {
		t.Fatal("successful member repeated", ops.calls)
	}
	if f.engine.Members.Finish(retry, nil, time.Now()) {
		t.Fatal("duplicate result advanced stage")
	}
}

func TestMemberPoolDifferentBatchesExecuteConcurrently(t *testing.T) {
	f, current, ops := poolFixture(t, 6)
	batches := f.engine.Members.Take(100, time.Now())
	ops.gate, ops.entered = make(chan struct{}), make(chan struct{}, len(batches))
	errs := make(chan error, len(batches))
	selected := 0
	for _, b := range batches {
		if slices.ContainsFunc(b.Members, func(m MemberBatchItem) bool { return m.Selected }) {
			selected++
		}
		go func() { _, err := finishPoolBatch(t.Context(), f.engine, b); errs <- err }()
	}
	for range selected {
		select {
		case <-ops.entered:
		case <-time.After(3 * time.Second):
			close(ops.gate)
			t.Fatal("member batches serialized")
		}
	}
	r, err := f.journal.GetRelation(t.Context(), f.decision.TenantID, f.decision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if more, err := f.engine.stepMemberPool(t.Context(), current, r, f.parent, f.decision.FrozenAt.Add(5*time.Second)); more || err != nil {
		t.Fatal("inflight members admitted parent", more, err)
	}
	close(ops.gate)
	for range batches {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if more, err := f.engine.stepMemberPool(t.Context(), current, r, f.parent, f.decision.FrozenAt.Add(6*time.Second)); !more || err != nil {
		t.Fatal("completed stage not advanced", more, err)
	}
	if len(f.engine.Members.stages) != 0 {
		t.Fatal("completed stage retained")
	}
}

func TestMemberPoolRebuildAfterLostResultsDoesNotRepeatOutputs(t *testing.T) {
	f, current, _ := poolFixture(t, 4)
	for _, b := range f.engine.Members.Take(100, time.Now()) {
		if _, err := f.engine.ProcessMemberBatch(t.Context(), b); err != nil {
			t.Fatal(err)
		}
		// 模拟业务写成功后进程退出，内存完成结果未回报。
	}
	outputs := len(f.relationFixture.state.inputs)
	r, err := f.journal.GetRelation(t.Context(), f.decision.TenantID, f.decision.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.engine.Members = NewMemberPool()
	f.engine.Batch.MemberBatchSize = 3 // 重启可按新预算重新拆批，不依赖历史批次身份。
	_, done := f.engine.memberPool(r.Relation, "link")
	if done {
		t.Fatal("restart guessed completion without replay")
	}
	for _, b := range f.engine.Members.Take(100, time.Now()) {
		if _, err := finishPoolBatch(t.Context(), f.engine, b); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.relationFixture.state.inputs) != outputs {
		t.Fatal("restart replay repeated state output")
	}
	if _, err := f.engine.stepMemberPool(t.Context(), current, r, f.parent, f.decision.FrozenAt.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.journal.LinkStep(t.Context(), f.decision.TenantID, f.decision.ID, f.parent, f.processor, f.decision.FrozenAt.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(f.action.inputs) != 1 {
		t.Fatal("parent action missing or repeated")
	}
	for key := range f.docs.data {
		switch strings.SplitN(key, "/", 2)[0] {
		case "merge_decisions", "merge_members", "merge_relations", "merge_relation_refs":
			continue
		default:
			t.Fatal("intermediate batch persisted")
		}
	}
}

func TestMemberPoolCancellationKeepsUnfinishedMembers(t *testing.T) {
	f, _, ops := poolFixture(t, 2)
	b := f.engine.Members.Take(1, time.Now())[0]
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if ready, err := finishPoolBatch(ctx, f.engine, b); ready || !errors.Is(err, context.Canceled) || len(ops.calls) != 0 {
		t.Fatal(ready, err, ops.calls)
	}
	retry := f.engine.Members.Take(1, time.Now().Add(2*time.Second))[0]
	if retry.Key != b.Key {
		t.Fatal("canceled batch lost")
	}
	if _, err := finishPoolBatch(t.Context(), f.engine, retry); err != nil {
		t.Fatal(err)
	}
}

func TestMemberPoolKeepsTerminalConfirmationAfterBatchResult(t *testing.T) {
	f, current, _ := poolFixture(t, 4)
	for _, b := range f.engine.Members.Take(100, time.Now()) {
		if _, err := finishPoolBatch(t.Context(), f.engine, b); err != nil {
			t.Fatal(err)
		}
	}
	// 成员先回报 linked，随后主流程恢复它，终态检查先于组汇总确认 terminal。
	terminal := endChild(t, f.relationFixture, "a")
	r, err := f.journal.GetRelation(t.Context(), f.decision.TenantID, f.decision.ID)
	if err != nil {
		t.Fatal(err)
	}
	at := f.decision.FrozenAt.Add(5 * time.Second)
	r, err = f.journal.ConfirmRelationMember(t.Context(), r, terminal, at)
	if err != nil {
		t.Fatal(err)
	}
	if more, err := f.engine.stepMemberPool(t.Context(), current, r, f.parent, at.Add(time.Second)); err != nil || !more {
		t.Fatal("stale linked result blocked stage", more, err)
	}
	r, err = f.journal.GetRelation(t.Context(), f.decision.TenantID, f.decision.ID)
	if err != nil || r.Relation.Members[0].State != "terminal" {
		t.Fatal("terminal confirmation overwritten", err)
	}
	if _, err := f.journal.LinkStep(t.Context(), f.decision.TenantID, f.decision.ID, f.parent, f.processor, at.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(f.action.inputs) != 1 {
		t.Fatal("remaining active members did not admit parent once")
	}
}

func TestMemberPoolParentCloseCancelsRemainingLinksAndUnlinksEveryMember(t *testing.T) {
	f, _, _ := poolFixture(t, 6)
	first := f.engine.Members.Take(1, time.Now())[0]
	if _, err := finishPoolBatch(t.Context(), f.engine, first); err != nil {
		t.Fatal(err)
	}
	parent := closeRelationParent(t, f.relationFixture)
	for _, b := range f.engine.Members.Take(100, time.Now()) {
		if _, err := finishPoolBatch(t.Context(), f.engine, b); err != nil {
			t.Fatal(err)
		}
	}
	at := f.decision.FrozenAt.Add(5 * time.Second)
	for range 3 {
		current, err := f.journal.Get(t.Context(), f.decision.TenantID, f.decision.ID)
		if err != nil {
			t.Fatal(err)
		}
		relation, err := f.journal.GetRelation(t.Context(), f.decision.TenantID, f.decision.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.engine.stepMemberPool(t.Context(), current, relation, parent, at); err != nil {
			t.Fatal(err)
		}
	}
	firstCleanup := f.engine.Members.Take(1, time.Now())[0]
	if _, err := finishPoolBatch(t.Context(), f.engine, firstCleanup); err != nil {
		t.Fatal(err)
	}
	cleanedID := firstCleanup.Members[0].AlertID
	beforeReplay, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, cleanedID)
	if err != nil {
		t.Fatal(err)
	}
	// 解联中途丢掉内存批次，重新派发整个阶段仍不得重复修改已经清理的成员。
	f.engine.Members = NewMemberPool()
	if _, err := f.engine.CheckRelationBatch(t.Context(), f.decision.TenantID, f.decision.ID, at); err != nil {
		t.Fatal(err)
	}
	for _, b := range f.engine.Members.Take(100, time.Now()) {
		if _, err := finishPoolBatch(t.Context(), f.engine, b); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.engine.CheckRelationBatch(t.Context(), f.decision.TenantID, f.decision.ID, at); err != nil {
		t.Fatal(err)
	}
	ended, err := f.journal.GetRelation(t.Context(), f.decision.TenantID, f.decision.ID)
	if err != nil || ended.Relation.State != "ended" || ended.Relation.EndOffset != len(ended.Relation.WaitMemberIDs) {
		t.Fatal("cleanup incomplete", ended.Relation.State, err)
	}
	for _, m := range ended.Relation.Members {
		a, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, m.AlertID)
		if err != nil {
			t.Fatal(err)
		}
		if a.Alert.Merge != nil && (slices.Contains(a.Alert.Merge.RelationIDs, f.decision.ID) || len(a.Alert.Merge.Pending) > 0) {
			t.Fatal("closed parent kept on member", m.AlertID)
		}
	}
	if len(f.action.inputs) != 0 {
		t.Fatal("ending relation triggered child or parent action")
	}
	afterReplay, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, cleanedID)
	if err != nil || afterReplay.Version != beforeReplay.Version {
		t.Fatal("cleanup replay rewrote completed member", err)
	}
}
