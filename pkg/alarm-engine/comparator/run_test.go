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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/contract"
)

func TestRunExposesObservationsOnlyAfterOffsetCommit(t *testing.T) {
	t.Parallel()

	run := mustRun(t)
	inputPayload, input := testTriggerInput(t, "normal")
	decisionPayload := testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger)
	key := mustPartitionKey(t, input)
	inputID := input.DetectionOutcomes[0].InputID

	goPrepared, err := run.Prepare(StreamRecord{Epoch: "run-1", Role: StreamGo, Topic: "go", Partition: 0, Offset: 10, Key: key, Value: decisionPayload})
	if err != nil {
		t.Fatalf("Prepare(go) error = %v", err)
	}
	if _, _, err := run.Assess("run-1", inputID, Gates{}); err == nil {
		t.Fatal("Assess() exposed an in-flight observation before commit")
	}
	if _, err := run.CommitSucceeded(goPrepared); err != nil {
		t.Fatalf("CommitSucceeded(go) error = %v", err)
	}
	assessment, ok, err := run.Assess("run-1", inputID, Gates{})
	if err != nil || !ok || assessment.Join != JoinPendingInput {
		t.Fatalf("Assess(after go) = %#v, ok=%v, error=%v", assessment, ok, err)
	}

	inputPrepared, err := run.Prepare(StreamRecord{Epoch: "run-1", Role: StreamInput, Topic: "input", Partition: 0, Offset: 20, Key: key, Value: inputPayload})
	if err != nil {
		t.Fatalf("Prepare(input) error = %v", err)
	}
	if _, err := run.CommitSucceeded(inputPrepared); err != nil {
		t.Fatalf("CommitSucceeded(input) error = %v", err)
	}
	pythonPrepared, err := run.Prepare(StreamRecord{Epoch: "run-1", Role: StreamPython, Topic: "python", Partition: 0, Offset: 30, Key: key, Value: decisionPayload})
	if err != nil {
		t.Fatalf("Prepare(python) error = %v", err)
	}
	if _, err := run.CommitSucceeded(pythonPrepared); err != nil {
		t.Fatalf("CommitSucceeded(python) error = %v", err)
	}
	assessment, ok, err = run.Assess("run-1", inputID, Gates{StableEpoch: true, CoverageComplete: true})
	if err != nil || !ok || assessment.Join != JoinComplete || assessment.Verdict != VerdictMatch {
		t.Fatalf("Assess(complete) = %#v, ok=%v, error=%v", assessment, ok, err)
	}
	for _, test := range []struct {
		role StreamRole
		want int64
	}{{StreamInput, 21}, {StreamGo, 11}, {StreamPython, 31}} {
		got, err := run.NextOffset("run-1", test.role, 0)
		if err != nil || got != test.want {
			t.Fatalf("NextOffset(%v)=%d,error=%v want %d", test.role, got, err, test.want)
		}
	}
}

func TestRunAcceptsKafkaOffsetGap(t *testing.T) {
	t.Parallel()

	run := mustRun(t)
	_, input := testTriggerInput(t, "normal")
	prepared, err := run.Prepare(StreamRecord{
		Epoch:     "run-1",
		Role:      StreamGo,
		Topic:     "go",
		Partition: 0,
		Offset:    11,
		Key:       mustPartitionKey(t, input),
		Value:     testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger),
	})
	if err != nil {
		t.Fatalf("Prepare() rejected a valid Kafka offset gap: %v", err)
	}
	if _, err := run.CommitSucceeded(prepared); err != nil {
		t.Fatalf("CommitSucceeded() error = %v", err)
	}
	if got, err := run.NextOffset("run-1", StreamGo, 0); err != nil || got != 12 {
		t.Fatalf("NextOffset()=%d,error=%v want 12", got, err)
	}
}

func TestRunInvalidatesOnCommitFailureOrRewind(t *testing.T) {
	t.Parallel()

	_, input := testTriggerInput(t, "normal")
	payload := testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger)
	key := mustPartitionKey(t, input)
	tests := []struct {
		name   string
		offset int64
		commit bool
	}{
		{name: "rewind", offset: 9},
		{name: "commit failure", offset: 10, commit: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			run := mustRun(t)
			prepared, err := run.Prepare(StreamRecord{Epoch: "run-1", Role: StreamGo, Topic: "go", Partition: 0, Offset: test.offset, Key: key, Value: payload})
			if test.commit {
				if err != nil {
					t.Fatalf("Prepare() error = %v", err)
				}
				if err := run.CommitFailed(prepared, errors.New("broker commit failed")); err == nil {
					t.Fatal("CommitFailed() error = nil")
				}
			} else if err == nil {
				t.Fatal("Prepare() accepted a discontinuous offset")
			}
			if run.Valid() {
				t.Fatal("run remained valid after a completion-boundary failure")
			}
			if _, err := run.Prepare(StreamRecord{Epoch: "run-1", Role: StreamGo, Topic: "go", Partition: 0, Offset: 10, Key: key, Value: payload}); err == nil {
				t.Fatal("invalid run accepted another record")
			}
		})
	}
}

func TestRunConcurrentPrepareReturnsBusyWithoutInvalidating(t *testing.T) {
	t.Parallel()

	run := mustRun(t)
	inputPayload, input := testTriggerInput(t, "normal")
	decisionPayload := testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger)
	key := mustPartitionKey(t, input)
	records := []StreamRecord{
		{Epoch: "run-1", Role: StreamInput, Topic: "input", Partition: 0, Offset: 20, Key: key, Value: inputPayload},
		{Epoch: "run-1", Role: StreamGo, Topic: "go", Partition: 0, Offset: 10, Key: key, Value: decisionPayload},
	}
	type result struct {
		index    int
		prepared Prepared
		err      error
	}
	start := make(chan struct{})
	results := make(chan result, len(records))
	for index, record := range records {
		go func(index int, record StreamRecord) {
			<-start
			prepared, err := run.Prepare(record)
			results <- result{index: index, prepared: prepared, err: err}
		}(index, record)
	}
	close(start)

	var succeeded, busy result
	for range records {
		item := <-results
		switch {
		case item.err == nil:
			succeeded = item
		case errors.Is(item.err, ErrRecordInFlight):
			busy = item
		default:
			t.Fatalf("Prepare() unexpected error = %v", item.err)
		}
	}
	if succeeded.prepared.token == 0 || busy.err == nil {
		t.Fatalf("concurrent Prepare results = success %#v, busy %#v", succeeded, busy)
	}
	if !run.Valid() {
		t.Fatal("concurrent Prepare invalidated the run")
	}
	if _, err := run.CommitSucceeded(succeeded.prepared); err != nil {
		t.Fatalf("CommitSucceeded() error = %v", err)
	}
	retry, err := run.Prepare(records[busy.index])
	if err != nil {
		t.Fatalf("Prepare(retry) error = %v", err)
	}
	if _, err := run.CommitSucceeded(retry); err != nil {
		t.Fatalf("CommitSucceeded(retry) error = %v", err)
	}
	if !run.Valid() {
		t.Fatal("serialized retry invalidated the run")
	}
}

func TestRunRejectsPreparedTokenFromAnotherRun(t *testing.T) {
	t.Parallel()

	_, input := testTriggerInput(t, "normal")
	record := StreamRecord{
		Epoch:     "run-1",
		Role:      StreamGo,
		Topic:     "go",
		Partition: 0,
		Offset:    10,
		Key:       mustPartitionKey(t, input),
		Value:     testDecisionBatch(t, input, contract.DecisionOutcomeNoTrigger),
	}
	oldRun := mustRun(t)
	oldPrepared, err := oldRun.Prepare(record)
	if err != nil {
		t.Fatalf("old Run Prepare() error = %v", err)
	}
	newRun := mustRun(t)
	if _, err := newRun.Prepare(record); err != nil {
		t.Fatalf("new Run Prepare() error = %v", err)
	}
	if _, err := newRun.CommitSucceeded(oldPrepared); err == nil {
		t.Fatal("CommitSucceeded() accepted a Prepared token from another Run")
	}
	if newRun.Valid() {
		t.Fatal("stale completion did not invalidate the target Run")
	}
}

func TestRunExplicitInvalidationRejectsOldAssignment(t *testing.T) {
	t.Parallel()

	run := mustRun(t)
	if err := run.Invalidate("old-run", errors.New("rebalance")); err == nil {
		t.Fatal("Invalidate() accepted an old epoch")
	}
	if !run.Valid() {
		t.Fatal("old epoch invalidation invalidated the current Run")
	}
	_, input := testTriggerInput(t, "normal")
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
	if err := run.Invalidate("run-1", errors.New("rebalance")); err != nil {
		t.Fatalf("Invalidate() error = %v", err)
	}
	if run.Valid() {
		t.Fatal("Invalidate() left Run valid")
	}
	if _, err := run.CommitSucceeded(prepared); err == nil {
		t.Fatal("invalid Run accepted a stale completion")
	}
	if _, err := run.NextOffset("run-1", StreamInput, 0); err == nil {
		t.Fatal("invalid Run exposed offsets")
	}
}

func TestRunRejectsInvalidAssignmentAndOldEpoch(t *testing.T) {
	t.Parallel()

	valid := testAssignments()
	tests := []struct {
		name   string
		mutate func([]PartitionAssignment) []PartitionAssignment
	}{
		{name: "missing role", mutate: func(value []PartitionAssignment) []PartitionAssignment { return value[:2] }},
		{name: "duplicate topic across roles", mutate: func(value []PartitionAssignment) []PartitionAssignment { value[1].Topic = value[0].Topic; return value }},
		{name: "duplicate coordinate", mutate: func(value []PartitionAssignment) []PartitionAssignment { return append(value, value[0]) }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assignments := append([]PartitionAssignment(nil), valid...)
			if _, err := NewRun("run-1", 10, test.mutate(assignments)); err == nil {
				t.Fatal("NewRun() accepted invalid assignments")
			}
		})
	}

	run := mustRun(t)
	_, input := testTriggerInput(t, "normal")
	if _, err := run.Prepare(StreamRecord{Epoch: "old-run", Role: StreamInput, Topic: "input", Partition: 0, Offset: 20, Key: mustPartitionKey(t, input), Value: []byte("invalid")}); err == nil {
		t.Fatal("Prepare() accepted an old epoch")
	}
	if !run.Valid() {
		t.Fatal("old epoch traffic invalidated the current run")
	}
}

func mustRun(t *testing.T) *Run {
	t.Helper()
	run, err := NewRun("run-1", 10, testAssignments())
	if err != nil {
		t.Fatalf("NewRun() error = %v", err)
	}
	return run
}

func testAssignments() []PartitionAssignment {
	return []PartitionAssignment{
		{Role: StreamInput, Topic: "input", Partition: 0, NextOffset: 20},
		{Role: StreamGo, Topic: "go", Partition: 0, NextOffset: 10},
		{Role: StreamPython, Topic: "python", Partition: 0, NextOffset: 30},
	}
}
