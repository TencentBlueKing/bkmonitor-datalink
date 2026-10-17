// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package merge

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

func TestMemberDeadlineClassificationPreservesMultipleCauses(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "plain", err: context.DeadlineExceeded, want: true},
		{name: "single wrapped cause", err: fmt.Errorf("operation: %w", errors.Join(context.DeadlineExceeded, nil)), want: true},
		{name: "two deadlines", err: errors.Join(context.DeadlineExceeded, context.DeadlineExceeded)},
		{name: "nested double deadline", err: fmt.Errorf("release: %w", errors.Join(context.DeadlineExceeded, fmt.Errorf("operation: %w", context.DeadlineExceeded)))},
		{name: "cancelled", err: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := memberDeadlineOnly(test.err); got != test.want {
				t.Fatal("incorrect deadline classification", got, test.err)
			}
		})
	}
}

func TestLinkMemberBudgetSavesPrefixAndKeepsRealFailures(t *testing.T) {
	for _, test := range []struct {
		name    string
		failure error
		early   bool
	}{
		{name: "work deadline yields"},
		{name: "unlock uncertainty", failure: errors.New("lease release uncertain")},
		{name: "operation and unlock both time out", failure: context.DeadlineExceeded},
		{name: "earlier dependency timeout", failure: context.DeadlineExceeded, early: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, current := linkBatchFixture(t, 1, 0)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second+100*time.Millisecond)
			defer cancel()
			ops := linkBatchOperator{RelationOperator: f.processor, link: func(call context.Context, tenant, id, relation string) (store.StoredAlert, error) {
				if id == "a" {
					return f.processor.LinkMergeMember(call, tenant, id, relation)
				}
				if test.early && id == "b" {
					if call.Err() != nil {
						t.Error("dependency timeout must precede the work deadline")
					}
					return store.StoredAlert{}, test.failure
				}
				<-call.Done()
				return store.StoredAlert{}, errors.Join(call.Err(), test.failure)
			}}
			next, err := f.journal.LinkMembersBatch(ctx, current, f.parent, ops, f.decision.FrozenAt.Add(3*time.Second), 128, 1)
			if ctx.Err() != nil || next.Decision.Progress.MemberOffset != 1 {
				t.Fatal("work deadline consumed the progress-write budget", next.Decision.Progress.MemberOffset, ctx.Err(), err)
			}
			if (test.failure == nil && err != nil) || (test.failure != nil && !errors.Is(err, test.failure)) {
				t.Fatal("work deadline hid a real failure or was reported as failure", err)
			}
			actual, readErr := f.repo.GetAlert(t.Context(), f.decision.TenantID, "a")
			if readErr != nil || actual.Alert.Merge.State != "merged" || actual.Alert.MergeChange != nil {
				t.Fatal("successful prefix does not match durable member state", readErr)
			}
			outputs := len(f.relationFixture.state.inputs)
			done, err := f.journal.LinkMembersBatch(t.Context(), next, f.parent, f.processor, f.decision.FrozenAt.Add(4*time.Second), 128, 1)
			if err != nil || done.Decision.Progress.MemberOffset != len(current.Decision.WaitMemberIDs) || len(f.relationFixture.state.inputs) != outputs+2 || len(f.action.inputs) != 0 {
				t.Fatal("retry replayed the committed prefix or failed to finish", done.Decision.Progress.MemberOffset, outputs, len(f.relationFixture.state.inputs), len(f.action.inputs), err)
			}
		})
	}
}

func TestEndMemberBudgetSavesPrefixAndRetriesWithoutDuplicateOutput(t *testing.T) {
	for _, test := range []struct {
		name    string
		failure error
	}{
		{name: "work deadline yields"},
		{name: "unlock uncertainty", failure: errors.New("unlink lease release uncertain")},
		{name: "operation and unlock both time out", failure: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := endingBatchFixture(t, 0)
			closeRelationParent(t, f.relationFixture)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second+100*time.Millisecond)
			defer cancel()
			ops := endingBatchOperator{RelationEndOperator: f.processor, unlink: func(call context.Context, tenant, id, relation string) (store.StoredAlert, error) {
				if id == "a" {
					return f.processor.UnlinkMergeMember(call, tenant, id, relation)
				}
				<-call.Done()
				return store.StoredAlert{}, errors.Join(call.Err(), test.failure)
			}}
			partial, err := f.journal.EndRelationBatchWithBudget(ctx, f.decision.TenantID, f.decision.ID, ops, f.decision.FrozenAt.Add(4*time.Second), 128, 1)
			if ctx.Err() != nil || partial.Relation.EndOffset != 1 || partial.Relation.State != "ending" {
				t.Fatal("unlink work deadline lost successful prefix", partial.Relation.EndOffset, err)
			}
			if (test.failure == nil && err != nil) || (test.failure != nil && !errors.Is(err, test.failure)) {
				t.Fatal("unlink mixed failure or voluntary yield misclassified", err)
			}
			states, actions := len(f.relationFixture.state.inputs), len(f.action.inputs)
			ended, err := f.journal.EndRelationBatchWithBudget(t.Context(), f.decision.TenantID, f.decision.ID, f.processor, f.decision.FrozenAt.Add(5*time.Second), 128, 1)
			if err != nil || ended.Relation.State != "ended" || ended.Relation.EndOffset != len(partial.Relation.WaitMemberIDs) || len(f.relationFixture.state.inputs) != states+1 || len(f.action.inputs) != actions {
				t.Fatal("unlink retry replayed completed member output", ended.Relation.State, ended.Relation.EndOffset, states, len(f.relationFixture.state.inputs), actions, len(f.action.inputs), err)
			}
		})
	}
}

func TestMemberBudgetDoesNotStartWithOnlyCheckpointTimeRemaining(t *testing.T) {
	t.Run("link", func(t *testing.T) {
		f, current := linkBatchFixture(t, 0, 0)
		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		defer cancel()
		ops := linkBatchOperator{RelationOperator: f.processor, link: func(context.Context, string, string, string) (store.StoredAlert, error) {
			t.Error("member started without checkpoint budget")
			return store.StoredAlert{}, nil
		}}
		next, err := f.journal.LinkMembersBatch(ctx, current, f.parent, ops, f.decision.FrozenAt.Add(3*time.Second), 128, 8)
		if err != nil || next.Version != current.Version {
			t.Fatal("insufficient budget changed link progress", err)
		}
	})
	t.Run("end", func(t *testing.T) {
		f := endingBatchFixture(t, 0)
		closeRelationParent(t, f.relationFixture)
		before := endingBatchRelation(t, f)
		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		defer cancel()
		ops := endingBatchOperator{RelationEndOperator: f.processor, recover: func(context.Context, string, string, string) (store.StoredAlert, error) {
			t.Error("parent work started without checkpoint budget")
			return store.StoredAlert{}, nil
		}}
		next, err := f.journal.EndRelationBatchWithBudget(ctx, f.decision.TenantID, f.decision.ID, ops, f.decision.FrozenAt.Add(4*time.Second), 128, 8)
		if err != nil || next.Version != before.Version {
			t.Fatal("insufficient budget changed end progress", err)
		}
	})
}

func TestMemberBudgetNeverHidesParentCancellation(t *testing.T) {
	f, current := linkBatchFixture(t, 0, 0)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ops := linkBatchOperator{RelationOperator: f.processor, link: func(call context.Context, tenant, id, relation string) (store.StoredAlert, error) {
		if id == "a" {
			return f.processor.LinkMergeMember(call, tenant, id, relation)
		}
		cancel()
		return store.StoredAlert{}, call.Err()
	}}
	if _, err := f.journal.LinkMembersBatch(ctx, current, f.parent, ops, f.decision.FrozenAt.Add(3*time.Second), 128, 1); !errors.Is(err, context.Canceled) {
		t.Fatal("parent cancellation became a successful yield", err)
	}
	saved, err := f.journal.Get(t.Context(), f.decision.TenantID, f.decision.ID)
	if err != nil || saved.Decision.Progress.MemberOffset != 0 {
		t.Fatal("canceled parent wrote a progress checkpoint", err)
	}
	outputs := len(f.relationFixture.state.inputs)
	done, err := f.journal.LinkMembersBatch(t.Context(), saved, f.parent, f.processor, f.decision.FrozenAt.Add(4*time.Second), 128, 1)
	if err != nil || done.Decision.Progress.MemberOffset != len(current.Decision.WaitMemberIDs) || len(f.relationFixture.state.inputs) != outputs+1 {
		t.Fatal("retry after parent cancellation duplicated committed output", done.Decision.Progress.MemberOffset, outputs, len(f.relationFixture.state.inputs), err)
	}
}

type sharedSlowMemberOperator struct {
	RelationOperator
	prepared preparedRelationLinker
	slots    chan struct{}
	active   *atomic.Int32
	maximum  *atomic.Int32
}

func (o sharedSlowMemberOperator) LinkMergeMemberPrepared(ctx context.Context, tenant, id string, relation domain.MergeRelation) (store.StoredAlert, error) {
	select {
	case o.slots <- struct{}{}:
		defer func() { <-o.slots }()
	case <-ctx.Done():
		return store.StoredAlert{}, ctx.Err()
	}
	n := o.active.Add(1)
	defer o.active.Add(-1)
	for old := o.maximum.Load(); n > old && !o.maximum.CompareAndSwap(old, n); old = o.maximum.Load() {
	}
	timer := time.NewTimer(5 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return o.prepared.LinkMergeMemberPrepared(ctx, tenant, id, relation)
	case <-ctx.Done():
		return store.StoredAlert{}, ctx.Err()
	}
}

func TestEightWindowsShareEightSlowMemberSlotsAndKeepCheckpointing(t *testing.T) {
	const windows, members = 8, 128
	type window struct {
		journal *Journal
		current StoredDecision
		parent  store.StoredAlert
		ops     sharedSlowMemberOperator
	}
	slots := make(chan struct{}, 8)
	var active, maximum atomic.Int32
	jobs := make([]window, windows)
	for i := range jobs {
		journal, current, parent, ops, _ := scaleLinkFixture(t, members)
		jobs[i] = window{journal: journal, current: current, parent: parent, ops: sharedSlowMemberOperator{RelationOperator: ops, prepared: ops, slots: slots, active: &active, maximum: &maximum}}
	}
	partial := false
	for round := range 100 {
		var workers sync.WaitGroup
		failures := make([]error, windows)
		for i := range jobs {
			if jobs[i].current.Decision.Progress.MemberOffset == members {
				continue
			}
			workers.Go(func() {
				job := &jobs[i]
				// 缩短成员工作阶段到80ms；仍保留真实的一秒CAS余量，模拟共享槽排队吃满批预算。
				ctx, cancel := context.WithTimeout(t.Context(), time.Second+80*time.Millisecond)
				defer cancel()
				next, err := job.journal.LinkMembersBatch(ctx, job.current, job.parent, job.ops, job.current.Decision.FrozenAt.Add(time.Duration(round+1)*time.Second), members, 8)
				if err == nil && next.Decision.Progress.MemberOffset < job.current.Decision.Progress.MemberOffset {
					err = errors.New("member checkpoint moved backward")
				}
				failures[i] = err
				if err == nil {
					job.current = next
				}
			})
		}
		workers.Wait()
		total := 0
		for i, job := range jobs {
			if failures[i] != nil {
				t.Fatal("shared slow slots lost their checkpoint budget", i, failures[i])
			}
			total += job.current.Decision.Progress.MemberOffset
		}
		if total > 0 && total < windows*members {
			partial = true
		}
		if total == windows*members {
			if !partial || maximum.Load() != 8 || active.Load() != 0 || len(slots) != 0 {
				t.Fatal("shared budget, partial progress, or cleanup was not exercised", partial, maximum.Load())
			}
			return
		}
	}
	t.Fatal("retries kept returning to the same unfinished member prefix")
}
