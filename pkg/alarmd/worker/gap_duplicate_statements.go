// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"context"
	"fmt"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// observeDuplicatedGapStatements reports one Plan carrying more than one gap
// marker statement in a single Slot.
//
// The evaluation contract forbids the shape -- EvaluationResult.Validate
// refuses a Plan with statements in both guard lists, or more than one in
// either -- but it runs on each series batch's result, and appendProvisional
// then appends to the two lists independently. Two batches contributing one
// statement each therefore produce a Slot-wide shape no single batch could,
// and the two apply sites write the same Plan-level key with the second
// carrying the revision the first has already moved past.
//
// Recorded and not refused, on purpose and for now. What the recording is for
// is deciding the merge rule: whether two statements for one Plan are an
// upstream defect to reject or a legitimate pair to apply in order. Refusing
// here would answer that question by making every Slot that reaches the shape
// fail, on a path a retry recovers today.
//
// Reported per Plan rather than per statement, with both lists' digests, so
// one line says which of the two shapes happened: a statement in each list, or
// two in one.
func (coordinator *SlotExecutionCoordinator) observeDuplicatedGapStatements(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	planResult execution.PlanEvaluationResult,
) {
	before, after := planResult.GuardBeforeEvents, planResult.GuardAfterState
	if len(before)+len(after) <= 1 {
		return
	}
	// One statement in each list and two in one list are different defects
	// with different fixes, so the line says which rather than only that the
	// count was over one.
	shape := "across_lists"
	if len(before) == 0 || len(after) == 0 {
		shape = "within_one_list"
	}
	// Carried as facts rather than as an error: emitObservation reads a
	// non-nil Err as the stage having failed, and this stage did not fail.
	// A reading that marks the Slot failed would be a worse report than the
	// silence it replaces.
	coordinator.emitObservation(ctx, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageGapGuardCommitted,
		Operation: observability.Operation(request.Operation),
		Direction: observability.DirectionInternal, Result: observability.ResultDegraded,
		ReasonCode: observability.ReasonCode(contract.ReasonGapGuardDuplicatedAcrossBatches),
		Trace: observability.TraceFields{
			StrategyID: planResult.Plan.StrategyID,
			BusinessID: planResult.Plan.BusinessID,
		},
		GapStatements: &observability.GapStatementFacts{
			Shape: shape, Statements: len(before) + len(after),
			BeforeEvents: gapStatementDigests(before), AfterState: gapStatementDigests(after),
		},
	})
}

// gapStatementDigests renders a list's statements as "digest@expected_revision"
// in a stable order, so two Slots' lines can be compared.
func gapStatementDigests(mutations []execution.PlanGapMutation) []string {
	if len(mutations) == 0 {
		return nil
	}
	digests := make([]string, 0, len(mutations))
	for _, mutation := range mutations {
		digests = append(digests, fmt.Sprintf("%s@%d", mutation.MutationDigest, mutation.ExpectedMarkerRevision))
	}
	sort.Strings(digests)
	return digests
}
