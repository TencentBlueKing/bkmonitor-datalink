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
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// The watchdog's grace (cmd/alarmd executionPastDeadlineGrace) and a window's
// bound (worker windowStepBound), restated: both are a minute.
const (
	watchdogGrace = time.Minute
	windowBound   = time.Minute
)

// hookedEvaluator runs hook once, before the first series it evaluates after
// a window's State apply returned (applied counts them): in the windowed
// fixture (a b | c d | e f) a point between the first window and the next,
// whatever order the series of a batch are evaluated in.
type hookedEvaluator struct {
	*recordingPorts
	applied *atomic.Int64
	once    sync.Once
	hook    func()
}

func (evaluator *hookedEvaluator) Evaluate(ctx context.Context, request execution.EvaluationRequest) (execution.EvaluationResult, error) {
	if evaluator.applied.Load() > 0 && evaluator.hook != nil {
		evaluator.once.Do(evaluator.hook)
	}
	return evaluator.recordingPorts.Evaluate(ctx, request)
}

// appliedState counts the State applies that returned.
type appliedState struct {
	*persistedRetryState
	applied *atomic.Int64
}

func (state *appliedState) ApplyRuntime(ctx context.Context, request execution.StateApplyRequest) (execution.StateApplyResult, error) {
	defer state.applied.Add(1)
	return state.persistedRetryState.ApplyRuntime(ctx, request)
}

// hookedWindowedSlot is windowedSlot with the evaluator hooked.
func hookedWindowedSlot(t *testing.T) (*recordingPorts, *persistedRetryState, *hookedEvaluator, *worker.SlotExecutionCoordinator) {
	t.Helper()
	trace := make([]string, 0)
	ports := &recordingPorts{trace: &trace, ready: true, extraSeries: windowedSeries("a", "b", "d", "e", "f")}
	state := &persistedRetryState{recordingPorts: ports, saved: map[execution.StateKeyIdentity]execution.StateMutation{}}
	applied := &atomic.Int64{}
	evaluator := &hookedEvaluator{recordingPorts: ports, applied: applied}
	coordinator, err := worker.NewSlotExecutionCoordinator(worker.Ports{OpenAlerts: ports, Finalization: ports, Activation: ports,
		Query: ports, Sequencer: ports, Evaluator: evaluator, Admission: ports, GapGuard: ports, NoData: worker.SharedNoDataStore,
		Hosts: worker.SharedHostBusiness, Events: ports, State: &appliedState{persistedRetryState: state, applied: applied},
		Progress: ports, ExecutionEvidence: newMemoryEvidenceStore(),
		Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {})},
		worker.ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxStateMutations: 2, MaxEvents: 100, MaxGapMutations: 10})
	if err != nil {
		t.Fatal(err)
	}
	return ports, state, evaluator, coordinator
}

// markedContext is an execution running under deadline, before its commit.
func markedContext(deadline time.Time) (context.Context, *execution.StageMarker) {
	marker := &execution.StageMarker{}
	marker.Begin(execution.SlotStageEvaluate)
	marker.Extend(deadline, deadline)
	return execution.WithStageMarker(context.Background(), marker), marker
}

// 2.2 step 6 a: a Slot applied window by window leaves its commit once a
// window's State is applied, so the watchdog can claim it between two
// windows. Claimed there, the next window writes nothing - no event, no
// State - and the attempt ends declined. The next holder's attempt resumes the
// landed window's series without sending them again, sends the rest once,
// and writes the tail and Progress.
func TestADeclineBetweenTwoWindowsWritesNothingMoreAndTheNextAttemptFinishesTheSlot(t *testing.T) {
	ports, state, evaluator, coordinator := hookedWindowedSlot(t)
	ctx, marker := markedContext(time.Now())
	claimed := false
	evaluator.hook = func() {
		_, _, claimed = marker.Decline(time.Now().Add(24*time.Hour), watchdogGrace)
	}
	request := windowedRequest()
	if _, err := coordinator.Execute(ctx, request); !errors.Is(err, execution.ErrExecutionDeclined) {
		t.Fatalf("first attempt err = %v, want it declined", err)
	}
	if !claimed {
		t.Fatal("the watchdog could not claim the Slot between two windows")
	}
	series := windowedSeries("a", "b", "c", "d", "e", "f")
	a, b, c, d, e, f := series[0], series[1], series[2], series[3], series[4], series[5]
	if got, want := sortedBatches(ports.eventBatches, 0), [][]string{{a, b}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first attempt event batches = %v, want only the window before the claim", got)
	}
	if len(state.saved) != 2 || countStage(*ports.trace, "progress_commit") != 0 {
		t.Fatalf("first attempt wrote State for %d series and %d Progress commits, want the first window's two and none",
			len(state.saved), countStage(*ports.trace, "progress_commit"))
	}

	evaluator.hook = nil
	batches := len(ports.eventBatches)
	result, err := coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed {
		t.Fatalf("next attempt result=%+v err=%v", result, err)
	}
	if got, want := sortedBatches(ports.eventBatches, batches), [][]string{{c, d}, {e, f}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("next attempt event batches = %v, want the unlanded windows once and nothing of the landed one", got)
	}
	if len(state.saved) != 6 || countStage(*ports.trace, "progress_commit") != 1 {
		t.Fatalf("after the next attempt State for %d series and %d Progress commits, want all six and its one",
			len(state.saved), countStage(*ports.trace, "progress_commit"))
	}
}

// 2.2 step 6 b: each window boundary moves the Slot's deadline a window's
// bound ahead. A Slot already past its query deadline by more than the grace
// but landing windows is not claimed between them; a window that takes longer
// than its bound and the grace after the boundary before it is.
func TestAProgressingWindowedSlotIsNotDeclinedAndAStuckWindowIs(t *testing.T) {
	for _, test := range []struct {
		name    string
		tickIn  time.Duration
		claimed bool
	}{
		{"progressing", 0, false},
		{"stuck", windowBound + watchdogGrace + time.Second, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, evaluator, coordinator := hookedWindowedSlot(t)
			// Past its own deadline by more than the grace: claimed at the
			// first tick if the boundary had not moved the deadline.
			ctx, marker := markedContext(time.Now().Add(-90 * time.Second))
			claimed := false
			evaluator.hook = func() {
				_, _, claimed = marker.Decline(time.Now().Add(test.tickIn), watchdogGrace)
			}
			result, err := coordinator.Execute(ctx, windowedRequest())
			if claimed != test.claimed {
				t.Fatalf("claimed between the windows = %v, want %v", claimed, test.claimed)
			}
			if test.claimed {
				if !errors.Is(err, execution.ErrExecutionDeclined) {
					t.Fatalf("a claimed Slot ended with %v, want it declined", err)
				}
				return
			}
			if err != nil || !result.Completed {
				t.Fatalf("a progressing Slot result=%+v err=%v, want it completed", result, err)
			}
		})
	}
}

// commitGapWatch is the watchdog at its quickest over one stretch of a
// window, from its events acknowledged to its State apply starting: it
// judges the Slot over and over on a clock a day ahead, past any deadline,
// and claims it the moment it finds it open.
type commitGapWatch struct {
	marker  *execution.StageMarker
	stop    chan struct{}
	done    chan struct{}
	claimed atomic.Bool
}

// begin starts the watch and returns once it has judged the Slot at least
// once, so it is running before the stretch it watches.
func (watch *commitGapWatch) begin() {
	if watch.stop != nil {
		return
	}
	watch.stop, watch.done = make(chan struct{}), make(chan struct{})
	judged := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(watch.done)
		far := time.Now().Add(24 * time.Hour)
		for {
			_, _, claimed := watch.marker.Decline(far, watchdogGrace)
			once.Do(func() { close(judged) })
			if claimed {
				watch.claimed.Store(true)
				return
			}
			select {
			case <-watch.stop:
				return
			default:
			}
		}
	}()
	<-judged
}

// end stops the watch and waits for it, so nothing it judges lands after.
func (watch *commitGapWatch) end() {
	if watch.stop == nil {
		return
	}
	close(watch.stop)
	<-watch.done
	watch.stop, watch.done = nil, nil
}

// watchedEvents begins the watch once a window's events are acknowledged.
type watchedEvents struct {
	*recordingPorts
	watch *commitGapWatch
}

func (events *watchedEvents) WriteBatch(ctx context.Context, batch []contract.TriggerEventV1) error {
	err := events.recordingPorts.WriteBatch(ctx, batch)
	events.watch.begin()
	return err
}

// watchedState ends the watch as a window's State apply starts, and judges
// the Slot once more from inside the apply, noting the stage it finds.
type watchedState struct {
	*persistedRetryState
	applied       atomic.Int64
	watch         *commitGapWatch
	claimedInside bool
	stages        []string
}

func (state *watchedState) ApplyRuntime(ctx context.Context, request execution.StateApplyRequest) (execution.StateApplyResult, error) {
	state.watch.end()
	if _, _, claimed := state.watch.marker.Decline(time.Now().Add(24*time.Hour), watchdogGrace); claimed {
		state.claimedInside = true
	}
	state.stages = append(state.stages, state.watch.marker.Stage())
	defer state.applied.Add(1)
	return state.persistedRetryState.ApplyRuntime(ctx, request)
}

// watchedWindowedSlot is hookedWindowedSlot with its events and State
// watched for marker.
func watchedWindowedSlot(t *testing.T, marker *execution.StageMarker) (*recordingPorts, *watchedState, *hookedEvaluator, *worker.SlotExecutionCoordinator) {
	t.Helper()
	trace := make([]string, 0)
	ports := &recordingPorts{trace: &trace, ready: true, extraSeries: windowedSeries("a", "b", "d", "e", "f")}
	watch := &commitGapWatch{marker: marker}
	state := &watchedState{watch: watch,
		persistedRetryState: &persistedRetryState{recordingPorts: ports, saved: map[execution.StateKeyIdentity]execution.StateMutation{}}}
	evaluator := &hookedEvaluator{recordingPorts: ports, applied: &state.applied}
	coordinator, err := worker.NewSlotExecutionCoordinator(worker.Ports{OpenAlerts: ports, Finalization: ports, Activation: ports,
		Query: ports, Sequencer: ports, Evaluator: evaluator, Admission: ports, GapGuard: ports, NoData: worker.SharedNoDataStore,
		Hosts: worker.SharedHostBusiness, Events: &watchedEvents{recordingPorts: ports, watch: watch}, State: state, Progress: ports,
		ExecutionEvidence: newMemoryEvidenceStore(),
		Observer:          observability.ObserverFunc(func(context.Context, observability.Observation) {})},
		worker.ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxStateMutations: 2, MaxEvents: 100, MaxGapMutations: 10})
	if err != nil {
		t.Fatal(err)
	}
	return ports, state, evaluator, coordinator
}

// 2.2 step 6, the other side of the boundary: a window is inside its commit
// from its events to the return of its State apply, so the watchdog never
// finds it open between the two. Claimed there, the window's acknowledged
// events would wait on a State write the Slot then refuses, for the next
// holder to send again. A watch judging the Slot as quickly as it can over
// that stretch of each window never claims it, nor does a judgement from
// inside the State apply, which finds the Slot in its State stage; once the
// first window's apply has returned, between two windows, the Slot is
// claimed.
//
// Nothing between the events and the State apply calls a port a fake could
// hook, so that stretch is watched from beside it, over many windows.
func TestAWindowStaysInsideItsCommitFromItsEventsUntilItsStateApplyReturns(t *testing.T) {
	const attempts = 64
	for attempt := 1; attempt <= attempts; attempt++ {
		last := attempt == attempts
		ctx, marker := markedContext(time.Now())
		ports, state, evaluator, coordinator := watchedWindowedSlot(t, marker)
		claimedBetween := false
		if last {
			evaluator.hook = func() {
				_, _, claimedBetween = marker.Decline(time.Now().Add(24*time.Hour), watchdogGrace)
			}
		}
		result, err := coordinator.Execute(ctx, windowedRequest())
		state.watch.end()
		if state.watch.claimed.Load() {
			t.Fatalf("attempt %d: the watchdog claimed the Slot between a window's events and its State apply (err %v)", attempt, err)
		}
		if state.claimedInside {
			t.Fatalf("attempt %d: the watchdog claimed the Slot inside a window's State apply", attempt)
		}
		if last {
			if !claimedBetween || !errors.Is(err, execution.ErrExecutionDeclined) {
				t.Fatalf("after the first window's apply returned: claimed=%v err=%v, want it claimed and the attempt declined",
					claimedBetween, err)
			}
			if want := []string{execution.SlotStageState}; !reflect.DeepEqual(state.stages, want) {
				t.Fatalf("stages inside the State applies = %v, want %v", state.stages, want)
			}
			continue
		}
		if err != nil || !result.Completed {
			t.Fatalf("attempt %d: result=%+v err=%v, want it completed", attempt, result, err)
		}
		if len(ports.eventBatches) != 3 {
			t.Fatalf("attempt %d: %d event batches, want one per window", attempt, len(ports.eventBatches))
		}
		want := []string{execution.SlotStageState, execution.SlotStageState, execution.SlotStageState}
		if !reflect.DeepEqual(state.stages, want) {
			t.Fatalf("attempt %d: stages inside the State applies = %v, want %v", attempt, state.stages, want)
		}
	}
}
