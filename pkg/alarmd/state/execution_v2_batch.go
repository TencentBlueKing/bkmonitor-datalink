// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"context"
	"errors"
	"fmt"
	"sync"

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
	missing        bool
	digest         string
	blobRevision   uint64
	applyVersion   execution.ApplyVersion
	mutationDigest execution.MutationDigest
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
func classifyWitnessedMutation(witness runtimeWitness, mutation execution.StateMutation) (execution.StateApplyStatus, bool) {
	if witness.missing {
		if mutation.ExpectedBlobRevision != 0 {
			return execution.StateApplyVersionConflict, false
		}
		return "", true
	}
	view := execution.RuntimeStateView{BlobRevision: witness.blobRevision,
		PersistedApplyVersion: witness.applyVersion, PersistedMutationDigest: witness.mutationDigest}
	view.VersionComparison = execution.CompareApplyVersion(view.PersistedApplyVersion, mutation.ApplyVersion)
	switch execution.ClassifyStateMutation(view, mutation) {
	case execution.StateAlreadyApplied:
		return execution.StateApplyAlreadyApplied, false
	case execution.StateStaleVersion:
		return execution.StateApplyStale, false
	case execution.StateVersionConflict:
		return execution.StateApplyVersionConflict, false
	}
	return "", true
}

// classifyFencedOutcome maps one pipeline reply onto the per-item statuses the
// sequential path produces. A conflict carries the current bytes and is
// classified exactly as a fresh read would be; a write that the version rule
// would still allow but whose bytes moved under us is a CAS conflict.
func (store *ExecutionStore) classifyFencedOutcome(
	contractRef execution.FrozenExecutionContractRef, mutation execution.StateMutation, outcome FencedWriteOutcome,
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
		if mutation.ExpectedBlobRevision != 0 {
			item.Status = execution.StateApplyVersionConflict
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
		switch execution.ClassifyStateMutation(view, mutation) {
		case execution.StateAlreadyApplied:
			item.Status = execution.StateApplyAlreadyApplied
		case execution.StateStaleVersion:
			item.Status = execution.StateApplyStale
		case execution.StateVersionConflict:
			item.Status = execution.StateApplyVersionConflict
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
	bytes   int
}

func (pipeline *runtimeApplyPipeline) add(
	ctx context.Context, backend FencedBatchBackend, target string, index int, write FencedWrite,
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
	pipeline.bytes += len(write.Value)
	return nil
}

func (pipeline *runtimeApplyPipeline) reset() {
	pipeline.indexes, pipeline.writes, pipeline.bytes = pipeline.indexes[:0], pipeline.writes[:0], 0
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
	stale := false
	for position, index := range pipeline.indexes {
		outcome := outcomes[position]
		if outcome.Err == nil && outcome.Status == FencedWriteStaleOwner {
			stale = true
			continue
		}
		pipeline.result.Items[index] = pipeline.store.classifyFencedOutcome(pipeline.request.Contract, pipeline.request.Items[index], outcome)
	}
	if stale {
		return fmt.Errorf("state: runtime state apply for %s: %w", pipeline.request.Contract.Slot.QueryGroup, ownership.ErrStaleFence)
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
		OwnerEpoch: fence.Fence.OwnerEpoch, LeaseToken: fence.Fence.LeaseToken, NowMillis: fence.At.UnixMilli()}
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
	result := execution.StateApplyResult{Items: make([]execution.StateApplyItemResult, len(request.Items))}
	keys := make([]string, len(request.Items))
	keyErrors := make([]error, len(request.Items))
	seen := make(map[string]struct{}, len(request.Items))
	duplicate := false
	for index, mutation := range request.Items {
		keys[index], keyErrors[index] = RuntimeStateKeyV2(store.options.Prefix, mutation.Identity)
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
	for index, mutation := range request.Items {
		item := execution.StateApplyItemResult{Identity: mutation.Identity}
		if err := mutation.ValidateDigest(); err != nil || keyErrors[index] != nil {
			item.Status, item.ReasonCode = execution.StateApplyDeterministicInvalid, execution.ReasonCode(contract.ReasonStateCorrupt)
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
				item.Status, item.ReasonCode = execution.StateApplyRetryable, execution.ReasonCode(contract.ReasonRedisUnavailable)
				result.Items[index] = item
				continue
			}
			result.Items[index] = store.applyRuntimeSequential(ctx, request.Contract, mutation, keys[index], casBackend)
			continue
		}
		if status, proceed := classifyWitnessedMutation(witness, mutation); !proceed {
			item.Status = status
			result.Items[index] = item
			continue
		}
		encoded, err := encodeRuntime(mutation, mutation.ExpectedBlobRevision+1)
		if err != nil || len(encoded) > store.options.MaxValueBytes {
			item.Status, item.ReasonCode = execution.StateApplyDeterministicInvalid, execution.ReasonCode(contract.ReasonStateBudgetExceeded)
			result.Items[index] = item
			continue
		}
		write := FencedWrite{Key: keys[index], ExpectedMissing: witness.missing, ExpectedDigest: witness.digest,
			Value: encoded, TTL: store.options.RuntimeTTL}
		if err := pipeline.add(ctx, batchBackend, target.Name, index, write); err != nil {
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
func (store *ExecutionStore) applyRuntimeSequential(
	ctx context.Context, contractRef execution.FrozenExecutionContractRef, mutation execution.StateMutation,
	key string, backend CompareAndSetBackend,
) execution.StateApplyItemResult {
	item := execution.StateApplyItemResult{Identity: mutation.Identity}
	values, err := backend.MGet(ctx, []string{key})
	if err != nil {
		item.Status, item.ReasonCode = execution.StateApplyRetryable, execution.ReasonCode(contract.ReasonRedisUnavailable)
		return item
	}
	var raw []byte
	if len(values) == 1 {
		raw = values[0]
	}
	if raw != nil {
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
		switch execution.ClassifyStateMutation(view, mutation) {
		case execution.StateAlreadyApplied:
			item.Status = execution.StateApplyAlreadyApplied
			return item
		case execution.StateStaleVersion:
			item.Status = execution.StateApplyStale
			return item
		case execution.StateVersionConflict:
			item.Status = execution.StateApplyVersionConflict
			return item
		}
	} else if mutation.ExpectedBlobRevision != 0 {
		item.Status = execution.StateApplyVersionConflict
		return item
	}
	encoded, err := encodeRuntime(mutation, mutation.ExpectedBlobRevision+1)
	if err != nil || len(encoded) > store.options.MaxValueBytes {
		item.Status, item.ReasonCode = execution.StateApplyDeterministicInvalid, execution.ReasonCode(contract.ReasonStateBudgetExceeded)
		return item
	}
	applied, err := backend.CompareAndSet(ctx, key, raw, raw == nil, encoded, store.options.RuntimeTTL)
	if err != nil {
		item.Status, item.ReasonCode = execution.StateApplyRetryable, execution.ReasonCode(contract.ReasonStateWriteRetryable)
	} else if !applied {
		item.Status, item.ReasonCode = execution.StateApplyCASConflict, execution.ReasonCode(contract.ReasonStateWriteRetryable)
	} else {
		item.Status = execution.StateApplied
	}
	return item
}

// runtimeLoadBatch collects consecutive same-target keys for one MGET.
type runtimeLoadBatch struct {
	target  StorageTarget
	indexes []int
	keys    []string
}

func (batch *runtimeLoadBatch) reset() {
	batch.indexes, batch.keys = batch.indexes[:0], batch.keys[:0]
}

// runtimeLoadFailure keeps the original per-key error mapping: an identity
// error is deterministic-invalid corrupt state, anything else is retryable IO.
func runtimeLoadFailure(view execution.RuntimeStateView, err error) execution.RuntimeStateView {
	var identityErr *IdentityError
	if errors.As(err, &identityErr) {
		view.BlobRevision, view.Status, view.ReasonCode = 1, execution.StateDeterministicInvalid, execution.ReasonCode(contract.ReasonStateCorrupt)
	} else {
		view.Status, view.ReasonCode = execution.StateRetryableIO, execution.ReasonCode(contract.ReasonRedisUnavailable)
	}
	return view
}

// loadRuntimeBatch reads one MGET batch and classifies every value on its own,
// recording a witness for each value the apply path may prove against. A
// corrupt or oversize blob affects only its own item; a failed read marks the
// whole batch retryable, exactly as the failed single reads did.
func (store *ExecutionStore) loadRuntimeBatch(
	ctx context.Context, request execution.StatePreflightRequest, batch *runtimeLoadBatch, views []execution.RuntimeStateView,
) {
	if len(batch.indexes) == 0 {
		return
	}
	values, err := batch.target.Backend.MGet(ctx, batch.keys)
	if err == nil && len(values) != len(batch.keys) {
		err = fmt.Errorf("state: invalid backend read cardinality")
	}
	for position, index := range batch.indexes {
		item := request.Items[index]
		view := execution.RuntimeStateView{Identity: item.Identity, Status: execution.StateMissingWarming}
		if err != nil {
			views[index] = runtimeLoadFailure(view, err)
			continue
		}
		raw := values[position]
		switch {
		case raw == nil:
			store.witnesses.remember(request.Contract.Slot, item.Identity, runtimeWitness{missing: true})
		case len(raw) > store.options.MaxValueBytes:
			view.BlobRevision, view.Status, view.ReasonCode = 1, execution.StateDeterministicInvalid, execution.ReasonCode(contract.ReasonStateBudgetExceeded)
		default:
			view = decodeRuntime(raw, item.Identity, request.Contract, item.ApplyVersion)
			if view.Status != execution.StateDeterministicInvalid {
				store.witnesses.remember(request.Contract.Slot, item.Identity, runtimeWitness{digest: ExpectedValueDigest(raw),
					blobRevision: view.BlobRevision, applyVersion: view.PersistedApplyVersion, mutationDigest: view.PersistedMutationDigest})
			}
		}
		views[index] = view
	}
}

var _ execution.FencedStateStore = (*ExecutionStore)(nil)
