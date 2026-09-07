// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// StatePreflightBatchItems bounds how many series one Runtime State preflight
// read carries. The worker groups completed series up to this size so a Slot
// with S series costs about S/StatePreflightBatchItems storage round trips
// instead of one per series. It is a constant, not a tenant knob: it protects
// storage and worker memory and never changes evaluation semantics.
const StatePreflightBatchItems = 256

// StateApplyMaxChunks bounds how many Store calls the worker may split one
// Plan's Runtime State (or Plan gap) apply into when its mutation count
// exceeds the Store per-call item limit. Together with that limit it fixes
// the largest Slot the Store side supports (8192 x 64 = 524288 mutations);
// a Slot beyond that bound is unsupported and completes deterministically
// instead of being retried. It is a constant, not a tenant or deployment
// knob: the Store call size itself never grows with process resources.
const StateApplyMaxChunks = 64

// SlotMutationCap derives the largest number of State (or Gap) mutations one
// Slot may produce: the process budget, additionally bounded by what
// StateApplyMaxChunks Store calls of storeItems can carry. A zero storeItems
// means the store has no per-call bound below the process budget.
func SlotMutationCap(storeItems, processBudget uint64) uint64 {
	if storeItems == 0 {
		return processBudget
	}
	if chunked := storeItems * StateApplyMaxChunks; chunked < processBudget {
		return chunked
	}
	return processBudget
}

// StateApplyFence carries the owner fence one Runtime State apply must verify
// inside the storage write itself. At is the wall-clock instant the fence is
// compared against the lease deadline; it uses the same rule as admission.
type StateApplyFence struct {
	Fence OwnerFence
	At    time.Time
}

func (fence StateApplyFence) Validate(contractRef FrozenExecutionContractRef) error {
	if err := fence.Fence.Validate(contractRef); err != nil {
		return err
	}
	if fence.At.IsZero() {
		return errors.New("alarmd execution: state apply fence time is required")
	}
	return nil
}

// FencedStateStore is an optional StateStore extension. Stores that can verify
// the owner fence together with every Runtime State write expose it; the worker
// prefers it whenever the Slot carries a valid fence and otherwise keeps using
// StateStore.ApplyRuntime. A stale fence is reported as an error for the whole
// request, never as a partial per-item result.
type FencedStateStore interface {
	ApplyRuntimeFenced(context.Context, StateApplyRequest, StateApplyFence) (StateApplyResult, error)
}

// RecordAnchor identifies one selected source point without copying it.
type RecordAnchor struct {
	RecordID   string
	SourceTime int64
}

func (anchor RecordAnchor) Validate() error {
	if anchor.RecordID == "" || anchor.SourceTime <= 0 {
		return errors.New("alarmd execution: incomplete record anchor")
	}
	return nil
}

// RuntimeLevelStateMutation is the typed new Level state written as part of
// one Plan/series blob. Storage codecs may encode it, but cannot reinterpret it.
type RuntimeLevelStateMutation struct {
	LevelID                 uint32
	LevelStateCompatibility string
	HistoryCompleteness     HistoryCompleteness
	GapReasonCode           ReasonCode
	WarmupRequirementRef    string
	LastProcessedEventTime  int64
}

// RuntimeLevelContractRef is the sole source for persisted Level compatibility
// and warmup identities. Evaluators and codecs must not invent these strings.
type RuntimeLevelContractRef struct {
	LevelID                 uint32
	LevelStateCompatibility string
	WarmupRequirementRef    string
	DetectFingerprint       string
}

// DeriveRuntimeLevelContractRefs closes persisted state identities over the
// Compiler-owned state and Level semantics.
func DeriveRuntimeLevelContractRefs(plan *strategy.CompiledPlan) ([]RuntimeLevelContractRef, error) {
	if plan == nil || plan.StateCompatibilityHash() == "" {
		return nil, errors.New("alarmd execution: compiled state contract is required")
	}
	levels := plan.Levels()
	refs := make([]RuntimeLevelContractRef, len(levels))
	for index, level := range levels {
		definition := level.Definition()
		fingerprints := level.Fingerprints()
		compatibility, err := contract.DeriveCanonicalDigestV2("alarmd-level-state-compatibility-v1", struct {
			PlanStateCompatibility string `json:"plan_state_compatibility"`
			LevelID                uint32 `json:"level_id"`
			DetectFingerprint      string `json:"detect_fingerprint"`
			TriggerFingerprint     string `json:"trigger_fingerprint"`
		}{plan.StateCompatibilityHash(), definition.LevelID, fingerprints.Detect, fingerprints.Trigger})
		if err != nil {
			return nil, fmt.Errorf("alarmd execution: derive Level state compatibility: %w", err)
		}
		warmup, err := contract.DeriveCanonicalDigestV2("alarmd-level-warmup-requirement-v1", struct {
			LevelID          uint32                    `json:"level_id"`
			StateRequirement strategy.StateRequirement `json:"state_requirement"`
		}{definition.LevelID, level.StateRequirement()})
		if err != nil {
			return nil, fmt.Errorf("alarmd execution: derive Level warmup requirement: %w", err)
		}
		refs[index] = RuntimeLevelContractRef{
			LevelID: definition.LevelID, LevelStateCompatibility: compatibility,
			WarmupRequirementRef: warmup, DetectFingerprint: fingerprints.Detect,
		}
	}
	sort.Slice(refs, func(left, right int) bool { return refs[left].LevelID < refs[right].LevelID })
	return refs, nil
}

// One validator owns this lazy lookup. It must not outlive that call or cache
// validation of a mutable StateMutation. Unused contracts remain unexamined.
type runtimeLevelContracts struct {
	plan   *strategy.CompiledPlan
	loaded bool
	refs   []RuntimeLevelContractRef
	index  map[uint32]RuntimeLevelContractRef
	err    error
}

func indexRuntimeLevelContracts(refs []RuntimeLevelContractRef) map[uint32]RuntimeLevelContractRef {
	index := make(map[uint32]RuntimeLevelContractRef, len(refs))
	for _, ref := range refs {
		if _, found := index[ref.LevelID]; !found {
			index[ref.LevelID] = ref
		}
	}
	return index
}

func (contracts *runtimeLevelContracts) load() {
	if contracts.loaded {
		return
	}
	contracts.loaded = true
	contracts.refs, contracts.err = DeriveRuntimeLevelContractRefs(contracts.plan)
	if contracts.err == nil {
		contracts.index = indexRuntimeLevelContracts(contracts.refs)
	}
}

func (contracts *runtimeLevelContracts) find(levelID uint32) (RuntimeLevelContractRef, bool) {
	contracts.load()
	ref, found := contracts.index[levelID]
	return ref, found
}

func (contracts *runtimeLevelContracts) seriesWarmup() (string, error) {
	contracts.load()
	if contracts.err != nil {
		return "", contracts.err
	}
	return contract.DeriveCanonicalDigestV2("alarmd-series-warmup-requirement-v1", contracts.refs)
}

// DeriveRuntimeSeriesWarmupRequirementRef closes a series-wide guard over the
// exact compiled Level warmup set.
func DeriveRuntimeSeriesWarmupRequirementRef(plan *strategy.CompiledPlan) (string, error) {
	refs, err := DeriveRuntimeLevelContractRefs(plan)
	if err != nil {
		return "", err
	}
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-series-warmup-requirement-v1", refs)
	if err != nil {
		return "", fmt.Errorf("alarmd execution: derive series warmup requirement: %w", err)
	}
	return digest, nil
}

func BuildStateMutation(mutation StateMutation) (StateMutation, error) {
	if mutation.MutationDigest != "" {
		return StateMutation{}, errors.New("alarmd execution: State mutation builder owns the digest")
	}
	mutation = normalizeStateMutation(mutation)
	digest, err := deriveStateMutationDigest(mutation)
	if err != nil {
		return StateMutation{}, err
	}
	mutation.MutationDigest = digest
	return mutation, nil
}

func (mutation StateMutation) ValidateDigest() error {
	if mutation.MutationDigest == "" {
		return errors.New("alarmd execution: State mutation digest is required")
	}
	if !stateMutationIsCanonical(mutation) {
		return errors.New("alarmd execution: State mutation collections are not canonically ordered")
	}
	expected, err := deriveStateMutationDigest(mutation)
	if err != nil {
		return err
	}
	if mutation.MutationDigest != expected {
		return errors.New("alarmd execution: State mutation digest does not match typed new state")
	}
	return nil
}

func deriveStateMutationDigest(mutation StateMutation) (MutationDigest, error) {
	if err := mutation.Identity.Plan.Validate(); err != nil {
		return "", err
	}
	if mutation.Identity.StateGeneration == "" || mutation.Identity.SeriesIdentityDigest == "" {
		return "", errors.New("alarmd execution: incomplete State mutation identity")
	}
	if err := mutation.ApplyVersion.Validate(); err != nil {
		return "", err
	}
	if len(mutation.AffectedRecords) == 0 || len(mutation.Levels) == 0 {
		return "", errors.New("alarmd execution: State mutation requires record anchors and Level state")
	}
	anchors := mutation.AffectedRecords
	for index, anchor := range anchors {
		if err := anchor.Validate(); err != nil {
			return "", err
		}
		if index > 0 && anchor == anchors[index-1] {
			return "", errors.New("alarmd execution: duplicate State mutation record anchor")
		}
	}
	levels := mutation.Levels
	seenLevels := make(map[uint32]struct{}, len(levels))
	for _, level := range levels {
		if _, duplicate := seenLevels[level.LevelID]; duplicate {
			return "", errors.New("alarmd execution: duplicate State mutation Level")
		}
		seenLevels[level.LevelID] = struct{}{}
		if err := validateRuntimeLevelMutation(level); err != nil {
			return "", err
		}
	}
	points := mutation.Points
	levelIDs := make(map[uint32]struct{}, len(levels))
	for _, level := range levels {
		levelIDs[level.LevelID] = struct{}{}
	}
	if err := validateStateHistory(points, levelIDs); err != nil {
		return "", err
	}
	if err := validateStateGuardMutation(mutation.SeriesGuard); err != nil {
		return "", err
	}
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-runtime-state-mutation-v1", struct {
		Identity        StateKeyIdentity            `json:"identity"`
		ApplyVersion    ApplyVersion                `json:"apply_version"`
		AffectedRecords []RecordAnchor              `json:"affected_records"`
		SeriesGuard     *StateGuardFact             `json:"series_guard,omitempty"`
		Levels          []RuntimeLevelStateMutation `json:"levels"`
		Points          []StateHistoryPoint         `json:"points"`
	}{mutation.Identity, mutation.ApplyVersion, anchors, mutation.SeriesGuard, levels, points})
	if err != nil {
		return "", fmt.Errorf("alarmd execution: derive State mutation digest: %w", err)
	}
	return MutationDigest(digest), nil
}

func normalizeStateMutation(mutation StateMutation) StateMutation {
	mutation.AffectedRecords = append([]RecordAnchor(nil), mutation.AffectedRecords...)
	sort.Slice(mutation.AffectedRecords, func(left, right int) bool {
		if mutation.AffectedRecords[left].SourceTime != mutation.AffectedRecords[right].SourceTime {
			return mutation.AffectedRecords[left].SourceTime < mutation.AffectedRecords[right].SourceTime
		}
		return mutation.AffectedRecords[left].RecordID < mutation.AffectedRecords[right].RecordID
	})
	mutation.Levels = append([]RuntimeLevelStateMutation(nil), mutation.Levels...)
	sort.Slice(mutation.Levels, func(left, right int) bool { return mutation.Levels[left].LevelID < mutation.Levels[right].LevelID })
	mutation.Points = append([]StateHistoryPoint(nil), mutation.Points...)
	for index := range mutation.Points {
		mutation.Points[index].Levels = append([]StateLevelFact(nil), mutation.Points[index].Levels...)
		sort.Slice(mutation.Points[index].Levels, func(left, right int) bool {
			return mutation.Points[index].Levels[left].LevelID < mutation.Points[index].Levels[right].LevelID
		})
	}
	return mutation
}

func stateMutationIsCanonical(mutation StateMutation) bool {
	normalized := normalizeStateMutation(mutation)
	if len(normalized.AffectedRecords) != len(mutation.AffectedRecords) || len(normalized.Levels) != len(mutation.Levels) ||
		len(normalized.Points) != len(mutation.Points) {
		return false
	}
	for index := range mutation.AffectedRecords {
		if mutation.AffectedRecords[index] != normalized.AffectedRecords[index] {
			return false
		}
	}
	for index := range mutation.Levels {
		if mutation.Levels[index] != normalized.Levels[index] {
			return false
		}
	}
	for pointIndex := range mutation.Points {
		if mutation.Points[pointIndex].RecordID != normalized.Points[pointIndex].RecordID ||
			mutation.Points[pointIndex].SourceTime != normalized.Points[pointIndex].SourceTime ||
			len(mutation.Points[pointIndex].Levels) != len(normalized.Points[pointIndex].Levels) {
			return false
		}
		for levelIndex := range mutation.Points[pointIndex].Levels {
			if mutation.Points[pointIndex].Levels[levelIndex] != normalized.Points[pointIndex].Levels[levelIndex] {
				return false
			}
		}
	}
	return true
}

func validateStateGuardMutation(guard *StateGuardFact) error {
	if guard == nil {
		return nil
	}
	if guard.Status != HistoryWarming && guard.Status != HistoryGapped || guard.WarmupRequirementRef == "" {
		return errors.New("alarmd execution: invalid State mutation series guard")
	}
	return ValidateResultReason(observability.ResultDegraded, guard.ReasonCode)
}

func validateRuntimeLevelMutation(level RuntimeLevelStateMutation) error {
	if level.LevelID == 0 || level.LevelStateCompatibility == "" || level.WarmupRequirementRef == "" {
		return errors.New("alarmd execution: incomplete State mutation Level")
	}
	switch level.HistoryCompleteness {
	case HistoryFull:
		if level.GapReasonCode != "" {
			return errors.New("alarmd execution: FULL State mutation Level carries a gap reason")
		}
	case HistoryWarming, HistoryGapped:
		if err := ValidateResultReason(observability.ResultDegraded, level.GapReasonCode); err != nil {
			return err
		}
	default:
		return errors.New("alarmd execution: invalid State mutation Level completeness")
	}
	return nil
}
