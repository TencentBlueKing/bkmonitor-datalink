package main

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A completion line that reports GAP_SKIPPED carries the cause field, whichever
// of its two fields carried the word.
//
// The line reports the reason. The cause was attached by looking at the
// completion kind, and the two do not have to agree: a Slot that ran - queried,
// evaluated, wrote its state - and then found its cursor moved on reports
// GAP_SKIPPED through the reason while the kind holds whatever the run
// produced. That population is the one the field was added for, sixty seconds
// and slower, skipping a Slot every round; and it was the one population the
// field was never attached to.
//
// An absent cause on exactly the lines it exists to explain is worse than no
// field: the absence cannot be told from a build that does not report it, so
// the reading is not "nothing held this round" but "no reading at all".
func TestAGapSkippedCompletionCarriesItsCauseWhenOnlyTheReasonSaysSo(t *testing.T) {
	var observations []observability.Observation
	executor := observedProductionSlotExecutor{
		next: slotExecutorFunc(func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
			// The shape under test: the run completed and its kind says so,
			// while the reason the line reports is GAP_SKIPPED.
			return execution.SlotExecutionResult{
				Completed: true, CompletionKind: execution.CompletionFull,
				Result:     observability.ResultDegraded,
				ReasonCode: execution.ReasonCode(contract.ReasonGapSkipped),
			}, nil
		}),
		observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observations = append(observations, observation)
		}),
	}
	if _, err := executor.Execute(context.Background(), execution.SlotExecutionRequest{}); err != nil {
		t.Fatalf("Execute() error=%v", err)
	}
	if len(observations) != 2 || observations[1].Stage != observability.StageSlotCompleted {
		t.Fatalf("observations=%+v, want a started line and a completion line", observations)
	}
	completion := observations[1]
	if completion.ReasonCode != observability.ReasonCode(contract.ReasonGapSkipped) {
		t.Fatalf("the completion reports %q; this case is about the line that says GAP_SKIPPED",
			completion.ReasonCode)
	}
	if completion.HeldBy == nil {
		t.Fatal("a GAP_SKIPPED completion line carries no held_by, so the Query Group reports that it " +
			"skipped the Slot and nothing about what kept it from running - which is the one reading " +
			"this field exists to give")
	}
	if completion.HeldBy.Decision == "" {
		t.Fatal("held_by is present with no decision; the vocabulary has a word for a round nothing " +
			"held, and an empty string is not it")
	}
}

// The counterpart: an ordinary completion does not grow the field.
//
// Without this the fix could be "attach it always", which costs a field on
// every completion line in the deployment to answer a question only the
// skipped ones ask.
func TestAnOrdinaryCompletionDoesNotCarryTheCause(t *testing.T) {
	var observations []observability.Observation
	executor := observedProductionSlotExecutor{
		next: slotExecutorFunc(func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
			return execution.SlotExecutionResult{
				Completed: true, CompletionKind: execution.CompletionFull,
				Result: observability.ResultSuccess,
			}, nil
		}),
		observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observations = append(observations, observation)
		}),
	}
	if _, err := executor.Execute(context.Background(), execution.SlotExecutionRequest{}); err != nil {
		t.Fatalf("Execute() error=%v", err)
	}
	if len(observations) != 2 {
		t.Fatalf("observations=%+v", observations)
	}
	if observations[1].HeldBy != nil {
		t.Fatalf("an ordinary completion carries held_by=%+v; the field is for the lines that report a "+
			"Slot given up on, and putting it on every line is a column nobody reads",
			observations[1].HeldBy)
	}
}

// The completion line says which completion the Slot reached, not only the
// reason it reports.
//
// The two disagree in the case that is hardest to read: a Slot whose Level
// outcomes are all UNKNOWN completes COMPLETED_WITH_UNAVAILABLE and copies
// GAP_SKIPPED up from the Level. On the reason alone that is the same line as
// a Slot given up on before it ran, and the two want opposite investigations -
// one is the Levels not reaching a verdict, the other the scheduler shedding
// work.
func TestACompletionLineSaysWhichCompletionItReached(t *testing.T) {
	var observations []observability.Observation
	executor := observedProductionSlotExecutor{
		next: slotExecutorFunc(func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
			return execution.SlotExecutionResult{
				Completed: true, CompletionKind: execution.CompletionUnavailable,
				Result:     observability.ResultDegraded,
				ReasonCode: execution.ReasonCode(contract.ReasonGapSkipped),
			}, nil
		}),
		observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observations = append(observations, observation)
		}),
	}
	if _, err := executor.Execute(context.Background(), execution.SlotExecutionRequest{}); err != nil {
		t.Fatalf("Execute() error=%v", err)
	}
	completion := observations[1]
	if completion.SlotCompletionKind != string(execution.CompletionUnavailable) {
		t.Fatalf("the line carries completion_kind=%q, want %q; with only the reason on it, a Slot whose "+
			"Levels reached no verdict cannot be told from one the scheduler gave up on",
			completion.SlotCompletionKind, execution.CompletionUnavailable)
	}
	if completion.ReasonCode == observability.ReasonCode(completion.SlotCompletionKind) {
		t.Fatal("the fixture has the reason and the kind saying the same thing, so this case cannot " +
			"show that they are separate fields")
	}
}
