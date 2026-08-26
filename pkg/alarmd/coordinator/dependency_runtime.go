// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package coordinator

import (
	"context"
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// RetryingEffectiveTimeProvider retries only source errors that explicitly
// identify themselves as EffectiveTime dependency failures. UNKNOWN facts and
// deterministic provider errors remain ordinary evaluation outcomes.
type RetryingEffectiveTimeProvider struct {
	provider strategy.EffectiveTimeProvider
	retry    *DependencyRetry
}

func NewRetryingEffectiveTimeProvider(
	provider strategy.EffectiveTimeProvider,
	gate *CriticalDependencyGate,
	config DependencyRetryConfig,
) (*RetryingEffectiveTimeProvider, error) {
	if provider == nil {
		return nil, errors.New("alarmd coordinator: EffectiveTime provider is required")
	}
	retry, err := NewDependencyRetry(gate, DependencyBlocker{
		Dependency: DependencyProvider, ReasonCode: contract.ReasonProviderUnavailable,
	}, config)
	if err != nil {
		return nil, err
	}
	return &RetryingEffectiveTimeProvider{provider: provider, retry: retry}, nil
}

func (provider *RetryingEffectiveTimeProvider) Resolve(
	ctx context.Context,
	requests []strategy.EffectiveTimeRequest,
) (facts []strategy.EffectiveTimeFact, err error) {
	if provider == nil || provider.provider == nil || provider.retry == nil {
		return nil, errors.New("alarmd coordinator: initialized retrying EffectiveTime provider is required")
	}
	err = provider.retry.Do(ctx, func(ctx context.Context) error {
		facts, err = provider.provider.Resolve(ctx, requests)
		if !isRetryableEffectiveTimeDependency(err) {
			return err
		}
		return &RetryableDependencyError{Err: err}
	})
	return facts, err
}

// RetryingRuntimeState retries only typed Redis backend failures. Deterministic
// validation, budget and state-shape errors leave the Store unchanged.
type RetryingRuntimeState struct {
	store      state.RuntimeStateStore
	loadRetry  *DependencyRetry
	writeRetry *DependencyRetry
}

func NewRetryingRuntimeState(
	store state.RuntimeStateStore,
	gate *CriticalDependencyGate,
	config DependencyRetryConfig,
) (*RetryingRuntimeState, error) {
	if store == nil {
		return nil, errors.New("alarmd coordinator: runtime state store is required")
	}
	loadRetry, err := NewDependencyRetry(gate, DependencyBlocker{
		Dependency: DependencyRedis, ReasonCode: contract.ReasonRedisUnavailable,
	}, config)
	if err != nil {
		return nil, err
	}
	writeRetry, err := NewDependencyRetry(gate, DependencyBlocker{
		Dependency: DependencyRedis, ReasonCode: contract.ReasonStateWriteRetryable,
	}, config)
	if err != nil {
		return nil, err
	}
	return &RetryingRuntimeState{store: store, loadRetry: loadRetry, writeRetry: writeRetry}, nil
}

func (runtimeState *RetryingRuntimeState) LoadWindows(
	ctx context.Context,
	request state.LoadWindowsRequest,
) (result state.LoadWindowsResult, err error) {
	if runtimeState == nil || runtimeState.store == nil || runtimeState.loadRetry == nil {
		return result, errors.New("alarmd coordinator: initialized retrying runtime state is required")
	}
	err = runtimeState.loadRetry.Do(ctx, func(ctx context.Context) error {
		result, err = runtimeState.store.LoadWindows(ctx, request)
		return markStateDependencyRetryable(err, state.DependencyOperationLoad)
	})
	return result, err
}

func (runtimeState *RetryingRuntimeState) WriteWindows(
	ctx context.Context,
	request state.WriteWindowsRequest,
) (result state.WriteWindowsResult, err error) {
	if runtimeState == nil || runtimeState.store == nil || runtimeState.writeRetry == nil {
		return result, errors.New("alarmd coordinator: initialized retrying runtime state is required")
	}
	err = runtimeState.writeRetry.Do(ctx, func(ctx context.Context) error {
		result, err = runtimeState.store.WriteWindows(ctx, request)
		return markStateDependencyRetryable(err, state.DependencyOperationWrite)
	})
	return result, err
}

// RetryingCriticalPhaseCompletion keeps retries inside the current completion
// phase. RoutedPartitionRunner therefore advances only after Event ACK and does
// not resend an ACKed Event batch while a later state write is recovering.
type RetryingCriticalPhaseCompletion struct {
	completion  CriticalPhaseCompletion
	eventsRetry *DependencyRetry
	stateRetry  *DependencyRetry
}

func NewRetryingCriticalPhaseCompletion(
	completion CriticalPhaseCompletion,
	gate *CriticalDependencyGate,
	config DependencyRetryConfig,
) (*RetryingCriticalPhaseCompletion, error) {
	if completion == nil {
		return nil, errors.New("alarmd coordinator: critical phase completion is required")
	}
	eventsRetry, err := NewDependencyRetry(gate, DependencyBlocker{
		Dependency: DependencyOutputKafka, ReasonCode: contract.ReasonOutputACKUnknown,
	}, config)
	if err != nil {
		return nil, err
	}
	stateRetry, err := NewDependencyRetry(gate, DependencyBlocker{
		Dependency: DependencyRedis, ReasonCode: contract.ReasonStateWriteRetryable,
	}, config)
	if err != nil {
		return nil, err
	}
	return &RetryingCriticalPhaseCompletion{
		completion: completion, eventsRetry: eventsRetry, stateRetry: stateRetry,
	}, nil
}

func (completion *RetryingCriticalPhaseCompletion) CompleteEvents(
	ctx context.Context,
	events []contract.TriggerEventV1,
) error {
	if completion == nil || completion.completion == nil || completion.eventsRetry == nil {
		return errors.New("alarmd coordinator: initialized retrying critical phase completion is required")
	}
	return completion.eventsRetry.Do(ctx, func(ctx context.Context) error {
		err := completion.completion.CompleteEvents(ctx, events)
		if !isRetryableOutputDependency(err) {
			return err
		}
		return &RetryableDependencyError{Err: err}
	})
}

func (completion *RetryingCriticalPhaseCompletion) CompleteState(
	ctx context.Context,
	request state.WriteWindowsRequest,
) error {
	if completion == nil || completion.completion == nil || completion.stateRetry == nil {
		return errors.New("alarmd coordinator: initialized retrying critical phase completion is required")
	}
	return completion.stateRetry.Do(ctx, func(ctx context.Context) error {
		return markStateDependencyRetryable(
			completion.completion.CompleteState(ctx, request), state.DependencyOperationWrite,
		)
	})
}

func markStateDependencyRetryable(err error, operation state.DependencyOperation) error {
	if err == nil {
		return nil
	}
	var dependencyErr *state.DependencyError
	if !errors.As(err, &dependencyErr) || dependencyErr == nil || dependencyErr.Operation != operation {
		return err
	}
	return &RetryableDependencyError{Err: err}
}

func isRetryableOutputDependency(err error) bool {
	if err == nil {
		return false
	}
	var dependencyErr interface{ RetryableOutputDependency() }
	return errors.As(err, &dependencyErr) && dependencyErr != nil
}

func isRetryableEffectiveTimeDependency(err error) bool {
	if err == nil {
		return false
	}
	var dependencyErr interface{ RetryableEffectiveTimeDependency() }
	return errors.As(err, &dependencyErr) && dependencyErr != nil
}
