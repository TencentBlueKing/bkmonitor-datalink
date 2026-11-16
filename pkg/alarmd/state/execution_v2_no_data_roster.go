// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"fmt"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The no-data memory read in parts (execution.PlanNoDataRosterReader,
// decision-020 batch 8, 2.7): HGET of the header, HSCAN of the groups a
// segment at a time, HGET of the header again, HMGET of the groups judged
// absent. Nothing here reads the record whole.

// ReadNoDataHeaders reads one Plan's memory header per request item, renews
// the record as LoadNoData does, and reads the strategy's manual closes on
// their own.
func (store *ExecutionStore) ReadNoDataHeaders(
	ctx context.Context, request execution.NoDataLoadRequest,
) (execution.NoDataLoadResult, error) {
	if err := request.Contract.Validate(); err != nil || len(request.Items) == 0 ||
		len(request.Items) > store.options.MaxItemsPerCall {
		return execution.NoDataLoadResult{}, fmt.Errorf("state: invalid no-data header read")
	}
	result := execution.NoDataLoadResult{Items: make([]execution.NoDataMemorySnapshot, len(request.Items))}
	for index, item := range request.Items {
		if err := ctx.Err(); err != nil {
			return execution.NoDataLoadResult{}, err
		}
		snapshot, renewal := store.readOneNoDataHeader(ctx, request, item)
		result.Items[index] = representationStamped(snapshot)
		if renewal != nil {
			result.Renewals = append(result.Renewals, *renewal)
		}
	}
	return result, nil
}

func (store *ExecutionStore) readOneNoDataHeader(
	ctx context.Context, request execution.NoDataLoadRequest, item execution.PlanNoDataLoadItem,
) (execution.NoDataMemorySnapshot, *execution.NoDataMemoryRenewal) {
	unavailable := execution.NoDataMemorySnapshot{Identity: item.Identity, Status: execution.NoDataMemoryUnavailable,
		ReasonCode: execution.ReasonCode(contract.ReasonRedisUnavailable)}
	target, routeErr := store.options.Router.Route(item.Identity.Plan.TenantID, item.Identity.Plan.StrategyID)
	if routeErr != nil {
		return unavailable, nil
	}
	backend, ok := target.Backend.(NoDataHashBackend)
	if !ok {
		// Nothing can have been written there; the write reports the gap.
		return execution.NoDataMemorySnapshot{Identity: item.Identity, Status: execution.NoDataMemoryMissing}, nil
	}
	key, err := PlanNoDataHashKeyV2(store.options.Prefix, item.Identity)
	if err != nil {
		return execution.NoDataMemorySnapshot{Identity: item.Identity, Status: execution.NoDataMemoryTerminal,
			ReasonCode: execution.ReasonCode(contract.ReasonStateCorrupt)}, nil
	}
	raw, readErr := backend.ReadHashField(ctx, key, noDataHeaderField)
	if readErr != nil {
		return unavailable, nil
	}
	if raw == nil {
		// The header is written by every write, so a record without one is no
		// record: the Plan has no memory, as a new Plan has none.
		return execution.NoDataMemorySnapshot{Identity: item.Identity, Status: execution.NoDataMemoryMissing}, nil
	}
	// Renewed before the header is decoded, and for a record this build
	// cannot read as much as for one it can, as the whole load renews.
	renewal := store.renewNoDataHash(ctx, target, item, key)
	snapshot, _, _ := decodeNoDataHashHeader(raw, item.Identity)
	snapshot = store.validatedNoData(request, item, named(snapshot, execution.NoDataRepresentationPerGroup))
	if snapshot.Status == execution.NoDataMemoryFound {
		snapshot.ManualCloses, snapshot.ManualClosesUnread = store.readNoDataManualClosesAlone(ctx, backend, item)
	}
	return snapshot, renewal
}

// readNoDataManualClosesAlone reads a strategy's close hash on its own. It is
// a separate key, bounded by MaxNoDataManualClosesPerStrategy fields, so
// reading it whole is the small read the memory's is not. A backend that
// holds no closes has none; one whose read fails costs the closes alone, as
// readNoDataHashAndCloses has it.
func (store *ExecutionStore) readNoDataManualClosesAlone(
	ctx context.Context, backend NoDataHashBackend, item execution.PlanNoDataLoadItem,
) (map[string]int64, bool) {
	if _, ok := backend.(NoDataManualCloseBackend); !ok {
		return nil, false
	}
	closeKey, err := NoDataManualCloseKey(store.options.Prefix, item.Identity.Plan.TenantID, item.Identity.Plan.StrategyID)
	if err != nil {
		return nil, false
	}
	fields, err := backend.ReadHash(ctx, closeKey)
	if err != nil {
		return nil, true
	}
	return parseNoDataManualCloses(fields), false
}

// RecheckNoDataHeader reads one memory's header again: HGET, no renewal, no
// closes. An error is the read failing; a record that moved or went comes back
// as what it now is.
func (store *ExecutionStore) RecheckNoDataHeader(
	ctx context.Context, identity execution.PlanNoDataIdentity,
) (execution.NoDataMemorySnapshot, error) {
	backend, key, err := store.noDataRosterKey(identity)
	if err != nil {
		return execution.NoDataMemorySnapshot{}, err
	}
	if backend == nil {
		return representationStamped(execution.NoDataMemorySnapshot{Identity: identity, Status: execution.NoDataMemoryMissing}), nil
	}
	raw, err := backend.ReadHashField(ctx, key, noDataHeaderField)
	if err != nil {
		return execution.NoDataMemorySnapshot{}, err
	}
	if raw == nil {
		return representationStamped(execution.NoDataMemorySnapshot{Identity: identity, Status: execution.NoDataMemoryMissing}), nil
	}
	snapshot, _, _ := decodeNoDataHashHeader(raw, identity)
	return representationStamped(named(snapshot, execution.NoDataRepresentationPerGroup)), nil
}

// ScanNoDataGroups reads one segment of a memory's groups: one HSCAN.
func (store *ExecutionStore) ScanNoDataGroups(
	ctx context.Context, request execution.NoDataScanRequest,
) (execution.NoDataScanPage, error) {
	backend, key, err := store.noDataRosterKey(request.Identity)
	if err != nil {
		return execution.NoDataScanPage{}, err
	}
	if backend == nil {
		return execution.NoDataScanPage{Status: execution.NoDataMemoryFound}, nil
	}
	count := request.Count
	if count <= 0 {
		count = execution.NoDataRosterSegmentGroups
	}
	fields, next, err := backend.ScanHashPage(ctx, key, request.Cursor, int64(count))
	if err != nil {
		return execution.NoDataScanPage{Status: execution.NoDataMemoryUnavailable,
			ReasonCode: execution.ReasonCode(contract.ReasonRedisUnavailable)}, nil
	}
	page := execution.NoDataScanPage{Next: next, Status: execution.NoDataMemoryFound,
		Groups: make([]execution.NoDataGroupMemory, 0, len(fields))}
	for _, field := range fields {
		if field.Name == noDataHeaderField {
			continue
		}
		group, ok := decodeNoDataGroupField(field.Name, field.Value, request.PresentAsOf)
		if !ok {
			return noDataReadCorrupt(), nil
		}
		page.Groups = append(page.Groups, group)
	}
	return page, nil
}

// ReadNoDataGroups reads some groups of a memory by key: one HMGET.
func (store *ExecutionStore) ReadNoDataGroups(
	ctx context.Context, request execution.NoDataGroupsRequest,
) (execution.NoDataGroupsResult, error) {
	backend, key, err := store.noDataRosterKey(request.Identity)
	if err != nil {
		return execution.NoDataGroupsResult{}, err
	}
	result := execution.NoDataGroupsResult{Status: execution.NoDataMemoryFound,
		Groups: make([]execution.NoDataGroupMemory, len(request.Keys))}
	if backend == nil || len(request.Keys) == 0 {
		return result, nil
	}
	names := make([]string, len(request.Keys))
	for index, group := range request.Keys {
		names[index] = noDataGroupPrefix + group
	}
	values, err := backend.ReadHashFields(ctx, key, names)
	if err != nil || len(values) != len(names) {
		return execution.NoDataGroupsResult{Status: execution.NoDataMemoryUnavailable,
			ReasonCode: execution.ReasonCode(contract.ReasonRedisUnavailable)}, nil
	}
	for index, value := range values {
		if value == nil {
			continue
		}
		group, ok := decodeNoDataGroupField(names[index], value, request.PresentAsOf)
		if !ok {
			corrupt := noDataReadCorrupt()
			return execution.NoDataGroupsResult{Status: corrupt.Status, ReasonCode: corrupt.ReasonCode}, nil
		}
		result.Groups[index] = group
	}
	return result, nil
}

// noDataRosterKey routes one memory: its backend and key, or a nil backend
// for one that cannot hold a per-group record, where nothing was written.
func (store *ExecutionStore) noDataRosterKey(identity execution.PlanNoDataIdentity) (NoDataHashBackend, string, error) {
	target, err := store.options.Router.Route(identity.Plan.TenantID, identity.Plan.StrategyID)
	if err != nil {
		return nil, "", err
	}
	backend, ok := target.Backend.(NoDataHashBackend)
	if !ok {
		return nil, "", nil
	}
	key, err := PlanNoDataHashKeyV2(store.options.Prefix, identity)
	if err != nil {
		return nil, "", err
	}
	return backend, key, nil
}

// decodeNoDataGroupField reads one group field by the rules decodeNoDataHash
// reads every field by, and the per-group rules a load checks.
func decodeNoDataGroupField(name string, value []byte, presentAsOf int64) (execution.NoDataGroupMemory, bool) {
	key, isGroup := strings.CutPrefix(name, noDataGroupPrefix)
	if !isGroup || key == "" {
		return execution.NoDataGroupMemory{}, false
	}
	group, decoded := decodeNoDataGroupValue(key, value, presentAsOf)
	if !decoded || execution.ValidateNoDataScannedGroup(group) != nil {
		return execution.NoDataGroupMemory{}, false
	}
	return group, true
}

func noDataReadCorrupt() execution.NoDataScanPage {
	return execution.NoDataScanPage{Status: execution.NoDataMemoryTerminal,
		ReasonCode: execution.ReasonCode(contract.ReasonStateCorrupt)}
}
