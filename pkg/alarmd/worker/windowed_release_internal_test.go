// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// streamInput binds one series' PRIMARY input the way ConsumeSeries leaves
// it: reserved under the input phase, held by its series, in stream.streamed,
// and prepared for the evaluation.
func (fixture *windowedFixture) streamInput(t *testing.T, series string, bytes uint64) {
	t.Helper()
	stream := fixture.stream
	level := fixture.due.CompiledPlan.Levels().At(0).Definition().LevelID
	consumer := execution.ConsumerRef{Plan: fixture.due.Identity, LevelID: level, HasLevel: true}
	requirement := execution.DataRequirement{RequirementID: "primary", Role: execution.InputRolePrimary}
	if stream.prepared.consumersByPlan == nil {
		stream.prepared.consumersByPlan = map[execution.PlanIdentity][]execution.ConsumerRef{fixture.due.Identity: {consumer}}
		stream.prepared.requirementsByConsumer = map[execution.ConsumerRef][]execution.DataRequirement{consumer: {requirement}}
		stream.streamed = make(map[streamedInputKey]streamedInput)
	}
	digest := execution.SeriesIdentityDigest(series)
	dataset := execution.NewDataset(nil)
	binding := execution.NamedInputBinding{Consumer: consumer, RequirementID: requirement.RequirementID, Role: execution.InputRolePrimary,
		Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Disposition: execution.AccessAvailable, Dataset: dataset}
	if err := stream.reserveRetainedInput(context.Background(), bytes); err != nil {
		t.Fatalf("reserve %s input: %v", series, err)
	}
	hold := stream.holdInput(digest, bytes)
	hold.bindings++
	shared, _ := stream.shared.share(binding)
	stream.streamed[streamedInputKey{consumer: consumer, series: digest, requirement: requirement.RequirementID}] =
		streamedInput{shared: shared, dataset: binding.Dataset, view: binding.View}
	stream.planSeries[fixture.due.Identity][digest] = struct{}{}
	stream.windows.prepared = append(stream.windows.prepared, preparedSeries{due: &fixture.due, identity: digest,
		inputs: []execution.SeriesEvaluationInputRequest{{Inputs: []execution.NamedInputBinding{binding}}}})
}

// mergeSeries merges one evaluated series - its mutation, its one event and
// the view it was read from - through the evaluation's own merge, window
// check first.
func (fixture *windowedFixture) mergeSeries(t *testing.T, position int, series string, viewBytes uint64) {
	t.Helper()
	key := fixture.key(series)
	result := execution.EvaluationResult{Contract: fixture.stream.header.Contract, Result: observability.ResultSuccess,
		ReasonCode: observability.ReasonNone, Plans: []execution.PlanEvaluationResult{{Plan: fixture.due.Identity,
			Disposition: execution.PlanDecided, StateResults: []execution.StateEvaluation{{
				Mutation: execution.StateMutation{Identity: key, ApplyVersion: fixture.version, MutationDigest: execution.MutationDigest("digest-" + series)},
				Events:   []contract.TriggerEventV1{{EventID: "event-" + series}},
			}}}}}
	view := execution.RuntimeStateView{Identity: key, VersionComparison: execution.ApplyVersionPersistedOlder}
	item := execution.StatePreflightItem{Identity: key, ApplyVersion: fixture.version}
	if err := fixture.stream.mergeEvaluatedSeries(context.Background(), position, result, viewBytes,
		[]execution.RuntimeStateView{view}, []execution.StatePreflightItem{item}); err != nil {
		t.Fatalf("merge %s: %v", series, err)
	}
}

// A Slot whose views alone are more than its share is applied in windows
// that each fit: the byte trigger cuts a window before the merge the share
// would refuse, the landed window gives its views, results and inputs back,
// and the Slot never holds more than its share. Each State key is written
// once.
func TestASlotOverItsShareIsAppliedInWindowsThatEachGiveTheirBytesBack(t *testing.T) {
	fixture := newWindowedFixture(t)
	stream, coordinator := fixture.stream, fixture.stream.coordinator
	share := coordinator.qgShareBytes()
	const series = 10
	const inputBytes, viewBytes = uint64(30_000), uint64(100_000)
	if series*(inputBytes+viewBytes) <= share {
		t.Fatalf("the Slot holds %d at most, under its share %d: nothing to cut", series*(inputBytes+viewBytes), share)
	}
	names := make([]string, series)
	for index := range names {
		names[index] = fmt.Sprintf("series-%02d", index)
		fixture.streamInput(t, names[index], inputBytes)
	}
	inputsOnly := RetainedReserved(coordinator)

	var peak uint64
	firstCut := -1
	for index, name := range names {
		before, windows := RetainedReserved(coordinator), stream.windows.count
		fixture.mergeSeries(t, index, name, viewBytes)
		reserved := RetainedReserved(coordinator)
		peak = max(peak, reserved)
		if total := stream.retainedTotal(); total > share {
			t.Fatalf("after %s the Slot holds %d, over its share %d", name, total, share)
		}
		if stream.windows.count == windows || firstCut >= 0 {
			continue
		}
		firstCut = index
		// The window landed before this series merged: it gave back what
		// the series before held, so the Slot holds less than it did
		// before this merge, this series' view included.
		if reserved >= before {
			t.Fatalf("reserved %d after the window landed and %s merged, %d before: the window gave nothing back", reserved, name, before)
		}
		if state := stream.retainedByPhase[retainPhaseState]; state >= 2*viewBytes {
			t.Fatalf("state phase holds %d after the window landed, want this series' view and its keys only", state)
		}
		remaining := uint64(series - index)
		// A landed series keeps nothing of its input: the part its bindings
		// share with the query's other series stays with them, held once.
		if want := remaining * inputBytes; stream.retainedByPhase[retainPhaseInput] != want {
			t.Fatalf("input phase = %d, want %d: the unlanded series' inputs and nothing of a landed one",
				stream.retainedByPhase[retainPhaseInput], want)
		}
		// Nothing of the landed series is reachable from the stream.
		if len(stream.streamed) != int(remaining) || len(stream.inputHolds) != int(remaining) {
			t.Fatalf("streamed=%d holds=%d after the window landed, want the %d series not landed", len(stream.streamed), len(stream.inputHolds), remaining)
		}
		if len(stream.state.Items) != 1 || len(stream.stateItems) != 1 || len(stream.evaluated.Plans[0].StateResults) != 1 {
			t.Fatalf("views=%d items=%d results=%d after the window landed, want only %s's", len(stream.state.Items),
				len(stream.stateItems), len(stream.evaluated.Plans[0].StateResults), name)
		}
		for position, prepared := range stream.windows.prepared {
			if landed := position < index; landed != (prepared.inputs == nil) {
				t.Fatalf("prepared series %d inputs held=%v, want held only for the series not landed", position, prepared.inputs != nil)
			}
		}
	}
	if firstCut < 0 {
		t.Fatal("no window was cut by its bytes: the Slot fitted its share whole")
	}
	if peak > share {
		t.Fatalf("peak reservation %d over the share %d", peak, share)
	}
	if err := coordinator.applyWindow(context.Background(), stream, true); err != nil {
		t.Fatal(err)
	}
	if len(fixture.ports.stateApplied) != series {
		t.Fatalf("State written for %d keys, want %d", len(fixture.ports.stateApplied), series)
	}
	written := map[execution.StateKeyIdentity]int{}
	for _, key := range fixture.ports.stateApplied {
		written[key]++
	}
	for key, times := range written {
		if times != 1 {
			t.Fatalf("key %s written %d times", key.SeriesIdentityDigest, times)
		}
	}
	// Every window landed: what is left is less than the inputs alone were.
	if reserved := RetainedReserved(coordinator); reserved >= inputsOnly {
		t.Fatalf("reserved %d after the last window, %d with the inputs alone: a window kept its bytes", reserved, inputsOnly)
	}
	if len(stream.streamed) != 0 || len(stream.inputHolds) != 0 || len(stream.state.Items) != 0 ||
		len(stream.stateItems) != 0 || len(stream.evaluated.Plans[0].StateResults) != 0 {
		t.Fatalf("after the last window streamed=%d holds=%d views=%d items=%d results=%d, want none", len(stream.streamed),
			len(stream.inputHolds), len(stream.state.Items), len(stream.stateItems), len(stream.evaluated.Plans[0].StateResults))
	}
	// The views are gone; what they said about the Plan for the tail is not.
	if _, unseen := stream.unseenPlans()[fixture.due.Key()]; unseen {
		t.Fatal("the Plan reads as unseen once its windows' views were let go")
	}
}

// A Slot that fits its share whole is not cut by its bytes: it is applied in
// one window, as before windows released anything.
func TestASlotWithinItsShareIsNotCutByItsBytes(t *testing.T) {
	fixture := newWindowedFixture(t)
	stream := fixture.stream
	for index := 0; index < 3; index++ {
		name := fmt.Sprintf("series-%02d", index)
		fixture.streamInput(t, name, 1_000)
		fixture.mergeSeries(t, index, name, 1_000)
	}
	if stream.windows.started || stream.windows.count != 0 {
		t.Fatalf("started=%v windows=%d, want the Slot left whole", stream.windows.started, stream.windows.count)
	}
	if len(stream.streamed) != 3 || len(stream.state.Items) != 3 {
		t.Fatalf("streamed=%d views=%d, want every series still held for the one Sequence", len(stream.streamed), len(stream.state.Items))
	}
}
