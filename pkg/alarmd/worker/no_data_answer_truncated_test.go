// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// cutSlots runs whole Slots of one no-data Plan that remembers host b, one
// round a period after the last, each read either streaming hotSeries or
// answering with no series at all, and either carrying a suspected cut on
// the physical query's route or not.
type cutSlots struct {
	t        *testing.T
	header   execution.InternalExecutionHeader
	store    *supplementNoDataStore
	observed *noDataObservations
	ports    *recordingPorts
	run      func(request execution.SlotExecutionRequest) (execution.SlotExecutionResult, error)
	rounds   int
}

func newCutSlots(t *testing.T) *cutSlots {
	t.Helper()
	header := supplementHeaderWith(t, &contract.NoDataConfigV1{Continuous: 1, Level: 2, AggDimension: []string{"host"}})
	evaluation := int64(header.Contract.Slot.EvaluationTime)
	slots := &cutSlots{t: t, header: header, observed: &noDataObservations{}, store: &supplementNoDataStore{
		groups:  []execution.NoDataGroupMemory{{GroupKey: "host=b," + contract.NoDataDimensionTag + "=true", LastSeen: evaluation - 60}},
		present: evaluation - 60,
	}}
	ports, evaluator, _ := workerG4Coordinator(t)
	ports.gapMissing = true
	coordinator, err := worker.NewSlotExecutionCoordinator(worker.Ports{OpenAlerts: ports,
		Finalization: ports, Activation: ports, Query: ports, Sequencer: ports, Evaluator: evaluator,
		Admission: ports, GapGuard: ports, NoData: slots.store, Hosts: worker.SharedHostBusiness,
		Events: ports, State: ports, Progress: ports, Observer: slots.observed,
	}, worker.ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10})
	if err != nil {
		t.Fatal(err)
	}
	slots.ports = ports
	slots.run = func(request execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
		return coordinator.Execute(context.Background(), request)
	}
	return slots
}

// slot runs the next round. An empty read answers with no series: the
// Plan's primary input arrives as a completion binding, FULL and EMPTY, as
// when every series the query returned was withheld before it was streamed.
func (slots *cutSlots) slot(what string, cut, empty bool) {
	t := slots.t
	t.Helper()
	round := slots.header
	round.Contract.Slot.EvaluationTime += execution.EvaluationTime(60 * slots.rounds)
	slots.rounds++
	read := map[execution.SeriesIdentityDigest]string{hotSeries: "80"}
	if empty {
		read = map[execution.SeriesIdentityDigest]string{}
	}
	batches, completion := supplementRead(t, round, read)
	if empty {
		requirement := round.Requirements[0]
		dataset := execution.NewDataset(nil)
		view, err := execution.NewDatasetView(dataset, nil)
		if err != nil {
			t.Fatal(err)
		}
		physical := &completion.PhysicalQueries[0]
		physical.DataState, physical.Delivery = execution.DataStateEmpty, execution.SeriesDelivery{}
		completion.CompletionBindings = []execution.NamedInputBinding{{
			Consumer: requirement.Consumers[0].Consumer, RequirementID: requirement.RequirementID,
			DatasetName: requirement.DatasetName, Role: requirement.Role, ProviderResult: physical.Ref,
			QueryWindow: requirement.AbsoluteWindow(round.Contract.Slot.EvaluationTime), Dataset: dataset, View: view,
			Completeness: execution.CompletenessFull, DataState: execution.DataStateEmpty,
			Disposition: execution.AccessAvailable, ImpactScope: execution.ImpactPlan,
			Provenance: execution.InputProvenance{PhysicalQuery: physical.PhysicalQuery, AttemptNo: 1},
		}}
	}
	if cut {
		completion.PhysicalQueries[0].RouteFacts.Truncation = &execution.ProviderTruncationFact{
			Kind: execution.TruncationTermsCut, Dimension: "host", Cap: 10000, SourceSemantics: "custom/event"}
	}
	slots.ports.executeOverride = streamExecution(round, batches, completion)
	request := workerSlotRequest(round.Contract)
	request.DuePlanTargets.DuePlanSetDigest = round.Contract.DuePlanSetDigest
	if result, err := slots.run(request); err != nil || !result.Completed {
		t.Fatalf("%s: Slot result %+v error %v", what, result, err)
	}
}

// expect checks what the last round filed and what it sent and wrote in all.
func (slots *cutSlots) expect(what, outcome string, absence, events, writes int) {
	t := slots.t
	t.Helper()
	_, filed, lines, _ := slots.observed.take()
	if !reflect.DeepEqual(filed, map[string]int{outcome: 1}) || lines != absence {
		t.Fatalf("%s filed %v with %d absence lines, want %s and %d", what, filed, lines, outcome, absence)
	}
	if got := noDataEventCount(slots.ports.acknowledged); got != events || slots.store.writes() != writes {
		t.Fatalf("%s: %d no-data events and %d writes in all, want %d and %d", what, got, slots.store.writes(), events, writes)
	}
}

// A Slot whose primary answer carries a suspected cut on its route files
// SKIPPED_ANSWER_TRUNCATED for its no-data Plan and judges no absence: no
// no-data event, no memory write, no absence line. The same read without the
// cut is the control: the Plan remembers host b, the read has no series of
// it, and the Slot judges b absent, sends its anomaly and stores it.
func TestASlotWhoseAnswerMayHaveBeenCutJudgesNoAbsence(t *testing.T) {
	slots := newCutSlots(t)
	slots.slot("the cut Slot", true, false)
	slots.expect("the cut Slot", "SKIPPED_ANSWER_TRUNCATED", 0, 0, 0)
	slots.slot("the Slot read whole", false, false)
	slots.expect("the Slot read whole", "EVALUATED", 1, 1, 1)
}

// The same when none of the answer reached the Plan as a series - every
// series the query returned withheld before it was streamed - so the Plan's
// primary input is a completion binding: the cut on that binding's physical
// query is read too. Read whole, the empty answer judges host b absent.
func TestACutAnswerThatReachedThePlanAsACompletionBindingJudgesNoAbsence(t *testing.T) {
	slots := newCutSlots(t)
	slots.slot("the cut empty Slot", true, true)
	slots.expect("the cut empty Slot", "SKIPPED_ANSWER_TRUNCATED", 0, 0, 0)
	slots.slot("the empty Slot read whole", false, true)
	slots.expect("the empty Slot read whole", "EVALUATED", 1, 1, 1)
}
