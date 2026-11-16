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
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// emptyNoDataStore answers every load with "this Plan has written nothing".
//
// It is what the fixtures of tests about something else use, so that the
// no-data port being required does not force each of them to have an opinion
// about no-data. A test that is about no-data uses a store that holds records.
type emptyNoDataStore struct {
	loads   int
	applied []execution.PlanNoDataMutation
}

func (store *emptyNoDataStore) LoadNoData(
	_ context.Context, request execution.NoDataLoadRequest,
) (execution.NoDataLoadResult, error) {
	store.loads++
	result := execution.NoDataLoadResult{Items: make([]execution.NoDataMemorySnapshot, len(request.Items))}
	for index, item := range request.Items {
		result.Items[index] = execution.NoDataMemorySnapshot{
			// NONE rather than an unset field: the store stamps it, and a
			// double that leaves it empty is standing in for a snapshot the
			// store cannot produce.
			Identity: item.Identity, Status: execution.NoDataMemoryMissing,
			Representation: execution.NoDataRepresentationNone,
		}
	}
	return result, nil
}

func (store *emptyNoDataStore) ApplyNoData(
	_ context.Context, request execution.NoDataApplyRequest,
) (execution.NoDataApplyResult, error) {
	result := execution.NoDataApplyResult{Items: make([]execution.NoDataApplyItemResult, len(request.Items))}
	for index, mutation := range request.Items {
		store.applied = append(store.applied, mutation)
		result.Items[index] = execution.NoDataApplyItemResult{
			Identity: mutation.Identity, Status: execution.NoDataApplied,
		}
	}
	return result, nil
}

// SharedNoDataStore is the one the fixtures of unrelated tests point at. Its
// counters are not read by those tests; a test that reads them makes its own.
var SharedNoDataStore = &emptyNoDataStore{}

// fixedHostBusiness answers from a map, which is what the CMDB index is.
type fixedHostBusiness struct {
	byIdentity map[string]string
}

func (lookup fixedHostBusiness) LookupHostBusiness(identity string) (string, bool) {
	business, held := lookup.byIdentity[identity]
	return business, held
}

// HostIndexResolved follows the index this fake stands for: one holding hosts
// is an index, one holding none is a process that has not built one. Tying it
// to the map rather than to a flag of its own is what keeps a fixture from
// claiming to have resolved a target out of an index with nothing in it.
func (lookup fixedHostBusiness) HostIndexResolved() bool { return len(lookup.byIdentity) > 0 }

// SharedHostBusiness holds no host, which is what a deployment whose CMDB index
// has not been built yet looks like. Tests about something else use it; a test
// about host resolution builds its own.
var SharedHostBusiness = fixedHostBusiness{}

// LoadedRoster answers the segmented reads of a memory (execution.
// PlanNoDataRosterReader) from a double's whole load, so a double written
// against LoadNoData answers the round's reads with the same record: the
// header is the load without its groups, a scan pages the groups in key
// order, Count a page, and a read by name picks them out.
type LoadedRoster func(context.Context, execution.NoDataLoadRequest) (execution.NoDataLoadResult, error)

func (load LoadedRoster) Headers(ctx context.Context, request execution.NoDataLoadRequest) (execution.NoDataLoadResult, error) {
	result, err := load(ctx, request)
	if err != nil {
		return execution.NoDataLoadResult{}, err
	}
	items := make([]execution.NoDataMemorySnapshot, len(result.Items))
	for index, item := range result.Items {
		item.Groups = nil
		items[index] = item
	}
	result.Items = items
	return result, nil
}

func (load LoadedRoster) One(ctx context.Context, identity execution.PlanNoDataIdentity) (execution.NoDataMemorySnapshot, error) {
	result, err := load(ctx, execution.NoDataLoadRequest{Items: []execution.PlanNoDataLoadItem{{Identity: identity}}})
	if err != nil {
		return execution.NoDataMemorySnapshot{}, err
	}
	for _, item := range result.Items {
		if item.Identity == identity {
			return item, nil
		}
	}
	return execution.NoDataMemorySnapshot{Identity: identity, Status: execution.NoDataMemoryMissing,
		Representation: execution.NoDataRepresentationNone}, nil
}

func (load LoadedRoster) Recheck(ctx context.Context, identity execution.PlanNoDataIdentity) (execution.NoDataMemorySnapshot, error) {
	snapshot, err := load.One(ctx, identity)
	snapshot.Groups, snapshot.ManualCloses, snapshot.ManualClosesUnread = nil, nil, false
	return snapshot, err
}

func (load LoadedRoster) Scan(ctx context.Context, request execution.NoDataScanRequest) (execution.NoDataScanPage, error) {
	snapshot, err := load.One(ctx, request.Identity)
	if err != nil {
		return execution.NoDataScanPage{}, err
	}
	groups := append([]execution.NoDataGroupMemory(nil), snapshot.Groups...)
	sort.Slice(groups, func(left, right int) bool { return groups[left].GroupKey < groups[right].GroupKey })
	count := request.Count
	if count <= 0 {
		count = execution.NoDataRosterSegmentGroups
	}
	start := min(int(request.Cursor), len(groups))
	end := min(start+count, len(groups))
	page := execution.NoDataScanPage{Groups: groups[start:end], Status: execution.NoDataMemoryFound}
	if end < len(groups) {
		page.Next = uint64(end)
	}
	return page, nil
}

func (load LoadedRoster) Read(ctx context.Context, request execution.NoDataGroupsRequest) (execution.NoDataGroupsResult, error) {
	snapshot, err := load.One(ctx, request.Identity)
	if err != nil {
		return execution.NoDataGroupsResult{}, err
	}
	held := make(map[string]execution.NoDataGroupMemory, len(snapshot.Groups))
	for _, group := range snapshot.Groups {
		held[group.GroupKey] = group
	}
	result := execution.NoDataGroupsResult{Status: execution.NoDataMemoryFound, Groups: make([]execution.NoDataGroupMemory, len(request.Keys))}
	for index, key := range request.Keys {
		result.Groups[index] = held[key]
	}
	return result, nil
}

func (store *emptyNoDataStore) ReadNoDataHeaders(ctx context.Context, request execution.NoDataLoadRequest) (execution.NoDataLoadResult, error) {
	return LoadedRoster(store.LoadNoData).Headers(ctx, request)
}

func (store *emptyNoDataStore) RecheckNoDataHeader(ctx context.Context, identity execution.PlanNoDataIdentity) (execution.NoDataMemorySnapshot, error) {
	return LoadedRoster(store.LoadNoData).Recheck(ctx, identity)
}

func (store *emptyNoDataStore) ScanNoDataGroups(ctx context.Context, request execution.NoDataScanRequest) (execution.NoDataScanPage, error) {
	return LoadedRoster(store.LoadNoData).Scan(ctx, request)
}

func (store *emptyNoDataStore) ReadNoDataGroups(ctx context.Context, request execution.NoDataGroupsRequest) (execution.NoDataGroupsResult, error) {
	return LoadedRoster(store.LoadNoData).Read(ctx, request)
}

// SetNoDataSegmentGroups cuts a coordinator's no-data memory reads into
// segments of groups fields, for a case that reads a small memory in many.
func SetNoDataSegmentGroups(coordinator *SlotExecutionCoordinator, groups int) {
	coordinator.noDataSegment = groups
}

// RetainedInputOf is what a Slot's consumer has charged to its input phase,
// for a black-box measurement to hold against the heap.
func RetainedInputOf(consumer execution.QueryExecutionConsumer) uint64 {
	return consumer.(*streamedExecution).retainedByPhase[retainPhaseInput]
}
