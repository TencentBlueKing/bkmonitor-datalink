// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// A gap guard conflict reaches the page under its own name.
//
// internal_unknown is where a site that looked at a failure and could not
// classify it puts things, and it is the worst possible label for this one: the
// refusal repeats every round for the same Query Group, so a reader sees a
// Query Group failing forever for an unstated cause. It was classifiable the
// whole time -- the refusal knows both values it compared.
//
// The counterexample is in the same table: an unclassifiable error still
// reports internal_unknown, because a mapping that named everything would be
// the same as one that named nothing.
func TestAGapGuardConflictIsNamedRatherThanCalledUnknown(t *testing.T) {
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "1001"}
	conflict := &worker.GapGuardConflictError{
		Plan: plan, StateGeneration: "state-v2",
		ApplyVersion: execution.ApplyVersion{StateApplyEpoch: 2},
		Persisted: worker.GapGuardProtection{
			Kind: "FOUND", ReasonCode: contract.ReasonQueryUnavailable, Scopes: 1,
			RequiredFullSlots: 2, MarkerRevision: 7,
		},
		Proposed: worker.GapGuardProtection{
			Kind: "STRENGTHEN", ReasonCode: contract.ReasonGapSkipped, Scopes: 1,
			RequiredFullSlots: 3, MarkerRevision: 7,
		},
	}

	for _, test := range []struct {
		name string
		err  error
		want observability.ReasonCode
	}{
		{name: "a gap guard conflict", err: conflict, want: observability.ReasonCode(contract.ReasonGapGuardConflict)},
		{
			name: "wrapped the way the coordinator returns it",
			err:  fmt.Errorf("alarmd worker: finalize query-free Slot: %w", conflict),
			want: observability.ReasonCode(contract.ReasonGapGuardConflict),
		},
		{
			name: "anything the site cannot classify",
			err:  errors.New("something nobody named"),
			want: observability.ReasonInternalUnknown,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var observations []observability.Observation
			observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				observations = append(observations, observability.NormalizeObservation(observation))
			})
			executor := observedProductionSlotExecutor{
				next: slotExecutorFunc(func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
					return execution.SlotExecutionResult{}, test.err
				}),
				observer: observer,
			}
			if _, err := executor.Execute(context.Background(), gapGuardSlotRequest(plan)); err == nil {
				t.Fatal("Execute() returned no error")
			}
			var completed *observability.Observation
			for index := range observations {
				if observations[index].Stage == observability.StageSlotCompleted {
					completed = &observations[index]
				}
			}
			if completed == nil {
				t.Fatalf("no slot_completed observation in %v", observedStages(observations))
			}
			if completed.Result != observability.ResultFailed || completed.ReasonCode != test.want {
				t.Fatalf("slot_completed = %s/%s, want failed/%s", completed.Result, completed.ReasonCode, test.want)
			}
		})
	}

	// The message carries both sides. A refusal naming neither leaves the
	// reader with two facts they cannot see and no way to tell which differs.
	text := conflict.Error()
	for _, want := range []string{
		"required_full_slots=2", "required_full_slots=3",
		contract.ReasonQueryUnavailable, contract.ReasonGapSkipped, "1001", "state-v2",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("conflict message %q does not carry %q", text, want)
		}
	}
	if !errors.Is(conflict, worker.ErrGapGuardConflict) {
		t.Fatal("the conflict does not unwrap to its sentinel")
	}
}

func gapGuardSlotRequest(plan execution.PlanIdentity) execution.SlotExecutionRequest {
	return execution.SlotExecutionRequest{
		Contract: execution.FrozenExecutionContractRef{
			Slot:             execution.SlotIdentity{QueryGroup: "gap-guard-conflict", EvaluationTime: 1_700_124_000},
			SnapshotRevision: "snapshot-v1", QueryRevision: "query-v1", ScheduleRevision: "schedule-v1",
			ScheduleSegmentStart: 1_700_123_940, DuePlanSetDigest: "due-v1",
		},
		DuePlanTargets: execution.FrozenDuePlanTargets{DuePlanSetDigest: "due-v1", Plans: []execution.PlanKey{{PlanIdentity: plan}}},
		Operation:      execution.OperationNormal, AttemptNo: 1,
	}
}
