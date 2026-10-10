// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution_test

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestQueryFreeProgressRequiresExactCompletionReason(t *testing.T) {
	for _, test := range []struct {
		name   string
		kind   execution.CompletionKind
		reason execution.ReasonCode
		valid  bool
	}{
		{name: "snapshot exact", kind: execution.CompletionSnapshotUnavailable, reason: execution.ReasonCode(contract.ReasonSnapshotUnavailable), valid: true},
		{name: "gap exact", kind: execution.CompletionGapSkipped, reason: execution.ReasonCode(contract.ReasonGapSkipped), valid: true},
		{name: "snapshot with gap reason", kind: execution.CompletionSnapshotUnavailable, reason: execution.ReasonCode(contract.ReasonGapSkipped)},
		{name: "gap with snapshot reason", kind: execution.CompletionGapSkipped, reason: execution.ReasonCode(contract.ReasonSnapshotUnavailable)},
		{name: "generic coverage reason", kind: execution.CompletionSnapshotUnavailable, reason: execution.ReasonCode(contract.ReasonQueryUnavailable)},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := queryFreeProgressRequest(test.kind, test.reason).Validate()
			if (err == nil) != test.valid {
				t.Fatalf("Validate() error=%v, valid=%t", err, test.valid)
			}
		})
	}
}

func queryFreeProgressRequest(kind execution.CompletionKind, reason execution.ReasonCode) execution.ProgressCommitRequest {
	contractRef := execution.FrozenExecutionContractRef{
		Slot: execution.SlotIdentity{
			QueryGroup: "query-group", EvaluationTime: 1_788_000_000,
		},
		SnapshotRevision: "snapshot-v1", QueryRevision: "query-v1",
		ScheduleRevision: "schedule-v1", ScheduleSegmentStart: 1_787_999_940, DuePlanSetDigest: "due-plans-v1",
	}
	return execution.ProgressCommitRequest{
		Identity: execution.ProgressIdentity{QueryGroup: "query-group"},
		OwnerFence: execution.OwnerFence{
			QueryGroup: "query-group", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "lease-1",
		},
		ExpectedNextSlot: contractRef.Slot.EvaluationTime,
		Completion: execution.SlotCompletion{
			Contract: contractRef, Kind: kind, Result: observability.ResultDegraded, ReasonCode: reason,
		},
	}
}

// Evidence belongs only on a completion that could not see for itself.
//
// A completion from the query path queried, evaluated and is reporting what it
// found: it is its own evidence. Letting it also carry a reading of an earlier
// attempt would give the gap fold two answers to one question, and the one it
// happened to prefer would decide whether a Slot counts as evaluated.
func TestOnlyAQueryFreeCompletionMayCarryExecutionEvidence(t *testing.T) {
	applied := &execution.ExecutionEvidence{
		Kind: execution.EvidenceStateApplied, PlansApplied: 1, PlansTotal: 1,
	}
	for _, kind := range []execution.CompletionKind{
		execution.CompletionGapSkipped, execution.CompletionSnapshotUnavailable,
	} {
		request := queryFreeProgressRequest(kind, queryFreeReason(kind))
		request.Completion.Evidence = applied
		if err := request.Validate(); err != nil {
			t.Fatalf("%s completion refused its evidence: %v", kind, err)
		}
	}
	// Each of these is a completion that is valid in every other respect, so
	// the only thing that can refuse it is the evidence. Built from a fixture
	// that was missing its PRIMARY facts, they would be refused for that
	// instead, and the mutation that deletes the rule would pass -- which is
	// what happened the first time this was written.
	for kind, primary := range map[execution.CompletionKind]*execution.PrimaryInputFact{
		execution.CompletionFull: {
			Completeness: execution.CompletenessFull, DataState: execution.DataStateData},
		execution.CompletionFullEmpty: {
			Completeness: execution.CompletenessFull, DataState: execution.DataStateEmpty},
		execution.CompletionPartialGap: {
			Completeness: execution.CompletenessPartial, DataState: execution.DataStateData},
		execution.CompletionUnavailable: {
			Completeness: execution.CompletenessUnavailable, DataState: execution.DataStateUnknown},
	} {
		request := queryPathProgressRequest(kind, primary)
		if err := request.Validate(); err != nil {
			t.Fatalf("fixture: a %s completion with no evidence is already invalid (%v), so this "+
				"case cannot show that the evidence is what refuses it", kind, err)
		}
		request.Completion.Evidence = applied
		if err := request.Validate(); err == nil {
			t.Fatalf("%s completion was allowed to carry execution evidence; the fold now has two "+
				"answers to whether this Slot was evaluated", kind)
		}
	}
}

// The counts and the kind have to agree with each other, because the fold reads
// them together and a disagreement is silently readable either way.
func TestExecutionEvidenceRefusesCountsThatContradictItsKind(t *testing.T) {
	for name, evidence := range map[string]execution.ExecutionEvidence{
		"applied more Plans than the Slot had": {
			Kind: execution.EvidenceStateApplied, PlansApplied: 4, PlansTotal: 3},
		"applied nothing and says state was applied": {
			Kind: execution.EvidenceStateApplied, PlansApplied: 0, PlansTotal: 3},
		"found nothing and names applied Plans": {
			Kind: execution.EvidenceNoneFound, PlansApplied: 2, PlansTotal: 3},
		"could not read and names applied Plans": {
			Kind: execution.EvidenceUnreadable, PlansApplied: 1, PlansTotal: 3},
		"negative count": {
			Kind: execution.EvidenceStateApplied, PlansApplied: -1, PlansTotal: 3},
		"unknown kind": {
			Kind: "MAYBE", PlansApplied: 1, PlansTotal: 3},
	} {
		t.Run(name, func(t *testing.T) {
			request := queryFreeProgressRequest(
				execution.CompletionGapSkipped, execution.ReasonCode(contract.ReasonGapSkipped))
			request.Completion.Evidence = &evidence
			if err := request.Validate(); err == nil {
				t.Fatalf("evidence %+v was accepted", evidence)
			}
		})
	}
}

// queryPathProgressRequest is a completion the query path would write, valid
// in every respect, so a test can change one thing and know what refused it.
func queryPathProgressRequest(
	kind execution.CompletionKind, primary *execution.PrimaryInputFact,
) execution.ProgressCommitRequest {
	request := queryFreeProgressRequest(kind, "")
	request.Completion.Primary = primary
	request.Completion.Result = observability.ResultSuccess
	request.Completion.ReasonCode = ""
	switch kind {
	case execution.CompletionPartialGap:
		request.Completion.Result = observability.ResultDegraded
		request.Completion.ReasonCode = execution.ReasonCode(contract.ReasonQueryPartial)
	case execution.CompletionUnavailable:
		request.Completion.Result = observability.ResultDegraded
		request.Completion.ReasonCode = execution.ReasonCode(contract.ReasonQueryUnavailable)
	}
	return request
}

func queryFreeReason(kind execution.CompletionKind) execution.ReasonCode {
	if kind == execution.CompletionSnapshotUnavailable {
		return execution.ReasonCode(contract.ReasonSnapshotUnavailable)
	}
	return execution.ReasonCode(contract.ReasonGapSkipped)
}
