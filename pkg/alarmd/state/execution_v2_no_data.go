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
	"time"

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
		snapshot, renewal := store.loadOneNoData(ctx, request, item)
		result.Items[index] = snapshot
		if renewal != nil {
			result.Renewals = append(result.Renewals, *renewal)
		}
	}
	return result, nil
}

// loadOneNoData reads one Plan's per-group memory. The single-value record a
// build before the per-group one wrote is not read: no build since writes it,
// the previous upstream snapshot included, so one still present is left to
// its TTL and the Plan reads as having no memory, as a new Plan does.
func (store *ExecutionStore) loadOneNoData(
	ctx context.Context, request execution.NoDataLoadRequest, item execution.PlanNoDataLoadItem,
) (execution.NoDataMemorySnapshot, *execution.NoDataMemoryRenewal) {
	hash, renewal, consulted := store.loadOneNoDataHash(ctx, request, item)
	if !consulted {
		return representationStamped(execution.NoDataMemorySnapshot{Identity: item.Identity, Status: execution.NoDataMemoryMissing}), renewal
	}
	return representationStamped(hash), renewal
}

// representationStamped gives every snapshot a representation, including the
// ones that read no record.
//
// NONE is a value, not an absence, and it is stamped here rather than filled in
// by each reader. It had been left empty and normalised at the one place that
// counted it, which left every other reader holding a field with two spellings
// for "no record" -- and the write path, which has to know which record a
// statement was derived from, was the reader that could not tell the difference
// between "no record" and "nobody said".
func representationStamped(snapshot execution.NoDataMemorySnapshot) execution.NoDataMemorySnapshot {
	if snapshot.Representation == "" {
		snapshot.Representation = execution.NoDataRepresentationNone
	}
	return snapshot
}

// named says which record a snapshot was read from, and only when one was.
//
// Only a record that was read names a shape. A failed or unreadable one carries
// nothing but its status and its schema, which is the rule the load contract
// already holds: nothing may be taken out of a record this build could not
// read, and "which key answered" would be the first exception to it.
func named(
	snapshot execution.NoDataMemorySnapshot, representation execution.NoDataRepresentation,
) execution.NoDataMemorySnapshot {
	if snapshot.Status == execution.NoDataMemoryFound {
		snapshot.Representation = representation
	}
	return snapshot
}

// loadOneNoDataHash reads the per-group representation. The second return is
// false when the backend cannot hold one at all: nothing can have been written
// there, so the Plan reads as having no memory, and the write reports the
// capability gap.
func (store *ExecutionStore) loadOneNoDataHash(
	ctx context.Context, request execution.NoDataLoadRequest, item execution.PlanNoDataLoadItem,
) (execution.NoDataMemorySnapshot, *execution.NoDataMemoryRenewal, bool) {
	target, routeErr := store.options.Router.Route(item.Identity.Plan.TenantID, item.Identity.Plan.StrategyID)
	if routeErr != nil {
		return execution.NoDataMemorySnapshot{Identity: item.Identity, Status: execution.NoDataMemoryUnavailable,
			ReasonCode: execution.ReasonCode(contract.ReasonRedisUnavailable)}, nil, true
	}
	backend, ok := target.Backend.(NoDataHashBackend)
	if !ok {
		return execution.NoDataMemorySnapshot{}, nil, false
	}
	key, err := PlanNoDataHashKeyV2(store.options.Prefix, item.Identity)
	if err != nil {
		return execution.NoDataMemorySnapshot{Identity: item.Identity, Status: execution.NoDataMemoryTerminal,
			ReasonCode: execution.ReasonCode(contract.ReasonStateCorrupt)}, nil, true
	}
	fields, readErr := backend.ReadHash(ctx, key)
	if readErr != nil {
		return execution.NoDataMemorySnapshot{Identity: item.Identity, Status: execution.NoDataMemoryUnavailable,
			ReasonCode: execution.ReasonCode(contract.ReasonRedisUnavailable)}, nil, true
	}
	if len(fields) == 0 {
		return execution.NoDataMemorySnapshot{Identity: item.Identity, Status: execution.NoDataMemoryMissing}, nil, true
	}
	// Renewed before the record is decoded, and for a record this build cannot
	// read just as much as for one it can: a paused Plan's memory must not
	// expire while the rollback it is waiting out is still going on.
	renewal := store.renewNoDataHash(ctx, target, item, key)
	return store.validatedNoData(request, item,
		named(decodeNoDataHash(fields, item.Identity), execution.NoDataRepresentationPerGroup)), renewal, true
}

// renewNoDataHash keeps a record that was read alive through the renewal gate.
//
// A hash is only touched for the groups that changed, so a Plan whose groups
// are all steady writes nothing for as long as that lasts and would let its
// memory expire underneath it without this. A failed renewal is not this Plan's failure to report - the
// record was read, and the read is what the caller asked for.
func (store *ExecutionStore) renewNoDataHash(
	ctx context.Context, target StorageTarget, item execution.PlanNoDataLoadItem, key string,
) *execution.NoDataMemoryRenewal {
	attempt, err := RenewGenerationKeyReporting(ctx, target, key, item.Retention,
		store.options.RestartMargin, store.options.MinTTL, store.options.MaxTTL, store.renewals, store.options.ReadHoldBound)
	if !attempt.Asked && err == nil {
		// The gate answered from what this process already knows. Reporting it
		// would bury the attempts that reached the store under the ones that
		// did not, and it is only the former that say whether renewal works.
		return nil
	}
	renewal := &execution.NoDataMemoryRenewal{
		Identity: item.Identity, Renewed: attempt.Renewed, TTLSeconds: int64(attempt.TTL.Seconds()),
	}
	switch {
	case err == nil:
	case errors.Is(err, ErrLifetimeUnsupported):
		// A backend that cannot renew leaks the lifetime of every memory it
		// holds. This path reports it and carries on, because stopping here
		// would take a Plan's threshold detection down over the lifetime of a
		// record that is still perfectly readable.
		//
		// The policy is still one policy, because this branch never runs on its
		// own: the condition is a routed backend that does not implement
		// LifetimeBackend, which is deployment-wide and static rather than per
		// Plan, and the gap guard's load on that same backend stops the Slot
		// anyway. So carrying on here loses no protection and gains a named,
		// countable reason from the first round after startup, where stopping
		// gives one failure per Plan per round with nothing naming the cause.
		// Making the two genuinely alike means detecting the capability when
		// the store is opened, so the wiring fails at startup rather than
		// sounding on every Slot; that is a general ruling and is filed
		// separately.
		renewal.ReasonCode = execution.ReasonCode(contract.ReasonBackendCapabilityMissing)
	default:
		renewal.ReasonCode = execution.ReasonCode(contract.ReasonRedisUnavailable)
	}
	return renewal
}

func (store *ExecutionStore) validatedNoData(
	request execution.NoDataLoadRequest, item execution.PlanNoDataLoadItem,
	snapshot execution.NoDataMemorySnapshot,
) execution.NoDataMemorySnapshot {
	one := execution.NoDataLoadRequest{Contract: request.Contract, Items: []execution.PlanNoDataLoadItem{item}}
	if err := execution.ValidateNoDataLoad(one,
		execution.NoDataLoadResult{Items: []execution.NoDataMemorySnapshot{snapshot}}); err != nil {
		return execution.NoDataMemorySnapshot{Identity: item.Identity, Status: execution.NoDataMemoryTerminal,
			ReasonCode: execution.ReasonCode(contract.ReasonStateCorrupt)}
	}
	return snapshot
}

// ApplyNoData replaces one Plan's whole no-data memory per request item.
func (store *ExecutionStore) ApplyNoData(
	ctx context.Context, request execution.NoDataApplyRequest,
) (execution.NoDataApplyResult, error) {
	if err := request.Contract.Validate(); err != nil || len(request.Items) == 0 ||
		len(request.Items) > store.options.MaxItemsPerCall {
		return execution.NoDataApplyResult{}, fmt.Errorf("state: invalid no-data apply request")
	}
	// Every memory's lifetime before any is written, as for gap markers.
	lifetimes := make([]time.Duration, len(request.Items))
	for index, mutation := range request.Items {
		ttl, err := generationWriteTTL(request.Retention, mutation.Identity.Plan,
			store.options.RestartMargin, store.options.MinTTL, store.options.MaxTTL, store.options.ReadHoldBound)
		if err != nil {
			return execution.NoDataApplyResult{}, fmt.Errorf("state: invalid no-data apply request: %w", err)
		}
		lifetimes[index] = ttl
	}
	result := execution.NoDataApplyResult{Items: make([]execution.NoDataApplyItemResult, len(request.Items))}
	for index, mutation := range request.Items {
		if err := ctx.Err(); err != nil {
			return execution.NoDataApplyResult{}, err
		}
		result.Items[index] = store.applyOneNoData(ctx, mutation, lifetimes[index])
	}
	return result, nil
}

func (store *ExecutionStore) applyOneNoData(
	ctx context.Context, mutation execution.PlanNoDataMutation, lifetime time.Duration,
) execution.NoDataApplyItemResult {
	item := execution.NoDataApplyItemResult{Identity: mutation.Identity}
	reject := func(reason string) execution.NoDataApplyItemResult {
		item.Status, item.ReasonCode = execution.NoDataRejected, execution.ReasonCode(reason)
		return item
	}
	tooLarge := func(record execution.NoDataRecordKind, size execution.NoDataRecordSize) execution.NoDataApplyItemResult {
		size.Record = record
		item.Size = &size
		return reject(contract.ReasonStateBudgetExceeded)
	}
	retry := func() execution.NoDataApplyItemResult {
		item.Status, item.ReasonCode = execution.NoDataRetryable, execution.ReasonCode(contract.ReasonRedisUnavailable)
		return item
	}
	conflict := func(kind execution.StateVersionConflictKind, persisted execution.MutationDigest,
		storedRevision uint64,
	) execution.NoDataApplyItemResult {
		item.Status = execution.NoDataConflict
		item.Conflict = &execution.NoDataConflictFacts{
			Kind: kind, Persisted: persisted, Proposed: mutation.MemoryDigest,
			ExpectedRevision: mutation.ExpectedMarkerRevision, StoredRevision: storedRevision,
			DerivedFrom: mutation.DerivedFrom,
		}
		return item
	}
	if err := mutation.ValidateDigest(); err != nil {
		return reject(contract.ReasonStateCorrupt)
	}
	if int(mutation.GroupCount) > store.options.MaxNoDataGroups {
		// The one bound left on a memory, and it is a guard rather than a
		// working limit: the representation has no size ceiling any more, so
		// this only catches an expected set that has run away. It is refused
		// the same way the byte bound was - named, counted, and not thrown -
		// because a Plan whose memory is refused goes on evaluating and only
		// stops remembering.
		return tooLarge(execution.NoDataRecordGroups, execution.NoDataRecordSize{
			Groups: int(mutation.GroupCount), Limit: store.options.MaxNoDataGroups,
		})
	}
	key, err := PlanNoDataHashKeyV2(store.options.Prefix, mutation.Identity)
	if err != nil {
		return reject(contract.ReasonStateCorrupt)
	}
	target, routeErr := store.options.Router.Route(mutation.Identity.Plan.TenantID, mutation.Identity.Plan.StrategyID)
	if routeErr != nil {
		return retry()
	}
	backend, ok := target.Backend.(NoDataHashBackend)
	if !ok {
		return reject(contract.ReasonBackendCapabilityMissing)
	}
	// The header and nothing else. Everything this write decides on is in it,
	// and reading the groups as well would double what every Plan transfers per
	// round -- on the very objects whose size this representation exists to
	// bring down, where the record the read would drag back is the thousands of
	// group fields the write is not touching.
	expectedHeader, readErr := backend.ReadHashField(ctx, key, noDataHeaderField)
	if readErr != nil {
		return retry()
	}
	// The revision this write leaves behind, counted on this record's own line.
	// A first write to it starts at one.
	nextRevision := uint64(1)
	if expectedHeader != nil {
		previous, _, _ := decodeNoDataHashHeader(expectedHeader, mutation.Identity)
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
			if previous.PersistedMutationDigest == mutation.MemoryDigest {
				item.Status = execution.NoDataAlreadyApplied
				return item
			}
			// One version, two statements. Not stale - nothing is newer - and
			// not applied - the memories differ - so it is named for what it
			// is, and it lasts one window: the next round carries a newer
			// apply version and passes on its own.
			return conflict(execution.StateVersionConflictSameVersionOtherStatement,
				previous.PersistedMutationDigest, previous.MarkerRevision)
		}
		// A revision belongs to the record that issued it. Only a statement
		// derived from this record has one to expect of it.
		if !mutation.ReplacesWholeRecord() && previous.MarkerRevision != mutation.ExpectedMarkerRevision {
			// A delta is only meaningful against the revision it was derived
			// from, so this is a conflict rather than something to reconcile.
			kind := execution.StateVersionConflictRevisionMoved
			if previous.MarkerRevision < mutation.ExpectedMarkerRevision {
				kind = execution.StateVersionConflictRevisionReset
			}
			return conflict(kind, previous.PersistedMutationDigest, previous.MarkerRevision)
		}
		// A whole-record statement is derived from no record, so it expects to
		// meet none. One it meets was written between this Slot's read and its
		// write, and replacing it would take the round that wrote it out of the
		// memory -- the retry reads it and derives against it instead.
		if mutation.ReplacesWholeRecord() {
			return conflict(execution.StateVersionConflictRevisionMoved,
				previous.PersistedMutationDigest, previous.MarkerRevision)
		}
		nextRevision = previous.MarkerRevision + 1
	} else if mutation.ExpectedMarkerRevision != 0 && !mutation.ReplacesWholeRecord() {
		// The caller read a record that is no longer there.
		return conflict(execution.StateVersionConflictMissing, "", 0)
	}
	header, encodeErr := json.Marshal(noDataHashHeader{
		Schema: executionNoDataSchema, Version: mutation.SchemaVersion, Identity: mutation.Identity,
		MarkerRevision: nextRevision, ApplyVersion: mutation.ApplyVersion,
		MemoryDigest: mutation.MemoryDigest, ScheduleRevision: mutation.ScheduleRevision,
		RosterVersion: mutation.RosterVersion, PresentAsOf: mutation.PresentAsOf,
		TrackingExhaustedAt: mutation.TrackingExhaustedAt,
	})
	if encodeErr != nil {
		return reject(contract.ReasonStateCorrupt)
	}
	set, deleted, deltaErr := encodeNoDataDelta(mutation)
	if deltaErr != nil {
		return reject(contract.ReasonStateCorrupt)
	}
	write := HashDeltaWrite{
		Key: key, HeaderField: noDataHeaderField,
		ExpectedMissing: expectedHeader == nil, Header: header,
		Set: set, Del: deleted,
		// A statement derived from no record carries the whole memory, so what
		// this one holds goes first. Leaving it would keep every group a writer
		// put there since the read, and no reader could tell the mixture from a
		// memory somebody wrote.
		Replace: mutation.ReplacesWholeRecord(),
		// The Plan's own lifetime, the one the load renews it to, for the same
		// reason a gap marker takes it: written at the floor, the memory of a
		// Plan whose interval is past a day was gone before its next round.
		TTL: lifetime,
	}
	if expectedHeader != nil {
		write.ExpectedDigest = HeaderDigest(expectedHeader)
	}
	outcome, applyErr := backend.ApplyHashDelta(ctx, write)
	switch {
	case applyErr != nil:
		return retry()
	case outcome.Status == HashDeltaConflict:
		// Something wrote between the read above and this call. The header it
		// left is classified exactly as a fresh read would classify it, so the
		// answer does not depend on which of the two paths saw it.
		return conflict(raceConflictKind(outcome.Current, mutation), currentMemoryDigest(outcome.Current),
			currentMarkerRevision(outcome.Current))
	default:
		item.Status = execution.NoDataApplied
	}
	return item
}

// raceConflictKind names a conflict discovered inside the write rather than by
// the read before it. The two have to name the same thing: a reader comparing
// counts across the two paths is asking one question.
func raceConflictKind(
	current []byte, mutation execution.PlanNoDataMutation,
) execution.StateVersionConflictKind {
	if mutation.ReplacesWholeRecord() {
		// A replacing statement expects no revision of this record, so there is
		// no comparison to name. What happened is the only thing that can: the
		// record moved between this writer's read and its write. Comparing the
		// numbers anyway reads the old record's seven against this record's one
		// and reports a reset, which says the key was deleted and recreated --
		// a different incident, and the one a reader would go looking for.
		return execution.StateVersionConflictRevisionMoved
	}
	if len(current) == 0 {
		if mutation.ExpectedMarkerRevision == 0 {
			// The caller expected no record and one appeared.
			return execution.StateVersionConflictRevisionMoved
		}
		return execution.StateVersionConflictMissing
	}
	var header noDataHashHeader
	if err := json.Unmarshal(current, &header); err != nil {
		return execution.StateVersionConflictVersionIncomparable
	}
	switch {
	case header.MarkerRevision > mutation.ExpectedMarkerRevision:
		return execution.StateVersionConflictRevisionMoved
	case header.MarkerRevision < mutation.ExpectedMarkerRevision:
		return execution.StateVersionConflictRevisionReset
	default:
		return execution.StateVersionConflictSameVersionOtherStatement
	}
}

func currentMemoryDigest(current []byte) execution.MutationDigest {
	var header noDataHashHeader
	if err := json.Unmarshal(current, &header); err != nil {
		return ""
	}
	return header.MemoryDigest
}

// currentMarkerRevision is the revision the record holds, for the conflict line
// to compare against the one the statement expected. A header nobody can decode
// reports zero, which is the same thing the absence of a record reports: both
// mean this build cannot name a revision, and neither is a revision anybody
// wrote.
func currentMarkerRevision(current []byte) uint64 {
	var header noDataHashHeader
	if err := json.Unmarshal(current, &header); err != nil {
		return 0
	}
	return header.MarkerRevision
}
