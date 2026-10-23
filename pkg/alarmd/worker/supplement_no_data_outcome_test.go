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
	"sync"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// supplementNoDataStore holds one Plan's record: the groups it remembers, or,
// while unreadable is set, a record a newer build wrote. It keeps what it was
// asked to write.
type supplementNoDataStore struct {
	mu         sync.Mutex
	unreadable bool
	groups     []execution.NoDataGroupMemory
	present    int64
	applied    []execution.PlanNoDataMutation
}

func (store *supplementNoDataStore) LoadNoData(
	_ context.Context, request execution.NoDataLoadRequest,
) (execution.NoDataLoadResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	result := execution.NoDataLoadResult{Items: make([]execution.NoDataMemorySnapshot, len(request.Items))}
	for index, item := range request.Items {
		if store.unreadable {
			result.Items[index] = execution.NoDataMemorySnapshot{Identity: item.Identity,
				Status: execution.NoDataMemoryUnreadable, SchemaVersion: execution.NoDataMemorySchema(4),
				ReasonCode: execution.ReasonCode(contract.ReasonStateSchemaUnsupported)}
			continue
		}
		result.Items[index] = execution.NoDataMemorySnapshot{
			Identity: item.Identity, Status: execution.NoDataMemoryFound, MarkerRevision: 1,
			SchemaVersion: execution.NoDataMemorySchemaV3, PersistedApplyVersion: item.ApplyVersion,
			PersistedMutationDigest: "digest", LastScheduleRevision: item.ScheduleRevision,
			RosterVersion: "HISTORY/1", Representation: execution.NoDataRepresentationPerGroup,
			PresentAsOf: store.present, Groups: store.groups,
		}
	}
	return result, nil
}

func (store *supplementNoDataStore) ApplyNoData(
	_ context.Context, request execution.NoDataApplyRequest,
) (execution.NoDataApplyResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	result := execution.NoDataApplyResult{Items: make([]execution.NoDataApplyItemResult, len(request.Items))}
	for index, mutation := range request.Items {
		store.applied = append(store.applied, mutation)
		result.Items[index] = execution.NoDataApplyItemResult{Identity: mutation.Identity, Status: execution.NoDataApplied}
	}
	return result, nil
}

func (store *supplementNoDataStore) writes() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return len(store.applied)
}

// noDataObservations is what the no-data side of the worker reported: the
// per-Slot census and partition, the per-Plan absence lines, and the stall
// reports.
type noDataObservations struct {
	mu      sync.Mutex
	census  int
	slots   map[string]int
	absence int
	stalls  []string
}

func (observed *noDataObservations) Observe(_ context.Context, observation observability.Observation) {
	observed.mu.Lock()
	defer observed.mu.Unlock()
	if observation.NoDataCensus != nil {
		observed.census++
	}
	if observation.NoDataSlot != nil {
		if observed.slots == nil {
			observed.slots = map[string]int{}
		}
		observed.slots[observation.NoDataSlot.Outcome] += observation.NoDataSlot.Plans
	}
	if observation.NoDataAbsence != nil {
		observed.absence++
	}
	if observation.NoDataStall != nil {
		observed.stalls = append(observed.stalls, observation.NoDataStall.Outcome)
	}
}

// take returns what was observed since the last take and starts again.
func (observed *noDataObservations) take() (census int, slots map[string]int, absence int, stalls []string) {
	observed.mu.Lock()
	defer observed.mu.Unlock()
	census, slots, absence, stalls = observed.census, observed.slots, observed.absence, observed.stalls
	observed.census, observed.slots, observed.absence, observed.stalls = 0, nil, 0, nil
	return census, slots, absence, stalls
}

func noDataEventCount(events []contract.TriggerEventV1) int {
	count := 0
	for _, event := range events {
		if _, tagged := event.RecordRef.Dimensions[contract.NoDataDimensionTag]; tagged {
			count++
		}
	}
	return count
}

// A supplement judges no absence and files no outcome.
//
// A supplement re-reads one Slot for series that arrived late and evaluates
// only those. Absence is judged on a whole, complete view of the period
// (decomposition section 1.1 and section 5.3 A1); a supplement's series are a
// subset by construction, so every group outside them would read as absent.
// And the outcome partition is per Slot: every Slot files exactly one outcome
// for each of its no-data Plans (decomposition section 5.9, the runtime
// partition), so a supplement that filed one would be a second outcome for a
// Slot that already had its own, and would count toward - or reset - the
// Plan's run of skipped rounds (section 5.9 item 3), which is a count of
// Slots.
//
// The Plan remembers host b and the read carries no series of it. The Slot
// itself judges b absent: one no-data anomaly, one outcome, one memory write.
// Supplements of the same Slot with the same read judge nothing: no no-data
// event, no write, no census, no outcome, no absence line. Then the run: two
// Slots skipped for an unreadable record, a supplement, and a third skipped
// Slot. The stall is reported on the third Slot, once (three consecutive
// skipped rounds, nodata-progress section 6, Q6): a supplement counted as a
// skip would report it a Slot early, and one counted as a judged round would
// start the run again and not report it at all.
func TestASupplementJudgesNoAbsenceAndFilesNoOutcome(t *testing.T) {
	header := supplementHeaderWith(t, &contract.NoDataConfigV1{Continuous: 1, Level: 2, AggDimension: []string{"host"}})
	evaluation := int64(header.Contract.Slot.EvaluationTime)
	store := &supplementNoDataStore{
		groups:  []execution.NoDataGroupMemory{{GroupKey: "host=b," + contract.NoDataDimensionTag + "=true", LastSeen: evaluation - 60}},
		present: evaluation - 60,
	}
	observed := &noDataObservations{}
	ports, evaluator, _ := workerG4Coordinator(t)
	ports.gapMissing = true
	coordinator, err := worker.NewSlotExecutionCoordinator(worker.Ports{OpenAlerts: ports,
		Finalization: ports, Activation: ports, Query: ports, Sequencer: ports, Evaluator: evaluator,
		Admission: ports, GapGuard: ports, NoData: store, Hosts: worker.SharedHostBusiness,
		Events: ports, State: ports, Progress: ports, Observer: observed,
	}, worker.ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10})
	if err != nil {
		t.Fatal(err)
	}
	read := map[execution.SeriesIdentityDigest]string{hotSeries: "80"}
	// Each Slot is its own round, a period after the last: a stall is
	// counted in rounds, and the same round attempted again counts once.
	rounds := 0
	slot := func(what string) {
		t.Helper()
		round := header
		round.Contract.Slot.EvaluationTime += execution.EvaluationTime(60 * rounds)
		rounds++
		batches, completion := supplementRead(t, round, read)
		ports.executeOverride = streamExecution(round, batches, completion)
		request := workerSlotRequest(round.Contract)
		request.DuePlanTargets.DuePlanSetDigest = round.Contract.DuePlanSetDigest
		if result, err := coordinator.Execute(context.Background(), request); err != nil || !result.Completed {
			t.Fatalf("%s: Slot result %+v error %v", what, result, err)
		}
	}
	supplement := func(what string) {
		t.Helper()
		batches, completion := supplementRead(t, header, read)
		ports.executeOverride = streamExecution(header, batches, completion)
		result, err := coordinator.Execute(context.Background(), supplementRequest(header.Contract, hotSeries))
		if err != nil || result.Supplement == nil {
			t.Fatalf("%s: supplement result %+v error %v", what, result, err)
		}
	}

	slot("the Slot")
	census, slots, absence, stalls := observed.take()
	if census != 1 || !reflect.DeepEqual(slots, map[string]int{"EVALUATED": 1}) || absence != 1 || len(stalls) != 0 {
		t.Fatalf("fixture: the Slot reported census %d, outcomes %v, absence lines %d, stalls %v; want one of "+
			"each for the one Plan it judged", census, slots, absence, stalls)
	}
	if got := noDataEventCount(ports.acknowledged); got != 1 || store.writes() != 1 {
		t.Fatalf("fixture: the Slot sent %d no-data events and wrote %d records; want host b's anomaly and "+
			"the memory that records it", got, store.writes())
	}

	for _, what := range []string{"the first supplement", "the second supplement"} {
		sent := len(ports.acknowledged)
		supplement(what)
		census, slots, absence, stalls = observed.take()
		if census != 0 || slots != nil || absence != 0 || stalls != nil {
			t.Fatalf("%s reported census %d, outcomes %v, absence lines %d, stalls %v; a supplement files nothing "+
				"in the Slot partition", what, census, slots, absence, stalls)
		}
		if got := noDataEventCount(ports.acknowledged[sent:]); got != 0 {
			t.Fatalf("%s sent %d no-data events; it judged absence on a subset of the Slot's series", what, got)
		}
		if store.writes() != 1 {
			t.Fatalf("%s wrote no-data memory (%d writes in all, want the Slot's one)", what, store.writes())
		}
	}

	store.mu.Lock()
	store.unreadable = true
	store.mu.Unlock()
	for index, step := range []struct {
		what       string
		supplement bool
		stalls     []string
	}{
		{what: "the first skipped Slot"},
		{what: "the second skipped Slot"},
		{what: "a supplement between them", supplement: true},
		{what: "the third skipped Slot", stalls: []string{"SKIPPED_MEMORY_UNREADABLE"}},
	} {
		if step.supplement {
			supplement(step.what)
		} else {
			slot(step.what)
		}
		_, slots, _, stalls = observed.take()
		want := map[string]int{"SKIPPED_MEMORY_UNREADABLE": 1}
		if step.supplement {
			want = nil
		}
		if !reflect.DeepEqual(slots, want) || !reflect.DeepEqual(stalls, step.stalls) {
			t.Fatalf("step %d (%s): outcomes %v stalls %v, want outcomes %v stalls %v",
				index+1, step.what, slots, stalls, want, step.stalls)
		}
	}
}
