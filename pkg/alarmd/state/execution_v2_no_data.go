// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// executionNoDataSchema names the record and nothing else.
//
// The gap envelope writes "alarmd-plan-gap-v2", which states the record's kind
// and its shape in one string. This one does not, because the shape is already
// a field: two fields saying the same thing can disagree, and nothing would be
// checking. The kind is here and the shape is in Version, which is the field
// the readability rule reads.
const executionNoDataSchema = "alarmd-plan-no-data"

// noDataHeader is everything that has to be read before the body can be.
//
// Decoding happens in two stages and cannot be done in one. A strict decoder
// meeting a field a newer schema added would fail, turning a record this build
// simply cannot read into a corrupt one; a lenient decoder would drop those
// fields and hand back what is left, which is the payload the load contract
// refuses on the grounds that it is a guess. Reading the header first means the
// question "can this build read this record" is answered before anything tries.
type noDataHeader struct {
	Schema   string                       `json:"schema"`
	Version  execution.NoDataMemorySchema `json:"version"`
	Identity execution.PlanNoDataIdentity `json:"identity"`
}

type noDataEnvelope struct {
	Schema           string                         `json:"schema"`
	Version          execution.NoDataMemorySchema   `json:"version"`
	Identity         execution.PlanNoDataIdentity   `json:"identity"`
	MarkerRevision   uint64                         `json:"marker_revision"`
	ApplyVersion     execution.ApplyVersion         `json:"apply_version"`
	MutationDigest   execution.MutationDigest       `json:"mutation_digest"`
	ScheduleRevision execution.PlanScheduleRevision `json:"schedule_revision"`
	RosterVersion    string                         `json:"roster_version"`
	Groups           []execution.NoDataGroupMemory  `json:"groups"`
}

// LoadNoData reads one Plan's no-data memory per request item.
func (store *ExecutionStore) LoadNoData(
	ctx context.Context, request execution.NoDataLoadRequest,
) (execution.NoDataLoadResult, error) {
	if err := request.Contract.Validate(); err != nil || len(request.Items) == 0 ||
		len(request.Items) > store.options.MaxItemsPerCall {
		return execution.NoDataLoadResult{}, fmt.Errorf("state: invalid no-data load request")
	}
	result := execution.NoDataLoadResult{Items: make([]execution.NoDataMemorySnapshot, len(request.Items))}
	for index, item := range request.Items {
		if err := ctx.Err(); err != nil {
			return execution.NoDataLoadResult{}, err
		}
		result.Items[index] = store.loadOneNoData(ctx, request, item)
	}
	return result, nil
}

func (store *ExecutionStore) loadOneNoData(
	ctx context.Context, request execution.NoDataLoadRequest, item execution.PlanNoDataLoadItem,
) execution.NoDataMemorySnapshot {
	snapshot := execution.NoDataMemorySnapshot{Identity: item.Identity, Status: execution.NoDataMemoryMissing}
	raw, err := store.readOneRenewing(ctx, item.Identity.Plan, item.Retention,
		func() (string, error) { return PlanNoDataKeyV2(store.options.Prefix, item.Identity) })
	if err != nil {
		var identityErr *IdentityError
		if errors.Is(err, ErrLifetimeUnsupported) {
			snapshot.Status = execution.NoDataMemoryTerminal
			snapshot.ReasonCode = execution.ReasonCode(contract.ReasonBackendCapabilityMissing)
		} else if errors.As(err, &identityErr) {
			snapshot.Status = execution.NoDataMemoryTerminal
			snapshot.ReasonCode = execution.ReasonCode(contract.ReasonStateCorrupt)
		} else {
			snapshot.Status = execution.NoDataMemoryUnavailable
			snapshot.ReasonCode = execution.ReasonCode(contract.ReasonRedisUnavailable)
		}
		return snapshot
	}
	if raw == nil {
		return snapshot
	}
	if len(raw) > store.options.MaxValueBytes {
		snapshot.Status = execution.NoDataMemoryTerminal
		snapshot.ReasonCode = execution.ReasonCode(contract.ReasonStateBudgetExceeded)
		return snapshot
	}
	snapshot = decodeNoData(raw, item.Identity)
	one := execution.NoDataLoadRequest{Contract: request.Contract, Items: []execution.PlanNoDataLoadItem{item}}
	if err := execution.ValidateNoDataLoad(one,
		execution.NoDataLoadResult{Items: []execution.NoDataMemorySnapshot{snapshot}}); err != nil {
		return execution.NoDataMemorySnapshot{Identity: item.Identity, Status: execution.NoDataMemoryTerminal,
			ReasonCode: execution.ReasonCode(contract.ReasonStateCorrupt)}
	}
	return snapshot
}

// decodeNoData reads a stored record, refusing to read its body until the
// header says this build understands its shape.
func decodeNoData(raw []byte, identity execution.PlanNoDataIdentity) execution.NoDataMemorySnapshot {
	corrupt := execution.NoDataMemorySnapshot{Identity: identity, Status: execution.NoDataMemoryTerminal,
		ReasonCode: execution.ReasonCode(contract.ReasonStateCorrupt)}
	var header noDataHeader
	if err := json.Unmarshal(raw, &header); err != nil {
		return corrupt
	}
	if header.Schema != executionNoDataSchema || header.Identity != identity || header.Version == 0 {
		return corrupt
	}
	if !execution.NoDataMemoryReadable(header.Version) {
		// The schema and nothing else. The body is in a shape this build has no
		// definition for, so anything taken out of it would be a guess with the
		// unrecognised fields already dropped.
		return execution.NoDataMemorySnapshot{Identity: identity, Status: execution.NoDataMemoryUnreadable,
			SchemaVersion: header.Version,
			ReasonCode:    execution.ReasonCode(contract.ReasonStateSchemaUnsupported)}
	}
	var envelope noDataEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return corrupt
	}
	if envelope.MarkerRevision == 0 {
		return corrupt
	}
	return execution.NoDataMemorySnapshot{
		Identity: identity, MarkerRevision: envelope.MarkerRevision,
		PersistedApplyVersion: envelope.ApplyVersion, PersistedMutationDigest: envelope.MutationDigest,
		Status: execution.NoDataMemoryFound, SchemaVersion: envelope.Version,
		LastScheduleRevision: envelope.ScheduleRevision, RosterVersion: envelope.RosterVersion,
		Groups: envelope.Groups,
	}
}

// ApplyNoData replaces one Plan's whole no-data memory per request item.
func (store *ExecutionStore) ApplyNoData(
	ctx context.Context, request execution.NoDataApplyRequest,
) (execution.NoDataApplyResult, error) {
	if err := request.Contract.Validate(); err != nil || len(request.Items) == 0 ||
		len(request.Items) > store.options.MaxItemsPerCall {
		return execution.NoDataApplyResult{}, fmt.Errorf("state: invalid no-data apply request")
	}
	result := execution.NoDataApplyResult{Items: make([]execution.NoDataApplyItemResult, len(request.Items))}
	for index, mutation := range request.Items {
		if err := ctx.Err(); err != nil {
			return execution.NoDataApplyResult{}, err
		}
		result.Items[index] = store.applyOneNoData(ctx, mutation)
	}
	return result, nil
}

func (store *ExecutionStore) applyOneNoData(
	ctx context.Context, mutation execution.PlanNoDataMutation,
) execution.NoDataApplyItemResult {
	item := execution.NoDataApplyItemResult{Identity: mutation.Identity}
	reject := func(reason string) execution.NoDataApplyItemResult {
		item.Status, item.ReasonCode = execution.NoDataRejected, execution.ReasonCode(reason)
		return item
	}
	retry := func() execution.NoDataApplyItemResult {
		item.Status, item.ReasonCode = execution.NoDataRetryable, execution.ReasonCode(contract.ReasonRedisUnavailable)
		return item
	}
	if err := mutation.ValidateDigest(); err != nil {
		return reject(contract.ReasonStateCorrupt)
	}
	key, err := PlanNoDataKeyV2(store.options.Prefix, mutation.Identity)
	if err != nil {
		return reject(contract.ReasonStateCorrupt)
	}
	target, routeErr := store.options.Router.Route(mutation.Identity.Plan.TenantID, mutation.Identity.Plan.StrategyID)
	if routeErr != nil {
		return retry()
	}
	backend, ok := target.Backend.(CompareAndSetBackend)
	if !ok {
		return reject(contract.ReasonBackendCapabilityMissing)
	}
	values, readErr := backend.MGet(ctx, []string{key})
	if readErr != nil || len(values) != 1 {
		return retry()
	}
	raw := values[0]
	if raw != nil {
		if len(raw) > store.options.MaxValueBytes {
			return reject(contract.ReasonStateBudgetExceeded)
		}
		previous := decodeNoData(raw, mutation.Identity)
		switch previous.Status {
		case execution.NoDataMemoryTerminal:
			return reject(string(previous.ReasonCode))
		case execution.NoDataMemoryUnreadable:
			// Not loaded, not applied to, not cleared. Overwriting a record
			// written by a newer build would destroy state that build is still
			// keeping, and it is the one case where the write has more to lose
			// than the round it belongs to.
			return reject(contract.ReasonStateSchemaUnsupported)
		}
		comparison := execution.CompareApplyVersion(previous.PersistedApplyVersion, mutation.ApplyVersion)
		if comparison == execution.ApplyVersionPersistedNewer {
			item.Status = execution.NoDataStale
			return item
		}
		if comparison == execution.ApplyVersionEqual {
			if previous.PersistedMutationDigest == mutation.MutationDigest {
				item.Status = execution.NoDataAlreadyApplied
			} else {
				item.Status = execution.NoDataConflict
			}
			return item
		}
		if previous.MarkerRevision != mutation.ExpectedMarkerRevision {
			item.Status = execution.NoDataConflict
			return item
		}
	} else if mutation.ExpectedMarkerRevision != 0 {
		// The caller read a record that is no longer there.
		item.Status = execution.NoDataConflict
		return item
	}
	next := noDataEnvelope{
		Schema: executionNoDataSchema, Version: mutation.SchemaVersion, Identity: mutation.Identity,
		MarkerRevision: mutation.ExpectedMarkerRevision + 1, ApplyVersion: mutation.ApplyVersion,
		MutationDigest: mutation.MutationDigest, ScheduleRevision: mutation.ScheduleRevision,
		RosterVersion: mutation.RosterVersion, Groups: mutation.Groups,
	}
	encoded, encodeErr := json.Marshal(next)
	if encodeErr != nil || len(encoded) > store.options.MaxValueBytes {
		return reject(contract.ReasonStateBudgetExceeded)
	}
	// The floor, for the same reason a gap marker takes it: the load renews to
	// whatever this Plan needs, and this only has to keep the key from being
	// born without a lifetime at all.
	applied, applyErr := backend.CompareAndSet(ctx, key, raw, raw == nil, encoded, GenerationScopedFloor)
	switch {
	case applyErr != nil:
		return retry()
	case !applied:
		item.Status = execution.NoDataConflict
	default:
		item.Status = execution.NoDataApplied
	}
	return item
}
