// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package merge

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/policy"
	policyruntime "linkd/internal/policy/runtime"
	"linkd/internal/store"
	"testing/synctest"
)

type lostWaitBatchOperations struct {
	Operations
	fail  string
	slow  bool
	calls []string
}

func (o *lostWaitBatchOperations) ReleaseMergeWindowWithReason(ctx context.Context, tenant, id, window, reason string) (store.StoredAlert, error) {
	o.calls = append(o.calls, id)
	if id == o.fail {
		if o.slow {
			<-ctx.Done()
			return store.StoredAlert{}, ctx.Err()
		}
		return store.StoredAlert{}, errors.New("member temporarily unavailable")
	}
	return o.Operations.ReleaseMergeWindowWithReason(ctx, tenant, id, window, reason)
}

func TestCheckWorkBatchSlowMemberDoesNotConsumeWholeJob(t *testing.T) {
	for _, phase := range []string{"read", "release"} {
		t.Run(phase, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newExecutorFixture(t)
				f.state.result.Lost, f.state.result.Frozen = true, false
				items := []store.MergeWorkItem{}
				for _, member := range f.state.result.Members {
					items = append(items, store.MergeWorkItem{Alert: member, WindowID: f.decision.WindowID})
				}
				first := items[0].Alert.Alert.AlertID
				if phase == "read" {
					read := f.engine.CurrentAlert
					f.engine.CurrentAlert = func(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
						if id == first {
							<-ctx.Done()
							return store.StoredAlert{}, ctx.Err()
						}
						return read(ctx, tenant, id)
					}
				} else {
					f.engine.Operations = &lostWaitBatchOperations{Operations: f.engine.Operations, fail: first, slow: true}
				}
				var healthyVersion store.VersionToken
				for round := range 3 {
					ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
					started := time.Now()
					err := f.engine.CheckWorkBatch(ctx, items, f.decision.Deadline.Add(time.Duration(round)*time.Second))
					parentErr := ctx.Err()
					cancel()
					if !errors.Is(err, context.DeadlineExceeded) || parentErr != nil || time.Since(started) != 3*time.Second {
						t.Fatalf("slow member consumed job: round=%d elapsed=%s error=%v parent=%v", round, time.Since(started), err, parentErr)
					}
					current, err := f.repo.GetAlert(t.Context(), items[1].Alert.Alert.BKTenantID, items[1].Alert.Alert.AlertID)
					if err != nil || (current.Alert.Merge != nil && len(current.Alert.Merge.Pending) != 0) {
						t.Fatal("healthy member stayed pending", err)
					}
					if round > 0 && current.Version != healthyVersion {
						t.Fatal("healthy member was repeated")
					}
					healthyVersion = current.Version
				}
				// 父取消优先于子操作超时，不能继续执行下一成员。
				read := f.engine.CurrentAlert
				reads := 0
				f.engine.CurrentAlert = func(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
					reads++
					return read(ctx, tenant, id)
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				timer := time.AfterFunc(time.Second, cancel)
				defer timer.Stop()
				if err := f.engine.CheckWorkBatch(ctx, items, f.decision.Deadline); !errors.Is(err, context.Canceled) || reads != 1 {
					t.Fatal("parent cancellation did not stop members", err, reads)
				}
			})
		})
	}
}

type delayedWindowJudge struct{ WindowJudge }

func (j delayedWindowJudge) Evaluate(ctx context.Context, tenant, window string, at time.Time, level func(string) (string, error)) (policyruntime.MergeJudgment, error) {
	select {
	case <-ctx.Done():
		return policyruntime.MergeJudgment{}, ctx.Err()
	case <-time.After(4 * time.Second):
		return j.WindowJudge.Evaluate(ctx, tenant, window, at, level)
	}
}

func TestCheckWorkBatchKeepsWholeWindowBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newExecutorFixture(t)
		f.state.result.Frozen = false
		f.engine.Judge = delayedWindowJudge{WindowJudge: f.engine.Judge}
		items := []store.MergeWorkItem{}
		for _, member := range f.state.result.Members {
			items = append(items, store.MergeWorkItem{Alert: member, WindowID: f.decision.WindowID})
		}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		if err := f.engine.CheckWorkBatch(ctx, items, f.decision.Deadline); err != nil || f.state.judgments != 1 {
			t.Fatal("member timeout shortened/repeated window judgment", err, f.state.judgments)
		}
	})
}

func TestCheckWorkBatchSeparatesWindowJudgmentAndMemberCompensation(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "live-window", true: "lost-window"}[lost], func(t *testing.T) {
			f := newExecutorFixture(t)
			f.state.result.Lost, f.state.result.Frozen = lost, false
			items := []store.MergeWorkItem{}
			for _, member := range f.state.result.Members {
				items = append(items, store.MergeWorkItem{Alert: member, WindowID: f.decision.WindowID})
			}
			if len(items) < 2 {
				t.Fatal("fixture needs multiple members")
			}
			ops := &lostWaitBatchOperations{Operations: f.engine.Operations, fail: items[0].Alert.Alert.AlertID}
			f.engine.Operations = ops
			err := f.engine.CheckWorkBatch(t.Context(), items, f.decision.Deadline)
			if !lost {
				if err != nil || f.state.judgments != 1 || len(ops.calls) != 0 {
					t.Fatal("live window repeated judgment", err, f.state.judgments)
				}
				return
			}
			if err == nil || len(ops.calls) != len(items) {
				t.Fatalf("failed member starved batch: calls=%d error=%v", len(ops.calls), err)
			}
			for _, item := range items[1:] {
				current, err := f.repo.GetAlert(t.Context(), item.Alert.Alert.BKTenantID, item.Alert.Alert.AlertID)
				if err != nil || (current.Alert.Merge != nil && len(current.Alert.Merge.Pending) != 0) {
					t.Fatal("later member not released", err)
				}
			}
			ops.fail = ""
			if err := f.engine.CheckWorkBatch(t.Context(), items, f.decision.Deadline.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if len(ops.calls) != len(items)+1 {
				t.Fatal("retry repeated completed member")
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := f.engine.CheckWorkBatch(ctx, items, f.decision.Deadline); !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation ignored", err)
			}
		})
	}
}

func executorBatchAtPhase(t *testing.T, phase string) (executorFixture, StoredDecision) {
	t.Helper()
	f := newExecutorFixture(t)
	// 此夹具的录制 Hook 是顺序替身；成员并发另由 LinkMembersBatch 的并发用例覆盖。
	f.engine.Batch = config.MergeControlConfig{MemberBatchSize: 128, Concurrency: 1}
	startExecutor(t, f)
	for range 20 {
		current, err := f.journal.Get(t.Context(), f.decision.TenantID, f.decision.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Decision.Progress.Phase == phase {
			return f, current
		}
		if current.Decision.Progress.Phase == "waiting_parent" {
			event, err := f.repo.GetEvent(t.Context(), f.decision.TenantID, current.Decision.Progress.ParentEvent.EventID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.processor.ProcessEvent(t.Context(), event); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := f.engine.StepDecisionBatch(t.Context(), f.decision.TenantID, f.decision.ID, f.decision.FrozenAt.Add(2*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatalf("executor did not reach phase %s", phase)
	return f, StoredDecision{}
}

func TestExecutorBatchWaitingParentDoesNotRequestContinuation(t *testing.T) {
	f, before := executorBatchAtPhase(t, "waiting_parent")
	for range 3 {
		more, err := f.engine.StepDecisionBatch(t.Context(), f.decision.TenantID, f.decision.ID, f.decision.FrozenAt.Add(3*time.Second))
		if err != nil || more {
			t.Fatal("unprocessed parent event requested a busy continuation", more, err)
		}
	}
	after, err := f.journal.Get(t.Context(), f.decision.TenantID, f.decision.ID)
	if err != nil || after.Version != before.Version || len(f.action.inputs) != 0 {
		t.Fatal("waiting changed progress or admitted a parent", err)
	}
}

func TestExecutorBatchReferenceProgressContinuesWithoutDecisionChange(t *testing.T) {
	f, before := executorBatchAtPhase(t, "linking")
	more, err := f.engine.StepDecisionBatch(t.Context(), f.decision.TenantID, f.decision.ID, f.decision.FrozenAt.Add(3*time.Second))
	if err != nil || !more {
		t.Fatal("reference batch progress was not reported", more, err)
	}
	after, err := f.journal.Get(t.Context(), f.decision.TenantID, f.decision.ID)
	if err != nil || after.Version != before.Version || after.Decision.Progress.MemberOffset != 0 {
		t.Fatal("reference-only step unexpectedly changed decision/member progress", err)
	}
	relation, err := f.journal.GetRelation(t.Context(), f.decision.TenantID, f.decision.ID)
	if err != nil || relation.Relation.IndexOffset != len(f.decision.MemberIDs)+1 || len(f.action.inputs) != 0 {
		t.Fatal("references incomplete or parent admitted before member linking", err)
	}
}

func TestExecutorBatchCompletesCreationAndAdmitsOnlyOneParentAction(t *testing.T) {
	f, _ := executorBatchAtPhase(t, "linking")
	completed := false
	for range 10 {
		more, err := f.engine.StepDecisionBatch(t.Context(), f.decision.TenantID, f.decision.ID, f.decision.FrozenAt.Add(3*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		current, err := f.journal.Get(t.Context(), f.decision.TenantID, f.decision.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Decision.Progress.WindowFinished {
			if more || current.Decision.Progress.Phase != "completed" {
				t.Fatal("finished decision still requested continuation")
			}
			completed = true
			break
		}
		if !more {
			t.Fatal("creation stopped before all persistent steps completed")
		}
	}
	if !completed || len(f.action.inputs) != 1 || f.action.inputs[0].Alert.EventSourceID != "builtin_alarm_merge" {
		t.Fatalf("creation did not produce one parent action: complete=%v actions=%d", completed, len(f.action.inputs))
	}
	parent := f.action.inputs[0].Alert
	if !parent.Merge.RelationsReady || !parent.AdmittedActiveMain() {
		t.Fatal("parent action preceded relationship readiness")
	}
	for _, id := range f.decision.MemberIDs {
		member, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, id)
		if err != nil || member.Alert.Merge == nil || member.Alert.Merge.State != "merged" || !slices.Contains(member.Alert.Merge.RelationIDs, f.decision.ID) || len(member.Alert.Merge.Pending) != 0 || member.Alert.MergeChange != nil || member.Alert.Admission.AdmittedAt != nil {
			t.Fatal("member not linked or improperly admitted", id, err)
		}
	}
	for range 3 {
		more, err := f.engine.StepDecisionBatch(t.Context(), f.decision.TenantID, f.decision.ID, f.decision.FrozenAt.Add(4*time.Second))
		if err != nil || more || len(f.action.inputs) != 1 || f.state.finishes != 1 {
			t.Fatal("completed creation repeated effects", more, err)
		}
	}
}

func TestExecutorBatchCaptures128WithoutSplittingIntoSmallerRequests(t *testing.T) {
	f := newExecutorFixture(t)
	f.engine.Batch = config.MergeControlConfig{MemberBatchSize: 128, Concurrency: 8}
	f.journal.docs = &countedBatchDocuments{memoryDocuments: f.docs}
	d := f.decision.Clone()
	d.MemberIDs, d.WaitMemberIDs = nil, nil
	for i := range 130 {
		id := fmt.Sprintf("member-%03d", i)
		d.MemberIDs = append(d.MemberIDs, id)
		d.WaitMemberIDs = append(d.WaitMemberIDs, id)
		a := f.state.result.Members[0].Alert.Clone()
		a.AlertID, a.Fingerprint = id, "fp-"+id
		a.TriggerEventID, a.LatestEventID = "event-"+id, "event-"+id
		a.Merge.Pending[0].MemberEventID = a.TriggerEventID
		if _, err := f.repo.CreateAlert(t.Context(), a); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.journal.Claim(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	var sizes []int
	f.engine.CurrentAlerts = func(ctx context.Context, tenant string, ids []string) (store.AlertBatch, error) {
		sizes = append(sizes, len(ids))
		if len(ids) > 128 {
			t.Errorf("current-alert request contains %d members", len(ids))
		}
		return f.repo.GetAlerts(ctx, tenant, ids)
	}
	f.engine.CurrentAlert = func(context.Context, string, string) (store.StoredAlert, error) {
		return store.StoredAlert{}, errors.New("capture fell back to per-alert reads")
	}
	for _, want := range []int{128, 130} {
		more, err := f.engine.StepDecisionBatch(t.Context(), d.TenantID, d.ID, d.FrozenAt.Add(time.Second))
		if err != nil || !more {
			t.Fatal("capture did not advance", more, err)
		}
		current, err := f.journal.Get(t.Context(), d.TenantID, d.ID)
		if err != nil || current.Decision.Progress.CaptureOffset != want || current.Decision.Progress.Phase != "capturing" {
			t.Fatal("capture exceeded configured block or lost progress", want, err)
		}
		if want == 128 {
			if _, err := f.journal.Snapshot(t.Context(), d.TenantID, d.ID, d.MemberIDs[128]); !errors.Is(err, policy.ErrNotFound) {
				t.Fatal("first capture exceeded 128-member batch", err)
			}
		}
	}
	if !slices.Equal(sizes, []int{128, 2}) {
		t.Fatal("unexpected current-alert request partition", sizes)
	}
	for _, id := range d.MemberIDs {
		if _, err := f.journal.Snapshot(t.Context(), d.TenantID, d.ID, id); err != nil {
			t.Fatal("captured progress has a missing snapshot", id, err)
		}
	}
}

func TestFreshClaimConfirmsAlreadyStoredSnapshotsWithoutReadingThemBack(t *testing.T) {
	f := newExecutorFixture(t)
	f.engine.Batch.MemberBatchSize = 1
	docs := &countedBatchDocuments{memoryDocuments: f.docs}
	f.journal.docs = docs
	if err := f.engine.claim(t.Context(), f.state.result, f.decision.FrozenAt); err != nil {
		t.Fatal(err)
	}
	current, err := f.journal.Get(t.Context(), f.decision.TenantID, f.decision.ID)
	if err != nil || current.Decision.Progress.CaptureOffset != len(f.state.result.Members) || docs.reads != 0 {
		t.Fatal("already stored first snapshots were re-read or left for serial progress", docs.reads, current.Decision.Progress.CaptureOffset, err)
	}
}
