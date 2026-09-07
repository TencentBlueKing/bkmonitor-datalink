// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package progress

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

const schemaV2 = "alarmd-schedule-progress-v2"
const schemaRangeV1 = "alarmd-schedule-progress-expired-range-v1"
const schemaRangeV2 = "alarmd-schedule-progress-expired-range-v2"

type ControlStore interface {
	ReadControl(context.Context, execution.QueryGroupIdentity, string) ([]byte, bool, error)
	FencedCompareAndSet(context.Context, ownership.FencedCASRequest) (ownership.FencedCASStatus, error)
}

type TemporaryLegacyDrainingCASStore interface {
	ControlStore
	ReadControlForTemporaryLegacyDrainingCAS(context.Context, execution.QueryGroupIdentity, string) (ownership.TemporaryLegacyDrainingCASRead, error)
}

type TemporaryLegacyDrainingCASLoadResult struct {
	RedisKey string
	Raw      []byte
	Load     execution.ProgressLoadResult
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
func (err *DeterministicInvalidError) Unwrap() error             { return err.Err }
func (err *DeterministicInvalidError) DeterministicControlFact() {}

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

// LoadProgressForTemporaryLegacyDrainingCAS returns the decoded Progress and
// exact Redis bytes for the approved one-shot legacy Draining cleanup. Remove
// it together with that temporary administrative command.
func (store *Store) LoadProgressForTemporaryLegacyDrainingCAS(ctx context.Context, identity execution.ProgressIdentity) (TemporaryLegacyDrainingCASLoadResult, error) {
	name, err := store.namespace(identity)
	if err != nil {
		return TemporaryLegacyDrainingCASLoadResult{}, err
	}
	control, ok := store.options.Control.(TemporaryLegacyDrainingCASStore)
	if !ok {
		return TemporaryLegacyDrainingCASLoadResult{}, errors.New("progress: control store does not expose temporary legacy Draining CAS read facts")
	}
	fact, err := control.ReadControlForTemporaryLegacyDrainingCAS(ctx, identity.QueryGroup, name)
	if err != nil {
		return TemporaryLegacyDrainingCASLoadResult{}, err
	}
	if fact.Missing {
		return TemporaryLegacyDrainingCASLoadResult{RedisKey: fact.RedisKey, Load: execution.ProgressLoadResult{Status: execution.ProgressMissing}}, nil
	}
	value, err := decode(fact.Raw)
	if err != nil {
		return TemporaryLegacyDrainingCASLoadResult{}, &DeterministicInvalidError{Err: err}
	}
	if value.Identity != identity {
		return TemporaryLegacyDrainingCASLoadResult{}, &DeterministicInvalidError{Err: fmt.Errorf("persisted identity does not match requested identity")}
	}
	load := execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &value}
	if err := load.Validate(identity); err != nil {
		return TemporaryLegacyDrainingCASLoadResult{}, err
	}
	return TemporaryLegacyDrainingCASLoadResult{RedisKey: fact.RedisKey, Raw: fact.Raw, Load: load}, nil
}

func (store *Store) BeginSlot(ctx context.Context, request execution.ProgressBeginRequest) (execution.ProgressBeginResult, error) {
	if request.Identity.QueryGroup == "" || request.Projection.Contract.Slot.QueryGroup != request.Identity.QueryGroup {
		return execution.ProgressBeginResult{}, fmt.Errorf("progress: invalid BeginSlot identity")
	}
	if err := request.OwnerFence.Validate(request.Projection.Contract); err != nil {
		return execution.ProgressBeginResult{}, err
	}
	if err := request.Projection.Validate(); err != nil {
		return execution.ProgressBeginResult{}, err
	}
	name, err := store.namespace(request.Identity)
	if err != nil {
		return execution.ProgressBeginResult{}, err
	}
	raw, missing, err := store.options.Control.ReadControl(ctx, request.Identity.QueryGroup, name)
	if err != nil {
		return execution.ProgressBeginResult{Status: execution.ProgressRetryableIO, ReasonCode: execution.ReasonCode(contract.ReasonRedisUnavailable)}, nil
	}
	current := execution.ScheduleProgress{Identity: request.Identity, NextSlot: request.Projection.Contract.Slot.EvaluationTime}
	if !missing {
		current, err = decode(raw)
		if err != nil {
			return execution.ProgressBeginResult{}, &DeterministicInvalidError{Err: err}
		}
		if current.Identity != request.Identity {
			return execution.ProgressBeginResult{Status: execution.ProgressConflict}, nil
		}
		if current.UnfinishedRange != nil {
			return execution.ProgressBeginResult{Status: execution.ProgressConflict}, nil
		}
		if current.UnfinishedSlot != nil {
			if request.Projection.Contract.ScheduleSegmentStart > current.UnfinishedSlot.Contract.ScheduleSegmentStart {
				// A later Schedule Segment now owns the unfinished Slot's time, so
				// the persisted contract can never be re-frozen: the newer Segment
				// supersedes it. The same EvaluationTime simply replaces the
				// projection; another one abandons the old Slot first.
				if current.NextSlot != request.Projection.Contract.Slot.EvaluationTime {
					conflict, abandonErr := store.abandonUnfinishedSlot(ctx, &current, request.Projection.Contract.Slot.EvaluationTime)
					if abandonErr != nil {
						return execution.ProgressBeginResult{}, abandonErr
					}
					if conflict {
						return execution.ProgressBeginResult{Status: execution.ProgressConflict}, nil
					}
				}
			} else if current.NextSlot != request.Projection.Contract.Slot.EvaluationTime {
				return execution.ProgressBeginResult{Status: execution.ProgressConflict}, nil
			} else if !current.UnfinishedSlot.Equal(request.Projection) {
				return execution.ProgressBeginResult{}, &DeterministicInvalidError{Err: fmt.Errorf("unfinished Slot projection differs from persisted facts")}
			}
		} else if current.NextSlot != request.Projection.Contract.Slot.EvaluationTime {
			// A cutover can leave the persisted cursor on the old Schedule grid.
			// Only the exact successor independently resolved from completed facts
			// may replace that cursor before the unfinished projection is written.
			currentNext, resolveErr := store.resolveCurrentNextSlot(ctx, current)
			if resolveErr != nil {
				return execution.ProgressBeginResult{}, resolveErr
			}
			if currentNext != request.Projection.Contract.Slot.EvaluationTime {
				return execution.ProgressBeginResult{Status: execution.ProgressConflict}, nil
			}
			current.NextSlot = currentNext
		}
	}
	priorUnfinished := current.UnfinishedSlot != nil
	projection := request.Projection
	current.UnfinishedSlot = &projection
	encoded, err := encode(current)
	if err != nil {
		return execution.ProgressBeginResult{}, err
	}
	status, applyErr := store.options.Control.FencedCompareAndSet(ctx, ownership.FencedCASRequest{
		Fence: request.OwnerFence, At: store.options.Now(), Namespace: name,
		ExpectedMissing: missing, Expected: raw, Value: encoded, TTL: 0,
	})
	switch status {
	case ownership.FencedCASApplied:
		execution.CaptureSlotCoverage(ctx, func(c *execution.SlotCoverageCapture) {
			if c.BeginCommitted != nil {
				c.BeginCommitted(priorUnfinished)
			}
		})
		return execution.ProgressBeginResult{Status: execution.ProgressCommitted}, nil
	case ownership.FencedCASConflict:
		return execution.ProgressBeginResult{Status: execution.ProgressConflict}, nil
	case ownership.FencedCASStaleOwner:
		return execution.ProgressBeginResult{Status: execution.ProgressStaleOwner}, nil
	default:
		_ = applyErr
		return execution.ProgressBeginResult{Status: execution.ProgressRetryableIO, ReasonCode: execution.ReasonCode(contract.ReasonRedisUnavailable)}, nil
	}
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
		if current.UnfinishedRange != nil {
			return execution.ProgressCommitResult{Status: execution.ProgressConflict}, nil
		}
		currentNext, resolveErr := store.resolveCurrentNextSlot(ctx, current)
		if resolveErr != nil {
			return execution.ProgressCommitResult{}, resolveErr
		}
		if currentNext != request.ExpectedNextSlot {
			return execution.ProgressCommitResult{Status: execution.ProgressConflict}, nil
		}
		if !request.Projection.IsZero() &&
			(current.UnfinishedSlot == nil || !current.UnfinishedSlot.Equal(request.Projection)) {
			return execution.ProgressCommitResult{}, &DeterministicInvalidError{Err: fmt.Errorf("persisted unfinished Slot projection does not match completion")}
		}
	} else if !request.Projection.IsZero() {
		return execution.ProgressCommitResult{Status: execution.ProgressConflict}, nil
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
		execution.CompletionTerminal, execution.CompletionGapSkipped,
		execution.CompletionSnapshotUnavailable:
		// ProgressCommitRequest.Validate has already checked the complete result,
		// PRIMARY and reason contract. Persist every completion enabled through
		// G3b so one local deterministic terminal cannot stop the Worker.
		return nil
	default:
		return fmt.Errorf("progress: store does not accept completion kind %q in the current Gate", completion.Kind)
	}
}

func shouldFoldRecentGap(
	current execution.ScheduleProgress,
	completion execution.SlotCompletion,
) bool {
	switch completion.Kind {
	case execution.CompletionPartialGap, execution.CompletionTerminal, execution.CompletionGapSkipped,
		execution.CompletionSnapshotUnavailable:
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

// abandonUnfinishedSlot drops the superseded unfinished Slot and moves the
// cursor to the successor the completed facts prove. A later requested Slot
// means the abandoned Slot was skipped, so it is first recorded in the recent
// gap summary exactly as a GAP_SKIPPED completion would be; an earlier one
// means the new grid still reaches that time, so nothing is recorded. A
// requested Slot that is not the proven successor conflicts and nothing is
// written.
func (store *Store) abandonUnfinishedSlot(
	ctx context.Context,
	current *execution.ScheduleProgress,
	requested execution.EvaluationTime,
) (bool, error) {
	abandoned := current.NextSlot
	current.UnfinishedSlot = nil
	if requested > abandoned {
		skipped := execution.ProgressCommitRequest{ExpectedNextSlot: abandoned, Completion: execution.SlotCompletion{
			Kind: execution.CompletionGapSkipped, ReasonCode: execution.ReasonCode(contract.ReasonGapSkipped)}}
		current.CurrentOrRecentGap = foldRecentGap(*current, skipped)
		current.LastCompletionKind = execution.CompletionGapSkipped
	}
	next, err := store.resolveCurrentNextSlot(ctx, *current)
	if err != nil {
		return false, err
	}
	if next != requested {
		return true, nil
	}
	current.NextSlot = next
	return false, nil
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
	schema := schemaV2
	if progress.UnfinishedRange != nil {
		schema = schemaRangeV1
		if progress.UnfinishedRange.EligibilityV2 != nil {
			schema = schemaRangeV2
		}
	}
	raw, err := json.Marshal(envelope{Schema: schema, Progress: progress})
	if err == nil && progress.UnfinishedRange != nil && len(raw) > execution.MaxExpiredRangeProjectionBytes {
		return nil, execution.ErrExpiredRangeProofTooLarge
	}
	return raw, err
}

func decode(raw []byte) (execution.ScheduleProgress, error) {
	if len(raw) > execution.MaxExpiredRangeProjectionBytes {
		// Inspect only the envelope discriminator before allocating a range's
		// nested schedules/targets. Existing single-slot v2 remains unchanged.
		var header struct {
			Schema string `json:"schema"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			return execution.ScheduleProgress{}, err
		}
		if header.Schema == schemaRangeV1 || header.Schema == schemaRangeV2 {
			return execution.ScheduleProgress{}, execution.ErrExpiredRangeProofTooLarge
		}
	}
	var value envelope
	if err := json.Unmarshal(raw, &value); err != nil {
		return execution.ScheduleProgress{}, err
	}
	if value.Schema != schemaV2 && value.Schema != schemaRangeV1 && value.Schema != schemaRangeV2 {
		return execution.ScheduleProgress{}, fmt.Errorf("unsupported schema %q", value.Schema)
	}
	if (value.Schema == schemaRangeV1 || value.Schema == schemaRangeV2) != (value.Progress.UnfinishedRange != nil) {
		return execution.ScheduleProgress{}, fmt.Errorf("progress schema does not match pending representation")
	}
	if value.Progress.UnfinishedRange != nil && (value.Schema == schemaRangeV2) != (value.Progress.UnfinishedRange.EligibilityV2 != nil) {
		return execution.ScheduleProgress{}, fmt.Errorf("progress range schema does not match proof version")
	}
	if value.Schema == schemaRangeV1 {
		// V1's accepted fields also remain unchanged: explicit null must not
		// smuggle a V2 discriminator under the legacy schema.
		var fields struct {
			Progress struct{ UnfinishedRange map[string]json.RawMessage }
		}
		if err := json.Unmarshal(raw, &fields); err != nil {
			return execution.ScheduleProgress{}, err
		}
		for name := range fields.Progress.UnfinishedRange {
			if strings.EqualFold(name, "EligibilityV2") {
				return execution.ScheduleProgress{}, fmt.Errorf("V2 eligibility in V1 range")
			}
		}
	}
	if value.Schema == schemaRangeV1 || value.Schema == schemaRangeV2 {
		if len(raw) > execution.MaxExpiredRangeProjectionBytes {
			return execution.ScheduleProgress{}, execution.ErrExpiredRangeProofTooLarge
		}
		if _, err := contract.CanonicalJSONV2(json.RawMessage(raw)); err != nil {
			return execution.ScheduleProgress{}, err
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&value); err != nil {
			return execution.ScheduleProgress{}, err
		}
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
