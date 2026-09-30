// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A batch that opens the marker overrides a batch that cleared it.
//
// This is the defect production showed: the two guard lists were appended to
// independently, so a batch whose series all completed contributed a clear and
// a later batch with an incomplete input contributed an open, and the Slot
// ended carrying both. The result contract forbids that and is checked on each
// batch's own result, so no batch ever saw it - the Slot did, and the apply
// failed on it every other round.
//
// The direction matters as much as the merge. Keeping the clear would report
// the marker closed for a Slot part of which never read its data, which is the
// gap being declared recovered over the evidence that says it is not.
func TestAnOpenFromOneBatchOverridesAnotherBatchsClear(t *testing.T) {
	cleared := gapMergeStatement(t, execution.GapClear, "", 0, execution.GapScope{})
	opened := gapMergeStatement(t, execution.GapOpen, contract.ReasonQueryUnavailable, 3, execution.GapScope{})

	plan := &execution.PlanEvaluationResult{GuardAfterState: []execution.PlanGapMutation{cleared}}
	next := execution.PlanEvaluationResult{GuardBeforeEvents: []execution.PlanGapMutation{opened}}
	if err := mergeGapGuardStatements(plan, next); err != nil {
		t.Fatal(err)
	}
	if len(plan.GuardAfterState) != 0 {
		t.Fatalf("the Slot still clears the marker (%+v) while another batch opened it; "+
			"both lists carrying a statement is the shape the result contract refuses",
			plan.GuardAfterState)
	}
	if len(plan.GuardBeforeEvents) != 1 || plan.GuardBeforeEvents[0].MutationDigest != opened.MutationDigest {
		t.Fatalf("the Slot opens with %+v, want the batch's own open", plan.GuardBeforeEvents)
	}

	// The other order, because the lists are merged independently and a rule
	// applied to only one direction would leave the shape reachable by
	// evaluating the same two batches the other way round.
	plan = &execution.PlanEvaluationResult{GuardBeforeEvents: []execution.PlanGapMutation{opened}}
	next = execution.PlanEvaluationResult{GuardAfterState: []execution.PlanGapMutation{cleared}}
	if err := mergeGapGuardStatements(plan, next); err != nil {
		t.Fatal(err)
	}
	if len(plan.GuardAfterState) != 0 || len(plan.GuardBeforeEvents) != 1 {
		t.Fatalf("clear then open and open then clear disagree: before=%+v after=%+v",
			plan.GuardBeforeEvents, plan.GuardAfterState)
	}
}

// Two batches opening different scopes produce the one statement a Slot that
// evaluated both in one batch would have produced.
//
// Left unmerged these are two statements for one marker, which the result
// contract refuses Slot-wide; picking one would drop the other's scope and
// leave a Level degraded with no marker of its scope to name it.
func TestOpensFromTwoBatchesUnionTheirScopes(t *testing.T) {
	first := gapMergeStatement(t, execution.GapOpen, contract.ReasonQueryUnavailable, 3,
		execution.GapScope{LevelID: 1, HasLevel: true})
	second := gapMergeStatement(t, execution.GapOpen, contract.ReasonQueryUnavailable, 3,
		execution.GapScope{LevelID: 2, HasLevel: true})

	plan := &execution.PlanEvaluationResult{GuardBeforeEvents: []execution.PlanGapMutation{first}}
	next := execution.PlanEvaluationResult{GuardBeforeEvents: []execution.PlanGapMutation{second}}
	if err := mergeGapGuardStatements(plan, next); err != nil {
		t.Fatal(err)
	}
	if len(plan.GuardBeforeEvents) != 1 {
		t.Fatalf("the Slot carries %d statements for one marker, want one", len(plan.GuardBeforeEvents))
	}
	merged := plan.GuardBeforeEvents[0]
	if len(merged.Scopes) != 2 {
		t.Fatalf("merged statement covers %+v, want both batches' scopes", merged.Scopes)
	}
	// The digest has to be the merged content's, not either half's: the store
	// treats a repeated digest as the write it already applied, so a union
	// wearing one half's digest would be skipped as already done.
	if merged.MutationDigest == first.MutationDigest || merged.MutationDigest == second.MutationDigest {
		t.Fatal("the merged statement kept a digest of one of its halves, which the store reads as already applied")
	}
	if err := merged.ValidateDigest(); err != nil {
		t.Fatalf("merged statement is not canonical: %v", err)
	}
}

// Two batches naming one scope for different reasons fold to the reason a
// single batch over both inputs would have carried.
//
// The result contract compares a degraded Level's reason against the reason on
// a marker of its scope, and they agree only because both are the same fold
// over the same inputs. Choosing here - first wins, or last - would be a
// second derivation, and it would disagree exactly on the rounds where two
// batches failed differently, which is when a backend is half down.
func TestOneScopeOpenedTwiceKeepsTheFoldedReason(t *testing.T) {
	scope := execution.GapScope{LevelID: 1, HasLevel: true}
	first := gapMergeStatement(t, execution.GapOpen, contract.ReasonQueryTimeout, 3, scope)
	second := gapMergeStatement(t, execution.GapOpen, contract.ReasonQueryUnavailable, 3, scope)
	want := contract.FoldGapReason([]string{contract.ReasonQueryTimeout, contract.ReasonQueryUnavailable})
	if want == contract.ReasonQueryTimeout {
		t.Fatal("the fixture's two reasons fold to the first one, so this case cannot tell a fold from a choice")
	}

	plan := &execution.PlanEvaluationResult{GuardBeforeEvents: []execution.PlanGapMutation{first}}
	next := execution.PlanEvaluationResult{GuardBeforeEvents: []execution.PlanGapMutation{second}}
	if err := mergeGapGuardStatements(plan, next); err != nil {
		t.Fatal(err)
	}
	merged := plan.GuardBeforeEvents[0]
	if len(merged.Scopes) != 1 {
		t.Fatalf("one scope opened twice produced %+v, want one scope", merged.Scopes)
	}
	if string(merged.Scopes[0].ReasonCode) != want {
		t.Fatalf("merged reason = %q, want the fold %q: a Level degraded with the folded reason needs a "+
			"marker carrying it, and any other choice is a second derivation of the same thing",
			merged.Scopes[0].ReasonCode, want)
	}
}

// Two batches clearing one marker differently are refused rather than reduced.
//
// A clear is derived from the marker the Slot loaded, so every batch proposing
// one proposes the same one. Two that differ mean two batches read different
// markers for one Plan in one Slot, and there is no winner to pick: whichever
// were kept, the other batch's series were evaluated against a marker the Slot
// then denies.
func TestTwoBatchesClearingOneMarkerDifferentlyAreRefused(t *testing.T) {
	first := gapMergeStatement(t, execution.GapClear, "", 0, execution.GapScope{})
	second := gapMergeStatement(t, execution.GapClear, "", 0, execution.GapScope{LevelID: 1, HasLevel: true})
	if first.MutationDigest == second.MutationDigest {
		t.Fatal("the fixture's two clears are the same statement, so this case cannot reach the disagreement")
	}

	plan := &execution.PlanEvaluationResult{GuardAfterState: []execution.PlanGapMutation{first}}
	next := execution.PlanEvaluationResult{GuardAfterState: []execution.PlanGapMutation{second}}
	err := mergeGapGuardStatements(plan, next)
	if err == nil {
		t.Fatalf("two disagreeing clears merged into %+v, want a refusal", plan.GuardAfterState)
	}
	if !strings.Contains(err.Error(), "clear one Plan gap marker differently") {
		t.Fatalf("refusal = %v, want it to say the two batches disagreed about the clear", err)
	}
	// And it carries the word, because this refusal reaches the completion
	// line. Without one it arrives there as an error nobody can group or
	// count - which is how a Query Group conflicting every other Slot for
	// half an hour once read as an unclassified defect.
	reason, named := GapGuardDisagreeReason(err)
	if !named || string(reason) != contract.ReasonGapGuardDisagree {
		t.Fatalf("GapGuardDisagreeReason() = (%q, %t), want %s",
			reason, named, contract.ReasonGapGuardDisagree)
	}
}

// Every refusal the merge makes carries the word, not only the one above.
//
// The accessor reads the error's type, so a refusal built any other way is
// unnamed at the line while looking named in the code that raised it. Each
// shape below is a different construction site.
func TestEveryMergeRefusalIsNamed(t *testing.T) {
	scope := execution.GapScope{LevelID: 1, HasLevel: true}
	open := gapMergeStatement(t, execution.GapOpen, contract.ReasonQueryUnavailable, 3, scope)
	clear := gapMergeStatement(t, execution.GapClear, "", 0, execution.GapScope{})

	otherRevision := open
	otherRevision.ExpectedMarkerRevision++
	weakerWarmup := gapMergeStatement(t, execution.GapOpen, contract.ReasonQueryUnavailable, 4, scope)
	otherClear := gapMergeStatement(t, execution.GapClear, "", 0, scope)
	clearRevision := clear
	clearRevision.ExpectedMarkerRevision++

	for name, pair := range map[string]struct {
		have, next execution.PlanEvaluationResult
	}{
		"opens expecting different revisions": {
			execution.PlanEvaluationResult{GuardBeforeEvents: []execution.PlanGapMutation{open}},
			execution.PlanEvaluationResult{GuardBeforeEvents: []execution.PlanGapMutation{otherRevision}},
		},
		"one scope with two warmup requirements": {
			execution.PlanEvaluationResult{GuardBeforeEvents: []execution.PlanGapMutation{open}},
			execution.PlanEvaluationResult{GuardBeforeEvents: []execution.PlanGapMutation{weakerWarmup}},
		},
		"clears of different content": {
			execution.PlanEvaluationResult{GuardAfterState: []execution.PlanGapMutation{clear}},
			execution.PlanEvaluationResult{GuardAfterState: []execution.PlanGapMutation{otherClear}},
		},
		"clears expecting different revisions": {
			execution.PlanEvaluationResult{GuardAfterState: []execution.PlanGapMutation{clear}},
			execution.PlanEvaluationResult{GuardAfterState: []execution.PlanGapMutation{clearRevision}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			plan := pair.have
			err := mergeGapGuardStatements(&plan, pair.next)
			if err == nil {
				t.Fatal("merged, want a refusal")
			}
			if _, named := GapGuardDisagreeReason(err); !named {
				t.Fatalf("refusal %v carries no word, so the line reports it as unknown", err)
			}
		})
	}
}

// Two batches clearing the same content while expecting different revisions
// are refused too.
//
// The digest closes the statement's content and not the revision it expects,
// so these two are equal by digest and would collapse into one - and the
// survivor would carry an expectation the other half never had, which is a
// conditional write made on a condition nobody checked.
func TestTwoBatchesClearingAtDifferentRevisionsAreRefused(t *testing.T) {
	first := gapMergeStatement(t, execution.GapClear, "", 0, execution.GapScope{})
	second := first
	second.ExpectedMarkerRevision = first.ExpectedMarkerRevision + 1
	if first.MutationDigest != second.MutationDigest {
		t.Fatal("the fixture's two clears differ by digest, so this case would pass on the digest rule alone")
	}

	plan := &execution.PlanEvaluationResult{GuardAfterState: []execution.PlanGapMutation{first}}
	next := execution.PlanEvaluationResult{GuardAfterState: []execution.PlanGapMutation{second}}
	err := mergeGapGuardStatements(plan, next)
	if err == nil {
		t.Fatalf("two clears expecting different revisions merged into %+v, want a refusal", plan.GuardAfterState)
	}
	if !strings.Contains(err.Error(), "expecting revision") {
		t.Fatalf("refusal = %v, want it to name the revisions that disagreed", err)
	}
}

// Two batches opening one Plan's marker while expecting different revisions
// are refused, rather than unioned onto one of the two expectations.
func TestTwoBatchesOpeningAtDifferentRevisionsAreRefused(t *testing.T) {
	first := gapMergeStatement(t, execution.GapOpen, contract.ReasonQueryUnavailable, 3,
		execution.GapScope{LevelID: 1, HasLevel: true})
	second := gapMergeStatement(t, execution.GapOpen, contract.ReasonQueryUnavailable, 3,
		execution.GapScope{LevelID: 2, HasLevel: true})
	second.ExpectedMarkerRevision = first.ExpectedMarkerRevision + 1

	plan := &execution.PlanEvaluationResult{GuardBeforeEvents: []execution.PlanGapMutation{first}}
	next := execution.PlanEvaluationResult{GuardBeforeEvents: []execution.PlanGapMutation{second}}
	err := mergeGapGuardStatements(plan, next)
	if err == nil {
		t.Fatalf("two opens expecting different revisions merged into %+v, want a refusal", plan.GuardBeforeEvents)
	}
	if !strings.Contains(err.Error(), "expecting revision") {
		t.Fatalf("refusal = %v, want it to name the revisions that disagreed", err)
	}
}

// gapMergeStatement builds one canonical statement about a Plan's marker,
// through the builder that owns the digest, so the merge is exercised on the
// shape the producers actually hand it.
func gapMergeStatement(
	t *testing.T,
	kind execution.GapMutationKind,
	reason string,
	warmup uint32,
	scope execution.GapScope,
) execution.PlanGapMutation {
	t.Helper()
	contractRef := execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: "qg-gap-merge", EvaluationTime: 1758400000},
		SnapshotRevision: "snapshot-one", QueryRevision: "query-one", ScheduleRevision: "schedule-one",
		ScheduleSegmentStart: 1758399000, DuePlanSetDigest: "due-one",
	}
	version, err := execution.BuildApplyVersion(contractRef, 1)
	if err != nil {
		t.Fatalf("BuildApplyVersion() error: %v", err)
	}
	mutation, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{
		Identity: execution.PlanGapIdentity{
			Plan:            execution.PlanIdentity{TenantID: "tenant-a", BusinessID: "2", StrategyID: "1001"},
			StateGeneration: "generation-one",
		},
		ExpectedMarkerRevision: 7,
		ApplyVersion:           version,
		ScheduleRevision:       "schedule-one",
		Scopes: []execution.GapScopeMutation{{
			Scope: scope, Kind: kind,
			ReasonCode: execution.ReasonCode(reason), RequiredFullSlots: warmup,
		}},
	})
	if err != nil {
		t.Fatalf("BuildPlanGapMutation() error: %v", err)
	}
	return mutation
}
