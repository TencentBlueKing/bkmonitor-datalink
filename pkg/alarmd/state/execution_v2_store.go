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

type ExecutionStoreOptions struct {
	Prefix          string
	Router          StorageRouter
	MaxValueBytes   int
	MaxItemsPerCall int
	RuntimeTTL      time.Duration
	// FenceKeys locates the ownership lease that fenced Runtime State writes
	// verify inside storage. It is optional: without it ApplyRuntimeFenced
	// applies unfenced and the admission-time fence check stands alone.
	FenceKeys FenceKeyResolver
}

type ExecutionStore struct {
	options   ExecutionStoreOptions
	witnesses *runtimeWitnessCache
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

func NewExecutionStore(options ExecutionStoreOptions) (*ExecutionStore, error) {
	if options.Prefix == "" || options.Router == nil || options.MaxValueBytes <= 0 ||
		options.MaxItemsPerCall <= 0 || options.RuntimeTTL <= 0 {
		return nil, fmt.Errorf("state: invalid execution store options")
	}
	return &ExecutionStore{options: options, witnesses: newRuntimeWitnessCache()}, nil
}

// LoadRuntime reads the requested keys in bounded MGET batches, grouping
// consecutive items that route to the same storage target. Each value is still
// classified on its own; a preflight witness is kept per readable key so the
// following ApplyRuntime can prove what it saw without reading again.
func (store *ExecutionStore) LoadRuntime(ctx context.Context, request execution.StatePreflightRequest) (execution.StatePreflightResult, error) {
	if err := request.Contract.Validate(); err != nil || len(request.Items) == 0 || len(request.Items) > store.options.MaxItemsPerCall {
		return execution.StatePreflightResult{}, fmt.Errorf("state: invalid runtime load request")
	}
	result := execution.StatePreflightResult{Items: make([]execution.RuntimeStateView, len(request.Items))}
	batch := &runtimeLoadBatch{}
	for index, item := range request.Items {
		view := execution.RuntimeStateView{Identity: item.Identity, Status: execution.StateMissingWarming}
		key, err := RuntimeStateKeyV2(store.options.Prefix, item.Identity)
		var target StorageTarget
		if err == nil {
			target, err = store.options.Router.Route(item.Identity.Plan.TenantID, item.Identity.Plan.StrategyID)
		}
		if err != nil {
			result.Items[index] = runtimeLoadFailure(view, err)
			continue
		}
		if len(batch.indexes) > 0 && (batch.target.Name != target.Name || len(batch.indexes) >= runtimeLoadBatchItems) {
			store.loadRuntimeBatch(ctx, request, batch, result.Items)
			batch.reset()
		}
		batch.target = target
		batch.indexes = append(batch.indexes, index)
		batch.keys = append(batch.keys, key)
	}
	store.loadRuntimeBatch(ctx, request, batch, result.Items)
	return execution.ClassifyStatePreflight(request, result)
}

func (store *ExecutionStore) AdmitRuntime(_ context.Context, request execution.StateApplyRequest) (execution.StateAdmissionResult, error) {
	if err := request.Contract.Validate(); err != nil || len(request.Items) == 0 || len(request.Items) > store.options.MaxItemsPerCall {
		return execution.StateAdmissionResult{}, fmt.Errorf("state: invalid runtime admission request")
	}
	result := execution.StateAdmissionResult{Items: make([]execution.StateAdmissionItemResult, len(request.Items))}
	for index, mutation := range request.Items {
		item := execution.StateAdmissionItemResult{Identity: mutation.Identity, Status: execution.StateAdmissionAccepted}
		if err := mutation.ValidateDigest(); err != nil {
			item.Status, item.ReasonCode = execution.StateAdmissionDeterministicInvalid, execution.ReasonCode(contract.ReasonStateCorrupt)
			result.Items[index] = item
			continue
		}
		encoded, err := encodeRuntime(mutation, mutation.ExpectedBlobRevision+1)
		if err != nil || len(encoded) > store.options.MaxValueBytes {
			item.Status, item.ReasonCode = execution.StateAdmissionDeterministicInvalid, execution.ReasonCode(contract.ReasonStateBudgetExceeded)
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

func decodeRuntime(raw []byte, identity execution.StateKeyIdentity, contractRef execution.FrozenExecutionContractRef, candidate execution.ApplyVersion) execution.RuntimeStateView {
	invalid := func(reason string) execution.RuntimeStateView {
		return execution.RuntimeStateView{Identity: identity, BlobRevision: 1, Status: execution.StateDeterministicInvalid,
			ReasonCode: execution.ReasonCode(reason)}
	}
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
	status := execution.StateFoundReady
	for i, level := range value.Levels {
		levels[i] = execution.RuntimeLevelStateView{LevelID: level.LevelID, LevelStateCompatibility: level.LevelStateCompatibility,
			HistoryCompleteness: level.HistoryCompleteness, GapReasonCode: level.GapReasonCode,
			WarmupRequirementRef: level.WarmupRequirementRef, LastProcessedEventTime: level.LastProcessedEventTime}
		if level.HistoryCompleteness == execution.HistoryGapped {
			status = execution.StateFoundGapped
		} else if level.HistoryCompleteness == execution.HistoryWarming && status != execution.StateFoundGapped {
			status = execution.StateFoundWarming
		}
	}
	if value.SeriesGuard != nil {
		if value.SeriesGuard.Status == execution.HistoryGapped {
			status = execution.StateFoundGapped
		} else if value.SeriesGuard.Status == execution.HistoryWarming && status != execution.StateFoundGapped {
			status = execution.StateFoundWarming
		}
	}
	view := execution.RuntimeStateView{Identity: identity, BlobRevision: value.BlobRevision,
		PersistedApplyVersion: value.ApplyVersion, PersistedMutationDigest: value.MutationDigest,
		LastProcessedEventTime: value.LastEventTime, SeriesGuard: value.SeriesGuard, Levels: levels, History: value.History, Status: status}
	classified, err := execution.ClassifyStatePreflight(execution.StatePreflightRequest{Contract: contractRef,
		Items: []execution.StatePreflightItem{{Identity: identity, ApplyVersion: candidate}}}, execution.StatePreflightResult{Items: []execution.RuntimeStateView{view}})
	if err != nil {
		return invalid(contract.ReasonStateCorrupt)
	}
	return classified.Items[0]
}

func (store *ExecutionStore) readOne(ctx context.Context, plan execution.PlanIdentity, key func() (string, error)) ([]byte, error) {
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
		raw, err := store.readOne(ctx, item.Identity.Plan, func() (string, error) { return PlanGapKeyV2(store.options.Prefix, item.Identity) })
		if err != nil {
			var identityErr *IdentityError
			if errors.As(err, &identityErr) {
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
		backend, ok := target.Backend.(CompareAndSetBackend)
		if routeErr != nil || !ok {
			item.Status, item.ReasonCode = execution.GapGuardRetryable, execution.ReasonCode(contract.ReasonRedisUnavailable)
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
				} else {
					item.Status = execution.GapGuardConflict
				}
				result.Items[index] = item
				continue
			}
		}
		if previous.MarkerRevision != mutation.ExpectedMarkerRevision {
			item.Status = execution.GapGuardConflict
			result.Items[index] = item
			continue
		}
		nextScopes := applyGapScopes(previous.Scopes, mutation.Scopes, previous.ScheduleRevision, mutation.ScheduleRevision)
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
		applied, applyErr := backend.CompareAndSet(ctx, key, raw, raw == nil, encoded, 0)
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
			if previousSchedule != nextSchedule || current.RequiredFullSlots != mutation.RequiredFullSlots || current.ReasonCode != mutation.ReasonCode {
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
