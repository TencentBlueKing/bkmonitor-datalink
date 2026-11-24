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
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// overlappingEvaluator is the recording evaluator, one call at a time into
// it, that says whether two evaluations were ever in flight at once. With
// wait, an evaluation first waits - up to a bound - for a second one.
type overlappingEvaluator struct {
	*recordingPorts
	wait       atomic.Bool
	mu         sync.Mutex
	inFlight   atomic.Int64
	overlap    chan struct{}
	once       sync.Once
	calls      atomic.Int64
	reading    func() int64
	besideRead atomic.Bool
	overlapped atomic.Bool
}

func (evaluator *overlappingEvaluator) Evaluate(ctx context.Context, request execution.EvaluationRequest) (execution.EvaluationResult, error) {
	if evaluator.inFlight.Add(1) >= 2 {
		evaluator.overlapped.Store(true)
		evaluator.once.Do(func() { close(evaluator.overlap) })
	}
	defer evaluator.inFlight.Add(-1)
	defer evaluator.calls.Add(1)
	if evaluator.reading != nil && evaluator.reading() > 0 {
		evaluator.besideRead.Store(true)
	}
	if evaluator.wait.Load() {
		select {
		case <-evaluator.overlap:
		case <-time.After(2 * time.Second): // fixed-wait: the bound of a wait for a second evaluation; serial evaluation never ends it otherwise
			evaluator.wait.Store(false)
		}
	}
	evaluator.mu.Lock()
	defer evaluator.mu.Unlock()
	return evaluator.recordingPorts.Evaluate(ctx, request)
}

// readCountingState is the recording State whose reads answer every key
// missing, as the first Slot of a Query Group finds them, touching nothing
// the Slot's goroutine writes: a read of the next batch runs beside it. With
// wait, every read after the first holds - up to a bound - until a series is
// evaluated while it is in flight, which only a read beside an evaluation
// sees; reading says how many reads are in flight.
type readCountingState struct {
	*persistedRetryState
	evaluated func() int64
	wait      bool
	reads     atomic.Int64
	reading   atomic.Int64
}

func (state *readCountingState) LoadRuntime(_ context.Context, request execution.StatePreflightRequest) (execution.StatePreflightResult, error) {
	state.reading.Add(1)
	defer state.reading.Add(-1)
	if state.reads.Add(1) > 1 && state.wait {
		from := state.evaluated()
		for deadline := time.Now().Add(2 * time.Second); state.evaluated() == from && time.Now().Before(deadline); { // fixed-wait: the fake goes on either way; the case reads the order, not this wait
			runtime.Gosched()
		}
	}
	items := make([]execution.RuntimeStateView, len(request.Items))
	for index, item := range request.Items {
		items[index] = execution.RuntimeStateView{Identity: item.Identity, Status: execution.StateMissingWarming}
	}
	return execution.StatePreflightResult{Items: items}, nil
}

// sideBySideRun runs one Slot of series with procs cores, and returns its
// event batches and State writes, whether two evaluations overlapped, and
// whether a series was evaluated while a preflight read was in flight.
func sideBySideRun(t *testing.T, procs int, series []string) ([][]string, map[execution.StateKeyIdentity]execution.StateMutation, bool, bool) {
	t.Helper()
	previous := runtime.GOMAXPROCS(procs)
	defer runtime.GOMAXPROCS(previous)
	trace := make([]string, 0)
	ports := &recordingPorts{trace: &trace, ready: true, extraSeries: series}
	saved := &persistedRetryState{recordingPorts: ports, saved: map[execution.StateKeyIdentity]execution.StateMutation{}}
	evaluator := &overlappingEvaluator{recordingPorts: ports, overlap: make(chan struct{})}
	state := &readCountingState{persistedRetryState: saved, evaluated: evaluator.calls.Load, wait: procs > 1}
	evaluator.reading = state.reading.Load
	evaluator.wait.Store(procs > 1)
	coordinator, err := worker.NewSlotExecutionCoordinator(worker.Ports{OpenAlerts: ports, Finalization: ports, Activation: ports,
		Query: ports, Sequencer: ports, Evaluator: evaluator, Admission: ports, GapGuard: ports, NoData: worker.SharedNoDataStore,
		Hosts: worker.SharedHostBusiness, Events: ports, State: state, Progress: ports, ExecutionEvidence: newMemoryEvidenceStore(),
		Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {})},
		worker.ProvisionalBudget{MaxSeries: 1000, MaxRetainedBytes: 64 << 20, MaxStateMutations: 1000, MaxEvents: 1000, MaxGapMutations: 10})
	if err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.Execute(context.Background(), windowedRequest())
	if err != nil || !result.Completed {
		t.Fatalf("procs %d: result=%+v err=%v", procs, result, err)
	}
	return ports.eventBatches, saved.saved, evaluator.overlapped.Load(), evaluator.besideRead.Load()
}

// A preflight batch is evaluated side by side on more than one core, and what
// the Slot writes - its event batches in their order and its State - is what
// the same Slot writes on one core, where the series are evaluated one after
// another.
func TestABatchEvaluatedSideBySideWritesWhatItWritesOnOneCore(t *testing.T) {
	series := make([]string, 0, 99)
	for index := range 99 {
		series = append(series, fmt.Sprintf("%064x", index+1))
	}
	oneBatches, oneState, oneOverlapped, _ := sideBySideRun(t, 1, series)
	if oneOverlapped {
		t.Fatal("two evaluations overlapped on one core")
	}
	fourBatches, fourState, fourOverlapped, _ := sideBySideRun(t, 4, series)
	if !fourOverlapped {
		t.Fatal("no two evaluations of a batch of a hundred series overlapped on four cores")
	}
	if !reflect.DeepEqual(fourBatches, oneBatches) {
		t.Fatalf("event batches on four cores = %v, want those on one core %v", fourBatches, oneBatches)
	}
	if !reflect.DeepEqual(fourState, oneState) {
		t.Fatalf("State on four cores differs from one core's: %d against %d writes", len(fourState), len(oneState))
	}
}

// Each full preflight batch's State read starts when the batch is full, and
// the batch before it is evaluated while it reads: on four cores series are
// evaluated beside a read, and the Slot writes what it writes on one, where
// the reads and the evaluations take turns.
func TestTheNextBatchsStateIsReadWhileTheBatchBeforeIsEvaluated(t *testing.T) {
	series := make([]string, 0, 600)
	for index := range 600 {
		series = append(series, fmt.Sprintf("%064x", index+1))
	}
	oneBatches, oneState, _, _ := sideBySideRun(t, 1, series)
	fourBatches, fourState, _, fourBeside := sideBySideRun(t, 4, series)
	if !fourBeside {
		t.Fatal("no series was evaluated while the next batch's State was read")
	}
	if !reflect.DeepEqual(fourBatches, oneBatches) || !reflect.DeepEqual(fourState, oneState) {
		t.Fatalf("four cores wrote %d event batches and %d States, one core %d and %d, or in another order",
			len(fourBatches), len(fourState), len(oneBatches), len(oneState))
	}
}
