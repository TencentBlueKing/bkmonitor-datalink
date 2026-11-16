// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// seriesEvaluation is one series of a preflight batch prepared for its
// evaluation: the request, made in the batch's order on the Slot's goroutine,
// and once evaluated (evaluateSideBySide) what the evaluation came to.
type seriesEvaluation struct {
	entry      completedSeries
	request    execution.EvaluationRequest
	stateItems []execution.StatePreflightItem
	loaded     execution.StatePreflightResult
	// incomplete is the inputs' bindings the guard reads as incomplete; a
	// series with any is validated after its marker is set, on the Slot's
	// goroutine (finishSeriesEvaluation).
	incomplete []execution.NamedInputBinding

	evaluated execution.EvaluationResult
	// err is the evaluation's own failure; invalid is a result that failed
	// its validation, checked beside the evaluation when no marker is to be
	// set first.
	err, invalid error
	validated    bool
	took         time.Duration
}

// mergeResumedSeries merges a series this attempt finds already written at
// this Slot without evaluating it again.
func (stream *streamedExecution) mergeResumedSeries(ctx context.Context, entry completedSeries, view execution.RuntimeStateView) error {
	defer stream.enter(slotPhaseSeries)()
	due := entry.due
	resumed, gapSuperseded, err := resumedSeriesResult(stream.header, due, view, stream.gaps)
	if err != nil {
		return err
	}
	// Its key was loaded, and its witness is held as any loaded key's is.
	if err := stream.mergeMeasured(ctx, resumed, stream.measureMerge(resumed, 0, loadedKeyHeldBytes)); err != nil {
		return err
	}
	stream.noteResumed(due.Identity)
	if gapSuperseded {
		stream.noteSupersededGap(ctx, due.GapIdentity())
	}
	stream.coordinator.observe(ctx, observability.ComponentState, observability.StageMutationCompared,
		stream.request.Operation, time.Now(), observability.ResultSuccess,
		observability.ReasonStateAlreadyAppliedBeforeEvaluation, nil)
	return nil
}

// prepareSeriesEvaluation makes one series' request, on the Slot's goroutine
// and in the batch's order: the effective time at the decision, the record
// ids its window reads, and the open alert set. A window the record ids would
// overfill is cut as before, after settle has evaluated and merged every
// series prepared before this one: the cut lands the series before it.
func (stream *streamedExecution) prepareSeriesEvaluation(
	ctx context.Context, entry completedSeries, view execution.RuntimeStateView, settle func() error,
) (*seriesEvaluation, error) {
	defer stream.enter(slotPhaseSeries)()
	due, series, inputs := entry.due, entry.series, entry.inputs
	job := &seriesEvaluation{entry: entry, stateItems: []execution.StatePreflightItem{entry.item},
		loaded: execution.StatePreflightResult{Items: []execution.RuntimeStateView{view}}}
	// The effective time is judged at the decision: the facts prepared when
	// the Slot began are judged again only if a window boundary has passed
	// since, which is when they stop holding.
	decision := stream.coordinator.now().Unix()
	if !effectiveFactsHoldAt(stream.effective, decision) {
		effective, err := PrepareEffectiveTimeFacts(ctx, stream.header, stream.coordinator.ports.EffectiveTime, decision)
		if err != nil {
			return nil, fmt.Errorf("alarmd worker: prepare EffectiveTime facts at the decision: %w", err)
		}
		stream.effective = effective
	}
	evaluationHeader, err := bindAlwaysEffectiveTimeFacts(stream.header, job.stateItems, stream.effective, entry.kind())
	if err != nil {
		return nil, fmt.Errorf("alarmd worker: bind series EffectiveTime facts: %w", err)
	}
	evaluationHeader.DecisionTime = decision
	// The window that evaluates the series is the one that reads its ids:
	// derived here, once a point, and gone when the window lands. A cut
	// needs every series before this one merged, and this one's Plan in
	// progress.
	if stream.recordIDsWouldCut(inputs) {
		if err := settle(); err != nil {
			return nil, err
		}
		stream.beginEvaluating(due.Identity)
	}
	if err := stream.materializeRecordIDs(ctx, entry.position, inputs); err != nil {
		return nil, err
	}
	job.request = execution.EvaluationRequest{Header: evaluationHeader, Inputs: inputs, State: job.loaded, Gaps: stream.gaps,
		HostOverlay: stream.hostOverlays[due.Identity][series], NamedOutputs: stream.primaryNamedOutputs(inputs)}
	// Told before the evaluation, so the strategy is in the copy's next read
	// even if no record of this round asks about it. The constructor requires
	// the port; the guard is for tests that build the struct.
	if openAlerts := stream.coordinator.ports.OpenAlerts; openAlerts != nil {
		openAlerts.TrackPlans(stream.header.Contract.Slot.QueryGroup, []execution.PlanIdentity{due.Identity})
		job.request.OpenAlerts = openAlerts
		if entry.closing {
			job.request.OpenAlerts = oneTimeClose{}
		}
	}
	for _, input := range inputs {
		for _, binding := range input.Inputs {
			// The same predicate the fold reads, so the marker proposed here
			// covers exactly the Levels whose outcomes will name its reason.
			if execution.InputIncompleteForGuard(binding) {
				job.incomplete = append(job.incomplete, binding)
			}
		}
	}
	return job, nil
}

// evaluateSideBySide evaluates the prepared series on up to GOMAXPROCS
// goroutines, then finishes and merges each in the order they were prepared.
// An evaluation reads only its request: the Evaluator already runs for many
// Slots at once, the open alert set is a lookup in memory, and the stage
// marker is atomic. What a series' result does to the Slot - the marker its
// incomplete inputs set, the observation, the merge and the window it may
// cut - happens here, one series after another, as it did when each series
// was evaluated alone: the windows, the events and the State come out the
// same. The first series that fails ends the batch, as before; the results
// after it are not merged.
func (stream *streamedExecution) evaluateSideBySide(ctx context.Context, jobs []*seriesEvaluation) error {
	if len(jobs) == 0 {
		return nil
	}
	leave := stream.enter(slotPhaseEvaluate)
	execution.MarkStage(ctx, execution.SlotStageEvaluate)
	evaluator := stream.coordinator.ports.Evaluator
	evaluate := func(job *seriesEvaluation) {
		started := time.Now()
		job.evaluated, job.err = evaluator.Evaluate(ctx, job.request)
		if job.err == nil && len(job.incomplete) == 0 {
			job.invalid, job.validated = job.evaluated.Validate(job.request), true
		}
		job.took = time.Since(started)
	}
	// No floor on the batch: the fan-out costs a few microseconds a batch
	// (BenchmarkSideBySideFanOut), a series' evaluation about 139 on the
	// slowest node measured (decision-020 throughput design 3.1), so even a
	// batch of two gains from a second core.
	if recovered := sideBySide(len(jobs), runtime.GOMAXPROCS(0), func(index int) { evaluate(jobs[index]) }); recovered != nil {
		// A panic in an evaluation is raised again on the Slot's goroutine,
		// as an evaluation's panic always was: the process ends, and the
		// value prints the evaluation's stack.
		leave()
		panic(*recovered)
	}
	leave()
	for _, job := range jobs {
		if err := stream.finishSeriesEvaluation(ctx, job); err != nil {
			return err
		}
	}
	return nil
}

// finishSeriesEvaluation is what follows one series' evaluation, on the
// Slot's goroutine: the marker its incomplete inputs set, the validation,
// the observation and the merge.
func (stream *streamedExecution) finishSeriesEvaluation(ctx context.Context, job *seriesEvaluation) error {
	defer stream.enter(slotPhaseSeries)()
	due, series, inputs := job.entry.due, job.entry.series, job.request.Inputs
	stream.beginEvaluating(due.Identity)
	// Its own time, not the time it waited for the others.
	started := time.Now().Add(-job.took)
	if job.err != nil {
		stream.observeEvaluationFailure(ctx, started, due, job.err)
		return wrapEvaluationError(codeEvaluationFailed, fmt.Errorf("alarmd worker: evaluate series: %w", job.err))
	}
	evaluated := job.evaluated
	if len(job.incomplete) != 0 {
		if len(evaluated.Plans) != 1 || evaluated.Plans[0].Plan != due.Identity {
			return errors.New("alarmd worker: incomplete named input produced an invalid Plan result")
		}
		mutation, mutationErr := stream.completionGapMutationFor(due, job.incomplete)
		if mutationErr != nil {
			return mutationErr
		}
		evaluated.Plans[0].GuardBeforeEvents = []execution.PlanGapMutation{mutation}
		evaluated.Plans[0].GuardAfterState = nil
	}
	invalid := job.invalid
	if !job.validated {
		invalid = evaluated.Validate(job.request)
	}
	if invalid != nil {
		stream.observeEvaluationFailure(ctx, started, due, invalid)
		return wrapEvaluationError(codeEvaluationResultInvalid, fmt.Errorf("alarmd worker: invalid series evaluation: %w", invalid))
	}
	stream.observeEvaluationCompleted(ctx, started, due, series, inputs, evaluated)
	retained, err := evaluationRetainedSize(job.loaded, execution.EvaluationResult{})
	if err != nil {
		return fmt.Errorf("alarmd worker: measure evaluated retention: %w", err)
	}
	if stream.supplement != nil {
		stream.noteSupplementEvaluated(due, series, inputs, &evaluated)
	}
	return stream.mergeEvaluatedSeries(ctx, job.entry.position, evaluated, retained, job.loaded.Items, job.stateItems)
}

// sideBySidePanic is a panic one of sideBySide's goroutines raised, with the
// stack it was raised on: raised again on the Slot's goroutine, the value
// prints both, so the line that panicked can still be read.
type sideBySidePanic struct {
	value any
	stack []byte
}

func (recovered *sideBySidePanic) Error() string {
	return fmt.Sprintf("%v\n\ngoroutine of the side-by-side evaluation:\n%s", recovered.value, recovered.stack)
}

// sideBySide runs do for every index below n on up to workers goroutines, or
// on this one for a single worker, and returns the first panic one raised -
// a *sideBySidePanic with the stack it was raised on - after which no index
// is started. It returns once every do has returned.
func sideBySide(n, workers int, do func(index int)) *any {
	workers = min(workers, n)
	if workers <= 1 {
		for index := range n {
			do(index)
		}
		return nil
	}
	var next atomic.Int64
	var wait sync.WaitGroup
	var panicked atomic.Pointer[any]
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			defer func() {
				if recovered := recover(); recovered != nil {
					var wrapped any = &sideBySidePanic{value: recovered, stack: debug.Stack()}
					panicked.CompareAndSwap(nil, &wrapped)
				}
			}()
			for index := next.Add(1) - 1; index < int64(n) && panicked.Load() == nil; index = next.Add(1) - 1 {
				do(int(index))
			}
		}()
	}
	wait.Wait()
	return panicked.Load()
}
