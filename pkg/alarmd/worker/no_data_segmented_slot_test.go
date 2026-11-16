// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// segmentedNoDataStore holds one Plan's record and answers the round's reads
// in parts as the Redis store does: a scan pages the groups in key order and
// returns repeat twice. It counts what each read carried, whole loads, and a
// write meeting an earlier one of its own version with another statement.
type segmentedNoDataStore struct {
	mu        sync.Mutex
	groups    []execution.NoDataGroupMemory
	present   int64
	repeat    string
	applied   []execution.PlanNoDataMutation
	conflicts int
	reads     int
	whole     int
	maxFields int
}

func (store *segmentedNoDataStore) header(item execution.PlanNoDataLoadItem) execution.NoDataMemorySnapshot {
	return execution.NoDataMemorySnapshot{Identity: item.Identity, Status: execution.NoDataMemoryFound, MarkerRevision: 1,
		SchemaVersion: execution.WrittenNoDataMemorySchema, PersistedMutationDigest: "digest",
		PersistedApplyVersion: execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: execution.EvaluationTime(store.present), SlotDigest: "earlier"},
		LastScheduleRevision:  "plan-schedule-v1", RosterVersion: "HISTORY/1",
		Representation: execution.NoDataRepresentationPerGroup, PresentAsOf: store.present}
}

func (store *segmentedNoDataStore) LoadNoData(_ context.Context, request execution.NoDataLoadRequest) (execution.NoDataLoadResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.whole++
	result := execution.NoDataLoadResult{}
	for _, item := range request.Items {
		snapshot := store.header(item)
		snapshot.Groups = store.groups
		result.Items = append(result.Items, snapshot)
	}
	return result, nil
}

func (store *segmentedNoDataStore) ReadNoDataHeaders(_ context.Context, request execution.NoDataLoadRequest) (execution.NoDataLoadResult, error) {
	result := execution.NoDataLoadResult{}
	for _, item := range request.Items {
		result.Items = append(result.Items, store.header(item))
	}
	return result, nil
}

func (store *segmentedNoDataStore) RecheckNoDataHeader(_ context.Context, identity execution.PlanNoDataIdentity) (execution.NoDataMemorySnapshot, error) {
	return store.header(execution.PlanNoDataLoadItem{Identity: identity}), nil
}

func (store *segmentedNoDataStore) ScanNoDataGroups(_ context.Context, request execution.NoDataScanRequest) (execution.NoDataScanPage, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	start := min(int(request.Cursor), len(store.groups))
	end := min(start+request.Count, len(store.groups))
	store.maxFields = max(store.maxFields, end-start)
	page := execution.NoDataScanPage{Status: execution.NoDataMemoryFound}
	for _, group := range store.groups[start:end] {
		page.Groups = append(page.Groups, group)
		if group.GroupKey == store.repeat {
			page.Groups = append(page.Groups, group)
		}
	}
	if end < len(store.groups) {
		page.Next = uint64(end)
	}
	return page, nil
}

func (store *segmentedNoDataStore) ReadNoDataGroups(_ context.Context, request execution.NoDataGroupsRequest) (execution.NoDataGroupsResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.reads++
	store.maxFields = max(store.maxFields, len(request.Keys))
	result := execution.NoDataGroupsResult{Status: execution.NoDataMemoryFound}
	for _, key := range request.Keys {
		found := execution.NoDataGroupMemory{}
		for _, group := range store.groups {
			if group.GroupKey == key {
				found = group
			}
		}
		result.Groups = append(result.Groups, found)
	}
	return result, nil
}

func (store *segmentedNoDataStore) ApplyNoData(_ context.Context, request execution.NoDataApplyRequest) (execution.NoDataApplyResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	result := execution.NoDataApplyResult{}
	for _, mutation := range request.Items {
		for _, earlier := range store.applied {
			if earlier.ApplyVersion == mutation.ApplyVersion && earlier.MemoryDigest != mutation.MemoryDigest {
				store.conflicts++
			}
		}
		store.applied = append(store.applied, mutation)
		result.Items = append(result.Items, execution.NoDataApplyItemResult{Identity: mutation.Identity, Status: execution.NoDataApplied})
	}
	return result, nil
}

// R2 (plan 5.4): a Slot of several windows - the per-Slot caps at five - whose
// no-data Plan remembers twenty-four hosts and hears from none of them reads
// the absent names in three absence windows of eight, says one absence per
// host, the one the scan returned twice included once, and writes the
// memory once, with no write meeting another of its own version. No read
// carries more than a segment, and nothing reads the memory whole.
func TestASlotOfSeveralWindowsAndThreeAbsenceWindowsWritesItsMemoryOnce(t *testing.T) {
	header := supplementHeaderWith(t, &contract.NoDataConfigV1{Continuous: 1, Level: 2, AggDimension: []string{"host"}})
	evaluation := int64(header.Contract.Slot.EvaluationTime)
	const hosts, segment = 24, 8
	store := &segmentedNoDataStore{present: evaluation - 60}
	for index := range hosts {
		key := fmt.Sprintf("host=h%02d,%s=true", index, contract.NoDataDimensionTag)
		store.groups = append(store.groups, execution.NoDataGroupMemory{GroupKey: key, LastSeen: evaluation - 60})
	}
	sort.Slice(store.groups, func(left, right int) bool { return store.groups[left].GroupKey < store.groups[right].GroupKey })
	store.repeat = store.groups[5].GroupKey

	ports, evaluator, _ := workerG4Coordinator(t)
	ports.gapMissing = true
	coordinator, err := worker.NewSlotExecutionCoordinator(worker.Ports{OpenAlerts: ports,
		Finalization: ports, Activation: ports, Query: ports, Sequencer: ports, Evaluator: evaluator,
		Admission: ports, GapGuard: ports, NoData: store, Hosts: worker.SharedHostBusiness,
		Events: ports, State: ports, Progress: ports, Observer: &noDataObservations{},
	}, worker.ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 64 << 20, MaxStateMutations: 5, MaxEvents: 5, MaxGapMutations: 10})
	if err != nil {
		t.Fatal(err)
	}
	worker.SetNoDataSegmentGroups(coordinator, segment)
	batches, completion := supplementRead(t, header, map[execution.SeriesIdentityDigest]string{hotSeries: "80"})
	ports.executeOverride = streamExecution(header, batches, completion)
	request := workerSlotRequest(header.Contract)
	request.DuePlanTargets.DuePlanSetDigest = header.Contract.DuePlanSetDigest
	result, err := coordinator.Execute(context.Background(), request)
	if err != nil || !result.Completed {
		t.Fatalf("Slot result %+v error %v", result, err)
	}

	if store.reads != hosts/segment {
		t.Fatalf("the absent names were read in %d windows, want %d", store.reads, hosts/segment)
	}
	if got := noDataEventCount(ports.acknowledged); got != hosts {
		t.Fatalf("%d no-data events, want one per absent host (%d)", got, hosts)
	}
	if len(store.applied) != 1 || store.conflicts != 0 {
		t.Fatalf("the memory was written %d times with %d same-version conflicts, want once and none",
			len(store.applied), store.conflicts)
	}
	if ports.stateApplyCalls < 2 {
		t.Fatalf("State was applied in %d calls; the caps of five should have cut the Slot into windows", ports.stateApplyCalls)
	}
	if store.maxFields > segment || store.whole != 0 {
		t.Fatalf("a read carried %d groups (segment %d) and %d reads were whole", store.maxFields, segment, store.whole)
	}
}
