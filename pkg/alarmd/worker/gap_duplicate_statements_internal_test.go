// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func gapStatement(strategy, digest string, revision uint64) execution.PlanGapMutation {
	return execution.PlanGapMutation{
		Identity: execution.PlanGapIdentity{
			Plan:            execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: strategy},
			StateGeneration: "state-v1",
		},
		MutationDigest:         execution.MutationDigest(digest),
		ExpectedMarkerRevision: revision,
	}
}

// The two shapes are reported apart, and a Plan with one statement reports
// nothing.
//
// Apart because they are different defects with different fixes: a statement
// in each list is the accumulation across series batches assembling what no
// batch produced, and two in one list is one batch or one merge producing two
// statements for one key. A line that only said "more than one" would send a
// reader to the wrong half.
func TestOnePlansDuplicatedGapStatementsSayWhichShapeHappened(t *testing.T) {
	for _, testCase := range []struct {
		name               string
		before, after      []execution.PlanGapMutation
		wantReported       bool
		wantShape          string
		wantStatements     int
		wantBefore, after2 string
	}{
		{name: "one statement is the ordinary Slot and reports nothing",
			before: []execution.PlanGapMutation{gapStatement("7", "aa", 3)}, wantReported: false},
		{name: "no statement at all reports nothing", wantReported: false},
		{name: "one in each list, which is what two series batches assemble",
			before:       []execution.PlanGapMutation{gapStatement("7", "aa", 3)},
			after:        []execution.PlanGapMutation{gapStatement("7", "bb", 3)},
			wantReported: true, wantShape: "across_lists", wantStatements: 2,
			wantBefore: "aa@3", after2: "bb@3"},
		{name: "two in one list, which one batch or one merge produced",
			before:       []execution.PlanGapMutation{gapStatement("7", "aa", 3), gapStatement("7", "bb", 3)},
			wantReported: true, wantShape: "within_one_list", wantStatements: 2,
			wantBefore: "aa@3,bb@3", after2: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var seen []observability.Observation
			coordinator := &SlotExecutionCoordinator{
				ports: Ports{Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
					seen = append(seen, observation)
				})},
			}
			coordinator.observeDuplicatedGapStatements(context.Background(),
				execution.SlotExecutionRequest{Operation: execution.OperationNormal},
				execution.PlanEvaluationResult{
					Plan:              execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"},
					GuardBeforeEvents: testCase.before, GuardAfterState: testCase.after,
				})

			if !testCase.wantReported {
				if len(seen) != 0 {
					t.Fatalf("a Plan with %d statement(s) was reported: %+v",
						len(testCase.before)+len(testCase.after), seen)
				}
				return
			}
			if len(seen) != 1 {
				t.Fatalf("observations = %d, want exactly one", len(seen))
			}
			observation := seen[0]
			if string(observation.ReasonCode) != contract.ReasonGapGuardDuplicatedAcrossBatches {
				t.Fatalf("reason = %q, want %s", observation.ReasonCode, contract.ReasonGapGuardDuplicatedAcrossBatches)
			}
			facts := observation.GapStatements
			if facts == nil {
				t.Fatal("the reading carries no statements, so a reader cannot tell the two shapes apart")
			}
			if facts.Shape != testCase.wantShape || facts.Statements != testCase.wantStatements {
				t.Fatalf("shape = %q with %d statements, want %q with %d",
					facts.Shape, facts.Statements, testCase.wantShape, testCase.wantStatements)
			}
			if got := strings.Join(facts.BeforeEvents, ","); got != testCase.wantBefore {
				t.Fatalf("before_events = %q, want %q", got, testCase.wantBefore)
			}
			if got := strings.Join(facts.AfterState, ","); got != testCase.after2 {
				t.Fatalf("after_state = %q, want %q", got, testCase.after2)
			}
			// The digests are the evidence: two Slots' lines are compared by
			// them, and a reading that named the shape without them would say
			// that it happened and nothing about what.
			for _, list := range [][]string{facts.BeforeEvents, facts.AfterState} {
				for _, entry := range list {
					if !strings.Contains(entry, "@") {
						t.Fatalf("statement %q carries no expected revision", entry)
					}
				}
			}
		})
	}
}

// The reading does not mark the Slot failed.
//
// It is a reading and not a refusal: the Slot went on, the retry recovers it
// today, and the whole point of measuring before changing anything is that
// nothing changes on its account yet. Reported through the facts rather than
// through Err for exactly this reason - emitObservation reads a non-nil Err as
// the stage having failed, so carrying the detail there would have published a
// failed stage for a Slot that completed.
func TestTheDuplicatedStatementReadingDoesNotFailTheSlot(t *testing.T) {
	var seen []observability.Observation
	coordinator := &SlotExecutionCoordinator{
		ports: Ports{Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			seen = append(seen, observation)
		})},
	}
	coordinator.observeDuplicatedGapStatements(context.Background(),
		execution.SlotExecutionRequest{Operation: execution.OperationNormal},
		execution.PlanEvaluationResult{
			Plan:              execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"},
			GuardBeforeEvents: []execution.PlanGapMutation{gapStatement("7", "aa", 3)},
			GuardAfterState:   []execution.PlanGapMutation{gapStatement("7", "bb", 3)},
		})
	if len(seen) != 1 {
		t.Fatalf("observations = %d, want exactly one", len(seen))
	}
	if seen[0].Err != nil {
		t.Fatalf("the reading carries an error (%v), which publishes the stage as failed", seen[0].Err)
	}
	if seen[0].Result == observability.ResultFailed {
		t.Fatalf("the reading published result %q for a Slot that completed", seen[0].Result)
	}
}

// refusingGapStore answers every apply with one refusal.
type refusingGapStore struct{ status execution.GapGuardApplyStatus }

func (store *refusingGapStore) LoadGaps(
	context.Context, execution.GapLoadRequest,
) (execution.GapLoadResult, error) {
	return execution.GapLoadResult{}, nil
}

func (store *refusingGapStore) LoadGapsInto(
	context.Context, execution.GapLoadRequest, func(execution.GapGuardSnapshot) error,
) error {
	return nil
}

func (store *refusingGapStore) ApplyGap(
	_ context.Context, request execution.GapGuardApplyRequest,
) (execution.GapGuardApplyResult, error) {
	result := execution.GapGuardApplyResult{Items: make([]execution.GapGuardApplyItemResult, len(request.Items))}
	for index, item := range request.Items {
		result.Items[index] = execution.GapGuardApplyItemResult{Identity: item.Identity, Status: store.status}
	}
	return result, nil
}

// A refusal names the apply site it came from.
//
// The Slot has two gap apply calls and both write the same Plan-level key, so
// the site is what separates "this Slot's other call moved the marker" from "a
// writer in another process did". Driven through applyGap with each site in
// turn, because a case that only reads the constants would pass while both
// call sites passed the same one.
func TestAGapRefusalNamesTheApplySiteItCameFrom(t *testing.T) {
	// First, that the two sites are two values. Everything below compares a
	// refusal against the constant it was given, so one constant spelled the
	// same as the other would satisfy every case while telling a reader the
	// Slot's two writes came from one place - which is the one thing the
	// field exists to distinguish.
	if GapSiteBeforeEvents == GapSiteAfterState {
		t.Fatalf("both apply sites are named %q, so a refusal cannot say which one it came from",
			GapSiteBeforeEvents)
	}
	for _, site := range []string{GapSiteBeforeEvents, GapSiteAfterState} {
		t.Run(site, func(t *testing.T) {
			coordinator := &SlotExecutionCoordinator{
				budget: sideEffectTestBudget("gap"),
				ports: Ports{GapGuard: &refusingGapStore{status: execution.GapGuardConflict},
					NoData: SharedNoDataStore, Hosts: SharedHostBusiness,
					Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {})},
			}
			mutation := gapStatement("7", "aa", 3)
			err := coordinator.applyGap(context.Background(), execution.OperationNormal,
				execution.FrozenExecutionContractRef{}, []execution.PlanGapMutation{mutation}, site)
			if err == nil {
				t.Fatal("a refused apply returned no error")
			}
			refusal, named := gapRefusalOf(err)
			if !named {
				t.Fatalf("the refusal did not survive the wrappers: %v", err)
			}
			if refusal.Site != site {
				t.Fatalf("site = %q, want %q: a refusal that names the wrong call sends a reader "+
					"looking for another process when it was this Slot's other write", refusal.Site, site)
			}
			// The revision is filled in from the mutation, not the store's
			// answer, so the two halves of the comparison are on one line.
			if refusal.ExpectedRevision != mutation.ExpectedMarkerRevision {
				t.Fatalf("expected revision = %d, want %d", refusal.ExpectedRevision, mutation.ExpectedMarkerRevision)
			}
		})
	}
}

func gapRefusalOf(err error) (*GapApplyRefusal, bool) {
	var refusal *GapApplyRefusal
	if !errors.As(err, &refusal) {
		return nil, false
	}
	return refusal, true
}
