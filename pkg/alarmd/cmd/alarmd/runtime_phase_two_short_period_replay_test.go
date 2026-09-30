// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A ten-second Slot first reached after its live deadline is replayed and
// completes.
//
// This is the production failure end to end. Strategy on a ten-second period;
// the completion deadline is thirty seconds and the deployment holds five back
// for the downstream, so the query deadline is T+25. The worker first reaches
// the Slot at T+26 -- past live, three grid points behind, the last distance
// the replay window allows.
//
// It used to die there and could not be rescued by scheduling faster. The
// scheduler said replay; access, for a recovery operation, answered that the
// data would not be ready until T+30; and T+30 is the fourth grid point, at
// which the scheduler gives the Slot up. The wait itself consumed the window.
// Every ten-second strategy lost every round in which it missed its live
// deadline, showing up only as a gap, with no query issued and no failure
// anywhere -- the system asking for a wait and then abandoning the Slot for
// having waited.
//
// Run against the real bundle on the deployment's own readiness settings
// rather than the fixture's convenience ones, because the two settings and the
// period are exactly what the defect was made of.
func TestATenSecondSlotReachedAfterItsLiveDeadlineStillCompletes(t *testing.T) {
	fixture := startShortPeriodFixture(t)
	ctx := context.Background()

	// T+26: past the query deadline of T+25, three grid points behind.
	fixture.clock.Store((fixture.base + 26) * 1000)
	result, attempted, err := fixture.runner.RunOne(ctx)
	if err != nil || !attempted {
		t.Fatalf("RunOne at T+26 = (%+v, %t, %v), want an attempted Slot", result, attempted, err)
	}
	if !result.Completed {
		t.Fatalf("RunOne at T+26 did not complete: %+v. A ten-second Slot that misses its live deadline "+
			"is told to wait until an instant at which it is already too far behind to run, so it is "+
			"skipped -- on every round, at any scheduling speed", result)
	}
	if result.CompletionKind == execution.CompletionGapSkipped {
		t.Fatalf("RunOne at T+26 finished %s, want the Slot executed rather than skipped", result.CompletionKind)
	}
	if fixture.uqCalls.Load() == 0 {
		t.Fatal("no query was issued. The Slot completed without reading anything, which is the outcome " +
			"this test exists to tell apart from a real completion")
	}

	// The cohort gauge the deployment is accepted on: this round is a
	// completion in the ten-second cohort and not a skipped grid point.
	var completions []observability.ShortPeriodCompletionFacts
	for _, observation := range fixture.observed() {
		if observation.ShortPeriodCompletion != nil {
			completions = append(completions, *observation.ShortPeriodCompletion)
		}
	}
	if len(completions) != 1 || completions[0].Cohort != "10s" {
		t.Fatalf("short period completions = %+v, want one in the 10s cohort", completions)
	}
	if completions[0].CompletionKind == string(execution.CompletionGapSkipped) {
		t.Fatalf("the 10s cohort reports %s, want a completion. This is the counter the fix is accepted "+
			"on: every missed live deadline used to add one here", completions[0].CompletionKind)
	}

	// And nothing refused the replay for a reason of its own.
	for _, observation := range fixture.observed() {
		if observation.ReplayExpiry != nil {
			t.Fatalf("the replay was given up on: %+v", *observation.ReplayExpiry)
		}
	}
}

type shortPeriodFixture struct {
	t              *testing.T
	base           int64
	clock          *atomic.Int64
	now            func() time.Time
	uqCalls        *atomic.Int64
	runner         phaseTwoQueryGroupRuntime
	queryGroup     execution.QueryGroupIdentity
	observationsMu sync.Mutex
	observations   []observability.Observation
}

func (fixture *shortPeriodFixture) observed() []observability.Observation {
	fixture.observationsMu.Lock()
	defer fixture.observationsMu.Unlock()
	return append([]observability.Observation(nil), fixture.observations...)
}

// startShortPeriodFixture opens the production bundle on one ten-second
// strategy, with the deployment's own settling wait and downstream reserve,
// and leaves the clock at the Segment's first grid point.
func startShortPeriodFixture(t *testing.T) *shortPeriodFixture {
	t.Helper()
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	installShortPeriodStrategy(t, ctx, redisClient)

	// Aligned to the ten-second grid this strategy runs on, which also keeps
	// it close to the wall clock. That matters: the query deadline the fake
	// clock derives is handed to the HTTP client as an absolute instant, and
	// that client compares it against the real one. Rounded to a wider
	// boundary the base sits up to a minute in the real past and the query
	// times out before it is sent -- on some runs and not others, which is
	// worse than always. Here the base is at most nine seconds behind and the
	// deadline it derives is T+25, so the margin is never negative.
	base := time.Now().Unix()
	base -= base % 10
	fixture := &shortPeriodFixture{t: t, base: base, clock: &atomic.Int64{}, uqCalls: &atomic.Int64{}}
	fixture.clock.Store(base * 1000)
	fixture.now = func() time.Time { return time.UnixMilli(fixture.clock.Load()) }

	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.uqCalls.Add(1)
		var payload struct {
			EndTime string `json:"end_time"`
		}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		end, err := strconv.ParseInt(payload.EndTime, 10, 64)
		if err != nil || end <= 0 {
			end = fixture.clock.Load() / 1000
		}
		if end > 1_000_000_000_000 {
			end /= 1000
		}
		series := `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],` +
			`"group_keys":["host"],"group_values":["127.0.0.1"],"values":[[` +
			strconv.FormatInt((end-1)*1000, 10) + `,5]]}`
		_, _ = writer.Write([]byte(`{"series":[` + series + `],"status":null,"trace_id":"short-period",` +
			`"is_partial":false,"result_table_id":["system.cpu"]}`))
	}))
	t.Cleanup(uqServer.Close)

	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd-short-period-replay"
	withCompatibilityOutput(&cfg, address)
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
	// The deployment's own numbers. The defect lives in the relationship
	// between these two and the period, so a fixture that shortens them for
	// convenience cannot see it.
	defaults := config.Default().PhaseTwo.Access
	cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(defaults.MinReadyDelay.Duration())
	cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(defaults.DownstreamExecutionReserve.Duration())
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(6 * time.Hour)
	cfg.PhaseTwo.Worker.RegistrationRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(6 * time.Hour)
	cfg.PhaseTwo.Ownership.ControlLeaderRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(6 * time.Hour)
	cfg.PhaseTwo.Ownership.LeaseRenewInterval = config.Duration(time.Minute)

	additionalObserver := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		fixture.observationsMu.Lock()
		defer fixture.observationsMu.Unlock()
		fixture.observations = append(fixture.observations, observation)
	})
	events := &recordingPhaseTwoEventSink{}
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: fixture.now, HTTPClient: uqServer.Client(), AdditionalObserver: additionalObserver,
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) { return events, nil },
		},
	)
	if err != nil {
		t.Fatalf("openProductionPhaseTwoBundleWithDependencies() error = %v", err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("phase-two production Start() error = %v", err)
	}
	t.Cleanup(func() {
		if shutdownErr := bundle.Shutdown(context.Background()); shutdownErr != nil {
			t.Errorf("phase-two production Shutdown() error = %v", shutdownErr)
		}
	})
	if len(bundle.queryGroups) != 1 || len(bundle.runners) != 1 {
		t.Fatalf("Query Groups/runners = %v/%d, want one", bundle.queryGroups, len(bundle.runners))
	}
	fixture.queryGroup = bundle.queryGroups[0]
	fixture.runner = settledRunner(bundle, fixture.queryGroup)

	production := bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	schedule, err := production.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, fixture.queryGroup)
	if err != nil || len(schedule.Plans) != 1 {
		t.Fatalf("initial schedule=%+v error=%v", schedule, err)
	}
	// The Slot this test drives, and the two numbers the defect is made of.
	if schedule.Plans[0].Spec.EvaluationIntervalSeconds != 10 {
		t.Fatalf("interval = %ds, want the ten-second cohort", schedule.Plans[0].Spec.EvaluationIntervalSeconds)
	}
	if schedule.Plans[0].Spec.CompletionOffsetSeconds() != 30 {
		t.Fatalf("completion offset = %ds, want 30; the query deadline this test reaches past is derived "+
			"from it", schedule.Plans[0].Spec.CompletionOffsetSeconds())
	}
	if schedule.Segment.Start != execution.EvaluationTime(base) {
		t.Fatalf("Segment start = %d, want the aligned base %d", schedule.Segment.Start, base)
	}
	return fixture
}

func installShortPeriodStrategy(t *testing.T, ctx context.Context, redisClient *redis.Client) {
	t.Helper()
	raw, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	var strategyDocument map[string]any
	if err := json.Unmarshal(raw, &strategyDocument); err != nil {
		t.Fatal(err)
	}
	strategyDocument["update_time"] = 1725000000
	item := strategyDocument["items"].([]any)[0].(map[string]any)
	item["query_configs"].([]any)[0].(map[string]any)["agg_interval"] = 10
	for _, detect := range strategyDocument["detects"].([]any) {
		detect.(map[string]any)["trigger_config"].(map[string]any)["check_window"] = 1
	}
	encoded, err := json.Marshal(strategyDocument)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]any{
		"alarm-config.strategy_ids":  `[1001]`,
		"alarm-config.strategy_1001": encoded,
	} {
		if err := redisClient.Set(ctx, key, value, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
}
