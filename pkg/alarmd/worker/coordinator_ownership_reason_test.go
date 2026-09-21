// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker_test

import (
	"context"
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// reasonOf is the reason the first observation of stage carried, "" when no
// observation of that stage was made.
func reasonOf(observations *[]observability.Observation, stage observability.Stage) observability.ReasonCode {
	for _, observation := range *observations {
		if observation.Stage == stage {
			return observation.ReasonCode
		}
	}
	return ""
}

// The store's refusal of the side-effect admission is named on the admission
// line by the store's own word. It was internal_unknown: the admission result
// carries no reason for an error, and the four typed refusals could only be
// told apart by reading the error text off a rate-limited log line.
func TestTheAdmissionLineNamesTheStoresRefusal(t *testing.T) {
	for _, test := range []struct {
		refusal error
		want    string
	}{
		{ownership.ErrNotDesired, contract.ReasonOwnershipNotDesired},
		{ownership.ErrStaleFence, contract.ReasonOwnershipStaleFence},
		{ownership.ErrLeaseBusy, contract.ReasonOwnershipLeaseBusy},
	} {
		t.Run(test.want, func(t *testing.T) {
			observations := make([]observability.Observation, 0, 8)
			observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				observations = append(observations, observability.NormalizeObservation(observation))
			})
			fixture := buildFixture(t, true, "admission_initial", observer, &observations)
			fixture.ports.failErr = test.refusal
			_, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
			if !errors.Is(err, test.refusal) {
				t.Fatalf("Execute() error = %v, want the refusal wrapped", err)
			}
			if got := reasonOf(&observations, observability.StageSideEffectAdmission); string(got) != test.want {
				t.Fatalf("admission line reason = %q, want %q", got, test.want)
			}
		})
	}
	// An anonymous failure of the same check is still unknown: the name is
	// the store's, not a guess.
	observations := make([]observability.Observation, 0, 8)
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		observations = append(observations, observability.NormalizeObservation(observation))
	})
	fixture := buildFixture(t, true, "admission_initial", observer, &observations)
	if _, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal)); err == nil {
		t.Fatal("injected admission failure did not fail the Slot")
	}
	if got := reasonOf(&observations, observability.StageSideEffectAdmission); got != observability.ReasonNotReported && got != observability.ReasonInternalUnknown {
		t.Fatalf("anonymous admission failure reason = %q, want it left unnamed", got)
	}
}

// A fenced State write the store refused is named on the state_applied line
// the same way -- stale fence, or the content scope having moved under the
// write -- so the three numbers a scope move is verified by are readable from
// this line: the old scope written before it took effect, refused with
// CONTENT_SCOPE_MOVED after, and the new scope written.
func TestTheStateAppliedLineNamesTheFencedRefusal(t *testing.T) {
	for _, test := range []struct {
		refusal error
		want    string
	}{
		{ownership.ErrStaleFence, contract.ReasonOwnershipStaleFence},
		{ownership.ErrContentScopeMoved, contract.ReasonContentScopeMoved},
	} {
		t.Run(test.want, func(t *testing.T) {
			fixture, fenced := newFencedFixture(t, false)
			fenced.refusal = test.refusal
			_, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
			if !errors.Is(err, test.refusal) {
				t.Fatalf("Execute() error = %v, want %v wrapped", err, test.refusal)
			}
			if got := reasonOf(fixture.observations, observability.StageStateApplied); string(got) != test.want {
				t.Fatalf("state_applied line reason = %q, want %q", got, test.want)
			}
		})
	}
}

// The sink refusing to write the round's events is named on the event_acked
// line by the sink's own reason word, with the sink's own sentence as facts
// apart from the error chain -- read through the two methods the sink's
// error carries, not by slicing its text.
func TestTheEventAckedLineCarriesTheSinksOwnRefusal(t *testing.T) {
	// Recorded as emitted, not normalized: the sink's words enter the reason
	// catalogue with the sink's own change, and this test is about the
	// coordinator carrying them, not about the catalogue knowing them.
	observations := make([]observability.Observation, 0, 16)
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		observations = append(observations, observation)
	})
	fixture := buildFixture(t, true, "event_ack", observer, &observations)
	fixture.ports.eventRejection = &eventRejectionShape{reason: "OUTPUT_CLIENT_REJECTED",
		detail: "kafka: invalid configuration (Producing headers requires Kafka at least v0.11)"}
	// A stated refusal is terminal for the Plan and the Slot completes under
	// the word; the line the refusal is read from is the event_acked one.
	_, _ = fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	var acked *observability.Observation
	for index := range observations {
		if observations[index].Stage == observability.StageEventACKed {
			acked = &observations[index]
		}
	}
	if acked == nil {
		t.Fatal("no event_acked observation")
	}
	if acked.Result != observability.ResultFailed || string(acked.ReasonCode) != "OUTPUT_CLIENT_REJECTED" {
		t.Fatalf("event_acked = result %s reason %s, want failed under the sink's word", acked.Result, acked.ReasonCode)
	}
	if acked.OutputRejection == nil || acked.OutputRejection.Reason != "OUTPUT_CLIENT_REJECTED" ||
		acked.OutputRejection.Detail != "kafka: invalid configuration (Producing headers requires Kafka at least v0.11)" {
		t.Fatalf("output rejection facts = %+v, want the sink's word and bare sentence", acked.OutputRejection)
	}
	// A retryable dependency failure carries no rejection facts and keeps
	// its own reason.
	observations = observations[:0]
	fixture = buildFixture(t, true, "event_ack", observer, &observations)
	_, _ = fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	for _, observation := range observations {
		if observation.Stage == observability.StageEventACKed && (observation.OutputRejection != nil || string(observation.ReasonCode) != "OUTPUT_ACK_UNKNOWN") {
			t.Fatalf("a retryable dependency failure = reason %s facts %+v, want OUTPUT_ACK_UNKNOWN and no facts", observation.ReasonCode, observation.OutputRejection)
		}
	}
}

// The event_acked line carries the sink's own count of what the batch
// became, when the sink gives one: a success whose events all produced no
// message says zero messages, and a sink that did not count leaves the field
// absent rather than zero.
func TestTheEventAckedLineCarriesTheSinksCount(t *testing.T) {
	observations := make([]observability.Observation, 0, 16)
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		observations = append(observations, observation)
	})
	acked := func() *observability.Observation {
		for index := range observations {
			if observations[index].Stage == observability.StageEventACKed {
				return &observations[index]
			}
		}
		t.Fatal("no event_acked observation")
		return nil
	}
	fixture := buildFixture(t, true, "", observer, &observations)
	fixture.ports.outputWrite = &observability.OutputWriteFacts{Published: 0, WithoutMessage: 1}
	if _, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal)); err != nil {
		t.Fatal(err)
	}
	line := acked()
	if line.Result != observability.ResultSuccess || line.OutputWrite == nil || line.OutputWrite.Published != 0 || line.OutputWrite.WithoutMessage != 1 {
		t.Fatalf("event_acked = result %s, output write %+v, want a success that handed the broker nothing, said so", line.Result, line.OutputWrite)
	}
	if line.Counts.Events == 0 {
		t.Fatalf("event_acked lost the event count: %+v", line.Counts)
	}

	observations = observations[:0]
	fixture = buildFixture(t, true, "", observer, &observations)
	if _, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal)); err != nil {
		t.Fatal(err)
	}
	if line := acked(); line.OutputWrite != nil {
		t.Fatalf("a sink that did not count wrote %+v", line.OutputWrite)
	}
}
