// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// rejectedPlanEventError is what the sink returns for an output it refuses
// on its own account: it names a reason and does not mark itself retryable.
type rejectedPlanEventError struct {
	reason string
	detail string
}

func (err *rejectedPlanEventError) Error() string                 { return err.reason + ": " + err.detail }
func (err *rejectedPlanEventError) OutputRejectionReason() string { return err.reason }

type committingProgressPorts struct {
	*planFailurePorts
	commits []execution.ProgressCommitRequest
}

func (ports *committingProgressPorts) CommitProgress(_ context.Context, request execution.ProgressCommitRequest) (execution.ProgressCommitResult, error) {
	ports.commits = append(ports.commits, request)
	return execution.ProgressCommitResult{Status: execution.ProgressCommitted}, nil
}

// An output the sink refuses on its own account -- a decision the converter
// will not write, a record the client will not send -- is decided from the
// Plan's own content and this deployment's own wiring, and a retry decides it
// the same way. So the Plan completes by the refusal's name: its State is not
// applied (its output was not written), the healthy sibling runs and is
// applied, and Progress moves past the Slot as TERMINAL with the reason. It
// used to come back as OUTPUT_ACK_UNKNOWN, retry every round and commit no
// Progress, with the page reading a Kafka that was up as down.
func TestFinalizePreparedFinishesAPlanWhoseOutputWasRefusedByName(t *testing.T) {
	for _, reason := range []string{contract.ReasonOutputConversionRejected, contract.ReasonOutputClientRejected} {
		t.Run(reason, func(t *testing.T) {
			fixture := newPlanIsolationFixture(t, &rejectedPlanEventError{reason: reason, detail: "the sink's own sentence"})
			progress := &committingProgressPorts{planFailurePorts: fixture.ports}
			fixture.coordinator.ports.Progress = progress
			// The isolation fixture never reached a Progress commit before;
			// a terminal completion does, and the commit is validated
			// against the Slot's frozen due Plan targets.
			evaluationMillis := int64(fixture.request.Contract.Slot.EvaluationTime) * 1000
			fixture.request.DuePlanTargets = execution.FrozenDuePlanTargets{
				DuePlanSetDigest: fixture.request.Contract.DuePlanSetDigest,
				Plans:            []execution.PlanKey{fixture.header.DuePlans[0].Key(), fixture.header.DuePlans[1].Key()},
			}
			fixture.request.EarliestQueryDeadlineUnixMilli = evaluationMillis + 1_000
			fixture.request.RecoveryUntilUnixMilli = evaluationMillis + 601_000
			fixture.request.KeepUntilUnixMilli = evaluationMillis + 677_000

			result, err := fixture.coordinator.finalizePrepared(
				context.Background(), fixture.request, fixture.header, fixture.bindings, fixture.loaded, fixture.evaluated,
			)
			if err != nil || !result.Completed || result.Result != observability.ResultTerminal ||
				result.ReasonCode != execution.ReasonCode(reason) {
				t.Fatalf("finalizePrepared() result=%+v error=%v, want a completed TERMINAL Slot named %s", result, err, reason)
			}
			if len(fixture.ports.eventAttempts) != 2 || fixture.ports.eventAttempts[0] != "failed-event" ||
				fixture.ports.eventAttempts[1] != "healthy-event" {
				t.Fatalf("event attempts=%v, want the refused Plan and then the healthy sibling", fixture.ports.eventAttempts)
			}
			if len(fixture.base.stateApplied) != 1 || fixture.base.stateApplied[0] != fixture.healthyState {
				t.Fatalf("applied State=%v, want only the healthy sibling's", fixture.base.stateApplied)
			}
			if len(progress.commits) != 1 {
				t.Fatalf("Progress commits=%d, want the Slot written down once", len(progress.commits))
			}
			completion := progress.commits[0].Completion
			if completion.Kind != execution.CompletionTerminal || completion.Result != observability.ResultTerminal ||
				completion.ReasonCode != execution.ReasonCode(reason) {
				t.Fatalf("committed completion=%+v, want TERMINAL named %s", completion, reason)
			}

			var refused *observability.Observation
			for index := range fixture.observations {
				observation := &fixture.observations[index]
				if observation.Stage == observability.StageEventACKed && observation.Result == observability.ResultFailed {
					refused = observation
					break
				}
			}
			if refused == nil || refused.ReasonCode != execution.ReasonCode(reason) || refused.Trace.StrategyID != "failed" ||
				refused.Err == nil {
				t.Fatalf("refused output observation=%+v, want the refusal's own reason on the failed Plan with the sentence attached", refused)
			}
			if refused.ReasonCode == execution.ReasonCode(contract.ReasonOutputACKUnknown) {
				t.Fatal("a refusal decided in this process was reported as an unknown broker acknowledgement")
			}
		})
	}
}

// A refusal that also claimed to be retryable would be a contradiction the
// caller has to resolve one way; it resolves it as the refusal, because a
// refusal never becomes an acknowledgement by waiting.
func TestAnOutputRefusalIsNeverReadAsARetryableDependency(t *testing.T) {
	err := &rejectedPlanEventError{reason: contract.ReasonOutputConversionRejected, detail: "x"}
	if reason, ok := outputRejectionReason(err); !ok || reason != execution.ReasonCode(contract.ReasonOutputConversionRejected) {
		t.Fatalf("outputRejectionReason() = (%q, %t)", reason, ok)
	}
	if _, ok := outputRejectionReason(errors.New("plain")); ok {
		t.Fatal("a plain error was read as an output refusal")
	}
	if _, ok := outputRejectionReason(&retryablePlanEventError{err: errors.New("broker")}); ok {
		t.Fatal("a dependency failure was read as an output refusal")
	}
}

// deferredPlanEventError is what the sink returns for a batch it did not
// start because the lease has less life left than the batch needs: it names
// the reason and marks neither a dependency nor a rejection.
type deferredPlanEventError struct{}

func (*deferredPlanEventError) Error() string {
	return "kafka trigger event sink: OUTPUT_LEASE_EXPIRING"
}
func (*deferredPlanEventError) OutputDeferralReason() string {
	return contract.ReasonOutputLeaseExpiring
}

// A batch the sink held back for the lease is a Plan that waits: its State
// is not applied and Progress does not move, like an unknown
// acknowledgement, but under its own name -- no broker was asked -- and the
// healthy sibling still runs and is applied.
func TestFinalizePreparedKeepsAPlanWhoseOutputWasHeldForTheLeaseWaitingByName(t *testing.T) {
	fixture := newPlanIsolationFixture(t, &deferredPlanEventError{})
	result, err := fixture.coordinator.finalizePrepared(
		context.Background(), fixture.request, fixture.header, fixture.bindings, fixture.loaded, fixture.evaluated,
	)
	if err != nil || result.Completed || result.Result != observability.ResultRetrying ||
		result.ReasonCode != execution.ReasonCode(contract.ReasonOutputLeaseExpiring) {
		t.Fatalf("finalizePrepared() result=%+v error=%v, want a retrying Slot named %s", result, err, contract.ReasonOutputLeaseExpiring)
	}
	if result.ReasonCode == execution.ReasonCode(contract.ReasonOutputACKUnknown) {
		t.Fatal("a batch that was never sent was reported as an unknown acknowledgement")
	}
	if len(fixture.ports.eventAttempts) != 2 || fixture.ports.eventAttempts[1] != "healthy-event" {
		t.Fatalf("event attempts=%v, want the held Plan and then the healthy sibling", fixture.ports.eventAttempts)
	}
	if len(fixture.base.stateApplied) != 1 || fixture.base.stateApplied[0] != fixture.healthyState {
		t.Fatalf("applied State=%v, want only the healthy sibling's", fixture.base.stateApplied)
	}
	if fixture.base.progressCommits != 0 {
		t.Fatalf("a waiting Slot committed Progress %d times", fixture.base.progressCommits)
	}
	var held *observability.Observation
	for index := range fixture.observations {
		observation := &fixture.observations[index]
		if observation.Stage == observability.StageEventACKed && observation.Result == observability.ResultFailed {
			held = observation
			break
		}
	}
	if held == nil || held.ReasonCode != execution.ReasonCode(contract.ReasonOutputLeaseExpiring) || held.Trace.StrategyID != "failed" {
		t.Fatalf("held output observation=%+v, want %s on the held Plan", held, contract.ReasonOutputLeaseExpiring)
	}
}
