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
	"math/rand"
	"time"
)

type DependencyRetryConfig struct {
	MinDelay time.Duration
	MaxDelay time.Duration
}

type DependencyOperation func(context.Context) error

type dependencyRetryJitter func(time.Duration) time.Duration
type dependencyRetryWait func(context.Context, time.Duration) error

// DependencyRetry is for a task that has already passed intake admission.
// It pauses new admission after a dependency failure but keeps this task in an
// unbounded, context-controlled retry loop until the dependency recovers.
type DependencyRetry struct {
	gate    *CriticalDependencyGate
	blocker DependencyBlocker
	config  DependencyRetryConfig
	jitter  dependencyRetryJitter
	wait    dependencyRetryWait
}

func NewDependencyRetry(
	gate *CriticalDependencyGate,
	blocker DependencyBlocker,
	config DependencyRetryConfig,
) (*DependencyRetry, error) {
	return newDependencyRetry(gate, blocker, config, defaultDependencyRetryJitter, waitDependencyRetryDelay)
}

func newDependencyRetry(
	gate *CriticalDependencyGate,
	blocker DependencyBlocker,
	config DependencyRetryConfig,
	jitter dependencyRetryJitter,
	wait dependencyRetryWait,
) (*DependencyRetry, error) {
	if gate == nil || blocker.Dependency == "" || blocker.ReasonCode == "" {
		return nil, errors.New("alarmd coordinator: retry gate, dependency and reason are required")
	}
	if config.MinDelay <= 0 || config.MaxDelay < config.MinDelay {
		return nil, errors.New("alarmd coordinator: retry delay range is invalid")
	}
	if jitter == nil || wait == nil {
		return nil, errors.New("alarmd coordinator: retry jitter and wait functions are required")
	}
	return &DependencyRetry{gate: gate, blocker: blocker, config: config, jitter: jitter, wait: wait}, nil
}

func (retry *DependencyRetry) Do(ctx context.Context, operation DependencyOperation) error {
	if retry == nil || retry.gate == nil || retry.jitter == nil || retry.wait == nil {
		return errors.New("alarmd coordinator: initialized dependency retry is required")
	}
	if ctx == nil || operation == nil {
		return errors.New("alarmd coordinator: retry context and operation are required")
	}

	delay := retry.config.MinDelay
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		operationErr := operation(ctx)
		if err := ctx.Err(); err != nil {
			return err
		}
		if operationErr == nil {
			_, resumeErr := retry.gate.Resume(retry.blocker.Dependency)
			return resumeErr
		}
		if _, err := retry.gate.Pause(retry.blocker); err != nil {
			return err
		}
		if err := retry.wait(ctx, boundedDependencyRetryJitter(retry.jitter(delay), delay)); err != nil {
			return err
		}
		delay = nextDependencyRetryDelay(delay, retry.config.MaxDelay)
	}
}

func defaultDependencyRetryJitter(base time.Duration) time.Duration {
	half := base / 2
	width := base - half
	if width <= 0 {
		return base
	}
	return half + time.Duration(rand.Int63n(int64(width)+1))
}

func boundedDependencyRetryJitter(delay, upper time.Duration) time.Duration {
	if delay < 0 {
		return 0
	}
	if delay > upper {
		return upper
	}
	return delay
}

func nextDependencyRetryDelay(current, maximum time.Duration) time.Duration {
	if current >= maximum || current > maximum/2 {
		return maximum
	}
	return current * 2
}

func waitDependencyRetryDelay(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
