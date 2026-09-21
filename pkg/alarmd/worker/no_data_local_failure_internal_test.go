// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
)

// A no-data round that cannot be worked out is that Plan's outcome, not the
// Slot's failure.
//
// A Plan's no-data detection is one half of what the Plan does, and it is the
// half nobody asked for by name. Failing the Slot over it threw away the
// threshold results the same round had already computed, left the progress
// uncommitted, and had the Slot retried to compute them again -- so a Plan
// whose no-data memory did not come back stopped that strategy's ordinary
// detection for as long as the condition lasted.
//
// The Plan that comes after it in the same Slot is the other half of this: the
// old behaviour returned at the first failure, so every Plan behind it lost its
// round too, whether or not it detects no-data at all.
func TestANoDataRoundThatCannotBeDerivedIsThatPlansOutcome(t *testing.T) {
	failing := noDataWiredPlan(t)
	working := failing
	working.Identity.StrategyID = "8"
	working.CompiledPlan = noDataPreflightPlan(t, "8", &contract.NoDataConfigV1{Continuous: 1, Level: 2})
	duePlans := []execution.DuePlan{failing, working}

	stream := &streamedExecution{
		coordinator: &SlotExecutionCoordinator{
			ports:  Ports{NoData: &emptyNoDataStore{}, Hosts: SharedHostBusiness, State: failingStatePort{}},
			budget: ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxGapMutations: 10, MaxStateMutations: 8},
		},
		header: execution.InternalExecutionHeader{
			Contract: noDataPreflightContract(t, duePlans), DuePlans: duePlans,
		},
	}
	if err := stream.loadNoDataMemory(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The first Plan's memory did not come back. That is what a partial load
	// leaves behind, and it is the state the old code answered by failing the
	// whole Slot.
	stream.noData = dropNoDataMemory(stream.noData, failing.Identity.StrategyID)

	err := stream.evaluateNoData(context.Background(), nil, 16)

	if len(stream.noDataOutcomes) != 2 {
		t.Fatalf("outcomes = %+v, want one per Plan: the Plan behind the failure has to have been "+
			"looked at, and the partition has to still add up", stream.noDataOutcomes)
	}
	if stream.noDataOutcomes[0] != nodata.OutcomeSkippedDerivationFailed {
		t.Fatalf("the Plan whose memory was missing landed on %q, want %q",
			stream.noDataOutcomes[0], nodata.OutcomeSkippedDerivationFailed)
	}
	if stream.noDataOutcomes[1] != nodata.OutcomeEvaluated {
		t.Fatalf("the Plan behind it landed on %q, want it evaluated: one Plan's no-data failure is "+
			"not a fact about any other Plan", stream.noDataOutcomes[1])
	}
	// The batch still fails: this fixture has no evaluator, and the second
	// Plan's series reach it. That is the point -- the round got that far.
	if err == nil {
		t.Fatal("fixture: the batch was expected to fail, so its success means the second Plan " +
			"produced nothing and this read the wrong thing")
	}
	// Nothing was remembered for the Plan that failed. A round that could not
	// be decided has nothing to write, and writing the memory it started with
	// would let the next round count an absence from a checkpoint no alert was
	// ever raised against.
	for _, mutation := range stream.noDataMutations {
		if mutation.Identity.Plan.StrategyID == failing.Identity.StrategyID {
			t.Fatalf("the failed Plan wrote memory: %+v", mutation)
		}
	}
}

// A failure this package did not wrap still reaches the Slot.
//
// This is what keeps the containment from swallowing everything. A cancelled
// context or a store that is gone is not a fact about no-data detection, and
// turning one into a per-Plan skip would report a dependency being down as a
// quiet count that nothing acts on.
func TestAFailureThatIsNotANoDataOneStillFailsTheSlot(t *testing.T) {
	if outcome, local := noDataLocalOutcome(errors.New("redis is gone")); local {
		t.Fatalf("an ordinary error was contained as %q", outcome)
	}
	if outcome, local := noDataLocalOutcome(context.Canceled); local {
		t.Fatalf("a cancelled context was contained as %q", outcome)
	}
	outcome, local := noDataLocalOutcome(derivationFailed(errors.New("memory was not loaded")))
	if !local || outcome != nodata.OutcomeSkippedDerivationFailed {
		t.Fatalf("a derivation failure reported (%q, %t)", outcome, local)
	}
	if outcome, local := noDataLocalOutcome(outputFailed(errors.New("no view"))); !local ||
		outcome != nodata.OutcomeSkippedOutputFailed {
		t.Fatalf("an output failure reported (%q, %t)", outcome, local)
	}
}

func dropNoDataMemory(loaded execution.NoDataLoadResult, strategyID string) execution.NoDataLoadResult {
	kept := execution.NoDataLoadResult{}
	for _, item := range loaded.Items {
		if item.Identity.Plan.StrategyID != strategyID {
			kept.Items = append(kept.Items, item)
		}
	}
	return kept
}
