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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

func TestNewKeyedSideEffectSequencerRequiresPositiveProcessCapacity(t *testing.T) {
	t.Parallel()

	if _, err := worker.NewKeyedSideEffectSequencer(0); err == nil {
		t.Fatal("NewKeyedSideEffectSequencer(0) error = nil, want validation error")
	}
}

func TestKeyedSideEffectSequencerSerializesSameStateKey(t *testing.T) {
	t.Parallel()

	sequencer := mustSequencer(t, 2)
	state := stateKey("1", "a")
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := runSequence(sequencer, sequencingScope(100, []execution.StateKeyIdentity{state}, nil), func(context.Context) error {
		close(firstEntered)
		<-releaseFirst
		return nil
	})
	waitSignal(t, firstEntered)

	secondEntered := make(chan struct{})
	secondDone := runSequence(sequencer, sequencingScope(101, []execution.StateKeyIdentity{state}, nil), func(context.Context) error {
		close(secondEntered)
		return nil
	})
	assertNoSignal(t, secondEntered)
	close(releaseFirst)
	waitNoError(t, firstDone)
	waitSignal(t, secondEntered)
	waitNoError(t, secondDone)
}

func TestKeyedSideEffectSequencerRunsDisjointKeysConcurrently(t *testing.T) {
	t.Parallel()

	sequencer := mustSequencer(t, 2)
	release := make(chan struct{})
	firstEntered := make(chan struct{})
	secondEntered := make(chan struct{})
	firstDone := runSequence(sequencer, sequencingScope(100, []execution.StateKeyIdentity{stateKey("1", "a")}, nil), func(context.Context) error {
		close(firstEntered)
		<-release
		return nil
	})
	secondDone := runSequence(sequencer, sequencingScope(100, []execution.StateKeyIdentity{stateKey("2", "b")}, nil), func(context.Context) error {
		close(secondEntered)
		<-release
		return nil
	})
	waitSignal(t, firstEntered)
	waitSignal(t, secondEntered)
	close(release)
	waitNoError(t, firstDone)
	waitNoError(t, secondDone)
}

func TestKeyedSideEffectSequencerAcquiresMultipleKeysWithoutDeadlock(t *testing.T) {
	t.Parallel()

	sequencer := mustSequencer(t, 2)
	left := stateKey("1", "a")
	right := stateKey("2", "b")
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := runSequence(sequencer, sequencingScope(100, []execution.StateKeyIdentity{left, right}, nil), func(context.Context) error {
		close(firstEntered)
		<-releaseFirst
		return nil
	})
	waitSignal(t, firstEntered)

	secondEntered := make(chan struct{})
	secondDone := runSequence(sequencer, sequencingScope(101, []execution.StateKeyIdentity{right, left}, nil), func(context.Context) error {
		close(secondEntered)
		return nil
	})
	assertNoSignal(t, secondEntered)
	close(releaseFirst)
	waitNoError(t, firstDone)
	waitSignal(t, secondEntered)
	waitNoError(t, secondDone)
}

func TestKeyedSideEffectSequencerUsesOneProcessCapacity(t *testing.T) {
	t.Parallel()

	sequencer := mustSequencer(t, 2)
	release := make(chan struct{})
	firstEntered := make(chan struct{})
	secondEntered := make(chan struct{})
	firstDone := runSequence(sequencer, sequencingScope(100, []execution.StateKeyIdentity{stateKey("1", "a")}, nil), blockingRun(firstEntered, release))
	secondDone := runSequence(sequencer, sequencingScope(100, nil, []execution.PlanGapIdentity{gapKey("2")}), blockingRun(secondEntered, release))
	waitSignal(t, firstEntered)
	waitSignal(t, secondEntered)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	thirdEntered := make(chan struct{})
	err := sequencer.Sequence(ctx, sequencingScope(100, []execution.StateKeyIdentity{stateKey("3", "c")}, nil), func(context.Context) error {
		close(thirdEntered)
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Sequence() error = %v, want context deadline while process capacity is full", err)
	}
	assertChannelOpen(t, thirdEntered)
	close(release)
	waitNoError(t, firstDone)
	waitNoError(t, secondDone)
}

func TestKeyedSideEffectSequencerCountsWaitingReservationsAgainstProcessCapacity(t *testing.T) {
	t.Parallel()

	sequencer := mustSequencer(t, 2)
	shared := stateKey("1", "a")
	release := make(chan struct{})
	firstEntered := make(chan struct{})
	firstDone := runSequence(sequencer, sequencingScope(100, []execution.StateKeyIdentity{shared}, nil), blockingRun(firstEntered, release))
	waitSignal(t, firstEntered)

	secondEntered := make(chan struct{})
	secondDone := runSequence(sequencer, sequencingScope(101, []execution.StateKeyIdentity{shared}, nil), func(context.Context) error {
		close(secondEntered)
		return nil
	})
	assertNoSignal(t, secondEntered)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	thirdEntered := make(chan struct{})
	err := sequencer.Sequence(ctx, sequencingScope(100, []execution.StateKeyIdentity{stateKey("2", "b")}, nil), func(context.Context) error {
		close(thirdEntered)
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Sequence() error = %v, want context deadline while active and waiting reservations fill capacity", err)
	}
	assertChannelOpen(t, thirdEntered)

	close(release)
	waitNoError(t, firstDone)
	waitSignal(t, secondEntered)
	waitNoError(t, secondDone)
}

func TestKeyedSideEffectSequencerCancellationAndRunErrorReleaseResources(t *testing.T) {
	t.Parallel()

	sequencer := mustSequencer(t, 2)
	state := stateKey("1", "a")
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := runSequence(sequencer, sequencingScope(100, []execution.StateKeyIdentity{state}, nil), blockingRun(firstEntered, releaseFirst))
	waitSignal(t, firstEntered)

	canceledCtx, cancel := context.WithCancel(context.Background())
	canceledDone := make(chan error, 1)
	go func() {
		canceledDone <- sequencer.Sequence(canceledCtx, sequencingScope(101, []execution.StateKeyIdentity{state}, nil), func(context.Context) error {
			return errors.New("canceled callback must not run")
		})
	}()
	cancel()
	if err := waitError(t, canceledDone); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Sequence() error = %v, want context.Canceled", err)
	}
	if err := sequencer.Sequence(context.Background(), sequencingScope(101, []execution.StateKeyIdentity{stateKey("2", "b")}, nil), func(context.Context) error {
		return nil
	}); err != nil {
		t.Fatalf("disjoint Sequence() after waiting cancellation = %v", err)
	}

	close(releaseFirst)
	waitNoError(t, firstDone)
	want := errors.New("injected side effect error")
	if err := sequencer.Sequence(context.Background(), sequencingScope(102, []execution.StateKeyIdentity{state}, nil), func(context.Context) error {
		return want
	}); !errors.Is(err, want) {
		t.Fatalf("Sequence() error = %v, want %v", err, want)
	}
	if err := sequencer.Sequence(context.Background(), sequencingScope(103, []execution.StateKeyIdentity{state}, nil), func(context.Context) error {
		return nil
	}); err != nil {
		t.Fatalf("Sequence() after cancellation and callback error = %v", err)
	}
}

func TestKeyedSideEffectSequencerSerializesPlanGapKey(t *testing.T) {
	t.Parallel()

	sequencer := mustSequencer(t, 2)
	gap := gapKey("1")
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := runSequence(sequencer, sequencingScope(100, nil, []execution.PlanGapIdentity{gap}), blockingRun(firstEntered, releaseFirst))
	waitSignal(t, firstEntered)

	secondEntered := make(chan struct{})
	secondDone := runSequence(sequencer, sequencingScope(101, nil, []execution.PlanGapIdentity{gap}), func(context.Context) error {
		close(secondEntered)
		return nil
	})
	assertNoSignal(t, secondEntered)
	close(releaseFirst)
	waitNoError(t, firstDone)
	waitSignal(t, secondEntered)
	waitNoError(t, secondDone)
}

func TestKeyedSideEffectSequencerRejectsInvalidScopeWithoutRunning(t *testing.T) {
	t.Parallel()

	sequencer := mustSequencer(t, 1)
	called := false
	err := sequencer.Sequence(context.Background(), execution.SequencingScope{}, func(context.Context) error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("Sequence(invalid scope) error = nil, want validation error")
	}
	if called {
		t.Fatal("invalid scope ran side effects")
	}
}

func mustSequencer(t *testing.T, capacity int) *worker.KeyedSideEffectSequencer {
	t.Helper()
	sequencer, err := worker.NewKeyedSideEffectSequencer(capacity)
	if err != nil {
		t.Fatal(err)
	}
	return sequencer
}

func sequencingScope(evaluationTime int64, states []execution.StateKeyIdentity, gaps []execution.PlanGapIdentity) execution.SequencingScope {
	return execution.SequencingScope{
		Slot: execution.SlotIdentity{
			QueryGroup: "query-group-1", EvaluationTime: execution.EvaluationTime(evaluationTime),
		},
		StateKeys: states,
		GapKeys:   gaps,
	}
}

func stateKey(strategyID, series string) execution.StateKeyIdentity {
	return execution.StateKeyIdentity{
		Plan:            execution.PlanIdentity{TenantID: "tenant", BusinessID: "business", StrategyID: strategyID},
		StateGeneration: "state-v1", SeriesIdentityDigest: execution.SeriesIdentityDigest(series),
	}
}

func gapKey(strategyID string) execution.PlanGapIdentity {
	return execution.PlanGapIdentity{
		Plan:            execution.PlanIdentity{TenantID: "tenant", BusinessID: "business", StrategyID: strategyID},
		StateGeneration: "state-v1",
	}
}

func runSequence(
	sequencer *worker.KeyedSideEffectSequencer,
	scope execution.SequencingScope,
	run func(context.Context) error,
) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- sequencer.Sequence(context.Background(), scope, run)
	}()
	return done
}

func blockingRun(entered chan<- struct{}, release <-chan struct{}) func(context.Context) error {
	return func(context.Context) error {
		close(entered)
		<-release
		return nil
	}
}

func waitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for sequence entry")
	}
}

func assertNoSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
		t.Fatal("sequence entered before conflicting keys were released")
	case <-time.After(50 * time.Millisecond):
	}
}

func assertChannelOpen(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
		t.Fatal("callback ran despite full process capacity")
	default:
	}
}

func waitNoError(t *testing.T, done <-chan error) {
	t.Helper()
	if err := waitError(t, done); err != nil {
		t.Fatal(err)
	}
}

func waitError(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for sequence completion")
		return nil
	}
}
