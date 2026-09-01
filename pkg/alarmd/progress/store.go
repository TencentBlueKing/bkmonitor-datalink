// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package progress

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

const schemaV2 = "alarmd-schedule-progress-v2"

type ControlStore interface {
	ReadControl(context.Context, execution.QueryGroupIdentity, string) ([]byte, bool, error)
	FencedCompareAndSet(context.Context, ownership.FencedCASRequest) (ownership.FencedCASStatus, error)
}

type ContinuousSlotResolver interface {
	NextSlotAfter(context.Context, execution.QueryGroupIdentity, execution.EvaluationTime) (execution.EvaluationTime, error)
}

type StoreOptions struct {
	Prefix  string
	Control ControlStore
	Slots   ContinuousSlotResolver
	Now     func() time.Time
}

type Store struct{ options StoreOptions }

type envelope struct {
	Schema   string                     `json:"schema"`
	Progress execution.ScheduleProgress `json:"progress"`
}

type DeterministicInvalidError struct{ Err error }

func (err *DeterministicInvalidError) Error() string {
	return fmt.Sprintf("progress: deterministic-invalid persisted value: %v", err.Err)
}
func (err *DeterministicInvalidError) Unwrap() error { return err.Err }

func NewStore(options StoreOptions) (*Store, error) {
	if options.Prefix == "" || strings.ContainsAny(options.Prefix, "{} \t\r\n") ||
		options.Control == nil || options.Slots == nil || options.Now == nil {
		return nil, fmt.Errorf("progress: invalid store options")
	}
	return &Store{options: options}, nil
}

func (store *Store) LoadProgress(ctx context.Context, identity execution.ProgressIdentity) (execution.ProgressLoadResult, error) {
	name, err := store.namespace(identity)
	if err != nil {
		return execution.ProgressLoadResult{}, err
	}
	raw, missing, err := store.options.Control.ReadControl(ctx, identity.QueryGroup, name)
	if err != nil {
		return execution.ProgressLoadResult{}, err
	}
	if missing {
		return execution.ProgressLoadResult{Status: execution.ProgressMissing}, nil
	}
	value, err := decode(raw)
	if err != nil {
		return execution.ProgressLoadResult{}, &DeterministicInvalidError{Err: err}
	}
	if value.Identity != identity {
		return execution.ProgressLoadResult{}, &DeterministicInvalidError{Err: fmt.Errorf("persisted identity does not match requested identity")}
	}
	result := execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &value}
	return result, result.Validate(identity)
}

func (store *Store) CommitProgress(ctx context.Context, request execution.ProgressCommitRequest) (execution.ProgressCommitResult, error) {
	if err := request.Validate(); err != nil {
		return execution.ProgressCommitResult{}, err
	}
	if err := validateEnabledCompletion(request.Completion); err != nil {
		return execution.ProgressCommitResult{}, err
	}
	name, err := store.namespace(request.Identity)
	if err != nil {
		return execution.ProgressCommitResult{}, err
	}
	raw, missing, err := store.options.Control.ReadControl(ctx, request.Identity.QueryGroup, name)
	if err != nil {
		return retryable(), nil
	}
	var current execution.ScheduleProgress
	if !missing {
		var decodeErr error
		current, decodeErr = decode(raw)
		if decodeErr != nil {
			return execution.ProgressCommitResult{}, &DeterministicInvalidError{Err: decodeErr}
		}
		if current.Identity != request.Identity {
			return execution.ProgressCommitResult{}, &DeterministicInvalidError{Err: fmt.Errorf("persisted identity does not match commit identity")}
		}
		currentNext, resolveErr := store.resolveCurrentNextSlot(ctx, current)
		if resolveErr != nil {
			return execution.ProgressCommitResult{}, resolveErr
		}
		if currentNext != request.ExpectedNextSlot {
			return execution.ProgressCommitResult{Status: execution.ProgressConflict}, nil
		}
	}
	nextSlot, err := store.options.Slots.NextSlotAfter(ctx, request.Identity.QueryGroup, request.ExpectedNextSlot)
	if err != nil {
		return execution.ProgressCommitResult{}, fmt.Errorf("progress: resolve next continuous Slot: %w", err)
	}
	if nextSlot <= request.ExpectedNextSlot {
		return execution.ProgressCommitResult{}, fmt.Errorf("progress: next continuous Slot must follow completion")
	}
	next := execution.ScheduleProgress{Identity: request.Identity, NextSlot: nextSlot,
		LastCompletionKind: request.Completion.Kind}
	if !missing {
		next.LastFullSlot = current.LastFullSlot
		next.CurrentOrRecentGap = current.CurrentOrRecentGap
	}
	if request.Completion.Primary != nil && request.Completion.Primary.Completeness == execution.CompletenessFull {
		next.LastFullSlot = request.ExpectedNextSlot
	}
	if shouldFoldRecentGap(current, request.Completion) {
		next.CurrentOrRecentGap = foldRecentGap(current, request)
	}
	encoded, err := encode(next)
	if err != nil {
		return execution.ProgressCommitResult{}, err
	}
	status, applyErr := store.options.Control.FencedCompareAndSet(ctx, ownership.FencedCASRequest{
		Fence: request.OwnerFence, At: store.options.Now(), Namespace: name,
		ExpectedMissing: missing, Expected: raw, Value: encoded, TTL: 0,
	})
	switch status {
	case ownership.FencedCASApplied:
		return execution.ProgressCommitResult{Status: execution.ProgressCommitted}, nil
	case ownership.FencedCASConflict:
		return execution.ProgressCommitResult{Status: execution.ProgressConflict}, nil
	case ownership.FencedCASStaleOwner:
		return execution.ProgressCommitResult{Status: execution.ProgressStaleOwner}, nil
	default:
		if applyErr != nil {
			return retryable(), nil
		}
		return retryable(), nil
	}
}

func validateEnabledCompletion(completion execution.SlotCompletion) error {
	switch completion.Kind {
	case execution.CompletionFull, execution.CompletionFullEmpty,
		execution.CompletionPartialGap, execution.CompletionUnavailable,
		execution.CompletionTerminal, execution.CompletionGapSkipped:
		// ProgressCommitRequest.Validate has already checked the complete result,
		// PRIMARY and reason contract. Persist every completion enabled through
		// G3b so one local deterministic terminal cannot stop the Worker.
		return nil
	default:
		// SNAPSHOT_UNAVAILABLE remains closed until its later Gate.
		return fmt.Errorf("progress: store does not accept completion kind %q in the current Gate", completion.Kind)
	}
}

func shouldFoldRecentGap(
	current execution.ScheduleProgress,
	completion execution.SlotCompletion,
) bool {
	switch completion.Kind {
	case execution.CompletionPartialGap, execution.CompletionTerminal, execution.CompletionGapSkipped:
		return true
	case execution.CompletionUnavailable:
		// A FULL+DATA result can remain guarded by an earlier query-free
		// GAP_SKIPPED episode. Preserve that episode instead of replacing it with
		// a second summary for the same durable guard.
		return completion.Primary == nil || completion.Primary.Completeness != execution.CompletenessFull ||
			completion.Primary.DataState != execution.DataStateData || current.CurrentOrRecentGap == nil ||
			current.CurrentOrRecentGap.Kind != execution.CompletionGapSkipped ||
			current.CurrentOrRecentGap.ReasonCode != completion.ReasonCode
	default:
		return false
	}
}

func foldRecentGap(
	current execution.ScheduleProgress,
	request execution.ProgressCommitRequest,
) *execution.ProgressGapSummary {
	if current.LastCompletionKind == request.Completion.Kind && current.CurrentOrRecentGap != nil &&
		current.CurrentOrRecentGap.Kind == request.Completion.Kind &&
		current.CurrentOrRecentGap.ReasonCode == request.Completion.ReasonCode {
		next := *current.CurrentOrRecentGap
		next.LastSlot = request.ExpectedNextSlot
		next.Count++
		return &next
	}
	return &execution.ProgressGapSummary{
		Kind: request.Completion.Kind, ReasonCode: request.Completion.ReasonCode,
		FirstSlot: request.ExpectedNextSlot, LastSlot: request.ExpectedNextSlot, Count: 1,
	}
}

func (store *Store) resolveCurrentNextSlot(
	ctx context.Context,
	current execution.ScheduleProgress,
) (execution.EvaluationTime, error) {
	completed := current.LastFullSlot
	if current.CurrentOrRecentGap != nil && current.CurrentOrRecentGap.LastSlot > completed {
		completed = current.CurrentOrRecentGap.LastSlot
	}
	if completed <= 0 {
		return current.NextSlot, nil
	}
	next, err := store.options.Slots.NextSlotAfter(ctx, current.Identity.QueryGroup, completed)
	if err != nil {
		return 0, fmt.Errorf("progress: resolve persisted continuous Slot: %w", err)
	}
	if next <= completed {
		return 0, fmt.Errorf("progress: persisted continuous Slot must follow last completion")
	}
	return next, nil
}

func (store *Store) namespace(identity execution.ProgressIdentity) (string, error) {
	if identity.QueryGroup == "" {
		return "", fmt.Errorf("progress: complete identity is required")
	}
	return store.options.Prefix + ":progress", nil
}

func encode(progress execution.ScheduleProgress) ([]byte, error) {
	if err := progress.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(envelope{Schema: schemaV2, Progress: progress})
}

func decode(raw []byte) (execution.ScheduleProgress, error) {
	var value envelope
	if err := json.Unmarshal(raw, &value); err != nil {
		return execution.ScheduleProgress{}, err
	}
	if value.Schema != schemaV2 {
		return execution.ScheduleProgress{}, fmt.Errorf("unsupported schema %q", value.Schema)
	}
	if err := value.Progress.Validate(); err != nil {
		return execution.ScheduleProgress{}, err
	}
	return value.Progress, nil
}

func retryable() execution.ProgressCommitResult {
	return execution.ProgressCommitResult{Status: execution.ProgressRetryableIO, ReasonCode: execution.ReasonCode(contract.ReasonRedisUnavailable)}
}

var _ execution.ProgressStore = (*Store)(nil)
