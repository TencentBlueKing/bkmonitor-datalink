// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package comparator

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestCoverageDeadlineStartsAtAuthoritativeCommit(t *testing.T) {
	t.Parallel()

	run, clock := mustCoverageRun(t, testAssignments())
	inputPayload, input := testTriggerInput(t, "normal")
	inputID := input.DetectionOutcomes[0].InputID
	prepared, err := run.Prepare(StreamRecord{
		Epoch:     "run-1",
		Role:      StreamGo,
		Topic:     "go",
		Partition: 0,
		Offset:    10,
		Key:       mustPartitionKey(t, input),
		Value:     testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger),
	})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if _, _, err := run.Coverage("run-1", inputID); !errors.Is(err, ErrRecordInFlight) {
		t.Fatalf("Coverage(before commit) error = %v, want ErrRecordInFlight", err)
	}
	if _, err := run.CommitSucceeded(prepared); err != nil {
		t.Fatalf("CommitSucceeded() error = %v", err)
	}

	snapshot, ok, err := run.Coverage("run-1", inputID)
	if err != nil || !ok || snapshot.Phase != CoveragePending || !slices.Equal(snapshot.MissingRoles, []StreamRole{StreamInput, StreamPython}) {
		t.Fatalf("Coverage(pending) = %#v, ok=%v, error=%v", snapshot, ok, err)
	}
	clock.Advance(11 * time.Second)
	snapshot, ok, err = run.Coverage("run-1", inputID)
	if err != nil || !ok || snapshot.Phase != CoveragePending || !snapshot.DeadlineAt.IsZero() || !slices.Equal(snapshot.MissingRoles, []StreamRole{StreamInput, StreamPython}) {
		t.Fatalf("Coverage(orphan pending) = %#v, ok=%v, error=%v", snapshot, ok, err)
	}
	snapshots, err := run.SweepCoverage("run-1")
	if err != nil || len(snapshots) != 1 || snapshots[0].InputID != inputID || snapshots[0].Phase != CoveragePending {
		t.Fatalf("SweepCoverage() = %#v, error=%v", snapshots, err)
	}
	if err := run.FreezeBarrier("run-1", inputID, capturedBarrier(clock)); err == nil {
		t.Fatal("FreezeBarrier() accepted an orphan without TriggerInput")
	}
	if !run.Valid() {
		t.Fatal("an orphan barrier invalidated the Run")
	}
	commitStreamRecord(t, run, StreamRecord{Epoch: "run-1", Role: StreamInput, Topic: "input", Partition: 0, Offset: 20, Key: mustPartitionKey(t, input), Value: inputPayload})
	snapshot, ok, err = run.Coverage("run-1", inputID)
	if err != nil || !ok || snapshot.Phase != CoveragePending || !slices.Equal(snapshot.MissingRoles, []StreamRole{StreamPython}) {
		t.Fatalf("Coverage(after authoritative input) = %#v, ok=%v, error=%v", snapshot, ok, err)
	}
	if want := clock.Now().Add(10 * time.Second); !snapshot.DeadlineAt.Equal(want) {
		t.Fatalf("Coverage deadline = %s, want %s", snapshot.DeadlineAt, want)
	}
}

func TestCoverageDeadlineExcludesPrepareToCommitDelay(t *testing.T) {
	t.Parallel()

	run, clock := mustCoverageRun(t, testAssignments())
	inputPayload, input := testTriggerInput(t, "normal")
	inputID := input.DetectionOutcomes[0].InputID
	prepared, err := run.Prepare(StreamRecord{
		Epoch:     "run-1",
		Role:      StreamInput,
		Topic:     "input",
		Partition: 0,
		Offset:    20,
		Key:       mustPartitionKey(t, input),
		Value:     inputPayload,
	})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	// The audit and offset acknowledgements outlast the coverage timeout.
	clock.Advance(11 * time.Second)
	if _, err := run.CommitSucceeded(prepared); err != nil {
		t.Fatalf("CommitSucceeded() error = %v", err)
	}

	snapshot, ok, err := run.Coverage("run-1", inputID)
	if err != nil || !ok || snapshot.Phase != CoveragePending {
		t.Fatalf("Coverage(just acknowledged) = %#v, ok=%v, error=%v", snapshot, ok, err)
	}
	if want := clock.Now().Add(10 * time.Second); !snapshot.DeadlineAt.Equal(want) {
		t.Fatalf("Coverage deadline = %s, want %s", snapshot.DeadlineAt, want)
	}
}

func TestCoverageBarrierMarksMissingAndLateRecovery(t *testing.T) {
	t.Parallel()

	run, clock := mustCoverageRun(t, testAssignments())
	inputPayload, input := testTriggerInput(t, "anomalous")
	key := mustPartitionKey(t, input)
	inputID := input.DetectionOutcomes[0].InputID
	commitStreamRecord(t, run, StreamRecord{Epoch: "run-1", Role: StreamInput, Topic: "input", Partition: 0, Offset: 20, Key: key, Value: inputPayload})
	commitStreamRecord(t, run, StreamRecord{Epoch: "run-1", Role: StreamPython, Topic: "python", Partition: 0, Offset: 30, Key: key, Value: testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger)})

	barrier := capturedBarrier(clock, PartitionBarrier{Role: StreamGo, Topic: "go", Partition: 0, HighWater: 12})
	if err := run.FreezeBarrier("run-1", inputID, barrier); err == nil {
		t.Fatal("FreezeBarrier() succeeded before the pending deadline")
	}
	if !run.Valid() {
		t.Fatal("an early barrier invalidated the Run")
	}
	clock.Advance(11 * time.Second)
	if err := run.FreezeBarrier("run-1", inputID, barrier); err == nil {
		t.Fatal("FreezeBarrier() accepted a pre-deadline HWM snapshot after the deadline")
	}
	if !run.Valid() {
		t.Fatal("a pre-deadline HWM snapshot invalidated the Run")
	}
	snapshot, _, err := run.Coverage("run-1", inputID)
	if err != nil || snapshot.Phase != CoverageOverdue || snapshot.BarrierFrozen {
		t.Fatalf("Coverage(before freeze) = %#v, error=%v", snapshot, err)
	}
	barrier = capturedBarrier(clock, PartitionBarrier{Role: StreamGo, Topic: "go", Partition: 0, HighWater: 12})
	if err := run.FreezeBarrier("run-1", inputID, barrier); err != nil {
		t.Fatalf("FreezeBarrier() error = %v", err)
	}
	snapshot, _, err = run.Coverage("run-1", inputID)
	if err != nil || snapshot.Phase != CoverageOverdue || !snapshot.BarrierFrozen {
		t.Fatalf("Coverage(before HWM) = %#v, error=%v", snapshot, err)
	}

	_, unrelated := testTriggerInput(t, "normal")
	commitStreamRecord(t, run, StreamRecord{
		Epoch:     "run-1",
		Role:      StreamGo,
		Topic:     "go",
		Partition: 0,
		Offset:    11,
		Key:       mustPartitionKey(t, unrelated),
		Value:     testDecisionBatch(t, unrelated, contract.DecisionOutcomeNoTrigger),
	})
	snapshot, _, err = run.Coverage("run-1", inputID)
	if err != nil || snapshot.Phase != CoverageMissingAtBarrier || !slices.Equal(snapshot.MissingAtBarrierRoles, []StreamRole{StreamGo}) {
		t.Fatalf("Coverage(missing) = %#v, error=%v", snapshot, err)
	}
	assessment, ok, err := run.Assess("run-1", inputID, Gates{StableEpoch: true})
	if err != nil || !ok || assessment.Join != JoinPendingGo || assessment.Eligibility != EligibilityNone || assessment.Verdict != VerdictNone {
		t.Fatalf("Assess(missing) = %#v, ok=%v, error=%v", assessment, ok, err)
	}

	commitStreamRecord(t, run, StreamRecord{
		Epoch:     "run-1",
		Role:      StreamGo,
		Topic:     "go",
		Partition: 0,
		Offset:    12,
		Key:       key,
		Value:     testDecisionBatch(t, input, contract.DecisionOutcomeTrigger),
	})
	snapshot, _, err = run.Coverage("run-1", inputID)
	if err != nil || snapshot.Phase != CoverageComplete || len(snapshot.MissingRoles) != 0 || !slices.Equal(snapshot.LateRoles, []StreamRole{StreamGo}) {
		t.Fatalf("Coverage(late complete) = %#v, error=%v", snapshot, err)
	}
	if err := run.FreezeBarrier("run-1", inputID, barrier); err != nil {
		t.Fatalf("FreezeBarrier(idempotent) error = %v", err)
	}
	assessment, ok, err = run.Assess("run-1", inputID, Gates{
		StableEpoch:          true,
		EpochStartSourceTime: int64Pointer(input.DetectionOutcomes[0].Record.SourceTime - 299),
	})
	if err != nil || !ok || assessment.Verdict != VerdictNone || assessment.Eligibility != EligibilityCoverageGap {
		t.Fatalf("Assess(late complete) = %#v, ok=%v, error=%v", assessment, ok, err)
	}
}

func TestCoverageBarrierRejectsFutureCapture(t *testing.T) {
	t.Parallel()

	run, clock := mustCoverageRun(t, testAssignments())
	inputPayload, input := testTriggerInput(t, "normal")
	key := mustPartitionKey(t, input)
	inputID := input.DetectionOutcomes[0].InputID
	commitStreamRecord(t, run, StreamRecord{Epoch: "run-1", Role: StreamInput, Topic: "input", Partition: 0, Offset: 20, Key: key, Value: inputPayload})
	commitStreamRecord(t, run, StreamRecord{Epoch: "run-1", Role: StreamPython, Topic: "python", Partition: 0, Offset: 30, Key: key, Value: testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger)})
	clock.Advance(11 * time.Second)
	barrier := BarrierSnapshot{
		CaptureStartedAt: clock.Now().Add(time.Second),
		Partitions:       []PartitionBarrier{{Role: StreamGo, Topic: "go", Partition: 0, HighWater: 12}},
	}
	if err := run.FreezeBarrier("run-1", inputID, barrier); err == nil {
		t.Fatal("FreezeBarrier() accepted a future capture time")
	}
	if run.Valid() {
		t.Fatal("a future capture time left the Run valid")
	}
}

func TestCoverageBeginBarrierCaptureUsesRunClock(t *testing.T) {
	t.Parallel()

	run, clock := mustCoverageRun(t, testAssignments())
	capturedAt, err := run.BeginBarrierCapture("run-1")
	if err != nil {
		t.Fatalf("BeginBarrierCapture() error = %v", err)
	}
	if !capturedAt.Equal(clock.Now()) {
		t.Fatalf("BeginBarrierCapture() = %s, want Run clock %s", capturedAt, clock.Now())
	}
	clock.Advance(time.Second)
	capturedAt, err = run.BeginBarrierCapture("run-1")
	if err != nil || !capturedAt.Equal(clock.Now()) {
		t.Fatalf("BeginBarrierCapture(second) = %s, error=%v, want %s", capturedAt, err, clock.Now())
	}
}

func TestCoverageRecordAfterBarrierWithOffsetGapIsLate(t *testing.T) {
	t.Parallel()

	run, clock := mustCoverageRun(t, testAssignments())
	inputPayload, input := testTriggerInput(t, "normal")
	key := mustPartitionKey(t, input)
	inputID := input.DetectionOutcomes[0].InputID
	commitStreamRecord(t, run, StreamRecord{Epoch: "run-1", Role: StreamInput, Topic: "input", Partition: 0, Offset: 20, Key: key, Value: inputPayload})
	commitStreamRecord(t, run, StreamRecord{Epoch: "run-1", Role: StreamPython, Topic: "python", Partition: 0, Offset: 30, Key: key, Value: testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger)})
	clock.Advance(11 * time.Second)
	if err := run.FreezeBarrier("run-1", inputID, capturedBarrier(clock, PartitionBarrier{Role: StreamGo, Topic: "go", Partition: 0, HighWater: 12})); err != nil {
		t.Fatalf("FreezeBarrier() error = %v", err)
	}
	commitStreamRecord(t, run, StreamRecord{
		Epoch:     "run-1",
		Role:      StreamGo,
		Topic:     "go",
		Partition: 0,
		Offset:    12,
		Key:       key,
		Value:     testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger),
	})
	snapshot, _, err := run.Coverage("run-1", inputID)
	if err != nil || snapshot.Phase != CoverageComplete || !slices.Equal(snapshot.LateRoles, []StreamRole{StreamGo}) {
		t.Fatalf("Coverage(gap after barrier) = %#v, error=%v", snapshot, err)
	}
}

func TestCoverageLateRecordUsesItsOwnPartitionBarrier(t *testing.T) {
	t.Parallel()

	assignments := append(testAssignments(), PartitionAssignment{Role: StreamGo, Topic: "go", Partition: 1, NextOffset: 40})
	run, clock := mustCoverageRun(t, assignments)
	inputPayload, input := testTriggerInput(t, "normal")
	key := mustPartitionKey(t, input)
	inputID := input.DetectionOutcomes[0].InputID
	commitStreamRecord(t, run, StreamRecord{Epoch: "run-1", Role: StreamInput, Topic: "input", Partition: 0, Offset: 20, Key: key, Value: inputPayload})
	commitStreamRecord(t, run, StreamRecord{Epoch: "run-1", Role: StreamPython, Topic: "python", Partition: 0, Offset: 30, Key: key, Value: testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger)})
	clock.Advance(11 * time.Second)
	if err := run.FreezeBarrier("run-1", inputID, capturedBarrier(clock,
		PartitionBarrier{Role: StreamGo, Topic: "go", Partition: 0, HighWater: 12},
		PartitionBarrier{Role: StreamGo, Topic: "go", Partition: 1, HighWater: 42},
	)); err != nil {
		t.Fatalf("FreezeBarrier() error = %v", err)
	}
	commitStreamRecord(t, run, StreamRecord{
		Epoch:     "run-1",
		Role:      StreamGo,
		Topic:     "go",
		Partition: 0,
		Offset:    12,
		Key:       key,
		Value:     testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger),
	})
	snapshot, _, err := run.Coverage("run-1", inputID)
	if err != nil || snapshot.Phase != CoverageComplete || !slices.Equal(snapshot.LateRoles, []StreamRole{StreamGo}) {
		t.Fatalf("Coverage(multi-partition late) = %#v, error=%v", snapshot, err)
	}
}

func TestCoverageClockRewindInvalidatesRun(t *testing.T) {
	t.Parallel()

	run, clock := mustCoverageRun(t, testAssignments())
	_, input := testTriggerInput(t, "normal")
	inputID := input.DetectionOutcomes[0].InputID
	commitStreamRecord(t, run, StreamRecord{
		Epoch:     "run-1",
		Role:      StreamGo,
		Topic:     "go",
		Partition: 0,
		Offset:    10,
		Key:       mustPartitionKey(t, input),
		Value:     testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger),
	})
	clock.Advance(time.Second)
	if _, _, err := run.Coverage("run-1", inputID); err != nil {
		t.Fatalf("Coverage() error = %v", err)
	}
	clock.Advance(-2 * time.Second)
	if _, _, err := run.Coverage("run-1", inputID); err == nil {
		t.Fatal("Coverage() accepted a clock rewind")
	}
	if run.Valid() {
		t.Fatal("clock rewind left the Run valid")
	}
}

func TestCoverageBarrierRequiresEveryMissingPartition(t *testing.T) {
	t.Parallel()

	assignments := append(testAssignments(), PartitionAssignment{Role: StreamGo, Topic: "go", Partition: 1, NextOffset: 40})
	run, clock := mustCoverageRun(t, assignments)
	inputPayload, input := testTriggerInput(t, "normal")
	key := mustPartitionKey(t, input)
	inputID := input.DetectionOutcomes[0].InputID
	commitStreamRecord(t, run, StreamRecord{Epoch: "run-1", Role: StreamInput, Topic: "input", Partition: 0, Offset: 20, Key: key, Value: inputPayload})
	commitStreamRecord(t, run, StreamRecord{Epoch: "run-1", Role: StreamPython, Topic: "python", Partition: 0, Offset: 30, Key: key, Value: testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger)})
	clock.Advance(11 * time.Second)
	if err := run.FreezeBarrier("old-run", inputID, capturedBarrier(clock)); err == nil {
		t.Fatal("FreezeBarrier() accepted an old epoch")
	}
	if !run.Valid() {
		t.Fatal("old epoch barrier invalidated the current Run")
	}
	if err := run.FreezeBarrier("run-1", inputID, capturedBarrier(clock, PartitionBarrier{Role: StreamGo, Topic: "go", Partition: 0, HighWater: 10})); err == nil {
		t.Fatal("FreezeBarrier() accepted an incomplete partition set")
	}
	if run.Valid() {
		t.Fatal("an incomplete barrier left the Run valid")
	}
}

func TestCoverageBarrierRejectsHighWaterBehindCommittedProgress(t *testing.T) {
	t.Parallel()

	run, clock := mustCoverageRun(t, testAssignments())
	inputPayload, input := testTriggerInput(t, "normal")
	key := mustPartitionKey(t, input)
	inputID := input.DetectionOutcomes[0].InputID
	commitStreamRecord(t, run, StreamRecord{Epoch: "run-1", Role: StreamInput, Topic: "input", Partition: 0, Offset: 20, Key: key, Value: inputPayload})
	commitStreamRecord(t, run, StreamRecord{Epoch: "run-1", Role: StreamPython, Topic: "python", Partition: 0, Offset: 30, Key: key, Value: testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger)})
	clock.Advance(11 * time.Second)
	if err := run.FreezeBarrier("run-1", inputID, capturedBarrier(clock, PartitionBarrier{Role: StreamGo, Topic: "go", Partition: 0, HighWater: 9})); err == nil {
		t.Fatal("FreezeBarrier() accepted a stale high water")
	}
	if run.Valid() {
		t.Fatal("a stale high water left the Run valid")
	}
}

func TestCoverageBarrierIsCopiedAndImmutable(t *testing.T) {
	t.Parallel()

	run, clock := mustCoverageRun(t, testAssignments())
	inputPayload, input := testTriggerInput(t, "normal")
	key := mustPartitionKey(t, input)
	inputID := input.DetectionOutcomes[0].InputID
	commitStreamRecord(t, run, StreamRecord{Epoch: "run-1", Role: StreamInput, Topic: "input", Partition: 0, Offset: 20, Key: key, Value: inputPayload})
	commitStreamRecord(t, run, StreamRecord{Epoch: "run-1", Role: StreamPython, Topic: "python", Partition: 0, Offset: 30, Key: key, Value: testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger)})
	clock.Advance(11 * time.Second)
	barrier := capturedBarrier(clock, PartitionBarrier{Role: StreamGo, Topic: "go", Partition: 0, HighWater: 12})
	if err := run.FreezeBarrier("run-1", inputID, barrier); err != nil {
		t.Fatalf("FreezeBarrier() error = %v", err)
	}
	barrier.Partitions[0].HighWater = 99
	_, unrelated := testTriggerInput(t, "anomalous")
	commitStreamRecord(t, run, StreamRecord{
		Epoch:     "run-1",
		Role:      StreamGo,
		Topic:     "go",
		Partition: 0,
		Offset:    11,
		Key:       mustPartitionKey(t, unrelated),
		Value:     testDecisionBatch(t, unrelated, contract.DecisionOutcomeNoTrigger),
	})
	snapshot, _, err := run.Coverage("run-1", inputID)
	if err != nil || snapshot.Phase != CoverageMissingAtBarrier {
		t.Fatalf("Coverage(after caller mutation) = %#v, error=%v", snapshot, err)
	}
	conflicting := capturedBarrier(clock, PartitionBarrier{Role: StreamGo, Topic: "go", Partition: 0, HighWater: 13})
	if err := run.FreezeBarrier("run-1", inputID, conflicting); err == nil {
		t.Fatal("FreezeBarrier() accepted a conflicting re-freeze")
	}
	if run.Valid() {
		t.Fatal("a conflicting re-freeze left the Run valid")
	}
}

func TestCoverageConfigurationAndClockFailClosed(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		option RunOption
	}{
		{name: "zero timeout", option: WithCoverageTimeout(0)},
		{name: "negative timeout", option: WithCoverageTimeout(-time.Second)},
		{name: "nil option", option: nil},
		{name: "nil clock", option: withRunClock(nil)},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewRun("run-1", 10, testAssignments(), test.option); err == nil {
				t.Fatal("NewRun() accepted an invalid option")
			}
		})
	}

	run, err := NewRun("run-1", 10, testAssignments(), WithCoverageTimeout(time.Second), withRunClock(func() time.Time { return time.Time{} }))
	if err != nil {
		t.Fatalf("NewRun() error = %v", err)
	}
	_, input := testTriggerInput(t, "normal")
	_, err = run.Prepare(StreamRecord{
		Epoch:     "run-1",
		Role:      StreamGo,
		Topic:     "go",
		Partition: 0,
		Offset:    10,
		Key:       mustPartitionKey(t, input),
		Value:     testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger),
	})
	if err == nil {
		t.Fatal("Prepare() accepted a zero clock before the external acknowledgement boundary")
	}
	if run.Valid() {
		t.Fatal("a zero clock left the Run valid")
	}
}

func TestCoverageCapacityReusesCompletedEntries(t *testing.T) {
	t.Parallel()

	clock := &manualRunClock{now: time.Date(2026, time.August, 18, 0, 0, 0, 0, time.UTC)}
	run, err := NewRun("run-1", 1, testAssignments(), WithCoverageTimeout(10*time.Second), withRunClock(clock.Now))
	if err != nil {
		t.Fatalf("NewRun() error = %v", err)
	}
	inputPayload, input := testTriggerInput(t, "normal")
	key := mustPartitionKey(t, input)
	commitAuditedStreamRecord(t, run, StreamRecord{Epoch: "run-1", Role: StreamInput, Topic: "input", Partition: 0, Offset: 20, Key: key, Value: inputPayload})
	commitAuditedStreamRecord(t, run, StreamRecord{Epoch: "run-1", Role: StreamGo, Topic: "go", Partition: 0, Offset: 10, Key: key, Value: testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger)})
	commitAuditedStreamRecord(t, run, StreamRecord{Epoch: "run-1", Role: StreamPython, Topic: "python", Partition: 0, Offset: 30, Key: key, Value: testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger)})
	secondPayload, second := testTriggerInput(t, "anomalous")
	prepared, err := run.Prepare(StreamRecord{
		Epoch:     "run-1",
		Role:      StreamInput,
		Topic:     "input",
		Partition: 0,
		Offset:    21,
		Key:       mustPartitionKey(t, second),
		Value:     secondPayload,
	})
	if err != nil {
		t.Fatalf("Prepare() did not reuse completed capacity: %v", err)
	}
	if _, err := run.CommitSucceeded(prepared); err != nil {
		t.Fatalf("CommitSucceeded() error = %v", err)
	}
	if !run.Valid() {
		t.Fatal("completed-entry reuse invalidated the Run")
	}
}

func mustCoverageRun(t *testing.T, assignments []PartitionAssignment) (*Run, *manualRunClock) {
	t.Helper()
	clock := &manualRunClock{now: time.Date(2026, time.August, 18, 0, 0, 0, 0, time.UTC)}
	run, err := NewRun(
		"run-1",
		10,
		assignments,
		WithCoverageTimeout(10*time.Second),
		withRunClock(clock.Now),
	)
	if err != nil {
		t.Fatalf("NewRun() error = %v", err)
	}
	return run, clock
}

func commitStreamRecord(t *testing.T, run *Run, record StreamRecord) {
	t.Helper()
	prepared, err := run.Prepare(record)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if _, err := run.CommitSucceeded(prepared); err != nil {
		t.Fatalf("CommitSucceeded() error = %v", err)
	}
}

func commitAuditedStreamRecord(t *testing.T, run *Run, record StreamRecord) {
	t.Helper()
	prepared, err := run.Prepare(record)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if _, err := run.PreviewAudits(prepared, Gates{StableEpoch: true}); err != nil {
		t.Fatalf("PreviewAudits() error = %v", err)
	}
	if _, err := run.CommitSucceeded(prepared); err != nil {
		t.Fatalf("CommitSucceeded() error = %v", err)
	}
}

func capturedBarrier(clock *manualRunClock, partitions ...PartitionBarrier) BarrierSnapshot {
	return BarrierSnapshot{CaptureStartedAt: clock.Now(), Partitions: partitions}
}

type manualRunClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *manualRunClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualRunClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}
