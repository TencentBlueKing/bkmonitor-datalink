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

const schemaV1 = "alarmd-schedule-progress-v1"

type ControlStore interface {
	ReadControl(context.Context, execution.QueryGroupIdentity, string) ([]byte, bool, error)
	FencedCompareAndSet(context.Context, ownership.FencedCASRequest) (ownership.FencedCASStatus, error)
}

type StoreOptions struct {
	Prefix  string
	Control ControlStore
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
	if options.Prefix == "" || strings.ContainsAny(options.Prefix, "{} \t\r\n") || options.Control == nil || options.Now == nil {
		return nil, fmt.Errorf("progress: invalid store options")
	}
	return &Store{options: options}, nil
}

func (store *Store) LoadProgress(ctx context.Context, namespace execution.ProgressNamespace) (execution.ProgressLoadResult, error) {
	name, err := store.namespace(namespace)
	if err != nil {
		return execution.ProgressLoadResult{}, err
	}
	raw, missing, err := store.options.Control.ReadControl(ctx, namespace.QueryGroup, name)
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
	if value.Namespace != namespace {
		return execution.ProgressLoadResult{}, &DeterministicInvalidError{Err: fmt.Errorf("persisted namespace does not match requested namespace")}
	}
	result := execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &value}
	return result, result.Validate(namespace)
}

func (store *Store) CommitProgress(ctx context.Context, request execution.ProgressCommitRequest) (execution.ProgressCommitResult, error) {
	if err := request.Validate(); err != nil {
		return execution.ProgressCommitResult{}, err
	}
	if request.Completion.Kind != execution.CompletionFull && request.Completion.Kind != execution.CompletionFullEmpty {
		return execution.ProgressCommitResult{}, fmt.Errorf("progress: G1 store accepts only FULL completion")
	}
	name, err := store.namespace(request.Namespace)
	if err != nil {
		return execution.ProgressCommitResult{}, err
	}
	raw, missing, err := store.options.Control.ReadControl(ctx, request.Namespace.QueryGroup, name)
	if err != nil {
		return retryable(), nil
	}
	if !missing {
		current, decodeErr := decode(raw)
		if decodeErr != nil {
			return execution.ProgressCommitResult{}, &DeterministicInvalidError{Err: decodeErr}
		}
		if current.Namespace != request.Namespace {
			return execution.ProgressCommitResult{}, &DeterministicInvalidError{Err: fmt.Errorf("persisted namespace does not match commit namespace")}
		}
		if current.NextSlot != request.ExpectedNextSlot {
			return execution.ProgressCommitResult{Status: execution.ProgressConflict}, nil
		}
	}
	next := execution.ScheduleProgress{Namespace: request.Namespace, NextSlot: request.NextSlotAfterCompletion,
		LastCompletionKind: request.Completion.Kind}
	if request.Completion.Kind == execution.CompletionFull || request.Completion.Kind == execution.CompletionFullEmpty {
		next.LastFullSlot = request.ExpectedNextSlot
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

func (store *Store) namespace(namespace execution.ProgressNamespace) (string, error) {
	if namespace.QueryGroup == "" || namespace.ScheduleRevision == "" {
		return "", fmt.Errorf("progress: complete namespace is required")
	}
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-progress-schedule-revision-v1", namespace.ScheduleRevision)
	if err != nil {
		return "", err
	}
	return store.options.Prefix + ":progress:" + digest[:32], nil
}

func encode(progress execution.ScheduleProgress) ([]byte, error) {
	if err := progress.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(envelope{Schema: schemaV1, Progress: progress})
}

func decode(raw []byte) (execution.ScheduleProgress, error) {
	var value envelope
	if err := json.Unmarshal(raw, &value); err != nil {
		return execution.ScheduleProgress{}, err
	}
	if value.Schema != schemaV1 {
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
