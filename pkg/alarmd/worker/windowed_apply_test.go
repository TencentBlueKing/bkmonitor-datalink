// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// windowedSeries is a digest per letter, beside the fixture's own "c" series.
func windowedSeries(letters ...string) []string {
	digests := make([]string, len(letters))
	for index, letter := range letters {
		digests[index] = strings.Repeat(letter, 64)
	}
	return digests
}

// windowedSlot is the recording fixture with State kept across attempts and
// a cap of two State writes per Slot, so six series are three windows of two:
// a b | c d | e f.
func windowedSlot(t *testing.T, extra []string) (*recordingPorts, *persistedRetryState, *worker.SlotExecutionCoordinator, *memoryEvidenceStore) {
	t.Helper()
	trace := make([]string, 0)
	ports := &recordingPorts{trace: &trace, ready: true, extraSeries: extra}
	state := &persistedRetryState{recordingPorts: ports, saved: map[execution.StateKeyIdentity]execution.StateMutation{}}
	evidence := newMemoryEvidenceStore()
	coordinator, err := worker.NewSlotExecutionCoordinator(worker.Ports{OpenAlerts: ports, Finalization: ports, Activation: ports,
		Query: ports, Sequencer: ports, Evaluator: ports, Admission: ports, GapGuard: ports, NoData: worker.SharedNoDataStore,
		Hosts: worker.SharedHostBusiness, Events: ports, State: state, Progress: ports, ExecutionEvidence: evidence,
		Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {})},
		worker.ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxStateMutations: 2, MaxEvents: 100, MaxGapMutations: 10})
	if err != nil {
		t.Fatal(err)
	}
	return ports, state, coordinator, evidence
}

// windowedRequest is the fixture's request with its windows around now, so
// a mark the attempt leaves is one the store keeps (liveSlotRequest).
func windowedRequest() execution.SlotExecutionRequest {
	request := slotRequest(execution.OperationNormal)
	now := time.Now()
	request.EarliestQueryDeadlineUnixMilli = now.Add(time.Minute).UnixMilli()
	request.RecoveryUntilUnixMilli = now.Add(10 * time.Minute).UnixMilli()
	request.KeepUntilUnixMilli = now.Add(20 * time.Minute).UnixMilli()
	return request
}

// sortedBatches is the series of each event batch from the index on, each
// batch sorted: the sink takes a batch in event order, the windows are read
// by which series they hold.
func sortedBatches(batches [][]string, from int) [][]string {
	sorted := make([][]string, 0, len(batches)-from)
	for _, batch := range batches[from:] {
		batch = append([]string(nil), batch...)
		sort.Strings(batch)
		sorted = append(sorted, batch)
	}
	return sorted
}

func countStage(trace []string, stage string) int {
	count := 0
	for _, traced := range trace {
		if traced == stage {
			count++
		}
	}
	return count
}

// nthStage is the position of the n-th (from one) entry of a stage.
func nthStage(t *testing.T, trace []string, stage string, n int) int {
	t.Helper()
	seen := 0
	for index, traced := range trace {
		if traced == stage {
			seen++
			if seen == n {
				return index
			}
		}
	}
	t.Fatalf("trace has %d %s, want at least %d: %v", seen, stage, n, trace)
	return -1
}

// A Slot of three windows is written while it is evaluated: each window's
// events are acknowledged before its State, the next window's series are
// evaluated after it, and Progress follows the last one. Head, windows and
// tail are five Sequences, one at a time.
func TestAWindowedSlotWritesEachWindowsEventsBeforeItsStateAndProgressAfterAll(t *testing.T) {
	ports, state, coordinator, _ := windowedSlot(t, windowedSeries("a", "b", "d", "e", "f"))
	result, err := coordinator.Execute(context.Background(), windowedRequest())
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v err=%v", result, err)
	}
	series := windowedSeries("a", "b", "c", "d", "e", "f")
	a, b, c, d, e, f := series[0], series[1], series[2], series[3], series[4], series[5]
	if got, want := sortedBatches(ports.eventBatches, 0), [][]string{{a, b}, {c, d}, {e, f}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("event batches = %v, want one per window", got)
	}
	trace := *ports.trace
	var writes []string
	for _, stage := range trace {
		if stage == "event_ack" || stage == "state_apply" || stage == "progress_commit" {
			writes = append(writes, stage)
		}
	}
	if want := []string{"event_ack", "state_apply", "event_ack", "state_apply", "event_ack", "state_apply", "progress_commit"}; !reflect.DeepEqual(writes, want) {
		t.Fatalf("write order = %v, want %v", writes, want)
	}
	// The first window is written once the third series' result would have
	// overfilled it. The series of a preflight batch (two here, the cap) are
	// evaluated together before any of them is merged, so it is written
	// before the batch after the third series' is evaluated: before the
	// fifth evaluation.
	if nthStage(t, trace, "state_apply", 1) > nthStage(t, trace, "evaluate", 5) {
		t.Fatalf("the first window was written after the evaluation went on: %v", trace)
	}
	if sequences := countStage(trace, "sequence"); sequences != 5 {
		t.Fatalf("Sequences = %d, want a head, three windows and a tail", sequences)
	}
	if len(state.saved) != 6 {
		t.Fatalf("State written for %d series, want 6", len(state.saved))
	}
}

// A window whose State write fails leaves the earlier windows written. The
// retry resumes their series - found at this Slot, not evaluated, nothing
// sent - writes the rest, and counts the resumed ones as landed: the attempt
// whose Progress fails leaves the Plan's applied mark, and the next attempt
// completes.
func TestARetryResumesTheWindowsWrittenAndCountsThemAsLanded(t *testing.T) {
	ports, state, coordinator, evidence := windowedSlot(t, windowedSeries("a", "b", "d", "e", "f"))
	ports.failStateApplyAt = 2
	request := windowedRequest()
	if _, err := coordinator.Execute(context.Background(), request); err == nil {
		t.Fatal("the second window's failed State write did not fail the attempt")
	}
	if len(state.saved) != 2 {
		t.Fatalf("first attempt wrote %d series, want the first window's two", len(state.saved))
	}

	ports.failStateApplyAt, ports.failStage = 0, "progress_commit"
	evaluated, batches := countStage(*ports.trace, "evaluate"), len(ports.eventBatches)
	if _, err := coordinator.Execute(context.Background(), request); err == nil || !strings.Contains(err.Error(), "progress_commit") {
		t.Fatalf("second attempt err = %v, want its Progress to fail", err)
	}
	if got := countStage(*ports.trace, "evaluate") - evaluated; got != 4 {
		t.Fatalf("second attempt evaluated %d series, want the four not written", got)
	}
	series := windowedSeries("c", "d", "e", "f")
	c, d, e, f := series[0], series[1], series[2], series[3]
	if got, want := sortedBatches(ports.eventBatches, batches), [][]string{{c, d}, {e, f}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second attempt event batches = %v, want %v and nothing of the resumed series", got, want)
	}
	if marked := evidence.markedPlans(request.Contract.Slot); marked != 1 {
		t.Fatalf("applied marks = %d, want the Plan, its resumed series counted as landed", marked)
	}

	ports.failStage = ""
	result, err := coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed || len(state.saved) != 6 {
		t.Fatalf("third attempt result=%+v err=%v stored=%d, want it completed over every series", result, err, len(state.saved))
	}
}

// R1: windows are cut by content on every attempt and a series is skipped by
// its own State, not by window number. A retry with one more series whose
// digest sorts before the first window shifts every window by one; the new
// series is evaluated and sent, and none of the series the first attempt did
// not write is lost.
func TestARetryWithAnEarlierNewSeriesLosesNoUnwrittenSeries(t *testing.T) {
	ports, state, coordinator, _ := windowedSlot(t, windowedSeries("a", "b", "d", "e", "f"))
	ports.failStateApplyAt = 2
	request := windowedRequest()
	if _, err := coordinator.Execute(context.Background(), request); err == nil {
		t.Fatal("the second window's failed State write did not fail the attempt")
	}
	ports.failStateApplyAt = 0
	ports.extraSeries = windowedSeries("0", "a", "b", "d", "e", "f")
	batches := len(ports.eventBatches)
	result, err := coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed {
		t.Fatalf("retry result=%+v err=%v", result, err)
	}
	sent := map[string]bool{}
	for _, batch := range ports.eventBatches[batches:] {
		for _, series := range batch {
			sent[series] = true
		}
	}
	for _, letter := range []string{"0", "c", "d", "e", "f"} {
		if !sent[strings.Repeat(letter, 64)] {
			t.Fatalf("series %s was not sent on the retry; sent %v", letter, sent)
		}
	}
	for _, letter := range []string{"a", "b"} {
		if sent[strings.Repeat(letter, 64)] {
			t.Fatalf("series %s, written by the first attempt, was sent again", letter)
		}
	}
	if len(state.saved) != 7 {
		t.Fatalf("State written for %d series, want all seven", len(state.saved))
	}
}

// B1: a window's events go out before its State. When they do not land, the
// window's State is not written and nor is anything of the Plan after it;
// the retry sends that window's events again and none of the windows that
// already landed.
func TestAWindowWhoseEventsDidNotLandIsSentAgainAndTheLandedOnesAreNot(t *testing.T) {
	ports, state, coordinator, _ := windowedSlot(t, windowedSeries("a", "b", "d", "e", "f"))
	ports.failEventAckAt = 2
	request := windowedRequest()
	first, err := coordinator.Execute(context.Background(), request)
	if err != nil || first.Completed || first.Result != observability.ResultRetrying {
		t.Fatalf("first attempt result=%+v err=%v, want it left retrying on the unknown ACK", first, err)
	}
	series := windowedSeries("a", "b", "c", "d", "e", "f")
	a, b, c, d, e, f := series[0], series[1], series[2], series[3], series[4], series[5]
	if got, want := sortedBatches(ports.eventBatches, 0), [][]string{{a, b}, {c, d}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first attempt event batches = %v, want the third window never sent", got)
	}
	if len(state.saved) != 2 {
		t.Fatalf("first attempt wrote %d series, want only the window whose events landed", len(state.saved))
	}

	ports.failEventAckAt = 0
	batches := len(ports.eventBatches)
	result, err := coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed {
		t.Fatalf("retry result=%+v err=%v", result, err)
	}
	if got, want := sortedBatches(ports.eventBatches, batches), [][]string{{c, d}, {e, f}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("retry event batches = %v, want the unlanded window again and the rest", got)
	}
}

// Nothing is written before the completion is in hand: whether the round was
// whole decides what a Slot writes, so a PARTIAL completion's Slot writes
// after it, and a Slot cut into windows starts writing them only once its
// completion has arrived.
func TestNothingIsWrittenBeforeTheCompletion(t *testing.T) {
	for _, test := range []struct {
		name     string
		degraded bool
		extra    []string
	}{
		{"a PARTIAL completion", true, nil},
		{"a Slot cut into windows", false, windowedSeries("a", "b", "d", "e", "f")},
	} {
		t.Run(test.name, func(t *testing.T) {
			ports, _, coordinator, _ := windowedSlot(t, nil)
			ports.degraded = test.degraded
			innerTrace := make([]string, 0)
			source := &recordingPorts{trace: &innerTrace, ready: true, degraded: test.degraded, extraSeries: test.extra}
			ports.executeOverride = func(ctx context.Context, request execution.QueryExecutionRequest, consumer execution.QueryExecutionConsumer) (execution.QueryExecutionCompletion, error) {
				completion, err := source.Execute(ctx, request, consumer)
				ports.record("completion")
				return completion, err
			}
			if _, err := coordinator.Execute(context.Background(), windowedRequest()); err != nil {
				t.Fatalf("Execute() err=%v", err)
			}
			trace := *ports.trace
			completed := nthStage(t, trace, "completion", 1)
			wrote := false
			for index, stage := range trace {
				switch stage {
				case "gap_before", "gap_after", "event_ack", "state_admission", "state_apply", "progress_commit":
					wrote = true
					if index < completed {
						t.Fatalf("%s before the completion: %v", stage, trace)
					}
				}
			}
			if !wrote {
				t.Fatalf("the Slot wrote nothing, so the order was not read: %v", trace)
			}
		})
	}
}
