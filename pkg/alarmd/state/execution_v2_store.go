// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

const executionStateSchemaV2 = "alarmd-runtime-state-v2"
const executionGapSchemaV2 = "alarmd-plan-gap-v2"

type CompareAndSetBackend interface {
	Backend
	CompareAndSet(context.Context, string, []byte, bool, []byte, time.Duration) (bool, error)
}

// LifetimeBackend renews the life of a key that is read rather than written.
//
// It is deliberately not folded into CompareAndSetBackend. Both of that
// interface's users reach it by type assertion, and both report a failed
// assertion as the store being unavailable - so widening it would turn a
// backend without this method into a deployment where every write reports Redis
// down, with nothing anywhere naming the actual cause. Renewal belongs to the
// read path and cannot be worth that.
//
// A backend that does not implement it is reported by the load that wanted it,
// not passed over: a renewal nobody performs leaves keys immortal, and the only
// place that shows up is a Redis instance months later.
type LifetimeBackend interface {
	RenewIfBelow(context.Context, string, time.Duration, time.Duration) (RenewalOutcome, error)
	// RenewManyIfBelow is the same decision for a batch of keys, answered in
	// one round trip rather than one per key. Both are required: a caller that
	// holds one key must not pay for a slice, and a caller holding a Slot's
	// worth of them must not pay for a round trip each.
	RenewManyIfBelow(context.Context, []string, time.Duration, time.Duration) ([]RenewalOutcome, error)
}

// RenewalOutcome is what one renewal found when it looked.
//
// Three states, not a bool, because the third one is the reason this exists.
// A renewal that finds no key is the moment a piece of state was lost without
// anything noticing -- the Plan will rebuild it as a new series and report
// nothing -- and a bool return spells that "not renewed", which is also what a
// key with plenty of life left says. Those two readings are opposites and the
// old signature could not tell them apart.
type RenewalOutcome string

const (
	// RenewalRenewed is a key that was running out and now is not.
	RenewalRenewed RenewalOutcome = "RENEWED"
	// RenewalFresh is a key with more than the threshold still to live. No
	// expiry was set; the round trip was still spent.
	RenewalFresh RenewalOutcome = "FRESH"
	// RenewalMissing is a key that was read this round and was gone by the
	// time the renewal asked about it. Nothing recreates it here: writing a
	// key with no value would only make every reader classify it as corrupt.
	RenewalMissing RenewalOutcome = "MISSING"
)

type ExecutionStoreOptions struct {
	Prefix          string
	Router          StorageRouter
	MaxValueBytes   int
	MaxItemsPerCall int
	// MaxNoDataGroups bounds how many groups one Plan's no-data memory may
	// hold. It is a guard against an expected set that has run away, not a
	// working limit: the memory is stored one field per group and has no size
	// ceiling, so this is an order of magnitude beyond what any Plan reaches.
	// Zero takes DefaultMaxNoDataGroups rather than meaning no bound, because
	// an unset field must not be the way a guard is removed.
	MaxNoDataGroups int
	// MinTTL, MaxTTL and RestartMargin bound the write TTL the store derives
	// per apply request from that request's Plan retention. MaxTTL is the
	// ceiling a derived TTL may not exceed, not the value keys are written at.
	MinTTL        time.Duration
	MaxTTL        time.Duration
	RestartMargin time.Duration
	// FenceKeys locates the ownership lease that fenced Runtime State writes
	// verify inside storage. It is optional: without it ApplyRuntimeFenced
	// applies unfenced and the admission-time fence check stands alone.
	FenceKeys FenceKeyResolver
}

type ExecutionStore struct {
	options   ExecutionStoreOptions
	witnesses *runtimeWitnessCache
	// renewals is what this process already asked Redis about the life of the
	// generation-scoped keys it loads. Per store rather than per package so
	// two stores in one process cannot answer for each other's keys.
	renewals *renewalGate
	// frozenRenewals is the same memory for Runtime State keys whose Level was
	// frozen this round. A second table rather than a shared one because the
	// two populations are sized differently -- generation-scoped keys are two
	// per Plan, Runtime State keys are one per series, thousands for a single
	// query group -- so one table would let a burst of series keys evict every
	// Plan's entry, and the reset counter could not say which population
	// overflowed.
	frozenRenewals *renewalGate
	// valueSizes is what one stored record has been costing, per Query Group,
	// learned from the reads that returned - so a preflight batch is bounded by
	// what it is expected to move rather than by key count alone.
	valueSizes struct {
		mu    sync.RWMutex
		bytes map[execution.QueryGroupIdentity]uint64
	}
}

type runtimeEnvelope struct {
	Schema         string                                `json:"schema"`
	Identity       execution.StateKeyIdentity            `json:"identity"`
	BlobRevision   uint64                                `json:"blob_revision"`
	ApplyVersion   execution.ApplyVersion                `json:"apply_version"`
	MutationDigest execution.MutationDigest              `json:"mutation_digest"`
	LastEventTime  int64                                 `json:"last_event_time"`
	SeriesGuard    *execution.StateGuardFact             `json:"series_guard,omitempty"`
	Levels         []execution.RuntimeLevelStateMutation `json:"levels"`
	History        []execution.StateHistoryPoint         `json:"history"`
}

type gapEnvelope struct {
	Schema           string                         `json:"schema"`
	Identity         execution.PlanGapIdentity      `json:"identity"`
	MarkerRevision   uint64                         `json:"marker_revision"`
	ApplyVersion     execution.ApplyVersion         `json:"apply_version"`
	MutationDigest   execution.MutationDigest       `json:"mutation_digest"`
	ScheduleRevision execution.PlanScheduleRevision `json:"schedule_revision"`
	Scopes           []execution.GapScopeState      `json:"scopes,omitempty"`
}

// DefaultMaxNoDataGroups is the group guard a store takes when none is given.
//
// It is derived from what the container can hold rather than from what a Plan
// is expected to have: the largest memory seen in production is a few thousand
// groups, and this is two orders of magnitude above it, which is what makes it
// a guard rather than something a Plan can reach by growing normally.
const DefaultMaxNoDataGroups = 100000

func NewExecutionStore(options ExecutionStoreOptions) (*ExecutionStore, error) {
	if options.Prefix == "" || options.Router == nil || options.MaxValueBytes <= 0 ||
		options.MaxItemsPerCall <= 0 || options.MinTTL <= 0 || options.MaxTTL < options.MinTTL ||
		options.RestartMargin < 0 || options.MaxNoDataGroups < 0 {
		return nil, fmt.Errorf("state: invalid execution store options")
	}
	if options.MaxNoDataGroups == 0 {
		options.MaxNoDataGroups = DefaultMaxNoDataGroups
	}
	if err := probeBackendCapabilities("execution store", options.Router, executionStoreCapabilities); err != nil {
		return nil, err
	}
	return &ExecutionStore{options: options, witnesses: newRuntimeWitnessCache(),
		renewals: newRenewalGate(), frozenRenewals: newRenewalGate()}, nil
}

// runtimeTTL derives how long the keys of one apply request have to survive
// from that request's Plan retention. StateTTL owns the formula so the two
// stores cannot drift; the execution store only supplies the deployment bounds.
//
// horizonSeconds, when positive, caps it: a series' runtime state lives at
// most H past its last write, so one that stops appearing is gone H later
// rather than a retention span or the deployment's maximum later. The cap
// never goes below what a series that keeps reporting needs to survive until
// its next Slot - one evaluation interval, its lateness and the restart
// margin - because a lifetime shorter than that would lose the state of a
// series that never went away. Lowering H therefore shortens lifetimes from
// the next write on, and raising it lengthens them from the next write on:
// a key that already expired is gone, so nothing reclaimed comes back.
func (store *ExecutionStore) runtimeTTL(retention []execution.StateRetentionRequirement, horizonSeconds int64) (time.Duration, error) {
	requirements := make([]LevelRequirement, len(retention))
	for index, level := range retention {
		// The window facts are left empty: StateTTL reads only the retention,
		// and it must read exactly the retention the window was built from.
		requirements[index] = NewLevelRequirement(level, "", 0)
	}
	ttl, err := StateTTL(requirements, store.options.RestartMargin, store.options.MinTTL, store.options.MaxTTL)
	if err != nil || horizonSeconds <= 0 {
		return ttl, err
	}
	return capByHorizon(ttl, requirements, store.options.RestartMargin, store.options.MinTTL,
		time.Duration(horizonSeconds)*time.Second), nil
}

// capByHorizon is the retention's lifetime capped at the horizon, and the
// horizon floored at what a reporting series needs between two writes.
func capByHorizon(ttl time.Duration, requirements []LevelRequirement, restartMargin, minimum, horizon time.Duration) time.Duration {
	limit := max(horizon, StateLifetimeFloor(requirements, restartMargin), minimum)
	return min(ttl, limit)
}

// StateLifetimeFloor is the shortest lifetime a series' runtime state can
// have and still survive from one write to the next while the series keeps
// reporting: the longest evaluation interval plus its lateness, and the
// restart margin.
func StateLifetimeFloor(requirements []LevelRequirement, restartMargin time.Duration) time.Duration {
	var floor time.Duration
	for _, requirement := range requirements {
		floor = max(floor, requirement.EvaluationInterval+requirement.LatenessTolerance)
	}
	return floor + restartMargin
}

// rejectRuntimeBudget reports a retention need no configured TTL can satisfy.
// It is a deterministic budget rejection per item, like an oversize blob, so
// the Slot records which Plan was refused and why instead of failing the whole
// batch with an opaque error.
func rejectRuntimeBudget(items []execution.StateMutation) []execution.StateApplyItemResult {
	results := make([]execution.StateApplyItemResult, len(items))
	for index, mutation := range items {
		results[index] = execution.StateApplyItemResult{Identity: mutation.Identity,
			Status:     execution.StateApplyDeterministicInvalid,
			ReasonCode: execution.ReasonCode(contract.ReasonStateBudgetExceeded)}
	}
	return results
}

// LoadRuntime reads the requested keys in bounded MGET batches, grouping
// consecutive items that route to the same storage target. Each value is still
// classified on its own; a preflight witness is kept per readable key so the
// following ApplyRuntime can prove what it saw without reading again.
//
// The read is in two passes, and the second one is usually empty. Every series
// may hold two records - the framed one every binary of this era writes, and
// the JSON envelope its predecessors wrote - and the round used to fetch both
// keys of every series whether or not the older one held anything. On a
// strategy retaining a long window that is the whole cost of the read: 1466
// points are 7 KB framed and 344 KB as an envelope, so a Query Group of 249
// series read 87.6 MB per round of which 85.7 MB was a representation with no
// writer, and a read that size stops fitting in its deadline. The first pass
// asks for the framed key alone; the second asks for the envelope only of the
// series whose frame is missing or cannot be read by itself, which is what the
// envelope was being kept for. A series whose frame answers is classified from
// the frame in the first pass, exactly as it was when both keys arrived
// together - the choice between the two records only ever mattered when both
// held one.
//
// The older records are not deleted here or anywhere: they are not renewed
// either (a frozen series is renewed under the representation it was read in),
// so they leave on their own TTL. EnvelopeReads says how many series still
// need the second read, which is how a deployment learns when the
// compatibility pass can go.
func (store *ExecutionStore) LoadRuntime(ctx context.Context, request execution.StatePreflightRequest) (execution.StatePreflightResult, error) {
	if err := request.Contract.Validate(); err != nil || len(request.Items) == 0 || len(request.Items) > store.options.MaxItemsPerCall {
		return execution.StatePreflightResult{}, fmt.Errorf("state: invalid runtime load request")
	}
	result := execution.StatePreflightResult{Items: make([]execution.RuntimeStateView, len(request.Items))}
	pass := &runtimeLoadPass{frames: make(map[int][]byte)}
	batch := &runtimeLoadBatch{}
	roundLargest, anyRead := 0, false
	flush := func() {
		bytes, largest, read := store.loadRuntimeBatch(ctx, request, batch, result.Items, pass)
		result.LoadedBytes += bytes
		// The frame pass measures, and so does the carry pass: a record it
		// reads is written this round under the new generation at the size it
		// was read, and the next round's frame pass reads it there. Leaving it
		// out committed the empty frame pass's zero as the Query Group's size,
		// and the next round asked for every carried record in one batch.
		if read && !pass.envelopes {
			// Only the frame pass measures. The Query Group's committed size
			// describes the key every write goes to and the one the next round
			// reads first; the envelopes are leaving, and a size learned from
			// them would bound the frame pass by records that will not be
			// there.
			anyRead = true
			if largest > roundLargest {
				roundLargest = largest
			}
		}
		batch.reset()
	}
	for index, item := range request.Items {
		view := execution.RuntimeStateView{Identity: item.Identity, Status: execution.StateMissingWarming}
		framedKey, err := RuntimeStateKeyV3(store.options.Prefix, item.Identity)
		var target StorageTarget
		if err == nil {
			target, err = store.options.Router.Route(item.Identity.Plan.TenantID, item.Identity.Plan.StrategyID)
		}
		if err != nil {
			result.Items[index] = runtimeLoadFailure(view, err)
			continue
		}
		if len(batch.indexes) > 0 && (batch.target.Name != target.Name || len(batch.indexes) >= store.runtimeLoadBatchLimit(request.Contract.Slot.QueryGroup, roundLargest, anyRead)) {
			flush()
		}
		batch.target = target
		batch.indexes = append(batch.indexes, index)
		batch.keys = append(batch.keys, framedKey)
	}
	flush()
	// The second pass, for the series the first one could not answer from the
	// frame alone. Its bound is what the store accepts as a value and nothing
	// else - not the frames the first pass measured, and not its own earlier
	// batches. See envelopeLoadBatchLimit for why both of those are traps.
	pass.envelopes = true
	for _, index := range pass.pending {
		item := request.Items[index]
		view := execution.RuntimeStateView{Identity: item.Identity, Status: execution.StateMissingWarming}
		envelopeKey, err := RuntimeStateKeyV2(store.options.Prefix, item.Identity)
		var target StorageTarget
		if err == nil {
			target, err = store.options.Router.Route(item.Identity.Plan.TenantID, item.Identity.Plan.StrategyID)
		}
		if err != nil {
			result.Items[index] = runtimeLoadFailure(view, err)
			continue
		}
		if len(batch.indexes) > 0 && (batch.target.Name != target.Name || len(batch.indexes) >= store.envelopeLoadBatchLimit()) {
			flush()
		}
		batch.target = target
		batch.indexes = append(batch.indexes, index)
		batch.keys = append(batch.keys, envelopeKey)
	}
	flush()
	// The third pass, for the series still without a record whose Plan
	// carries history from the generation it moved from: that generation's
	// frame, under the same batching and the same byte count as the other
	// two. Only the frame is read -- a record the previous generation still
	// held only as an envelope is old enough to warm up again.
	pass.envelopes, pass.carry = false, true
	for index, item := range request.Items {
		if item.CarryFrom == "" || item.CarryFrom == item.Identity.StateGeneration || result.Items[index].Status != execution.StateMissingWarming {
			continue
		}
		previous := item.Identity
		previous.StateGeneration = item.CarryFrom
		carriedKey, err := RuntimeStateKeyV3(store.options.Prefix, previous)
		var target StorageTarget
		if err == nil {
			target, err = store.options.Router.Route(item.Identity.Plan.TenantID, item.Identity.Plan.StrategyID)
		}
		if err != nil {
			pass.carryUnreadable++
			continue
		}
		// Bounded as the envelope pass is, by the largest value the store
		// accepts and not by anything learned: the frame pass has just found
		// no record for every one of these series, so what it learned about
		// this Query Group says nothing about the size of what the previous
		// generation holds.
		if len(batch.indexes) > 0 && (batch.target.Name != target.Name || len(batch.indexes) >= store.envelopeLoadBatchLimit()) {
			flush()
		}
		batch.target = target
		batch.indexes = append(batch.indexes, index)
		batch.keys = append(batch.keys, carriedKey)
	}
	flush()
	result.CarryFound, result.CarryMissing, result.CarryUnreadable = pass.carryFound, pass.carryMissing, pass.carryUnreadable
	result.EnvelopeReads = len(pass.pending)
	result.EnvelopeAnswered, result.NoRecordYet = pass.envelopeAnswered, pass.noRecordYet
	result.EnvelopeCorrupt = pass.envelopeCorrupt
	result.FrameCorruptRescued, result.FrameCorruptLost = pass.frameCorruptRescued, pass.frameCorruptLost
	result.Unclassified = pass.unclassified
	// One commit for the whole preflight: the round read every key of the
	// Query Group, so this is a complete measurement of the population rather
	// than whatever the last batch happened to hold.
	store.commitValueBytes(request.Contract.Slot.QueryGroup, roundLargest, anyRead)
	return execution.ClassifyStatePreflight(request, result)
}

func (store *ExecutionStore) AdmitRuntime(_ context.Context, request execution.StateApplyRequest) (execution.StateAdmissionResult, error) {
	if err := request.Contract.Validate(); err != nil || len(request.Items) == 0 || len(request.Items) > store.options.MaxItemsPerCall {
		return execution.StateAdmissionResult{}, fmt.Errorf("state: invalid runtime admission request")
	}
	if _, err := store.runtimeTTL(request.Retention, request.HorizonSeconds); err != nil {
		if !errors.Is(err, ErrStateBudget) {
			return execution.StateAdmissionResult{}, fmt.Errorf("state: invalid runtime admission request: %w", err)
		}
		result := execution.StateAdmissionResult{Items: make([]execution.StateAdmissionItemResult, len(request.Items))}
		for index, mutation := range request.Items {
			result.Items[index] = execution.StateAdmissionItemResult{Identity: mutation.Identity,
				Status:     execution.StateAdmissionDeterministicInvalid,
				ReasonCode: execution.ReasonCode(contract.ReasonStateBudgetExceeded)}
		}
		return result, result.Validate()
	}
	result := execution.StateAdmissionResult{Items: make([]execution.StateAdmissionItemResult, len(request.Items))}
	for index, mutation := range request.Items {
		item := execution.StateAdmissionItemResult{Identity: mutation.Identity, Status: execution.StateAdmissionAccepted}
		if err := mutation.ValidateDigest(); err != nil {
			item.Status, item.ReasonCode = execution.StateAdmissionDeterministicInvalid, execution.ReasonCode(contract.ReasonStateCorrupt)
			item.RefusalRule = PackedRuleMutationDigestMismatch
			result.Items[index] = item
			continue
		}
		// Sized in the representation the write stores. The revision only
		// widens one varint in the header, so any revision measures the same.
		encoded, refusal, rule, legacyIDs := store.encodeForWrite(mutation, mutation.ExpectedBlobRevision+1)
		if refusal != "" {
			item.Status, item.ReasonCode = execution.StateAdmissionDeterministicInvalid, execution.ReasonCode(refusal)
			item.RefusalRule = rule
		} else {
			item.EncodedBytes, item.LegacyRecordIDs = len(encoded), legacyIDs
		}
		result.Items[index] = item
	}
	return result, result.Validate()
}

// ApplyRuntime applies without an owner fence. Items whose key was witnessed by
// the preceding LoadRuntime are compared by digest in pipelined batches; the
// rest take the sequential read-then-compare path unchanged.
func (store *ExecutionStore) ApplyRuntime(ctx context.Context, request execution.StateApplyRequest) (execution.StateApplyResult, error) {
	return store.applyRuntime(ctx, request, nil)
}

// encodeRuntime writes the JSON envelope: what every binary before the framed
// record wrote under the runtime key. No production path writes it any more;
// it stays so a test can seed the key an earlier binary would have left, and
// so the baseline that records what the envelope costs keeps measuring the
// real thing.
func encodeRuntime(mutation execution.StateMutation, revision uint64) ([]byte, error) {
	levels := append([]execution.RuntimeLevelStateMutation(nil), mutation.Levels...)
	sort.Slice(levels, func(i, j int) bool { return levels[i].LevelID < levels[j].LevelID })
	last := int64(0)
	for _, level := range levels {
		if level.LastProcessedEventTime > last {
			last = level.LastProcessedEventTime
		}
	}
	return json.Marshal(runtimeEnvelope{executionStateSchemaV2, mutation.Identity, revision, mutation.ApplyVersion,
		mutation.MutationDigest, last, mutation.SeriesGuard, levels, mutation.Points})
}

// decodeRuntime reads a stored record in whichever representation it was
// written and classifies it against the candidate the caller is about to
// apply. The shape is read from the bytes, not from the key they came from:
// the framed record announces itself with its magic, and everything else is
// the JSON envelope. The view says which one it was.
func decodeRuntime(raw []byte, identity execution.StateKeyIdentity, contractRef execution.FrozenExecutionContractRef, candidate execution.ApplyVersion) execution.RuntimeStateView {
	invalid := func(reason string) execution.RuntimeStateView {
		return execution.RuntimeStateView{Identity: identity, BlobRevision: 1, Status: execution.StateDeterministicInvalid,
			ReasonCode: execution.ReasonCode(reason)}
	}
	var view execution.RuntimeStateView
	if packedFrame(raw) {
		decoded, err := decodeRuntimePacked(raw, identity)
		switch {
		case errors.Is(err, ErrUnsupportedState):
			return invalid(contract.ReasonStateSchemaUnsupported)
		case err != nil:
			return invalid(contract.ReasonStateCorrupt)
		}
		view = decoded
		view.Representation = execution.StateRepresentationFramed
	} else {
		var value runtimeEnvelope
		if err := json.Unmarshal(raw, &value); err != nil {
			return invalid(contract.ReasonStateCorrupt)
		}
		if value.Schema != executionStateSchemaV2 {
			return invalid(contract.ReasonStateSchemaUnsupported)
		}
		if value.Identity != identity || value.BlobRevision == 0 {
			return invalid(contract.ReasonStateCorrupt)
		}
		levels := make([]execution.RuntimeLevelStateView, len(value.Levels))
		for i, level := range value.Levels {
			levels[i] = execution.RuntimeLevelStateView{LevelID: level.LevelID, LevelStateCompatibility: level.LevelStateCompatibility,
				HistoryCompleteness: level.HistoryCompleteness, GapReasonCode: level.GapReasonCode,
				WarmupRequirementRef: level.WarmupRequirementRef, LastProcessedEventTime: level.LastProcessedEventTime}
		}
		view = execution.RuntimeStateView{Identity: identity, BlobRevision: value.BlobRevision,
			Representation:        execution.StateRepresentationEnvelope,
			PersistedApplyVersion: value.ApplyVersion, PersistedMutationDigest: value.MutationDigest,
			LastProcessedEventTime: value.LastEventTime, SeriesGuard: value.SeriesGuard, Levels: levels, History: value.History}
	}
	view.Status = storedLoadStatus(view.Levels, view.SeriesGuard)
	classified, err := execution.ClassifyStatePreflight(execution.StatePreflightRequest{Contract: contractRef,
		Items: []execution.StatePreflightItem{{Identity: identity, ApplyVersion: candidate}}}, execution.StatePreflightResult{Items: []execution.RuntimeStateView{view}})
	if err != nil {
		return invalid(contract.ReasonStateCorrupt)
	}
	return classified.Items[0]
}

// storedLoadStatus is what the Levels and the series guard say about a record
// that was found: gapped wins over warming wins over ready, the same reading
// for both representations.
func storedLoadStatus(levels []execution.RuntimeLevelStateView, guard *execution.StateGuardFact) execution.StateLoadStatus {
	status := execution.StateFoundReady
	for _, level := range levels {
		if level.HistoryCompleteness == execution.HistoryGapped {
			status = execution.StateFoundGapped
		} else if level.HistoryCompleteness == execution.HistoryWarming && status != execution.StateFoundGapped {
			status = execution.StateFoundWarming
		}
	}
	if guard != nil {
		if guard.Status == execution.HistoryGapped {
			status = execution.StateFoundGapped
		} else if guard.Status == execution.HistoryWarming && status != execution.StateFoundGapped {
			status = execution.StateFoundWarming
		}
	}
	return status
}

// readOneRenewing reads one generation-scoped key and, when it is there and its
// life is running out, extends it.
//
// The renewal is on this path because this is the path a live key is on every
// Slot. A key whose Plan no longer exists, or whose execution content changed,
// stops arriving here and ages out; a key still in use is renewed whether or
// not its Plan wrote anything this round, which is what a write-driven renewal
// could not do.
//
// A key that is not there is not renewed, which saves the command for every
// Plan that has never written a record - that being every Plan until its first
// no-data round.
func (store *ExecutionStore) readOneRenewing(
	ctx context.Context, plan execution.PlanIdentity, retention []execution.StateRetentionRequirement,
	key func() (string, error),
) ([]byte, error) {
	resolved, err := key()
	if err != nil {
		return nil, err
	}
	target, err := store.options.Router.Route(plan.TenantID, plan.StrategyID)
	if err != nil {
		return nil, err
	}
	values, err := target.Backend.MGet(ctx, []string{resolved})
	if err != nil {
		return nil, err
	}
	if len(values) != 1 {
		return nil, fmt.Errorf("state: invalid backend read cardinality")
	}
	if values[0] == nil {
		return nil, nil
	}
	if err := RenewGenerationKey(ctx, target, resolved, retention,
		store.options.RestartMargin, store.options.MinTTL, store.options.MaxTTL, store.renewals); err != nil {
		return nil, err
	}
	return values[0], nil
}

func (store *ExecutionStore) LoadGaps(ctx context.Context, request execution.GapLoadRequest) (execution.GapLoadResult, error) {
	result := execution.GapLoadResult{}
	err := store.LoadGapsInto(ctx, request, func(snapshot execution.GapGuardSnapshot) error {
		result.Items = append(result.Items, snapshot)
		return nil
	})
	if err != nil {
		return execution.GapLoadResult{}, err
	}
	return result, execution.ValidateGapLoad(request, result)
}

func (store *ExecutionStore) LoadGapsInto(ctx context.Context, request execution.GapLoadRequest, accept func(execution.GapGuardSnapshot) error) error {
	if err := request.Contract.Validate(); err != nil || len(request.Items) == 0 || len(request.Items) > store.options.MaxItemsPerCall {
		return fmt.Errorf("state: invalid gap load request")
	}
	if accept == nil {
		return fmt.Errorf("state: gap consumer is required")
	}
	for _, item := range request.Items {
		if err := ctx.Err(); err != nil {
			return err
		}
		snapshot := execution.GapGuardSnapshot{Identity: item.Identity, Status: execution.GapMissing}
		raw, err := store.readOneRenewing(ctx, item.Identity.Plan, item.Retention,
			func() (string, error) { return PlanGapKeyV2(store.options.Prefix, item.Identity) })
		if err != nil {
			var identityErr *IdentityError
			if errors.Is(err, ErrLifetimeUnsupported) {
				snapshot.Status = execution.GapTerminal
				snapshot.ReasonCode = execution.ReasonCode(contract.ReasonBackendCapabilityMissing)
			} else if errors.As(err, &identityErr) {
				snapshot.Status, snapshot.ReasonCode = execution.GapTerminal, execution.ReasonCode(contract.ReasonStateCorrupt)
			} else {
				snapshot.Status, snapshot.ReasonCode = execution.GapUnavailable, execution.ReasonCode(contract.ReasonRedisUnavailable)
			}
		} else if raw != nil {
			if len(raw) > store.options.MaxValueBytes {
				snapshot.Status, snapshot.ReasonCode = execution.GapTerminal, execution.ReasonCode(contract.ReasonStateBudgetExceeded)
			} else {
				snapshot = decodeGap(raw, item.Identity, request.Contract, item)
			}
		}
		one := execution.GapLoadRequest{Contract: request.Contract, Items: []execution.PlanGapLoadItem{item}}
		if err := execution.ValidateGapLoad(one, execution.GapLoadResult{Items: []execution.GapGuardSnapshot{snapshot}}); err != nil {
			return err
		}
		if err := accept(snapshot); err != nil {
			return err
		}
	}
	return nil
}

func (store *ExecutionStore) ApplyGap(ctx context.Context, request execution.GapGuardApplyRequest) (execution.GapGuardApplyResult, error) {
	if err := request.Contract.Validate(); err != nil || len(request.Items) == 0 || len(request.Items) > store.options.MaxItemsPerCall {
		return execution.GapGuardApplyResult{}, fmt.Errorf("state: invalid gap apply request")
	}
	result := execution.GapGuardApplyResult{Items: make([]execution.GapGuardApplyItemResult, len(request.Items))}
	for index, mutation := range request.Items {
		item := execution.GapGuardApplyItemResult{Identity: mutation.Identity}
		if err := mutation.ValidateDigest(); err != nil {
			item.Status, item.ReasonCode = execution.GapGuardRejected, execution.ReasonCode(contract.ReasonStateCorrupt)
			result.Items[index] = item
			continue
		}
		key, err := PlanGapKeyV2(store.options.Prefix, mutation.Identity)
		if err != nil {
			item.Status, item.ReasonCode = execution.GapGuardRejected, execution.ReasonCode(contract.ReasonStateCorrupt)
			result.Items[index] = item
			continue
		}
		target, routeErr := store.options.Router.Route(mutation.Identity.Plan.TenantID, mutation.Identity.Plan.StrategyID)
		if routeErr != nil {
			item.Status, item.ReasonCode = execution.GapGuardRetryable, execution.ReasonCode(contract.ReasonRedisUnavailable)
			result.Items[index] = item
			continue
		}
		backend, ok := target.Backend.(CompareAndSetBackend)
		if !ok {
			// Not the store being unavailable: the store this deployment routed
			// to cannot do what this write needs. Retrying reaches the same
			// backend and gets the same answer, so a retryable status here would
			// retry it forever while the page said Redis was down.
			item.Status = execution.GapGuardRejected
			item.ReasonCode = execution.ReasonCode(contract.ReasonBackendCapabilityMissing)
			result.Items[index] = item
			continue
		}
		values, readErr := backend.MGet(ctx, []string{key})
		if readErr != nil || len(values) != 1 {
			item.Status, item.ReasonCode = execution.GapGuardRetryable, execution.ReasonCode(contract.ReasonRedisUnavailable)
			result.Items[index] = item
			continue
		}
		raw := values[0]
		var previous gapEnvelope
		var sameSlotScopes []execution.GapScopeState
		if raw != nil {
			if len(raw) > store.options.MaxValueBytes {
				item.Status, item.ReasonCode = execution.GapGuardRejected, execution.ReasonCode(contract.ReasonStateBudgetExceeded)
				result.Items[index] = item
				continue
			}
			snapshot := decodeGap(raw, mutation.Identity, request.Contract, execution.PlanGapLoadItem{Identity: mutation.Identity, ApplyVersion: mutation.ApplyVersion, ScheduleRevision: mutation.ScheduleRevision})
			if snapshot.Status == execution.GapTerminal {
				item.Status, item.ReasonCode = execution.GapGuardRejected, snapshot.ReasonCode
				result.Items[index] = item
				continue
			}
			_ = json.Unmarshal(raw, &previous)
			comparison := execution.CompareApplyVersion(previous.ApplyVersion, mutation.ApplyVersion)
			if comparison == execution.ApplyVersionPersistedNewer {
				item.Status = execution.GapGuardStale
				result.Items[index] = item
				continue
			}
			if comparison == execution.ApplyVersionEqual {
				if previous.MutationDigest == mutation.MutationDigest {
					item.Status = execution.GapGuardAlreadyApplied
					result.Items[index] = item
					continue
				}
				var extended bool
				if previous.ScheduleRevision == mutation.ScheduleRevision {
					sameSlotScopes, extended = execution.ExtendSameSlotGap(previous.Scopes, mutation.Scopes)
				}
				if !extended {
					item.Status = execution.GapGuardConflict
					result.Items[index] = item
					continue
				}
			}
		}
		if previous.MarkerRevision != mutation.ExpectedMarkerRevision {
			item.Status = execution.GapGuardConflict
			result.Items[index] = item
			continue
		}
		nextScopes := sameSlotScopes
		if nextScopes == nil {
			nextScopes = applyGapScopes(previous.Scopes, mutation.Scopes, previous.ScheduleRevision, mutation.ScheduleRevision)
		}
		if err := validatePersistedGapScopes(nextScopes); err != nil {
			item.Status, item.ReasonCode = execution.GapGuardRejected, execution.ReasonCode(contract.ReasonStateCorrupt)
			result.Items[index] = item
			continue
		}
		next := gapEnvelope{Schema: executionGapSchemaV2, Identity: mutation.Identity,
			MarkerRevision: mutation.ExpectedMarkerRevision + 1, ApplyVersion: mutation.ApplyVersion,
			MutationDigest: mutation.MutationDigest, ScheduleRevision: mutation.ScheduleRevision,
			Scopes: nextScopes}
		encoded, encodeErr := json.Marshal(next)
		if encodeErr != nil || len(encoded) > store.options.MaxValueBytes {
			item.Status, item.ReasonCode = execution.GapGuardRejected, execution.ReasonCode(contract.ReasonStateBudgetExceeded)
			result.Items[index] = item
			continue
		}
		// Written at the floor, not at the Plan's own derived lifetime and not
		// without one. The load that runs at the start of every Slot renews it
		// to whatever this Plan actually needs, so the write only has to make
		// sure the key is never born immortal - which is what a Plan whose last
		// act was creating this key used to leave behind. Carrying the Plan's
		// retention here as well would be a third copy of one fact for a value
		// the next Slot overwrites anyway.
		applied, applyErr := backend.CompareAndSet(ctx, key, raw, raw == nil, encoded, GenerationScopedFloor)
		if applyErr != nil {
			item.Status, item.ReasonCode = execution.GapGuardRetryable, execution.ReasonCode(contract.ReasonStateWriteRetryable)
		} else if !applied {
			item.Status = execution.GapGuardConflict
		} else {
			item.Status = execution.GapGuardApplied
		}
		result.Items[index] = item
	}
	return result, result.Validate()
}

func validatePersistedGapScopes(scopes []execution.GapScopeState) error {
	for _, scope := range scopes {
		if scope.Status == execution.GapStatusWarming && scope.ObservedFullSlots >= scope.RequiredFullSlots {
			return fmt.Errorf("state: completed warmup must use GapClear")
		}
	}
	return nil
}

func decodeGap(raw []byte, identity execution.PlanGapIdentity, contractRef execution.FrozenExecutionContractRef, item execution.PlanGapLoadItem) execution.GapGuardSnapshot {
	invalid := execution.GapGuardSnapshot{Identity: identity, Status: execution.GapTerminal, ReasonCode: execution.ReasonCode(contract.ReasonStateCorrupt)}
	var value gapEnvelope
	if err := json.Unmarshal(raw, &value); err != nil {
		return invalid
	}
	if value.Schema != executionGapSchemaV2 {
		invalid.ReasonCode = execution.ReasonCode(contract.ReasonStateSchemaUnsupported)
		return invalid
	}
	if value.Identity != identity || value.MarkerRevision == 0 {
		return invalid
	}
	status := execution.GapFound
	if len(value.Scopes) == 0 {
		status = execution.GapClearedTombstone
	}
	snapshot := execution.GapGuardSnapshot{Identity: identity, MarkerRevision: value.MarkerRevision,
		PersistedApplyVersion: value.ApplyVersion, PersistedMutationDigest: value.MutationDigest,
		Status: status, LastScheduleRevision: value.ScheduleRevision, Scopes: value.Scopes}
	request := execution.GapLoadRequest{Contract: contractRef, Items: []execution.PlanGapLoadItem{item}}
	if err := execution.ValidateGapLoad(request, execution.GapLoadResult{Items: []execution.GapGuardSnapshot{snapshot}}); err != nil {
		return invalid
	}
	return snapshot
}

func applyGapScopes(previous []execution.GapScopeState, mutations []execution.GapScopeMutation, previousSchedule, nextSchedule execution.PlanScheduleRevision) []execution.GapScopeState {
	states := make(map[execution.GapScope]execution.GapScopeState, len(previous))
	for _, state := range previous {
		// A warmup count belongs to the schedule revision it was earned under,
		// and this write moves the marker to a new one. Every scope loses its
		// count, not only the scopes this mutation names: the envelope carries
		// one revision for all of them, so a scope left out of the mutation
		// would keep counting slots observed under a schedule that no longer
		// exists. It held while every recovery named every scope; the first
		// mutation that names a subset -- a Slot with no series, which can
		// speak for the Plan's scopes and not for a Level's -- separated
		// "the envelope's revision moved" from "the counts were discarded".
		if previousSchedule != nextSchedule {
			state.ObservedFullSlots = 0
		}
		states[state.Scope] = state
	}
	for _, mutation := range mutations {
		switch mutation.Kind {
		case execution.GapClear:
			delete(states, mutation.Scope)
		case execution.GapOpen, execution.GapStrengthen:
			states[mutation.Scope] = execution.GapScopeState{Scope: mutation.Scope, Status: execution.GapStatusGapped,
				ReasonCode: mutation.ReasonCode, RequiredFullSlots: mutation.RequiredFullSlots}
		case execution.GapWarmup:
			current := states[mutation.Scope]
			// The schedule revision is not asked about here: the sweep above
			// has already discarded every count it invalidates, and asking
			// again would put one rule in two places. What is left are the
			// per-scope reasons a count stops applying to its own scope.
			if current.RequiredFullSlots != mutation.RequiredFullSlots || current.ReasonCode != mutation.ReasonCode {
				current.ObservedFullSlots = 0
			}
			current.Scope, current.Status = mutation.Scope, execution.GapStatusWarming
			current.ReasonCode, current.RequiredFullSlots = mutation.ReasonCode, mutation.RequiredFullSlots
			if current.ObservedFullSlots < current.RequiredFullSlots {
				current.ObservedFullSlots++
			}
			states[mutation.Scope] = current
		}
	}
	result := make([]execution.GapScopeState, 0, len(states))
	for _, state := range states {
		result = append(result, state)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Scope.HasLevel != result[j].Scope.HasLevel {
			return !result[i].Scope.HasLevel
		}
		return result[i].Scope.LevelID < result[j].Scope.LevelID
	})
	return result
}

var _ execution.StateStore = (*ExecutionStore)(nil)
var _ execution.GapGuardStore = (*ExecutionStore)(nil)
