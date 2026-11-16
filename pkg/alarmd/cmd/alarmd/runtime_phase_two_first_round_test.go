// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// firstRoundRun is one minute's strategy on the production bundle, a real Redis
// and a clock the case steps, keeping what the rounds said.
type firstRoundRun struct {
	bundle *phaseTwoWorkerBundle
	clock  *atomic.Int64
	base   int64
	events *recordingPhaseTwoEventSink

	mu       sync.Mutex
	started  int
	deferred int
	done     int
	slack    []float64
	// boundaries is what access said of each arrival's queries, and waits the
	// in-place waits on the completion lines.
	boundaries []string
	waits      int
}

func (run *firstRoundRun) observe(_ context.Context, observation observability.Observation) {
	run.mu.Lock()
	defer run.mu.Unlock()
	switch observation.Stage {
	case observability.StageSlotStarted:
		run.started++
	case observability.StageSlotCompleted:
		if observation.ReasonCode == observability.ReasonCode(contract.ReasonQueryNotReady) {
			run.deferred++
		} else if observation.Result == observability.ResultSuccess {
			run.done++
		}
		if observation.InPlaceWait != nil {
			run.waits++
		}
	case observability.StageSlotReadinessArrival:
		if facts := observation.SlotReadiness; facts != nil {
			run.boundaries = append(run.boundaries, string(facts.Boundary))
			if facts.Slack {
				run.slack = append(run.slack, facts.SlackSeconds)
			}
		}
	}
}

func (run *firstRoundRun) counts() (started, deferred, done int) {
	run.mu.Lock()
	defer run.mu.Unlock()
	return run.started, run.deferred, run.done
}

// firstRoundThreshold is a strategy reading its one window, and
// firstRoundRingRatio one that also reads a history window ending earlier.
var (
	firstRoundThreshold = func(t *testing.T) []byte {
		return controlledG4StrategyDocument(t, 4191, strategy.DetectorKindThreshold, "usage", firstRoundTable, []string{"host"},
			[]any{[]any{map[string]any{"method": "gte", "threshold": 0}}})
	}
	firstRoundRingRatio = func(t *testing.T) []byte {
		return controlledG4StrategyDocument(t, 4191, strategy.DetectorKindSimpleRingRatio, "usage", firstRoundTable, []string{"host"},
			map[string]any{"floor": 50, "ceil": nil})
	}
)

const firstRoundTable = "system.cpu"

func newFirstRoundRun(t *testing.T, settling time.Duration, strategyDocument func(*testing.T) []byte) *firstRoundRun {
	t.Helper()
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	document := strategyDocument(t)
	for key, value := range map[string]any{"alarm-config.strategy_ids": "[4191]", "alarm-config.strategy_4191": string(document)} {
		if err := redisClient.Set(ctx, key, value, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	run := &firstRoundRun{clock: &atomic.Int64{}, base: controlledG4Base(t), events: &recordingPhaseTwoEventSink{}}
	run.clock.Store(run.base - 30)
	cfg := controlledG4RuntimeConfig(address, "http://first-round-uq", "alarmd-first-round")
	cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(settling)
	cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(5 * time.Second)
	bundle, err := openProductionPhaseTwoBundleWithDependencies(ctx, cfg, metric.NewRecorder(metric.BuildInfo{}),
		observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		}, phaseTwoProductionExternalDependencies{
			Now: func() time.Time { return time.Unix(run.clock.Load(), 0) }, HTTPClient: controlledG4UQClient(t, run.base, firstRoundTable),
			AdditionalObserver: observability.ObserverFunc(run.observe),
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return run.events, nil
			}),
		})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := bundle.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	if err := bundle.Start(ctx); err != nil {
		t.Fatal(err)
	}
	run.bundle = bundle
	return run
}

// at runs the scheduler once at second, and returns the second the Query Group's
// bound then says.
func (run *firstRoundRun) at(t *testing.T, second int64) int64 {
	t.Helper()
	run.clock.Store(second)
	if err := runScheduledOnceSettled(context.Background(), run.bundle); err != nil {
		t.Fatalf("round at %+d: %v", second-run.base, err)
	}
	run.bundle.mu.RLock()
	defer run.bundle.mu.RUnlock()
	for _, lifecycle := range run.bundle.runners {
		return lifecycle.runner.DueBound().NotDueUntilUnix
	}
	t.Fatal("no Query Group is owned")
	return 0
}

// From its grid point to its event, a minute's Slot without a hold has one round:
// nothing is started before its read, it is not turned away there, and it writes
// its event once. What says it is the read is access, by how long the data had
// been readable when the round arrived: under a second, the second it is rounded up to.
func TestAMinuteSlotWithoutAHoldRunsOnceAtItsRead(t *testing.T) {
	// A settling wait at the floor seeds no hold.
	run := newFirstRoundRun(t, execution.MinimumSettlingWait, firstRoundThreshold)
	bound := run.at(t, run.base)
	if bound <= run.base {
		t.Fatalf("at its grid point the bound is %+d, want its read after it", bound-run.base)
	}
	if started, deferred, done := run.counts(); started != 0 || deferred != 0 || done != 0 {
		t.Fatalf("at its grid point started=%d deferred=%d done=%d, want nothing", started, deferred, done)
	}
	if again := run.at(t, bound-1); again != bound {
		t.Fatalf("a second before the bound it is %+d, want %+d", again-run.base, bound-run.base)
	}
	if started, _, _ := run.counts(); started != 0 {
		t.Fatalf("a second before the bound %d Slots started", started)
	}
	run.at(t, bound)
	if started, deferred, done := run.counts(); started != 1 || deferred != 0 || done != 1 {
		t.Fatalf("at the bound started=%d deferred=%d done=%d, want one round that ran the Slot", started, deferred, done)
	}
	run.mu.Lock()
	slack := append([]float64(nil), run.slack...)
	run.mu.Unlock()
	if len(slack) != 1 || slack[0] < 0 || slack[0] >= 1 {
		t.Fatalf("access says the data had been readable for %v s, want one arrival under a second", slack)
	}
	if events := run.events.snapshot(); len(events) != 1 || events[0].EvaluationTime != run.base {
		t.Fatalf("events = %d, want the Slot's one event", len(events))
	}
}

// A held Slot still comes round at its read without the hold, and access turns it
// away once, by name, until the hold has passed. The deployment's wait above the
// floor seeds the hold.
func TestAHeldMinuteSlotIsTurnedAwayOnce(t *testing.T) {
	run := newFirstRoundRun(t, 30*time.Second, firstRoundThreshold)
	bound := run.at(t, run.base)
	if bound <= run.base {
		t.Fatalf("at its grid point the bound is %+d, want its read after it", bound-run.base)
	}
	if started, _, _ := run.counts(); started != 0 {
		t.Fatalf("at its grid point %d Slots started", started)
	}
	held := run.at(t, bound)
	if started, deferred, done := run.counts(); started != 1 || deferred != 1 || done != 0 {
		t.Fatalf("at the read without the hold started=%d deferred=%d done=%d, want one round turned away", started, deferred, done)
	}
	if held <= bound {
		t.Fatalf("turned away until %+d, want after %+d", held-run.base, bound-run.base)
	}
	// A turned-away Slot's bound is its backoff's, floored to the second: the
	// read itself is inside that second, and the clock here only moves by steps.
	run.at(t, held+1)
	if started, deferred, done := run.counts(); started != 2 || deferred != 1 || done != 1 {
		t.Fatalf("at the held read started=%d deferred=%d done=%d, want the Slot run", started, deferred, done)
	}
	if events := run.events.snapshot(); len(events) != 1 {
		t.Fatalf("events = %d, want the Slot's one event", len(events))
	}
}

// A minute's Slot reading a history window as well, ready long before its primary,
// reads both at the primary's read, and waits in place for nothing. It used to
// send the history at the grid point and hold its execution slot for the primary.
func TestAMinuteMixedSlotHasNoInPlaceWait(t *testing.T) {
	run := newFirstRoundRun(t, execution.MinimumSettlingWait, firstRoundRingRatio)
	bound := run.at(t, run.base)
	if bound <= run.base {
		t.Fatalf("at its grid point the bound is %+d, want its primary's read after it", bound-run.base)
	}
	if started, _, _ := run.counts(); started != 0 {
		t.Fatalf("at its grid point %d Slots started", started)
	}
	run.at(t, bound)
	if started, deferred, done := run.counts(); started != 1 || deferred != 0 || done != 1 {
		t.Fatalf("at the bound started=%d deferred=%d done=%d, want one round that ran the Slot", started, deferred, done)
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	if len(run.boundaries) != 1 || run.boundaries[0] != string(observability.ReadinessBoundaryMixed) {
		t.Fatalf("access says the queries' boundaries are %v, want one mixed: this case is about queries ready apart", run.boundaries)
	}
	if run.waits != 0 {
		t.Fatalf("%d in-place waits at the primary's read", run.waits)
	}
}
