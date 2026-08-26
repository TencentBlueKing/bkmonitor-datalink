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
	"reflect"
	"testing"
	"time"
)

func TestDependencyRetryPausesIntakeAndResumesAfterRecovery(t *testing.T) {
	t.Parallel()

	gate := NewCriticalDependencyGate(nil)
	waits := make([]time.Duration, 0, 3)
	retry, err := newDependencyRetry(
		gate,
		DependencyBlocker{Dependency: DependencyRedis, ReasonCode: "REDIS_UNAVAILABLE"},
		DependencyRetryConfig{MinDelay: 10 * time.Millisecond, MaxDelay: 25 * time.Millisecond},
		func(base time.Duration) time.Duration { return base },
		func(_ context.Context, delay time.Duration) error {
			waits = append(waits, delay)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	operationError := errors.New("redis unavailable")
	if err := retry.Do(context.Background(), func(context.Context) error {
		attempts++
		if attempts < 4 {
			return operationError
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if attempts != 4 || !reflect.DeepEqual(waits, []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 25 * time.Millisecond}) {
		t.Fatalf("retry = attempts:%d waits:%v", attempts, waits)
	}
	if !gate.Ready() {
		t.Fatalf("gate remained paused after recovery: %#v", gate.Snapshot())
	}
}

func TestDependencyRetryDoesNotExhaustBeforeContextCancellation(t *testing.T) {
	t.Parallel()

	gate := NewCriticalDependencyGate(nil)
	ctx, cancel := context.WithCancel(context.Background())
	retry, err := newDependencyRetry(
		gate,
		DependencyBlocker{Dependency: DependencyProvider, ReasonCode: "PROVIDER_UNAVAILABLE"},
		DependencyRetryConfig{MinDelay: time.Millisecond, MaxDelay: 4 * time.Millisecond},
		func(base time.Duration) time.Duration { return base },
		func(ctx context.Context, _ time.Duration) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	want := errors.New("provider unavailable")
	err = retry.Do(ctx, func(context.Context) error {
		attempts++
		if attempts == 6 {
			cancel()
		}
		return want
	})
	if !errors.Is(err, context.Canceled) || attempts != 6 {
		t.Fatalf("Do() = attempts:%d err:%v, want six attempts then cancellation", attempts, err)
	}
	if gate.Ready() {
		t.Fatal("cancellation incorrectly marked the failed dependency recovered")
	}
}

func TestDependencyRetryContinuesTaskThatStartedBeforePause(t *testing.T) {
	t.Parallel()

	gate := NewCriticalDependencyGate(nil)
	blocker := DependencyBlocker{Dependency: DependencyOutputKafka, ReasonCode: "KAFKA_UNAVAILABLE"}
	if _, err := gate.Pause(blocker); err != nil {
		t.Fatal(err)
	}
	retry, err := NewDependencyRetry(gate, blocker, DependencyRetryConfig{
		MinDelay: time.Millisecond, MaxDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	if err := retry.Do(context.Background(), func(context.Context) error {
		attempts++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || !gate.Ready() {
		t.Fatalf("started retry = attempts:%d snapshot:%#v", attempts, gate.Snapshot())
	}
}

func TestDependencyRetryBoundsInjectedJitter(t *testing.T) {
	t.Parallel()

	waits := make([]time.Duration, 0, 2)
	retry, err := newDependencyRetry(
		NewCriticalDependencyGate(nil),
		DependencyBlocker{Dependency: DependencyRedis, ReasonCode: "REDIS_UNAVAILABLE"},
		DependencyRetryConfig{MinDelay: 5 * time.Millisecond, MaxDelay: 10 * time.Millisecond},
		func(base time.Duration) time.Duration { return base * 100 },
		func(_ context.Context, delay time.Duration) error {
			waits = append(waits, delay)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	attempts := 0
	if err := retry.Do(context.Background(), func(context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("retry")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(waits, []time.Duration{5 * time.Millisecond, 10 * time.Millisecond}) {
		t.Fatalf("bounded waits = %v", waits)
	}
}

func TestDependencyRetryStopsBeforeFirstAttemptWhenContextCanceled(t *testing.T) {
	t.Parallel()

	retry, err := NewDependencyRetry(
		NewCriticalDependencyGate(nil),
		DependencyBlocker{Dependency: DependencyRedis, ReasonCode: "REDIS_UNAVAILABLE"},
		DependencyRetryConfig{MinDelay: time.Millisecond, MaxDelay: time.Millisecond},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	attempts := 0
	err = retry.Do(ctx, func(context.Context) error {
		attempts++
		return nil
	})
	if !errors.Is(err, context.Canceled) || attempts != 0 {
		t.Fatalf("Do() = attempts:%d err:%v", attempts, err)
	}
}

func TestDependencyRetryCancelsDefaultBackoffImmediately(t *testing.T) {
	t.Parallel()

	retry, err := NewDependencyRetry(
		NewCriticalDependencyGate(nil),
		DependencyBlocker{Dependency: DependencyRedis, ReasonCode: "REDIS_UNAVAILABLE"},
		DependencyRetryConfig{MinDelay: time.Hour, MaxDelay: time.Hour},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	err = retry.Do(ctx, func(context.Context) error {
		attempts++
		cancel()
		return errors.New("redis unavailable")
	})
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatalf("Do() = attempts:%d err:%v", attempts, err)
	}
}

func TestDependencyRetryDoesNotMarkRecoveryAfterContextCancellation(t *testing.T) {
	t.Parallel()

	gate := NewCriticalDependencyGate(nil)
	blocker := DependencyBlocker{Dependency: DependencyRedis, ReasonCode: "REDIS_UNAVAILABLE"}
	if _, err := gate.Pause(blocker); err != nil {
		t.Fatal(err)
	}
	retry, err := NewDependencyRetry(gate, blocker, DependencyRetryConfig{
		MinDelay: time.Millisecond, MaxDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	err = retry.Do(ctx, func(context.Context) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || gate.Ready() {
		t.Fatalf("Do() = %v, snapshot = %#v", err, gate.Snapshot())
	}
}

func TestDependencyRetryRejectsInvalidConstruction(t *testing.T) {
	t.Parallel()

	validBlocker := DependencyBlocker{Dependency: DependencyRedis, ReasonCode: "REDIS_UNAVAILABLE"}
	for _, test := range []struct {
		name    string
		gate    *CriticalDependencyGate
		blocker DependencyBlocker
		config  DependencyRetryConfig
	}{
		{name: "gate", blocker: validBlocker, config: DependencyRetryConfig{MinDelay: time.Millisecond, MaxDelay: time.Millisecond}},
		{name: "blocker", gate: NewCriticalDependencyGate(nil), config: DependencyRetryConfig{MinDelay: time.Millisecond, MaxDelay: time.Millisecond}},
		{name: "delay", gate: NewCriticalDependencyGate(nil), blocker: validBlocker, config: DependencyRetryConfig{MinDelay: 2 * time.Millisecond, MaxDelay: time.Millisecond}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewDependencyRetry(test.gate, test.blocker, test.config); err == nil {
				t.Fatal("NewDependencyRetry() succeeded")
			}
		})
	}
}
