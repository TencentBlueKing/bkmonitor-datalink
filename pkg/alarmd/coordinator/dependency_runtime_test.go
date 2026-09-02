// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package coordinator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestRetryingEffectiveTimeProviderRetriesOnlyMarkedDependencyErrors(t *testing.T) {
	t.Parallel()

	root := errors.New("calendar source unavailable")
	provider := &effectiveTimeProviderStub{resolve: func(call int) ([]strategy.EffectiveTimeFact, error) {
		if call == 1 {
			return nil, &retryableEffectiveTimeStubError{err: root}
		}
		return []strategy.EffectiveTimeFact{{}}, nil
	}}
	transitions := make([]DependencyGateTransition, 0, 2)
	gate := NewCriticalDependencyGate(func(transition DependencyGateTransition) {
		transitions = append(transitions, transition)
	})
	retrying, err := NewRetryingEffectiveTimeProvider(provider, gate, immediateDependencyRetryConfig())
	if err != nil {
		t.Fatal(err)
	}
	facts, err := retrying.Resolve(context.Background(), nil)
	if err != nil || len(facts) != 1 {
		t.Fatalf("Resolve() = (%+v, %v), want one fact", facts, err)
	}
	if provider.calls != 2 || len(transitions) != 2 {
		t.Fatalf("calls=%d transitions=%+v, want one retry with pause/resume", provider.calls, transitions)
	}
	if blocker := transitions[0].Current.Blockers; len(blocker) != 1 ||
		blocker[0].Dependency != DependencyProvider || blocker[0].ReasonCode != contract.ReasonProviderUnavailable {
		t.Fatalf("pause blockers = %+v, want provider unavailable", blocker)
	}
	if !gate.Ready() {
		t.Fatalf("gate snapshot = %+v, want ready after provider recovery", gate.Snapshot())
	}
}

func TestRetryingEffectiveTimeProviderDoesNotRetryUnknownOrLocalErrors(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "unknown", err: strategy.ErrEffectiveTimeUnknown},
		{name: "local", err: errors.New("invalid provider response")},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := &effectiveTimeProviderStub{resolve: func(int) ([]strategy.EffectiveTimeFact, error) {
				return nil, test.err
			}}
			gate := NewCriticalDependencyGate(nil)
			retrying, err := NewRetryingEffectiveTimeProvider(provider, gate, immediateDependencyRetryConfig())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := retrying.Resolve(context.Background(), nil); err != test.err {
				t.Fatalf("Resolve() error = %v, want exact %v", err, test.err)
			}
			if provider.calls != 1 || !gate.Ready() {
				t.Fatalf("calls=%d gate=%+v, want no retry and ready gate", provider.calls, gate.Snapshot())
			}
		})
	}
}

func TestRetryingRuntimeStateRetriesOnlyMatchingDependencyOperations(t *testing.T) {
	t.Parallel()

	loadRoot := errors.New("redis load unavailable")
	writeRoot := errors.New("redis write unavailable")
	store := &runtimeStateStoreStub{
		load: func(call int) (state.LoadWindowsResult, error) {
			if call == 1 {
				return state.LoadWindowsResult{}, &state.DependencyError{
					Operation: state.DependencyOperationLoad, Target: "monitor-01", Err: loadRoot,
				}
			}
			return state.LoadWindowsResult{Items: []state.LoadedWindow{{}}}, nil
		},
		write: func(call int) (state.WriteWindowsResult, error) {
			if call == 1 {
				return state.WriteWindowsResult{}, &state.DependencyError{
					Operation: state.DependencyOperationWrite, Target: "monitor-01", Err: writeRoot,
				}
			}
			return state.WriteWindowsResult{Items: []state.WriteWindowResult{{Status: state.WritePersisted}}}, nil
		},
	}
	gate := NewCriticalDependencyGate(nil)
	runtimeState, err := NewRetryingRuntimeState(store, gate, immediateDependencyRetryConfig())
	if err != nil {
		t.Fatal(err)
	}
	if result, err := runtimeState.LoadWindows(context.Background(), state.LoadWindowsRequest{}); err != nil || len(result.Items) != 1 {
		t.Fatalf("LoadWindows() = (%+v, %v)", result, err)
	}
	if result, err := runtimeState.WriteWindows(context.Background(), state.WriteWindowsRequest{}); err != nil || len(result.Items) != 1 {
		t.Fatalf("WriteWindows() = (%+v, %v)", result, err)
	}
	if store.loadCalls != 2 || store.writeCalls != 2 {
		t.Fatalf("calls = load:%d write:%d, want 2 each", store.loadCalls, store.writeCalls)
	}
	if snapshot := gate.Snapshot(); snapshot.State != DependencyGateReady || len(snapshot.Blockers) != 0 {
		t.Fatalf("gate snapshot = %+v, want ready", snapshot)
	}
}

func TestRetryingRuntimeStateReturnsMismatchedAndOrdinaryErrorsUnchanged(t *testing.T) {
	t.Parallel()

	mismatched := &state.DependencyError{
		Operation: state.DependencyOperationWrite, Target: "monitor-01", Err: errors.New("wrong operation"),
	}
	ordinary := errors.New("state budget exceeded")
	store := &runtimeStateStoreStub{
		load: func(int) (state.LoadWindowsResult, error) {
			return state.LoadWindowsResult{}, mismatched
		},
		write: func(int) (state.WriteWindowsResult, error) {
			return state.WriteWindowsResult{}, ordinary
		},
	}
	gate := NewCriticalDependencyGate(nil)
	runtimeState, err := NewRetryingRuntimeState(store, gate, immediateDependencyRetryConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtimeState.LoadWindows(context.Background(), state.LoadWindowsRequest{}); err != mismatched {
		t.Fatalf("LoadWindows() error = %v, want exact mismatched dependency error", err)
	}
	if _, err := runtimeState.WriteWindows(context.Background(), state.WriteWindowsRequest{}); err != ordinary {
		t.Fatalf("WriteWindows() error = %v, want exact ordinary error", err)
	}
	if store.loadCalls != 1 || store.writeCalls != 1 {
		t.Fatalf("calls = load:%d write:%d, want one each", store.loadCalls, store.writeCalls)
	}
	if !gate.Ready() {
		t.Fatalf("gate snapshot = %+v, want ready", gate.Snapshot())
	}
}

func TestRetryingRuntimeStateCancellationKeepsDependencyRootCause(t *testing.T) {
	t.Parallel()

	root := errors.New("redis load interrupted")
	ctx, cancel := context.WithCancel(context.Background())
	store := &runtimeStateStoreStub{load: func(int) (state.LoadWindowsResult, error) {
		cancel()
		return state.LoadWindowsResult{}, &state.DependencyError{
			Operation: state.DependencyOperationLoad, Target: "monitor-01", Err: root,
		}
	}}
	runtimeState, err := NewRetryingRuntimeState(store, NewCriticalDependencyGate(nil), immediateDependencyRetryConfig())
	if err != nil {
		t.Fatal(err)
	}
	_, err = runtimeState.LoadWindows(ctx, state.LoadWindowsRequest{})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, root) {
		t.Fatalf("LoadWindows() error = %v, want cancellation and dependency root cause", err)
	}
}

func TestRetryingCriticalPhaseCompletionRetriesOutputAndStateDependencies(t *testing.T) {
	t.Parallel()

	outputRoot := errors.New("broker ACK unknown")
	stateRoot := errors.New("redis write unavailable")
	completion := &criticalPhaseCompletionStub{
		events: func(call int) error {
			if call == 1 {
				return &retryableOutputStubError{err: outputRoot}
			}
			return nil
		},
		state: func(call int) error {
			if call == 1 {
				return &state.DependencyError{Operation: state.DependencyOperationWrite, Target: "monitor-01", Err: stateRoot}
			}
			return nil
		},
	}
	retrying, err := NewRetryingCriticalPhaseCompletion(
		completion, NewCriticalDependencyGate(nil), immediateDependencyRetryConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := retrying.CompleteEvents(context.Background(), []contract.TriggerEventV1{{}}); err != nil {
		t.Fatal(err)
	}
	if err := retrying.CompleteState(context.Background(), state.WriteWindowsRequest{}); err != nil {
		t.Fatal(err)
	}
	if completion.eventCalls != 2 || completion.stateCalls != 2 {
		t.Fatalf("calls = events:%d state:%d, want 2 each", completion.eventCalls, completion.stateCalls)
	}
}

func TestRetryingCriticalPhaseCompletionDoesNotResendACKedEventsDuringStateRecovery(t *testing.T) {
	t.Parallel()

	completion := &criticalPhaseCompletionStub{
		events: func(int) error { return nil },
		state: func(call int) error {
			if call == 1 {
				return &state.DependencyError{
					Operation: state.DependencyOperationWrite,
					Target:    "monitor-01",
					Err:       errors.New("redis write unavailable"),
				}
			}
			return nil
		},
	}
	retrying, err := NewRetryingCriticalPhaseCompletion(
		completion, NewCriticalDependencyGate(nil), immediateDependencyRetryConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := retrying.CompleteEvents(context.Background(), []contract.TriggerEventV1{{}}); err != nil {
		t.Fatal(err)
	}
	if err := retrying.CompleteState(context.Background(), state.WriteWindowsRequest{}); err != nil {
		t.Fatal(err)
	}
	if completion.eventCalls != 1 || completion.stateCalls != 2 {
		t.Fatalf("calls = events:%d state:%d, want ACKed events once and state twice", completion.eventCalls, completion.stateCalls)
	}
}

func TestRetryingCriticalPhaseCompletionDoesNotRetryLocalOrMismatchedErrors(t *testing.T) {
	t.Parallel()

	local := errors.New("encode contract event")
	mismatched := &state.DependencyError{
		Operation: state.DependencyOperationLoad, Target: "monitor-01", Err: errors.New("wrong phase"),
	}
	completion := &criticalPhaseCompletionStub{
		events: func(int) error { return local },
		state:  func(int) error { return mismatched },
	}
	retrying, err := NewRetryingCriticalPhaseCompletion(
		completion, NewCriticalDependencyGate(nil), immediateDependencyRetryConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := retrying.CompleteEvents(context.Background(), nil); err != local {
		t.Fatalf("CompleteEvents() error = %v, want exact local error", err)
	}
	if err := retrying.CompleteState(context.Background(), state.WriteWindowsRequest{}); err != mismatched {
		t.Fatalf("CompleteState() error = %v, want exact mismatched dependency error", err)
	}
	if completion.eventCalls != 1 || completion.stateCalls != 1 {
		t.Fatalf("calls = events:%d state:%d, want one each", completion.eventCalls, completion.stateCalls)
	}
}

func TestRetryingCriticalPhaseCancellationKeepsOutputRootCause(t *testing.T) {
	t.Parallel()

	root := errors.New("broker connection closed")
	ctx, cancel := context.WithCancel(context.Background())
	completion := &criticalPhaseCompletionStub{events: func(int) error {
		cancel()
		return &retryableOutputStubError{err: root}
	}}
	retrying, err := NewRetryingCriticalPhaseCompletion(
		completion, NewCriticalDependencyGate(nil), immediateDependencyRetryConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	err = retrying.CompleteEvents(ctx, nil)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, root) {
		t.Fatalf("CompleteEvents() error = %v, want cancellation and output root cause", err)
	}
}

func TestRetryingCriticalPhaseCancellationKeepsStateRootCause(t *testing.T) {
	t.Parallel()

	root := errors.New("redis write interrupted")
	ctx, cancel := context.WithCancel(context.Background())
	completion := &criticalPhaseCompletionStub{state: func(int) error {
		cancel()
		return &state.DependencyError{Operation: state.DependencyOperationWrite, Target: "monitor-01", Err: root}
	}}
	retrying, err := NewRetryingCriticalPhaseCompletion(
		completion, NewCriticalDependencyGate(nil), immediateDependencyRetryConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	err = retrying.CompleteState(ctx, state.WriteWindowsRequest{})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, root) {
		t.Fatalf("CompleteState() error = %v, want cancellation and state root cause", err)
	}
}

func immediateDependencyRetryConfig() DependencyRetryConfig {
	return DependencyRetryConfig{MinDelay: time.Nanosecond, MaxDelay: time.Nanosecond}
}

type runtimeStateStoreStub struct {
	load       func(int) (state.LoadWindowsResult, error)
	write      func(int) (state.WriteWindowsResult, error)
	loadCalls  int
	writeCalls int
}

func (stub *runtimeStateStoreStub) LoadWindows(context.Context, state.LoadWindowsRequest) (state.LoadWindowsResult, error) {
	stub.loadCalls++
	return stub.load(stub.loadCalls)
}

func (stub *runtimeStateStoreStub) WriteWindows(context.Context, state.WriteWindowsRequest) (state.WriteWindowsResult, error) {
	stub.writeCalls++
	return stub.write(stub.writeCalls)
}

type criticalPhaseCompletionStub struct {
	events     func(int) error
	state      func(int) error
	eventCalls int
	stateCalls int
}

func (stub *criticalPhaseCompletionStub) CompleteEvents(context.Context, []contract.TriggerEventV1) error {
	stub.eventCalls++
	return stub.events(stub.eventCalls)
}

func (stub *criticalPhaseCompletionStub) CompleteState(context.Context, state.WriteWindowsRequest) error {
	stub.stateCalls++
	return stub.state(stub.stateCalls)
}

type retryableOutputStubError struct {
	err error
}

func (err *retryableOutputStubError) Error() string              { return err.err.Error() }
func (err *retryableOutputStubError) Unwrap() error              { return err.err }
func (err *retryableOutputStubError) RetryableOutputDependency() {}

type effectiveTimeProviderStub struct {
	resolve func(int) ([]strategy.EffectiveTimeFact, error)
	calls   int
}

func (stub *effectiveTimeProviderStub) Resolve(
	context.Context, []strategy.EffectiveTimeRequest,
) ([]strategy.EffectiveTimeFact, error) {
	stub.calls++
	return stub.resolve(stub.calls)
}

type retryableEffectiveTimeStubError struct {
	err error
}

func (err *retryableEffectiveTimeStubError) Error() string                     { return err.err.Error() }
func (err *retryableEffectiveTimeStubError) Unwrap() error                     { return err.err }
func (err *retryableEffectiveTimeStubError) RetryableEffectiveTimeDependency() {}
