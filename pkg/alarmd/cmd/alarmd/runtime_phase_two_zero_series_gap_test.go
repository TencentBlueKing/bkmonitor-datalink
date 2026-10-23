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
	"encoding/json"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// levelGuard is the Level scope of the persisted marker, if one stands: its
// observed FULL rounds and requirement.
func (fixture *cutoverStallFixture) levelGuard(ctx context.Context) (observed, required float64, standing bool) {
	fixture.t.Helper()
	envelope, found := fixture.readPlanGapMarker(ctx)
	if !found {
		return 0, 0, false
	}
	scopes, _ := envelope["scopes"].([]any)
	for _, entry := range scopes {
		scope, _ := entry.(map[string]any)
		inner, _ := scope["Scope"].(map[string]any)
		if hasLevel, _ := inner["HasLevel"].(bool); hasLevel {
			observed, _ = scope["ObservedFullSlots"].(float64)
			required, _ = scope["RequiredFullSlots"].(float64)
			return observed, required, true
		}
	}
	return 0, 0, false
}

// seedLevelGuard replaces the persisted marker's scopes with one fresh Level
// scope carrying reason: the marker a failed query opens, nothing observed
// yet, due the Plan's own requirement. It reports whether a marker was there.
//
// Fresh on purpose: the backlog rounds before the first current one answer
// whole too, and warm whatever marker they meet - which is the behaviour
// under test, but it would leave the first current round meeting a marker
// already part-way to clearing.
func (fixture *cutoverStallFixture) seedLevelGuard(ctx context.Context, reason string) bool {
	fixture.t.Helper()
	envelope, found := fixture.readPlanGapMarker(ctx)
	if !found {
		return false
	}
	envelope["scopes"] = []any{map[string]any{
		"Scope": map[string]any{"LevelID": 1, "HasLevel": true}, "Status": "GAPPED", "ReasonCode": reason,
		"RequiredFullSlots": fixture.requiredFullSlots(ctx), "ObservedFullSlots": 0,
	}}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		fixture.t.Fatal(err)
	}
	key, _ := fixture.planGapMarkerKey(ctx)
	if err := fixture.redisClient.Set(ctx, key, encoded, 0).Err(); err != nil {
		fixture.t.Fatal(err)
	}
	return true
}

// runCurrentDataRounds runs the Query Group from the stalled cutover until it
// has executed rounds more current Slots that asked the query, calling each
// after every one of them. Until the first current round the persisted marker
// is reseeded to one fresh Level scope with the given reason, so the first
// current round meets exactly that.
func (fixture *cutoverStallFixture) runCurrentDataRounds(
	ctx context.Context,
	reason string,
	rounds int,
	each func(round int, outcome execution.SlotExecutionResult, observed []observability.Observation),
) {
	t := fixture.t
	t.Helper()
	limits := fixture.production.dependencies.RecoveryLimits
	const staleAge = 2 * time.Hour
	if staleAge <= 2*limits.MaxReplayAge {
		t.Fatalf("stale age %s must exceed MaxReplayAge %s by far", staleAge, limits.MaxReplayAge)
	}
	fixture.clock.Store(int64(fixture.firstNewSlot)*1000 + staleAge.Milliseconds() + 17_000)
	current, rewritten := 0, false
	for iteration := 1; iteration <= 1000 && current < rounds; iteration++ {
		if nextAt := fixture.runner.NextReadyAt(); nextAt.After(fixture.now()) {
			fixture.clock.Store(nextAt.UnixMilli() + 1)
		}
		if current == 0 && fixture.seedLevelGuard(ctx, reason) {
			rewritten = true
		}
		at := fixture.now()
		before := len(fixture.observed())
		queriesBefore := fixture.uqCalls.Load()
		outcome, attempted, err := fixture.runner.RunOne(ctx)
		if err != nil {
			t.Fatalf("round stopped: RunOne error = %v (Progress=%+v)", err, fixture.progress(ctx))
		}
		if !attempted {
			next := at.Unix() - at.Unix()%60 + 60
			fixture.clock.Store(next*1000 + 1500)
			continue
		}
		if !outcome.Completed {
			if outcome.Result == observability.ResultRetrying {
				t.Fatalf("round returned retrying %s (Progress=%+v)", outcome.ReasonCode, fixture.progress(ctx))
			}
			fixture.clock.Store(at.Add(time.Second).UnixMilli())
			continue
		}
		slot := int64(0)
		for _, observation := range fixture.observed()[before:] {
			if observation.Stage == observability.StageSlotCompleted {
				slot = observation.Trace.EvaluationTime
			}
		}
		if slot+60 > at.Unix() && fixture.uqCalls.Load() > queriesBefore {
			current++
			each(current, outcome, fixture.observed()[before:])
		}
	}
	if !rewritten {
		t.Fatal("no Plan gap marker was ever persisted to rewrite, so the guard was never exercised")
	}
	if current < rounds {
		t.Fatalf("ran %d current data rounds, want %d", current, rounds)
	}
}

// committedCause is the completion cause and its reason the round's progress
// was committed under.
func committedCause(observed []observability.Observation) (string, string) {
	for _, observation := range observed {
		if observation.Stage == observability.StageProgressCommitted {
			return string(observation.ProgressCompletionCause), string(observation.ProgressCompletionReason)
		}
	}
	return "", ""
}

func newZeroSeriesGapFixture(t *testing.T) *cutoverStallFixture {
	t.Helper()
	previousWindow := cutoverStallTriggerWindow
	cutoverStallTriggerWindow = 3
	t.Cleanup(func() { cutoverStallTriggerWindow = previousWindow })
	return newCutoverStalledFixture(t, func(cfg *config.Config) {
	})
}

// A Plan that matches no series clears the guard a failed query opened on its
// Level once as many whole rounds have passed as the guard requires - and not
// one round sooner. Each of those rounds answered FULL with nothing for the
// Plan: a known absence, not the unknown the guard is about, and after the
// required count the failed round is outside every window a Level decides
// over.
//
// Before, only a round in which a series wrote state warmed a Level scope, so
// a Plan matching no series kept the guard for as long as it matched none,
// held its Levels' reason on the Slot every round, and read as a query that
// kept failing.
func TestAPlanMatchingNoSeriesClearsItsLevelGuardAfterTheRequiredWholeRounds(t *testing.T) {
	fixture := newZeroSeriesGapFixture(t)
	ctx := context.Background()
	fixture.uqHosts = func(int64) []string { return []string{} }
	required := int(fixture.requiredFullSlots(ctx))
	if required < 2 {
		t.Fatalf("RequiredFullSlots = %d, want at least 2 so both sides of the boundary are rounds", required)
	}
	fixture.runCurrentDataRounds(ctx, contract.ReasonQueryUnavailable, required, func(round int, _ execution.SlotExecutionResult, _ []observability.Observation) {
		observed, _, standing := fixture.levelGuard(ctx)
		switch {
		case round < required && !standing:
			t.Fatalf("round %d of %d: the Level guard cleared early", round, required)
		case round < required && int(observed) != round:
			t.Fatalf("round %d of %d: the Level guard has observed %v rounds, want %d", round, required, observed, round)
		case round == required && standing:
			t.Fatalf("round %d of %d: the Level guard still stands (observed %v)", round, required, observed)
		}
	})
}

// A series that comes back while the guard is warming still meets it: the
// rounds it was absent for counted towards the requirement, but the guard is
// standing when the series is first evaluated again - its Level is held, the
// Slot says so with the guard's reason - and it clears only when the whole
// count is reached. The round after, the series is decided on its own.
func TestASeriesThatReturnsDuringTheWarmupIsStillUnderTheGuard(t *testing.T) {
	fixture := newZeroSeriesGapFixture(t)
	ctx := context.Background()
	required := int(fixture.requiredFullSlots(ctx))
	if required < 3 {
		t.Fatalf("RequiredFullSlots = %d, want at least 3: one empty round, one returning round, one to clear", required)
	}
	returning := false
	fixture.uqHosts = func(int64) []string {
		if returning {
			return []string{"127.0.0.1"}
		}
		return []string{}
	}
	fixture.runCurrentDataRounds(ctx, contract.ReasonQueryUnavailable, required+1, func(round int, outcome execution.SlotExecutionResult, seen []observability.Observation) {
		observed, _, standing := fixture.levelGuard(ctx)
		if round < required && (!standing || int(observed) != round) {
			t.Fatalf("round %d of %d (series back: %t): guard standing %t, observed %v, want standing at %d",
				round, required, returning, standing, observed, round)
		}
		if round >= required && standing {
			t.Fatalf("round %d of %d: the Level guard still stands (observed %v)", round, required, observed)
		}
		held := outcome.CompletionKind == execution.CompletionUnavailable &&
			outcome.ReasonCode == observability.ReasonCode(contract.ReasonQueryUnavailable)
		switch {
		case returning && round <= required && !held:
			t.Fatalf("round %d of %d: the returning series was not held by the guard: %s %s",
				round, required, outcome.CompletionKind, outcome.ReasonCode)
		case round > required && held:
			t.Fatalf("round %d, after the guard cleared: still held: %s %s", round, outcome.CompletionKind, outcome.ReasonCode)
		}
		// The round answered whole, so what held the series is the guard's
		// warmup and not this round's query: the cause says so, beside the
		// guard's reason.
		if returning && round <= required {
			if cause, reason := committedCause(seen); cause != string(execution.CauseGapGuardWarming) || reason != contract.ReasonQueryUnavailable {
				t.Fatalf("round %d of %d: committed under %q / %q, want %s / %s", round, required, cause, reason,
					execution.CauseGapGuardWarming, contract.ReasonQueryUnavailable)
			}
		}
		// The series comes back from the second round on: the first round
		// is empty and counts, the rest evaluate the series under the guard.
		returning = true
	})
}
