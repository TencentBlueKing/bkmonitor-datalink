// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// Batch bounds for Runtime State storage round trips. They are constants on
// purpose: they protect storage and worker memory and never change what is
// stored, so they are not tenant or deployment knobs.
const (
	// runtimeLoadBatchItems bounds the keys carried by one MGET.
	runtimeLoadBatchItems = execution.StatePreflightBatchItems
	// runtimeLoadBatchBytes bounds what one MGET is expected to return.
	//
	// The apply side has had a byte bound since it was written, and the reason
	// given there applies unchanged here: with MaxValueBytes up to 512 KiB, an
	// item bound alone lets one call carry 128 MiB. The read side was given
	// only the item bound, and a Query Group whose records had grown to 345 KiB
	// each duly asked for 86 MB in a single MGET - which does not fit a 3 s
	// read timeout, failed identically on every attempt, and took the whole
	// Slot with it.
	//
	// Sized like the apply side's, since the two move the same records through
	// the same connection.
	runtimeLoadBatchBytes = 8 << 20
	// runtimeApplyBatchItems bounds the compare-and-set calls in one pipeline
	// round trip.
	runtimeApplyBatchItems = 256
	// runtimeApplyBatchBytes bounds the encoded new values staged for one
	// pipeline round trip. With MaxValueBytes up to 512 KiB an item bound alone
	// could stage 128 MiB; whichever bound is reached first cuts the batch.
	runtimeApplyBatchBytes = 8 << 20
	// runtimeWitnessMaxGroups and runtimeWitnessMaxItems bound the preflight
	// witnesses retained between LoadRuntime and ApplyRuntime. Evicting a
	// witness only sends its item through the sequential re-read path.
	runtimeWitnessMaxGroups = 4096
	runtimeWitnessMaxItems  = 1 << 18
)

// FenceKeyResolver locates the ownership facts fenced Runtime State writes
// verify. The ownership Redis store implements it read-only.
type FenceKeyResolver interface {
	FenceKeys(execution.QueryGroupIdentity) ownership.FenceKeys
}

// runtimeWitness is what LoadRuntime proves about one key so ApplyRuntime can
// compare without reading the value again: the digest of the bytes it saw plus
// the persisted version facts the old re-read path classified against.
type runtimeWitness struct {
	// The record the evaluator read: the newer of the two representations,
	// or missing when neither key held one. The version rule classifies the
	// mutation against these, because they are what the mutation's expected
	// revision and version were taken from.
	missing        bool
	digest         string
	blobRevision   uint64
	applyVersion   execution.ApplyVersion
	mutationDigest execution.MutationDigest
	// The framed key on its own, whatever the evaluator read from. Every
	// write goes to the framed key, so the compare-and-set expects these -
	// missing means create, otherwise the exact bytes seen - and the record
	// written continues framedRevision, never the envelope's count. A
	// revision belongs to one representation; reading the envelope's into a
	// write on the framed key is what made every first write a conflict.
	framedMissing  bool
	framedDigest   string
	framedRevision uint64
}

type runtimeSlotWitnesses struct {
	slot     execution.SlotIdentity
	sequence uint64
	items    map[execution.StateKeyIdentity]runtimeWitness
}

// runtimeWitnessCache keeps preflight witnesses per query group for the Slot
// currently being executed. Slots of one query group run sequentially, so a
// newer Slot replaces the previous witnesses; concurrent query groups keep
// separate entries. A witness is consumed by the apply that uses it.
type runtimeWitnessCache struct {
	mu       sync.Mutex
	groups   map[execution.QueryGroupIdentity]*runtimeSlotWitnesses
	total    int
	sequence uint64
}

func newRuntimeWitnessCache() *runtimeWitnessCache {
	return &runtimeWitnessCache{groups: make(map[execution.QueryGroupIdentity]*runtimeSlotWitnesses)}
}

func (cache *runtimeWitnessCache) remember(slot execution.SlotIdentity, identity execution.StateKeyIdentity, witness runtimeWitness) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	group := cache.groups[slot.QueryGroup]
	if group == nil || group.slot != slot {
		if group != nil {
			cache.total -= len(group.items)
		}
		group = &runtimeSlotWitnesses{slot: slot, items: make(map[execution.StateKeyIdentity]runtimeWitness)}
		cache.groups[slot.QueryGroup] = group
	}
	if _, exists := group.items[identity]; !exists {
		cache.total++
	}
	group.items[identity] = witness
	cache.sequence++
	group.sequence = cache.sequence
	cache.evictLocked(slot.QueryGroup)
}

func (cache *runtimeWitnessCache) take(slot execution.SlotIdentity, identity execution.StateKeyIdentity) (runtimeWitness, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	group := cache.groups[slot.QueryGroup]
	if group == nil || group.slot != slot {
		return runtimeWitness{}, false
	}
	witness, found := group.items[identity]
	if !found {
		return runtimeWitness{}, false
	}
	delete(group.items, identity)
	cache.total--
	if len(group.items) == 0 {
		delete(cache.groups, slot.QueryGroup)
	}
	return witness, true
}

// evictLocked drops the least recently touched groups other than the one being
// filled until both bounds hold. The current group is never evicted so one
// large Slot keeps its witnesses.
func (cache *runtimeWitnessCache) evictLocked(current execution.QueryGroupIdentity) {
	for len(cache.groups) > 1 && (len(cache.groups) > runtimeWitnessMaxGroups || cache.total > runtimeWitnessMaxItems) {
		var victim execution.QueryGroupIdentity
		oldest := uint64(0)
		found := false
		for queryGroup, group := range cache.groups {
			if queryGroup == current {
				continue
			}
			if !found || group.sequence < oldest {
				victim, oldest, found = queryGroup, group.sequence, true
			}
		}
		if !found {
			return
		}
		cache.total -= len(cache.groups[victim].items)
		delete(cache.groups, victim)
	}
}

// classifyWitnessedMutation reproduces the version classification the
// sequential path performs on a fresh read, using the facts recorded at
// preflight. It reports whether the write may proceed.
func classifyWitnessedMutation(witness runtimeWitness, mutation execution.StateMutation) (execution.StateApplyItemResult, bool) {
	item := execution.StateApplyItemResult{Identity: mutation.Identity}
	if witness.missing {
		if mutation.ExpectedBlobRevision != 0 {
			item.MarkVersionConflict(execution.StateVersionConflictMissing, execution.RuntimeStateView{})
			return item, false
		}
		return item, true
	}
	view := execution.RuntimeStateView{BlobRevision: witness.blobRevision,
		PersistedApplyVersion: witness.applyVersion, PersistedMutationDigest: witness.mutationDigest}
	view.VersionComparison = execution.CompareApplyVersion(view.PersistedApplyVersion, mutation.ApplyVersion)
	classified := execution.ClassifyStateMutationDetail(view, mutation)
	switch classified.Disposition {
	case execution.StateAlreadyApplied:
		item.Status, item.AlreadyApplied, item.StoredBlobRevision = execution.StateApplyAlreadyApplied, classified.AlreadyApplied, view.BlobRevision
		return item, false
	case execution.StateStaleVersion:
		item.Status = execution.StateApplyStale
		return item, false
	case execution.StateVersionConflict:
		item.MarkVersionConflict(classified.VersionConflict, view)
		return item, false
	}
	return item, true
}

// classifyFencedOutcome maps one pipeline reply onto the per-item statuses the
// sequential path produces. A conflict carries the current bytes and is
// classified exactly as a fresh read would be; a write that the version rule
// would still allow but whose bytes moved under us is a CAS conflict.
//
// The conflict is on the framed key, and the revision the write expected there
// is expectedFramed - the framed key's own count, not the mutation's expected
// revision, which belongs to whichever representation the evaluator read. The
// version rule is applied in the framed key's revision space so that a plain
// race on that key is named as one, rather than as a reset or a move between
// two counts that were never comparable.
func (store *ExecutionStore) classifyFencedOutcome(
	contractRef execution.FrozenExecutionContractRef, mutation execution.StateMutation, outcome FencedWriteOutcome, expectedFramed uint64,
) execution.StateApplyItemResult {
	item := execution.StateApplyItemResult{Identity: mutation.Identity}
	if outcome.Err != nil {
		item.Status, item.ReasonCode = execution.StateApplyRetryable, execution.ReasonCode(contract.ReasonStateWriteRetryable)
		return item
	}
	switch outcome.Status {
	case FencedWriteApplied:
		item.Status = execution.StateApplied
	case FencedWriteConflictMissing:
		if expectedFramed != 0 {
			item.MarkVersionConflict(execution.StateVersionConflictMissing, execution.RuntimeStateView{})
		} else {
			item.Status, item.ReasonCode = execution.StateApplyCASConflict, execution.ReasonCode(contract.ReasonStateWriteRetryable)
		}
	case FencedWriteConflict:
		raw := outcome.Current
		if len(raw) > store.options.MaxValueBytes {
			item.Status, item.ReasonCode = execution.StateApplyDeterministicInvalid, execution.ReasonCode(contract.ReasonStateBudgetExceeded)
			return item
		}
		view := decodeRuntime(raw, mutation.Identity, contractRef, mutation.ApplyVersion)
		if view.Status == execution.StateDeterministicInvalid {
			item.Status, item.ReasonCode = execution.StateApplyDeterministicInvalid, view.ReasonCode
			return item
		}
		view.VersionComparison = execution.CompareApplyVersion(view.PersistedApplyVersion, mutation.ApplyVersion)
		framedExpectation := mutation
		framedExpectation.ExpectedBlobRevision = expectedFramed
		classified := execution.ClassifyStateMutationDetail(view, framedExpectation)
		switch classified.Disposition {
		case execution.StateAlreadyApplied:
			item.Status, item.AlreadyApplied, item.StoredBlobRevision = execution.StateApplyAlreadyApplied, classified.AlreadyApplied, view.BlobRevision
		case execution.StateStaleVersion:
			item.Status = execution.StateApplyStale
		case execution.StateVersionConflict:
			item.MarkVersionConflict(classified.VersionConflict, view)
		default:
			item.Status, item.ReasonCode = execution.StateApplyCASConflict, execution.ReasonCode(contract.ReasonStateWriteRetryable)
		}
	default:
		item.Status, item.ReasonCode = execution.StateApplyRetryable, execution.ReasonCode(contract.ReasonStateWriteRetryable)
	}
	return item
}

// runtimeApplyPipeline stages digest-proven writes for one storage target and
// sends them in bounded round trips. Results are written back by item index so
// the caller's order is preserved.
type runtimeApplyPipeline struct {
	store   *ExecutionStore
	guard   *FenceGuard
	request execution.StateApplyRequest
	result  *execution.StateApplyResult
	backend FencedBatchBackend
	target  string
	indexes []int
	writes  []FencedWrite
	// expected is the framed key's revision each write was built against,
	// zero when the key was missing: what a conflict reply is read against.
	expected []uint64
	bytes    int
}

func (pipeline *runtimeApplyPipeline) add(
	ctx context.Context, backend FencedBatchBackend, target string, index int, write FencedWrite, expected uint64,
) error {
	if len(pipeline.writes) > 0 && (pipeline.target != target || len(pipeline.writes) >= runtimeApplyBatchItems ||
		pipeline.bytes+len(write.Value) > runtimeApplyBatchBytes) {
		if err := pipeline.flush(ctx); err != nil {
			return err
		}
	}
	pipeline.backend, pipeline.target = backend, target
	pipeline.indexes = append(pipeline.indexes, index)
	pipeline.writes = append(pipeline.writes, write)
	pipeline.expected = append(pipeline.expected, expected)
	pipeline.bytes += len(write.Value)
	return nil
}

func (pipeline *runtimeApplyPipeline) reset() {
	pipeline.indexes, pipeline.writes, pipeline.expected, pipeline.bytes = pipeline.indexes[:0], pipeline.writes[:0], pipeline.expected[:0], 0
}

// flush exchanges the staged batch. When the exchange itself fails every
// staged item is in doubt and becomes retryable, exactly like one failed EVAL
// on the sequential path. A stale owner fails the whole request after the
// other replies of the same batch have been recorded.
func (pipeline *runtimeApplyPipeline) flush(ctx context.Context) error {
	if len(pipeline.writes) == 0 {
		return nil
	}
	defer pipeline.reset()
	outcomes, err := pipeline.backend.CompareAndSetManyByDigest(ctx, pipeline.guard, pipeline.writes)
	if err != nil || len(outcomes) != len(pipeline.writes) {
		for _, index := range pipeline.indexes {
			pipeline.result.Items[index] = execution.StateApplyItemResult{Identity: pipeline.request.Items[index].Identity,
				Status: execution.StateApplyRetryable, ReasonCode: execution.ReasonCode(contract.ReasonStateWriteRetryable)}
		}
		return nil
	}
	stale, moved := false, false
	for position, index := range pipeline.indexes {
		outcome := outcomes[position]
		if outcome.Err == nil && outcome.Status == FencedWriteStaleOwner {
			stale = true
			continue
		}
		if outcome.Err == nil && outcome.Status == FencedWriteContentMoved {
			moved = true
			continue
		}
		pipeline.result.Items[index] = pipeline.store.classifyFencedOutcome(pipeline.request.Contract, pipeline.request.Items[index], outcome, pipeline.expected[position])
	}
	if stale {
		return fmt.Errorf("state: runtime state apply for %s: %w", pipeline.request.Contract.Slot.QueryGroup, ownership.ErrStaleFence)
	}
	if moved {
		// Not a stale fence: the lease is live and the Query Group is still
		// this worker's. What is behind is the view it wrote from, so the
		// error names that and the caller re-reads rather than releases.
		return fmt.Errorf("state: runtime state apply for %s: %w", pipeline.request.Contract.Slot.QueryGroup, ownership.ErrContentScopeMoved)
	}
	return nil
}

// ApplyRuntimeFenced applies like ApplyRuntime but verifies the owner fence
// inside every write. Without a FenceKeyResolver the store cannot locate the
// lease and applies unfenced, relying on the admission-time check as before.
func (store *ExecutionStore) ApplyRuntimeFenced(
	ctx context.Context, request execution.StateApplyRequest, fence execution.StateApplyFence,
) (execution.StateApplyResult, error) {
	if err := fence.Validate(request.Contract); err != nil {
		return execution.StateApplyResult{}, fmt.Errorf("state: invalid runtime apply fence: %w", err)
	}
	if store.options.FenceKeys == nil {
		return store.applyRuntime(ctx, request, nil)
	}
	guard := &FenceGuard{Keys: store.options.FenceKeys.FenceKeys(fence.Fence.QueryGroup), OwnerID: fence.Fence.OwnerID,
		OwnerEpoch: fence.Fence.OwnerEpoch, LeaseToken: fence.Fence.LeaseToken, ContentScope: fence.ContentScope}
	if err := guard.validate(); err != nil {
		return execution.StateApplyResult{}, err
	}
	return store.applyRuntime(ctx, request, guard)
}

func (store *ExecutionStore) applyRuntime(
	ctx context.Context, request execution.StateApplyRequest, guard *FenceGuard,
) (execution.StateApplyResult, error) {
	if err := request.Contract.Validate(); err != nil || len(request.Items) == 0 || len(request.Items) > store.options.MaxItemsPerCall {
		return execution.StateApplyResult{}, fmt.Errorf("state: invalid runtime apply request")
	}
	// One TTL for the request: every key it writes belongs to the same Plan and
	// so has the same retention need. Admission derives it the same way, so a
	// request that was admitted cannot be refused here.
	ttl, err := store.runtimeTTL(request.Retention, request.HorizonSeconds)
	if err != nil {
		if !errors.Is(err, ErrStateBudget) {
			return execution.StateApplyResult{}, fmt.Errorf("state: invalid runtime apply request: %w", err)
		}
		refused := execution.StateApplyResult{Items: rejectRuntimeBudget(request.Items)}
		return refused, refused.Validate()
	}
	result := execution.StateApplyResult{Items: make([]execution.StateApplyItemResult, len(request.Items))}
	keys := make([]string, len(request.Items))
	keyErrors := make([]error, len(request.Items))
	seen := make(map[string]struct{}, len(request.Items))
	duplicate := false
	for index, mutation := range request.Items {
		// Every write goes to the framed key. The envelope is read, never
		// written, and expires on its own TTL once nothing writes it.
		keys[index], keyErrors[index] = RuntimeStateKeyV3(store.options.Prefix, mutation.Identity)
		if keyErrors[index] != nil {
			continue
		}
		if _, repeated := seen[keys[index]]; repeated {
			// A later item must observe the earlier write of the same key.
			// Only the sequential path re-reads between the two, so a request
			// with repeated keys never enters the pipeline.
			duplicate = true
		}
		seen[keys[index]] = struct{}{}
	}
	pipeline := &runtimeApplyPipeline{store: store, guard: guard, request: request, result: &result}
	written := make(map[string]struct{}, len(request.Items))
	for index, mutation := range request.Items {
		item := execution.StateApplyItemResult{Identity: mutation.Identity}
		// Two refusals, not one. Both reach the line as STATE_CORRUPT, and
		// which one happened is the difference between reading what the
		// producer computed and reading the identity it computed it for.
		if err := mutation.ValidateDigest(); err != nil {
			item.Status, item.ReasonCode = execution.StateApplyDeterministicInvalid, execution.ReasonCode(contract.ReasonStateCorrupt)
			item.RefusalRule = PackedRuleMutationDigestMismatch
			result.Items[index] = item
			continue
		}
		if keyErrors[index] != nil {
			item.Status, item.ReasonCode = execution.StateApplyDeterministicInvalid, execution.ReasonCode(contract.ReasonStateCorrupt)
			item.RefusalRule = PackedRuleIdentityKeyUnderivable
			result.Items[index] = item
			continue
		}
		target, err := store.options.Router.Route(mutation.Identity.Plan.TenantID, mutation.Identity.Plan.StrategyID)
		if err != nil {
			item.Status, item.ReasonCode = execution.StateApplyRetryable, execution.ReasonCode(contract.ReasonRedisUnavailable)
			result.Items[index] = item
			continue
		}
		witness, witnessed := store.witnesses.take(request.Contract.Slot, mutation.Identity)
		batchBackend, batched := target.Backend.(FencedBatchBackend)
		if !witnessed || !batched || duplicate {
			if err := pipeline.flush(ctx); err != nil {
				return execution.StateApplyResult{}, err
			}
			casBackend, ok := target.Backend.(CompareAndSetBackend)
			if !ok {
				// The routed backend cannot do what this write needs, which is
				// wiring rather than weather: retrying reaches the same backend.
				item.Status = execution.StateApplyDeterministicInvalid
				item.ReasonCode = execution.ReasonCode(contract.ReasonBackendCapabilityMissing)
				result.Items[index] = item
				continue
			}
			var fromEnvelope bool
			result.Items[index], fromEnvelope = store.applyRuntimeSequential(ctx, request.Contract, mutation, keys[index], ttl, casBackend)
			if fromEnvelope {
				result.EnvelopeReads++
			}
			// The later copy of a key this request already wrote meets the
			// earlier copy's bytes one revision up. Name that for what it is
			// -- the producer sent one series twice -- so it does not count as
			// a re-sent write.
			if _, earlier := written[keys[index]]; earlier {
				result.Items[index].RepeatedKey = true
				if result.Items[index].Status == execution.StateApplyAlreadyApplied &&
					result.Items[index].AlreadyApplied == execution.StateAlreadyAppliedRevisionSkew {
					result.Items[index].AlreadyApplied = execution.StateAlreadyAppliedRepeatedKey
				}
			}
			written[keys[index]] = struct{}{}
			continue
		}
		if classified, proceed := classifyWitnessedMutation(witness, mutation); !proceed {
			result.Items[index] = classified
			continue
		}
		encoded, refusal, rule, legacyIDs := store.encodeForWrite(mutation, witness.framedRevision+1)
		if refusal != "" {
			item.Status, item.ReasonCode = execution.StateApplyDeterministicInvalid, execution.ReasonCode(refusal)
			item.RefusalRule = rule
			result.Items[index] = item
			continue
		}
		item.LegacyRecordIDs = legacyIDs
		write := FencedWrite{Key: keys[index], ExpectedMissing: witness.framedMissing, ExpectedDigest: witness.framedDigest,
			Value: encoded, TTL: ttl}
		if err := pipeline.add(ctx, batchBackend, target.Name, index, write, witness.framedRevision); err != nil {
			return execution.StateApplyResult{}, err
		}
	}
	if err := pipeline.flush(ctx); err != nil {
		return execution.StateApplyResult{}, err
	}
	return result, result.Validate()
}

// applyRuntimeSequential is the original per-key path: read the current value,
// classify it, then compare-and-set against the exact bytes just read. It
// serves callers without a preflight witness and requests with repeated keys.
//
// The second result says whether the envelope decided this item: the record
// the write was classified against came from it, or it refused the write with
// no frame beside it. That is this path's own count of its dependence on the
// older representation, reported on the apply and never folded into the
// preflight's.
func (store *ExecutionStore) applyRuntimeSequential(
	ctx context.Context, contractRef execution.FrozenExecutionContractRef, mutation execution.StateMutation,
	framedKey string, ttl time.Duration, backend CompareAndSetBackend,
) (execution.StateApplyItemResult, bool) {
	item := execution.StateApplyItemResult{Identity: mutation.Identity}
	envelopeKey, err := RuntimeStateKeyV2(store.options.Prefix, mutation.Identity)
	if err != nil {
		item.Status, item.ReasonCode = execution.StateApplyDeterministicInvalid, execution.ReasonCode(contract.ReasonStateCorrupt)
		item.RefusalRule = PackedRuleIdentityKeyUnderivable
		return item, false
	}
	values, err := backend.MGet(ctx, []string{envelopeKey, framedKey})
	if err != nil {
		item.Status, item.ReasonCode = execution.StateApplyRetryable, execution.ReasonCode(contract.ReasonRedisUnavailable)
		return item, false
	}
	var envelopeRaw, framedRaw []byte
	if len(values) == 2 {
		envelopeRaw, framedRaw = values[0], values[1]
	}
	// Both keys, and the newer of the two records, which is what LoadRuntime
	// did before its read was split in two. The load no longer does: once a
	// frame is there it never reads the envelope at all, and this path still
	// reads both and takes whichever is newer. So a series whose envelope is
	// the newer statement reads as the envelope here and as the frame there.
	//
	// It is left differing rather than split to match. This path runs for a
	// series without a preflight witness - a repeated key, or a caller with no
	// preflight - and it re-reads in order to compare against exact bytes, so
	// the second key costs it a value it already has the round trip for. The
	// load's split exists to stop fetching a 344 KB record for every series of
	// every Slot; there is no such multiplier here.
	//
	// What the difference costs is that the load's envelope count cannot see
	// this path. Whether this path still depends on the envelope is its own
	// count - the second result, reported on the apply as
	// envelope_reads_apply and counted in state_envelope_apply_items_total -
	// and the envelope can go only when that count and the load's
	// old_representation outcome have both stayed at zero.
	view, source := store.readStoredRecordSourced(execution.StatePreflightRequest{Contract: contractRef},
		execution.StatePreflightItem{Identity: mutation.Identity, ApplyVersion: mutation.ApplyVersion}, envelopeRaw, framedRaw)
	fromEnvelope := source == runtimeViewEnvelope
	witness, _ := store.witnesses.take(contractRef.Slot, mutation.Identity)
	if view.Status == execution.StateDeterministicInvalid {
		item.Status, item.ReasonCode = execution.StateApplyDeterministicInvalid, view.ReasonCode
		return item, fromEnvelope
	}
	if classified, proceed := classifyWitnessedMutation(witness, mutation); !proceed {
		return classified, fromEnvelope
	}
	encoded, refusal, rule, legacyIDs := store.encodeForWrite(mutation, witness.framedRevision+1)
	if refusal != "" {
		item.Status, item.ReasonCode = execution.StateApplyDeterministicInvalid, execution.ReasonCode(refusal)
		item.RefusalRule = rule
		return item, fromEnvelope
	}
	item.LegacyRecordIDs = legacyIDs
	applied, err := backend.CompareAndSet(ctx, framedKey, framedRaw, framedRaw == nil, encoded, ttl)
	if err != nil {
		item.Status, item.ReasonCode = execution.StateApplyRetryable, execution.ReasonCode(contract.ReasonStateWriteRetryable)
	} else if !applied {
		item.Status, item.ReasonCode = execution.StateApplyCASConflict, execution.ReasonCode(contract.ReasonStateWriteRetryable)
	} else {
		item.Status = execution.StateApplied
	}
	return item, fromEnvelope
}

// encodeForWrite frames the record every write stores, naming the refusal
// when it cannot: a record that disagrees with the framed contract is
// STATE_CORRUPT - the producer sent something no representation can hold -
// and one that frames but does not fit is the budget.
func (store *ExecutionStore) encodeForWrite(mutation execution.StateMutation, revision uint64) ([]byte, string, string, int) {
	encoded, legacy, err := encodeRuntimePackedCounted(mutation, revision)
	switch {
	case errors.Is(err, ErrPackedContract):
		// The rule travels with the reason. Eight rules share STATE_CORRUPT,
		// and which one refused is the difference between a producer that
		// stopped deriving record ids and one that sent two fingerprints for
		// a Level - different code, different fix.
		return nil, contract.ReasonStateCorrupt, PackedRefusalRule(err), 0
	case err != nil || len(encoded) > store.options.MaxValueBytes:
		return nil, contract.ReasonStateBudgetExceeded, "", 0
	}
	return encoded, "", "", legacy
}

// runtimeValueSizeGroups bounds how many Query Groups the store remembers a
// record size for. Past it the table is dropped and relearned, which costs one
// safe-sized batch per Query Group and never more than that.
const runtimeValueSizeGroups = 4096

// expectedValueBytes is what one record of this Query Group has been costing,
// learned from the reads that came back.
//
// Per Query Group, because record size is a property of one strategy's
// retention and Level count and nothing else. A process-wide average is the
// number that is wrong for every Query Group at once: a replica holding two
// thousand ordinary objects of a few KiB and one object of 345 KiB averages to
// a few KiB, so the one object that needs a small batch is the one that gets
// the largest - which is the shape this bound exists for, unbounded by the
// bound meant to catch it.
//
// Zero until this Query Group has been read.
func (store *ExecutionStore) expectedValueBytes(group execution.QueryGroupIdentity) (uint64, bool) {
	store.valueSizes.mu.RLock()
	defer store.valueSizes.mu.RUnlock()
	size, learned := store.valueSizes.bytes[group]
	return size, learned
}

// observeValueBytes folds one batch's result into that Query Group's average.
//
// Called only for a batch that came back. A failed read has loaded zero bytes,
// and folding that in teaches the opposite of what the failure shows: the
// average falls, the next batch is allowed to be at least as large, and the
// read that was already too big to finish is reissued at the same size. An
// instrument that learns "smaller" from the event it exists to catch reports
// the reverse of the truth precisely when it is consulted.
//
// Recent reads weigh more, because the moment this matters is right after a
// strategy's retention was raised, and a lifetime mean is slowest exactly then.
func (store *ExecutionStore) commitValueBytes(group execution.QueryGroupIdentity, largest int, read bool) {
	if !read || largest < 0 {
		// A read that did not come back teaches nothing. Folded in, a failed
		// batch would lower the bound and the read that was already too big to
		// finish would be reissued at the same size or larger - an instrument
		// that learns "smaller" from the event it exists to catch.
		return
	}
	// The largest single record this round, not the mean of them.
	//
	// A bound sized by a mean is wrong whenever the population is not uniform,
	// and one round's keys are not: a Query Group holds records of every shape
	// its Plans produce, and during a representation migration it holds two
	// populations tens of times apart. The mean is pulled down by the many
	// small ones, the batch grows to match, and the few large ones in it blow
	// the budget. The largest is the only statistic a bound can be built from.
	//
	// Scoped to the round rather than kept forever. A preflight reads every key
	// of the Query Group, so this round's largest is a complete measurement of
	// the population, and next round's bound is built from it. Keeping a
	// high-water mark instead would never come down: a strategy whose records
	// shrank - which is exactly what a migration does - would stay on the
	// smallest batch for as long as the process ran, and the learning this
	// bound exists for would be dead.
	sample := uint64(largest)
	if sample > 0 {
		// A record grows by a point or two between rounds; the bound is built
		// with room for that rather than being exactly last round's largest,
		// so ordinary growth does not spend a round over budget.
		sample += sample / 16
	}
	store.valueSizes.mu.Lock()
	defer store.valueSizes.mu.Unlock()
	if store.valueSizes.bytes == nil {
		store.valueSizes.bytes = make(map[execution.QueryGroupIdentity]uint64, 64)
	}
	if _, known := store.valueSizes.bytes[group]; !known && len(store.valueSizes.bytes) >= runtimeValueSizeGroups {
		// Relearn rather than grow without bound or evict by some rule nobody
		// can predict. Every Query Group pays one safe-sized batch and is back
		// where it was.
		store.valueSizes.bytes = make(map[execution.QueryGroupIdentity]uint64, 64)
	}
	store.valueSizes.bytes[group] = sample
}

// runtimeLoadBatchLimit is how many keys of this Query Group one MGET may ask
// for: the item bound, reduced to what those records are expected to weigh.
//
// A Query Group nothing has been read for yet gets the only bound that holds
// whatever its records turn out to be - the batch budget divided by the largest
// value the store will accept. That is 16 keys at the shipped limits, which is
// small for the great majority of records nowhere near that size, and it is the
// right price for exactly one call: the alternative is to guess, and the guess
// that matters is the one made for the object whose records are enormous.
// roundLargest is the largest record this preflight has seen so far. Against
// a committed bound it can only tighten: the batches of one round are sized
// by the bound, so the first batch is whatever keys came first, and a first
// batch of small records says nothing about the keys not yet read - the
// committed bound is the last complete measurement of them, and a round that
// replaced it with its own first sixteen would size its second batch for
// 4 KiB records and read 512 KiB ones with it, sixteen times the budget in
// one call. That is the mixed population this bound exists for: a Query
// Group whose records are changing representation holds both sizes at once,
// and so does one whose series differ in age. For a Query Group nothing is
// committed for, the running largest is the only measurement there is, and
// it is what makes a cold Query Group cheap: the first batch is the safe
// sixteen keys, and every batch after it is sized by what those turned out
// to weigh.
func (store *ExecutionStore) runtimeLoadBatchLimit(group execution.QueryGroupIdentity, roundLargest int, roundRead bool) int {
	expected, learned := store.expectedValueBytes(group)
	if roundRead {
		// Including a largest of zero. A round whose keys all came back empty
		// has measured this Query Group - that is what a strategy which has
		// not written state yet looks like, and it is the common case on a
		// cold replica - and refusing to count it would leave every such
		// Query Group on the safe sixteen keys for as long as it stayed cold,
		// which for a thousand keys is sixty round trips where four would do.
		// A round that did not come back is the one that measures nothing, and
		// that is what roundRead is false for.
		if !learned || uint64(roundLargest) > expected {
			expected = uint64(roundLargest)
		}
		learned = true
	}
	if !learned {
		expected = uint64(store.options.MaxValueBytes)
	}
	if expected == 0 {
		// Either nothing bounds a value here, or this Query Group's records
		// have been coming back empty. Both are the item bound.
		return runtimeLoadBatchItems
	}
	limit := int(runtimeLoadBatchBytes / expected)
	switch {
	case limit < 1:
		// One key per call. A single record over the whole batch budget is
		// still read - refusing it here would stop a Plan the store accepts -
		// and it is read alone rather than beside others.
		return 1
	case limit > runtimeLoadBatchItems:
		return runtimeLoadBatchItems
	default:
		return limit
	}
}

// envelopeLoadBatchLimit bounds the second pass, and deliberately learns
// nothing from the first.
//
// The first pass measures frames. On the one round shape the envelope read
// exists for - a series that has an envelope and no frame - every frame comes
// back empty, and a largest of zero reads as "this Query Group's records are
// empty", which is the item bound: 256 keys in one call. The records about to
// be asked for are the largest ones the store holds, so that bound asks for
// 249 envelopes of 344 KB in a single MGET, ten times the batch budget, and
// the read dies on its deadline. It does not recover either: a failed read
// commits no size, so the next round sends the same call again. The symptom
// this split exists to remove would have moved into the pass that only runs
// while the migration is unfinished.
//
// So the envelope pass is bounded by what the store accepts as a value rather
// than by anything any round saw - sixteen keys per call under the production
// limit, which is what the first read of any cold Query Group has always been
// bounded by. It is a conservative bound for a pass that should be empty and
// is meant to disappear; a per-representation committed memory would earn back
// the difference for the fleets that are mid-migration, and is worth doing only
// if one of them is slow enough to notice.
func (store *ExecutionStore) envelopeLoadBatchLimit() int {
	// Deliberately not learned from this pass's own reads either. A batch that
	// measures small envelopes would raise the bound for the batches after it,
	// and the population this pass reads is mixed by construction: a Query
	// Group changing representation holds both sizes at once, and so does one
	// whose series differ in age. Sixteen short windows at 4 KB would lift the
	// bound to the item cap and the next batch of long ones would ask for
	// 89 MB. The sibling bound survives that only because it takes the max of
	// a committed measurement of the whole population; this pass has none and
	// deliberately commits none, since the records it reads are leaving.
	expected := uint64(store.options.MaxValueBytes)
	if expected == 0 {
		return runtimeLoadBatchItems
	}
	limit := int(runtimeLoadBatchBytes / expected)
	switch {
	case limit < 1:
		return 1
	case limit > runtimeLoadBatchItems:
		return runtimeLoadBatchItems
	default:
		return limit
	}
}

// runtimeLoadBatch collects consecutive same-target keys for one MGET.
type runtimeLoadBatch struct {
	target  StorageTarget
	indexes []int
	keys    []string
}

// runtimeLoadPass carries what the first pass learned into the second: which
// series still need the older representation, and the framed bytes they came
// with, so both records are classified together exactly as they were when one
// call fetched both keys.
type runtimeLoadPass struct {
	envelopes bool
	// carry is the third pass: the previous generation's frame of each
	// series that has no record of its own and whose Plan carries.
	carry                                     bool
	carryFound, carryMissing, carryUnreadable int
	pending                                   []int
	frames                                    map[int][]byte
	// The four the second pass splits into; see the table where they are
	// counted. Only envelopeAnswered ever reaches zero, which is why one
	// number over all four could not say when the migration is over.
	envelopeAnswered    int
	envelopeCorrupt     int
	noRecordYet         int
	frameCorruptRescued int
	frameCorruptLost    int
	unclassified        int
}

func (batch *runtimeLoadBatch) reset() {
	batch.indexes, batch.keys = batch.indexes[:0], batch.keys[:0]
}

// runtimeLoadFailure maps one failed read to a refusal.
//
// It used to have two buckets - an identity error was deterministic-invalid
// corrupt state, and "anything else" was retryable IO named after the
// dependency. A read of ours that did not fit its own timeout fell into the
// second, so the fleet view reported a Redis outage for a Redis that was
// answering every other caller. A timeout is now named for what it is: this
// process asked for more than it left time to receive.
func runtimeLoadFailure(view execution.RuntimeStateView, err error) execution.RuntimeStateView {
	var identityErr *IdentityError
	switch {
	case errors.As(err, &identityErr):
		view.BlobRevision, view.Status, view.ReasonCode = 1, execution.StateDeterministicInvalid, execution.ReasonCode(contract.ReasonStateCorrupt)
	case isCallDeadline(err):
		// A deadline on the call, not the connection giving up: the time this
		// read had was spent before or during it by the work above. Named
		// apart from the connection's own timeout because the two are fixed
		// in different places -- see the reason's own comment.
		view.Status, view.ReasonCode = execution.StateRetryableIO, execution.ReasonCode(contract.ReasonStateReadDeadline)
	case isReadTimeout(err):
		view.Status, view.ReasonCode = execution.StateRetryableIO, execution.ReasonCode(contract.ReasonStateReadTimeout)
	default:
		view.Status, view.ReasonCode = execution.StateRetryableIO, execution.ReasonCode(contract.ReasonRedisUnavailable)
	}
	return view
}

// IsStateReadTimeout is isReadTimeout for callers outside this package, so the
// worker names a failed preflight with the same test the store classifies one
// with rather than a second opinion about what a timeout looks like.
func IsStateReadTimeout(err error) bool { return isReadTimeout(err) }

// isCallDeadline reports whether a read ended because a deadline on the call
// expired. It is checked before isReadTimeout, which accepts both shapes: a
// context deadline surfaces as a net error marked Timeout on some paths, so
// asking the narrower question first is what keeps the two words apart.
//
// A dial that timed out is excluded here for the same reason it is excluded
// there: nothing was read, and the dependency being unreachable is its own
// word.
//
// A cancelled call is not one of these. context.Canceled is the work above
// being stopped -- a graceful shutdown, or a sibling batch's failure bringing
// the parent context down -- and not this read running out of the time it had,
// which is what this word says. The distinction is not academic: the word
// lands on a defect row in the fleet, so counting cancellation here would file
// a defect for every replica every time one is taken down, which is several
// times a day on a deployment that ships. A word that has just been split out
// of two meanings does not get to take on a third.
func isCallDeadline(err error) bool {
	if err == nil {
		return false
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return false
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// isReadTimeout reports whether a read failed by running out of time rather
// than by the dependency refusing or dropping it.
//
// Both shapes are checked because they arrive by different routes and only one
// of them is a context: the client enforces its own read timeout and returns a
// net error marked Timeout, while a deadline on the call returns the context
// error. A build that checked only the context would keep calling the common
// case - the client timeout - a dependency outage.
func isReadTimeout(err error) bool {
	if err == nil {
		return false
	}
	// A dial that timed out is the dependency not being reachable - a black
	// hole, a partition, a server that is gone - and it is the one timeout that
	// really is REDIS_UNAVAILABLE. Folding it in here would send it to the
	// fleet view as this deployment's own doing and point the page at a read
	// size that had nothing to do with it, which is the same misattribution
	// this naming exists to end, aimed the other way.
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// loadRuntimeBatch reads one MGET batch and classifies every value on its own,
// recording a witness for each value the apply path may prove against. A
// corrupt or oversize blob affects only its own item; a failed read marks the
// whole batch retryable, exactly as the failed single reads did.
func (store *ExecutionStore) loadRuntimeBatch(
	ctx context.Context, request execution.StatePreflightRequest, batch *runtimeLoadBatch, views []execution.RuntimeStateView, pass *runtimeLoadPass,
) (loaded int64, largest int, read bool) {
	if len(batch.indexes) == 0 {
		return 0, 0, false
	}
	fetchStarted := time.Now()
	values, err := batch.target.Backend.MGet(ctx, batch.keys)
	if timing := execution.PreflightTimingFrom(ctx); timing != nil {
		decodeStarted := time.Now()
		timing.Fetch += decodeStarted.Sub(fetchStarted)
		defer func() { timing.Decode += time.Since(decodeStarted) }()
	}
	if err == nil && len(values) != len(batch.keys) {
		err = fmt.Errorf("state: invalid backend read cardinality")
	}
	for position, index := range batch.indexes {
		item := request.Items[index]
		if pass.carry {
			// The series stays the missing record it is; what the previous
			// generation held rides beside it. A read that failed carries
			// nothing rather than failing a Slot the series could run
			// without it.
			if err != nil {
				pass.carryUnreadable++
				continue
			}
			raw := values[position]
			loaded += int64(len(raw))
			if len(raw) > largest {
				largest = len(raw)
			}
			views[index].Carried = store.readCarriedRecord(request, item, raw, pass)
			continue
		}
		view := execution.RuntimeStateView{Identity: item.Identity, Status: execution.StateMissingWarming}
		if err != nil {
			views[index] = runtimeLoadFailure(view, err)
			continue
		}
		raw := values[position]
		loaded += int64(len(raw))
		if len(raw) > largest {
			largest = len(raw)
		}
		if pass.envelopes {
			// The envelope arrived for a series whose frame could not answer.
			// The frame's bytes travel with it so the two are classified
			// together, which is the shape this decision has always had when
			// both records existed.
			view := store.readStoredRecord(request, item, raw, pass.frames[index])
			// Which of the four the second pass actually found. One number for
			// all of them cannot say when the migration is over, because only
			// one of the four ever ends:
			//
			//	frame        envelope answered   what it is
			//	-----------  -----------------   ----------------------------
			//	absent       yes                 the older representation, the
			//	                                 only count that must reach zero
			//	absent       no                  a series with no record yet --
			//	                                 new or empty, normal for ever
			//	present, bad yes                 a corrupt frame the older record
			//	                                 rescued: a defect, not a writer
			//	present, bad no                  a corrupt frame nothing rescued
			//
			// Counted here, where both records are in hand, rather than where
			// the decision to read twice was taken: there the frame's absence
			// is all that is known, and absence is exactly what the first two
			// rows share.
			//
			// The envelope's own bytes decide the middle two: raw is nil when
			// the key held nothing and non-nil when it held something that did
			// not read. Both are facts of this read, so a bucket that merged
			// them would be doing to the older record exactly what one count
			// over all of these did to the frame -- leaving a damaged record
			// indistinguishable from a series that never had one.
			//
			// frameCorruptLost does not split the same way: its record is lost
			// whether the envelope was absent or unreadable, and the frame has
			// already named the defect. The five are a partition, which is what
			// lets the total below cross-check them.
			answered := view.Representation == execution.StateRepresentationEnvelope
			switch {
			case pass.frames[index] == nil && answered:
				pass.envelopeAnswered++
			case pass.frames[index] == nil && raw != nil:
				pass.envelopeCorrupt++
			case pass.frames[index] == nil:
				pass.noRecordYet++
			case pass.frames[index] != nil && answered:
				pass.frameCorruptRescued++
			case pass.frames[index] != nil:
				pass.frameCorruptLost++
			default:
				// Unreachable as the five stand, and deliberately written so
				// that it can stop being unreachable. A shape none of the five
				// names lands here and is counted rather than dropped, which
				// is what makes them a partition by construction instead of by
				// whatever a fixture happens to contain: a shape a fixture has
				// no data for is invisible to every case built on one, the sum
				// over the buckets included.
				pass.unclassified++
			}
			views[index] = view
			continue
		}
		classified, needsEnvelope := store.readFramedRecord(request, item, raw)
		if needsEnvelope {
			pass.pending = append(pass.pending, index)
			pass.frames[index] = raw
			continue
		}
		views[index] = classified
	}
	return loaded, largest, err == nil
}

// readCarriedRecord decodes a series' frame under the generation its Plan
// carries from. Only a record that reads as a found one is carried; one that
// is absent, oversize or does not read carries nothing, and each is counted.
func (store *ExecutionStore) readCarriedRecord(
	request execution.StatePreflightRequest, item execution.StatePreflightItem, raw []byte, pass *runtimeLoadPass,
) *execution.RuntimeStateView {
	if raw == nil {
		pass.carryMissing++
		return nil
	}
	if len(raw) > store.options.MaxValueBytes {
		pass.carryUnreadable++
		return nil
	}
	previous := item.Identity
	previous.StateGeneration = item.CarryFrom
	view := decodeRuntime(raw, previous, request.Contract, item.ApplyVersion)
	switch view.Status {
	case execution.StateFoundReady, execution.StateFoundWarming, execution.StateFoundGapped:
		pass.carryFound++
		return &view
	default:
		pass.carryUnreadable++
		return nil
	}
}

// readFramedRecord classifies a series from its framed record alone, and says
// when it cannot.
//
// It cannot in exactly the cases the older representation was kept for: no
// frame at all, or a frame whose bytes do not read as a record this binary
// knows how to refuse on its own. A frame written by a newer binary is not one
// of them - that refusal is the honest answer whatever the older key holds,
// and it is the answer the pair of keys already produced.
//
// The size check is the frame's alone, where the pair of keys was refused if
// either exceeded the limit. A series whose envelope is oversize and whose
// frame is not is now read rather than refused - the readable record answers
// and the unreadable one is not fetched. That is the more usable side of a
// refusal that existed to keep a value nobody could write back from being
// classified as good, and the envelope is not written back by anything.
func (store *ExecutionStore) readFramedRecord(
	request execution.StatePreflightRequest, item execution.StatePreflightItem, framedRaw []byte,
) (execution.RuntimeStateView, bool) {
	if framedRaw == nil {
		return execution.RuntimeStateView{}, true
	}
	if len(framedRaw) > store.options.MaxValueBytes {
		return execution.RuntimeStateView{Identity: item.Identity, BlobRevision: 1, Status: execution.StateDeterministicInvalid,
			ReasonCode: execution.ReasonCode(contract.ReasonStateBudgetExceeded)}, false
	}
	decoded := decodeRuntime(framedRaw, item.Identity, request.Contract, item.ApplyVersion)
	if decoded.Status == execution.StateDeterministicInvalid &&
		decoded.ReasonCode != execution.ReasonCode(contract.ReasonStateSchemaUnsupported) {
		return execution.RuntimeStateView{}, true
	}
	// The frame is handed on decoded. Decoding it a second time there cost
	// as much as the first -- a record of fourteen hundred points is read
	// point by point, each with its derived id -- and was half of what
	// reading a long-history Query Group's state cost.
	view, _ := store.readStoredRecordDecoded(request, item, nil, framedRaw, &decoded)
	return view, false
}

// readStoredRecord turns the two values one series may hold into the one view
// the evaluator reads, and remembers what the apply path will need.
//
// The view is the newer of the two records (chooseRuntimeView), because a
// Query Group bounces between binaries during a rollout and an old owner
// writes the envelope after a new one wrote the framed record. The witness
// carries that record's version facts for the version rule and, separately,
// the framed key's own facts for the compare-and-set, since that is the key
// every write goes to. A framed record that does not decode is not chosen
// but is still witnessed by its bytes, so the next whole write replaces it
// rather than conflicting with it forever.
func (store *ExecutionStore) readStoredRecord(
	request execution.StatePreflightRequest, item execution.StatePreflightItem, envelopeRaw, framedRaw []byte,
) execution.RuntimeStateView {
	view, _ := store.readStoredRecordSourced(request, item, envelopeRaw, framedRaw)
	return view
}

// readStoredRecordSourced is readStoredRecord that also says which record
// answered: the frame, the envelope, or neither. An unreadable record that
// is the answer counts as the key it came from, because deleting that key
// changes the answer.
func (store *ExecutionStore) readStoredRecordSourced(
	request execution.StatePreflightRequest, item execution.StatePreflightItem, envelopeRaw, framedRaw []byte,
) (execution.RuntimeStateView, runtimeViewSource) {
	return store.readStoredRecordDecoded(request, item, envelopeRaw, framedRaw, nil)
}

// readStoredRecordDecoded is readStoredRecordSourced for a caller that has
// already decoded framedRaw, with the same request and item, and passes the
// result as framedDecoded; nil decodes it here. Decoding is a function of
// those inputs alone, so the view is the one a second decode would give.
func (store *ExecutionStore) readStoredRecordDecoded(
	request execution.StatePreflightRequest, item execution.StatePreflightItem, envelopeRaw, framedRaw []byte,
	framedDecoded *execution.RuntimeStateView,
) (execution.RuntimeStateView, runtimeViewSource) {
	oversize := func() execution.RuntimeStateView {
		return execution.RuntimeStateView{Identity: item.Identity, BlobRevision: 1, Status: execution.StateDeterministicInvalid,
			ReasonCode: execution.ReasonCode(contract.ReasonStateBudgetExceeded)}
	}
	if len(framedRaw) > store.options.MaxValueBytes {
		return oversize(), runtimeViewFramed
	}
	if len(envelopeRaw) > store.options.MaxValueBytes {
		return oversize(), runtimeViewEnvelope
	}
	witness := runtimeWitness{missing: true, framedMissing: framedRaw == nil}
	var envelope, framed *execution.RuntimeStateView
	var invalid *execution.RuntimeStateView
	invalidSource := runtimeViewNone
	if envelopeRaw != nil {
		decoded := decodeRuntime(envelopeRaw, item.Identity, request.Contract, item.ApplyVersion)
		if decoded.Status == execution.StateDeterministicInvalid {
			invalid, invalidSource = &decoded, runtimeViewEnvelope
		} else {
			envelope = &decoded
		}
	}
	if framedRaw != nil {
		witness.framedDigest = ExpectedValueDigest(framedRaw)
		var decoded execution.RuntimeStateView
		if framedDecoded != nil {
			decoded = *framedDecoded
		} else {
			decoded = decodeRuntime(framedRaw, item.Identity, request.Contract, item.ApplyVersion)
		}
		if decoded.Status == execution.StateDeterministicInvalid {
			if decoded.ReasonCode == execution.ReasonCode(contract.ReasonStateSchemaUnsupported) {
				// A frame this binary does not know is a newer binary's
				// record, not garbage. Falling back to the envelope here and
				// writing whole over the frame would roll the series back
				// silently on every cross-frame-version rollback; a refusal
				// by name is the honest answer, and it is what the envelope
				// key already gets for the same shape.
				return decoded, runtimeViewFramed
			}
			invalid, invalidSource = &decoded, runtimeViewFramed
		} else {
			framed = &decoded
			witness.framedRevision = decoded.BlobRevision
		}
	}
	chosen, source := chooseRuntimeView(framed, envelope)
	if source == runtimeViewNone {
		if invalid != nil {
			// Whatever was there does not read. Not witnessed: the apply path
			// re-reads and refuses it by name, as it did before two keys.
			return *invalid, invalidSource
		}
		store.witnesses.remember(request.Contract.Slot, item.Identity, witness)
		return execution.RuntimeStateView{Identity: item.Identity, Status: execution.StateMissingWarming}, runtimeViewNone
	}
	witness.missing = false
	witness.blobRevision, witness.applyVersion, witness.mutationDigest = chosen.BlobRevision, chosen.PersistedApplyVersion, chosen.PersistedMutationDigest
	if source == runtimeViewFramed {
		witness.digest = witness.framedDigest
	} else {
		witness.digest = ExpectedValueDigest(envelopeRaw)
	}
	store.witnesses.remember(request.Contract.Slot, item.Identity, witness)
	return chosen, source
}

var _ execution.FencedStateStore = (*ExecutionStore)(nil)
