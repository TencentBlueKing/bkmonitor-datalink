// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// memoryEvidenceStore is the mark, in memory, with the same rules the Redis one
// holds: a lifetime that has already passed writes nothing, and members outside
// the Slot's due set do not count.
type memoryEvidenceStore struct {
	mutex sync.Mutex
	marks map[execution.SlotIdentity]map[execution.PlanIdentity]struct{}
	// readErr makes the read fail, which is UNREADABLE and not "nothing there".
	readErr error
	// writeErr makes the write fail, which must not change what the Slot
	// reports to the scheduler.
	writeErr error
	writes   int
}

func newMemoryEvidenceStore() *memoryEvidenceStore {
	return &memoryEvidenceStore{marks: map[execution.SlotIdentity]map[execution.PlanIdentity]struct{}{}}
}

func (store *memoryEvidenceStore) Record(
	_ context.Context, slot execution.SlotIdentity, _ []execution.PlanIdentity,
	applied []execution.PlanIdentity, recoveryUntil time.Time, now time.Time,
) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.writes++
	if store.writeErr != nil {
		return store.writeErr
	}
	if !recoveryUntil.After(now) {
		return nil
	}
	marked := store.marks[slot]
	if marked == nil {
		marked = map[execution.PlanIdentity]struct{}{}
		store.marks[slot] = marked
	}
	for _, plan := range applied {
		marked[plan] = struct{}{}
	}
	return nil
}

func (store *memoryEvidenceStore) Read(
	_ context.Context, slot execution.SlotIdentity, duePlans []execution.PlanIdentity,
) (execution.ExecutionEvidence, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	evidence := execution.ExecutionEvidence{Kind: execution.EvidenceNoneFound, PlansTotal: len(duePlans)}
	if store.readErr != nil {
		return evidence, store.readErr
	}
	marked := store.marks[slot]
	applied := 0
	for _, plan := range duePlans {
		if _, found := marked[plan]; found {
			applied++
		}
	}
	if applied == 0 {
		return evidence, nil
	}
	evidence.Kind, evidence.PlansApplied = execution.EvidenceStateApplied, applied
	return evidence, nil
}

func (store *memoryEvidenceStore) markedPlans(slot execution.SlotIdentity) int {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	return len(store.marks[slot])
}

// liveSlotRequest is the fixture request with its windows placed around now, so
// the Slot is one that can still be finalized. The mark's lifetime ends at
// RecoveryUntil, so a request whose windows sit in the past writes no mark --
// correctly, and it would make this test pass for the wrong reason.
func liveSlotRequest(contractRef execution.FrozenExecutionContractRef) execution.SlotExecutionRequest {
	request := workerSlotRequest(contractRef)
	now := time.Now()
	request.EarliestQueryDeadlineUnixMilli = now.Add(time.Minute).UnixMilli()
	request.RecoveryUntilUnixMilli = now.Add(10 * time.Minute).UnixMilli()
	request.KeepUntilUnixMilli = now.Add(20 * time.Minute).UnixMilli()
	return request
}

// CX-02: a Slot whose state was written and whose bookkeeping was not is not a
// Slot that was never evaluated.
//
// The first attempt queries, evaluates, sends its events, writes its state, and
// then fails to write Progress. The Slot is therefore unfinished, and by the
// time anything retries it the replay window has passed, so it finishes without
// querying -- holding nothing but its frozen due Plans, and with no way to know
// any of that happened. It used to record a gap: "永久没检测", on a Slot that
// detected and alerted.
func TestASlotThatWroteStateAndLostItsBookkeepingIsNotAGap(t *testing.T) {
	header, batches := workerG4StreamFixture(t, strategy.DetectorKindSimpleRingRatio)
	ports, _, coordinator := workerG4CoordinatorWithEvidence(t, newMemoryEvidenceStore())
	evidence := ports.evidence
	ports.executeOverride = streamExecution(header, batches, fullCompletion(t, header, batches))
	request := liveSlotRequest(header.Contract)

	// The first attempt: everything works until the bookkeeping.
	ports.failStage = "progress_commit"
	if _, err := coordinator.Execute(context.Background(), request); err == nil {
		t.Fatal("the fixture did not fail the Progress commit; this test needs an attempt that wrote " +
			"state and then could not write the Slot down")
	}
	if marked := evidence.markedPlans(header.Contract.Slot); marked != 1 {
		t.Fatalf("marked %d Plans, want the one this attempt applied: without the mark the second "+
			"attempt has nothing to find", marked)
	}

	// The retry, past the replay window, finishing without a query.
	ports.failStage = ""
	ports.finalizationMode = execution.FinalizationGapSkipped
	retry := request
	retry.ReplayExpired = true
	retry.AttemptNo = 2
	result, err := coordinator.Execute(context.Background(), retry)
	if err != nil {
		t.Fatal(err)
	}
	if result.CompletionKind != execution.CompletionGapSkipped {
		t.Fatalf("completion = %q, want GAP_SKIPPED: the Slot did miss its window", result.CompletionKind)
	}
	completion := ports.lastProgress.Completion
	if completion.Evidence == nil {
		t.Fatal("the query-free completion carried no evidence; it cannot tell an evaluated Slot from " +
			"one that never ran")
	}
	want := execution.ExecutionEvidence{Kind: execution.EvidenceStateApplied, PlansApplied: 1, PlansTotal: 1}
	if *completion.Evidence != want {
		t.Fatalf("evidence = %+v, want %+v", *completion.Evidence, want)
	}
	if !completion.Evidence.FullyApplied() {
		t.Fatal("every due Plan was applied and the evidence does not say so, so the gap fold will " +
			"still open a gap")
	}
}

// An attempt that wrote its Slot down leaves nothing behind.
//
// The zero cost of the normal path is a property to hold, not a description of
// the code: the mark exists to answer a question only an unfinished attempt can
// raise, and one left by a finished Slot is a key nobody will ever read.
func TestAnAttemptThatCommittedItsProgressWritesNoMark(t *testing.T) {
	header, batches := workerG4StreamFixture(t, strategy.DetectorKindSimpleRingRatio)
	ports, _, coordinator := workerG4CoordinatorWithEvidence(t, newMemoryEvidenceStore())
	evidence := ports.evidence
	ports.executeOverride = streamExecution(header, batches, fullCompletion(t, header, batches))

	if _, err := coordinator.Execute(context.Background(), liveSlotRequest(header.Contract)); err != nil {
		t.Fatal(err)
	}
	if evidence.writes != 0 {
		t.Fatalf("a Slot that committed its Progress wrote %d marks, want none: the normal path is "+
			"meant to cost nothing", evidence.writes)
	}
}

// A mark that could not be written does not change what the Slot reports.
//
// The mark is bookkeeping about bookkeeping. Letting it decide whether a Slot
// retries would put the least important write in the chain in charge of the
// most important decision.
func TestAMarkThatCouldNotBeWrittenDoesNotChangeTheSlotsAnswer(t *testing.T) {
	header, batches := workerG4StreamFixture(t, strategy.DetectorKindSimpleRingRatio)
	store := newMemoryEvidenceStore()
	store.writeErr = errors.New("the store did not answer")
	ports, _, coordinator := workerG4CoordinatorWithEvidence(t, store)
	ports.executeOverride = streamExecution(header, batches, fullCompletion(t, header, batches))
	ports.failStage = "progress_commit"

	_, err := coordinator.Execute(context.Background(), liveSlotRequest(header.Contract))
	if err == nil {
		t.Fatal("the fixture did not fail the Progress commit")
	}
	if store.writes != 1 {
		t.Fatalf("the mark was attempted %d times, want once", store.writes)
	}
	// The error is the Progress commit's, not the mark's.
	if got := err.Error(); !contains(got, "progress") {
		t.Fatalf("error = %q, want the Progress failure rather than the mark's", got)
	}
}

// A mark that could not be read is UNREADABLE, and the Slot still finishes.
//
// Not "nothing there": that reading would record a gap on a Slot nobody can
// speak for, and holding the Slot open instead would turn a reading into a
// dependency for a Slot that has already missed its window.
func TestAMarkThatCouldNotBeReadIsUnreadableAndTheSlotStillFinishes(t *testing.T) {
	header, _ := workerG4StreamFixture(t, strategy.DetectorKindSimpleRingRatio)
	store := newMemoryEvidenceStore()
	store.readErr = errors.New("the store did not answer")
	ports, _, coordinator := workerG4CoordinatorWithEvidence(t, store)
	ports.finalizationMode = execution.FinalizationGapSkipped
	request := liveSlotRequest(header.Contract)
	request.ReplayExpired = true

	result, err := coordinator.Execute(context.Background(), request)
	if err != nil {
		t.Fatalf("a Slot that could not read the mark did not finish: %v", err)
	}
	if result.CompletionKind != execution.CompletionGapSkipped {
		t.Fatalf("completion = %q, want GAP_SKIPPED", result.CompletionKind)
	}
	completion := ports.lastProgress.Completion
	if completion.Evidence == nil || completion.Evidence.Kind != execution.EvidenceUnreadable {
		t.Fatalf("evidence = %+v, want UNREADABLE: a read that failed is not a Slot that never ran",
			completion.Evidence)
	}
	if completion.Evidence.FullyApplied() {
		t.Fatal("an unreadable mark reads as fully applied, so the gap would be dropped on no evidence")
	}
}

// fullCompletion is every query of the fixture arriving complete, which is the
// ordinary path this test needs before the bookkeeping fails.
func fullCompletion(
	t *testing.T, header execution.InternalExecutionHeader, batches []execution.SeriesExecutionBatch,
) execution.QueryExecutionCompletion {
	t.Helper()
	completion := execution.QueryExecutionCompletion{AllRequiredCompleted: true}
	for _, batch := range batches {
		completion.PhysicalQueries = append(completion.PhysicalQueries, execution.PhysicalQueryCompletion{
			Ref: batch.CompletionRef, PhysicalQuery: batch.PhysicalQuery, QueryRevision: batch.QueryRevision,
			Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Delivery: batch.Delivery,
		})
	}
	completion.CompletionBindings = accessShapedCompletionBindings(t, header, completion.PhysicalQueries)
	return completion
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return index
		}
	}
	return -1
}

// A retry whose state writes all say "already applied" leaves no mark.
//
// Already-applied is an earlier attempt's work. That attempt, if it also failed
// to write its Slot down, left its own mark; if it did not, the Slot is
// finished. Either way this attempt did nothing to record, and counting those
// Plans here would let it claim work it did not do -- against a total that
// decides whether the Slot still owes a gap, so the inflation is exactly what
// would stop a partly executed Slot reporting one.
func TestAnAttemptThatOnlyFoundStateAlreadyAppliedLeavesNoMark(t *testing.T) {
	header, batches := workerG4StreamFixture(t, strategy.DetectorKindSimpleRingRatio)
	store := newMemoryEvidenceStore()
	ports, _, coordinator := workerG4CoordinatorWithEvidence(t, store)
	ports.executeOverride = streamExecution(header, batches, fullCompletion(t, header, batches))
	ports.stateApplyAlreadyApplied = true
	ports.failStage = "progress_commit"

	if _, err := coordinator.Execute(context.Background(), liveSlotRequest(header.Contract)); err == nil {
		t.Fatal("the fixture did not fail the Progress commit")
	}
	if store.writes != 0 {
		t.Fatalf("an attempt whose state was already applied wrote %d marks, want none: those Plans "+
			"are an earlier attempt's, and claiming them here inflates the count the gap fold "+
			"compares against the whole due set", store.writes)
	}
}

// Both query-free modes read the mark.
//
// SNAPSHOT_UNAVAILABLE is query-free for a different reason than GAP_SKIPPED,
// and it is just as able to be the second half of an attempt that evaluated,
// alerted and then lost its bookkeeping. Reading the mark on one and not the
// other would leave that Slot recording a gap it does not owe, for a reason
// nothing about it would explain.
func TestASnapshotUnavailableFinalizationAlsoReadsTheMark(t *testing.T) {
	header, batches := workerG4StreamFixture(t, strategy.DetectorKindSimpleRingRatio)
	ports, _, coordinator := workerG4CoordinatorWithEvidence(t, newMemoryEvidenceStore())
	ports.executeOverride = streamExecution(header, batches, fullCompletion(t, header, batches))
	request := liveSlotRequest(header.Contract)

	ports.failStage = "progress_commit"
	if _, err := coordinator.Execute(context.Background(), request); err == nil {
		t.Fatal("the fixture did not fail the Progress commit")
	}

	ports.failStage = ""
	ports.finalizationMode = execution.FinalizationSnapshotUnavailable
	retry := request
	retry.ReplayExpired = true
	retry.AttemptNo = 2
	result, err := coordinator.Execute(context.Background(), retry)
	if err != nil {
		t.Fatal(err)
	}
	if result.CompletionKind != execution.CompletionSnapshotUnavailable {
		t.Fatalf("completion = %q, want SNAPSHOT_UNAVAILABLE", result.CompletionKind)
	}
	completion := ports.lastProgress.Completion
	if completion.Evidence == nil {
		t.Fatal("a SNAPSHOT_UNAVAILABLE completion carried no evidence; it is query-free too, and the " +
			"Slot behind it can have been evaluated just the same")
	}
	want := execution.ExecutionEvidence{Kind: execution.EvidenceStateApplied, PlansApplied: 1, PlansTotal: 1}
	if *completion.Evidence != want {
		t.Fatalf("evidence = %+v, want %+v", *completion.Evidence, want)
	}
}
