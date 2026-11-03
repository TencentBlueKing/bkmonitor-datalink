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
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func termsCutRoute() execution.ProviderRouteFacts {
	return execution.ProviderRouteFacts{Truncation: &execution.ProviderTruncationFact{
		Kind: execution.TruncationTermsCut, Dimension: "bk_target_ip", Cap: 10000, SourceSemantics: "custom/event"}}
}

func noDataBinding(plan execution.PlanIdentity, role execution.InputRole) execution.NamedInputBinding {
	return execution.NamedInputBinding{Consumer: execution.ConsumerRef{Plan: plan, LevelID: 1, HasLevel: true}, Role: role}
}

// Only a primary answer says which groups reported, so only a primary
// answer that may have been cut marks its Plan: a cut dependency answer, an
// uncut primary one, and another Plan's cut answer leave it unmarked.
func TestOnlyAPrimaryAnswerThatMayHaveBeenCutMarksItsPlan(t *testing.T) {
	due := noDataWiredPlan(t)
	other := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "8"}
	stream := noDataWiredStream(t, due, &emptyNoDataStore{})
	stream.noteAnswerTruncation(noDataBinding(due.Identity, execution.InputRoleAlgorithmDependency), termsCutRoute())
	stream.noteAnswerTruncation(noDataBinding(due.Identity, execution.InputRolePrimary), execution.ProviderRouteFacts{})
	stream.noteAnswerTruncation(noDataBinding(other, execution.InputRolePrimary), termsCutRoute())
	if stream.answerTruncated[due.Identity] {
		t.Fatal("the Plan is marked cut by a dependency answer, an uncut answer or another Plan's answer")
	}
	stream.noteAnswerTruncation(noDataBinding(due.Identity, execution.InputRolePrimary), termsCutRoute())
	if !stream.answerTruncated[due.Identity] || !stream.answerTruncated[other] {
		t.Fatalf("marked %v, want both Plans whose primary answer may have been cut", stream.answerTruncated)
	}
}

// A Plan whose primary answer may have been cut files SKIPPED_ANSWER_TRUNCATED
// and does nothing else: no series in the batch, no state written, no memory
// queued. The same Plan read as whole decides the item absent and stores it,
// which is the control that makes the skip mean something.
func TestAPlanWhoseAnswerMayHaveBeenCutSkipsItsNoDataRoundAndRemembersNothing(t *testing.T) {
	due := noDataWiredPlan(t)
	control := noDataWiredStream(t, due, &emptyNoDataStore{})
	if err := control.loadNoDataMemory(context.Background()); err != nil {
		t.Fatal(err)
	}
	whole, err := control.noDataRoundFor(due, nil, execution.CompletenessFull)
	if err != nil {
		t.Fatal(err)
	}
	if whole.outcome != nodata.OutcomeEvaluated || len(whole.series) == 0 || whole.mutation == nil {
		t.Fatalf("read as whole the round = %q with %d series and mutation %v; the control has to judge",
			whole.outcome, len(whole.series), whole.mutation != nil)
	}

	stream := noDataWiredStream(t, due, &emptyNoDataStore{})
	if err := stream.loadNoDataMemory(context.Background()); err != nil {
		t.Fatal(err)
	}
	stream.noteAnswerTruncation(noDataBinding(due.Identity, execution.InputRolePrimary), termsCutRoute())
	// Nothing reaches the batch, so the fixture's missing state port is the
	// assertion: a series sent on would panic here.
	if err := stream.evaluateNoData(context.Background(), nil, 16); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stream.noDataOutcomes, []nodata.SlotOutcome{nodata.OutcomeSkippedAnswerTruncated}) {
		t.Fatalf("outcomes = %v, want exactly %q", stream.noDataOutcomes, nodata.OutcomeSkippedAnswerTruncated)
	}
	// The round is held among the judged like every skip, and its absence
	// line is not emitted: only an EVALUATED round says what it found.
	if stream.noDataStateMutations != 0 || len(stream.noDataMutations) != 0 {
		t.Fatalf("a cut round spent %d state writes and queued %d memories, want none",
			stream.noDataStateMutations, len(stream.noDataMutations))
	}
}

// Three cut rounds in a row are a stall, reported once on the third under
// the outcome's own name; a round read whole ends the streak, and the next
// three cut rounds are a new stall.
func TestThreeCutRoundsAreAStallAndARoundReadWholeEndsIt(t *testing.T) {
	due := noDataWiredPlan(t)
	duePlans := []execution.DuePlan{due}
	recorded := []observability.Observation{}
	coordinator := &SlotExecutionCoordinator{
		ports: Ports{NoData: &emptyNoDataStore{}, Hosts: SharedHostBusiness, State: failingStatePort{},
			Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				recorded = append(recorded, observation)
			})},
		budget: ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxGapMutations: 10, MaxStateMutations: 100, MaxEvents: 100},
	}
	contract := noDataPreflightContract(t, duePlans)
	whole := map[int]bool{4: true}
	for round := 1; round <= 7; round++ {
		stream := &streamedExecution{coordinator: coordinator,
			header: execution.InternalExecutionHeader{Contract: contract, DuePlans: duePlans}}
		stream.request.Contract = contract
		stream.request.Contract.Slot.EvaluationTime += execution.EvaluationTime(60 * round)
		if err := stream.loadNoDataMemory(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !whole[round] {
			stream.noteAnswerTruncation(noDataBinding(due.Identity, execution.InputRolePrimary), termsCutRoute())
		}
		// The round read whole sends its series on to a state port that
		// refuses them; its outcome is filed before that, and the streak
		// reads the outcome.
		_ = stream.evaluateNoData(context.Background(), nil, 16)
		mark := len(recorded)
		stream.observeNoDataOutcomes(context.Background())
		var stalls []string
		for _, observation := range recorded[mark:] {
			if observation.NoDataStall != nil {
				stalls = append(stalls, observation.NoDataStall.Outcome)
			}
		}
		want := []string(nil)
		if round == 3 || round == 7 {
			want = []string{string(nodata.OutcomeSkippedAnswerTruncated)}
		}
		if !reflect.DeepEqual(stalls, want) {
			t.Fatalf("round %d reported stalls %v, want %v", round, stalls, want)
		}
	}
}
