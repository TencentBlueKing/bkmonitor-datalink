// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/go-redis/redis/v8"
)

// TestALongLivedSegmentKeepsDetectingAcrossASnapshotChangeAndATakeover runs
// one strategy's Segment for five rounds on the wiring startup uses: no roles
// configured and no role runtime passed, so the opener attaches one the way
// startup does. Midway the source publishes a second strategy (a snapshot
// change while the first Segment runs); then the replica stops and a new
// process takes the Control Leader and the Query Groups over, as a rolling
// update does. Every round is detected exactly once on whichever replica owns
// it, the new strategy is detected from its first whole window after publication, and
// the new Leader's fleet view never loses the Control Leader's facts.
func TestALongLivedSegmentKeepsDetectingAcrossASnapshotChangeAndATakeover(t *testing.T) {
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	strategyDocument, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	const period = int64(300)
	base := phaseTwoQueryableSlotBoundary()
	var clock atomic.Int64
	clock.Store(base)
	now := func() time.Time { return time.Unix(clock.Load(), 0) }
	if err := redisClient.Set(ctx, "alarm-config.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_1001", strategyDocument, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.last_updated", strconv.FormatInt(base-period, 10), 0).Err(); err != nil {
		t.Fatal(err)
	}
	var uqCalls atomic.Int64
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		uqCalls.Add(1)
		var payload struct {
			EndTime string `json:"end_time"`
		}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		end, parseErr := strconv.ParseInt(payload.EndTime, 10, 64)
		if parseErr != nil || end <= 0 {
			end = clock.Load()
		}
		_, _ = writer.Write([]byte(`{"series":[{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],` +
			`"group_keys":["host"],"group_values":["127.0.0.1"],"values":[[` + strconv.FormatInt((end-1)*1000, 10) + `,95]]}],` +
			`"status":null,"trace_id":"long-lived","is_partial":false,"result_table_id":["system.cpu"]}`))
	}))
	defer uqServer.Close()

	events := &recordingPhaseTwoEventSink{}
	var completionsMu sync.Mutex
	var completions []observability.Observation
	completionObserver := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		if observation.Stage != observability.StageSlotCompleted {
			return
		}
		completionsMu.Lock()
		defer completionsMu.Unlock()
		completions = append(completions, observation)
	})
	configFor := func(replica string) config.Config {
		cfg := validGoAccessRuntimeConfig()
		cfg.Redis.Address = address
		withCompatibilityOutput(&cfg, address)
		cfg.Redis.StatePrefix = "alarmd:phase-two:long-lived-segment"
		cfg.HTTP.Listen = reserveBundleAddress()
		cfg.PhaseTwo.Worker.ID = replica
		cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
		cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
		cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(time.Millisecond)
		cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(time.Millisecond)
		// The injected clock jumps a period per round within a second of real
		// time, faster than any renewal ticks: registrations and leases are
		// given hours so a jump does not read as an expiry. So the takeover
		// below is a graceful one - the first replica's Shutdown releases and
		// the next takes over at once - and this test says nothing about a
		// takeover by expiry after a crash.
		cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(6 * time.Hour)
		cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(6 * time.Hour)
		cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(6 * time.Hour)
		return cfg
	}
	open := func(replica string) *phaseTwoWorkerBundle {
		cfg := configFor(replica)
		// No RoleRuntime: the opener attaches one as startup does.
		bundle, openErr := openProductionPhaseTwoBundleWithDependencies(
			ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
			newPhaseTwoApplicationHealth(),
			func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
				return controlplane.NewLegacyRedisStrategySource(client, prefix)
			},
			phaseTwoProductionExternalDependencies{
				Now: now, HTTPClient: uqServer.Client(), AdditionalObserver: completionObserver,
				PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
					return events, nil
				}),
			},
		)
		if openErr != nil {
			t.Fatalf("open production bundle %s: %v", replica, openErr)
		}
		if err := bundle.Start(ctx); err != nil {
			t.Fatalf("%s Start() error = %v", replica, err)
		}
		return bundle
	}
	// detected is the evaluation times, in seconds, of every ABNORMAL event
	// for one strategy, in order, duplicates kept.
	detected := func(strategy string) []int64 {
		times := []int64{}
		for _, event := range events.snapshot() {
			if event.EventKind == "ABNORMAL" && event.PlanRef.StrategyID == strategy {
				at := event.EvaluationTime
				if at > 1e11 {
					at /= 1000
				}
				times = append(times, at)
			}
		}
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		return times
	}
	round := func(bundle *phaseTwoWorkerBundle, n int64) {
		t.Helper()
		clock.Store(base + n*period + 1)
		if err := runScheduledOnceSettled(ctx, bundle); err != nil {
			t.Fatalf("round %d on %s: %v", n, bundle.dependencies.Config.PhaseTwo.Worker.ID, err)
		}
	}

	first := open("long-lived-replica-a")
	firstClosed := false
	t.Cleanup(func() {
		if !firstClosed {
			_ = first.Shutdown(context.Background())
		}
	})
	if stats := first.dependencies.ViewStreamStats(); !stats.Leading || stats.ControlEpoch == 0 {
		t.Fatalf("first replica stream = %+v, want it leading", stats)
	}
	firstEpoch := first.dependencies.ViewStreamStats().ControlEpoch
	round(first, 0)
	round(first, 1)

	// A snapshot change while the Segment runs: the source lists a second
	// strategy and moves its change marker; two control rounds publish it.
	// Its own aggregation, so it compiles to its own Query Group rather than
	// joining the first strategy's.
	second := bytes.Replace(strategyDocument, []byte(`"id": 1001`), []byte(`"id": 1002`), 1)
	second = bytes.Replace(second, []byte(`"agg_method": "avg"`), []byte(`"agg_method": "max"`), 1)
	if bytes.Count(second, []byte(`"id": 1002`)) != 1 || bytes.Count(second, []byte(`"agg_method": "max"`)) != 1 {
		t.Fatal("fixture: the second strategy's id or aggregation was not rewritten")
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_1002", second, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_ids", `[1001,1002]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.last_updated", strconv.FormatInt(base+period+2, 10), 0).Err(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := first.refreshAndReconcile(ctx, true); err != nil {
			t.Fatalf("publish the second strategy: %v", err)
		}
	}
	waitPhaseTwoCondition(t, 10*time.Second, "the first replica opens the new strategy's Query Group", func() bool {
		_ = first.refreshAndReconcile(ctx, true)
		return len(first.runners) == 2
	})
	round(first, 2)
	first.dependencies.PublishFleet(ctx)

	// The takeover a rolling update makes: the replica stops and releases its
	// leases, and a new process takes the Control Leader and the Query Groups.
	if err := first.Shutdown(ctx); err != nil {
		t.Fatalf("first Shutdown() error = %v", err)
	}
	firstClosed = true
	next := open("long-lived-replica-b")
	t.Cleanup(func() { _ = next.Shutdown(context.Background()) })
	waitPhaseTwoCondition(t, 10*time.Second, "the new replica leads and owns both Query Groups", func() bool {
		if err := next.refreshAndReconcile(ctx, true); err != nil {
			return false
		}
		stats := next.dependencies.ViewStreamStats()
		return stats.Leading && stats.ControlEpoch > firstEpoch && len(next.runners) == 2
	})
	if health := readControlFactsHealth(t, next); health.hasGap("CONTROL_FACTS_UNAVAILABLE") {
		t.Fatalf("new Leader's view before its first control record = %+v", health)
	}
	round(next, 3)
	round(next, 4)
	next.dependencies.PublishFleet(ctx)
	health := readControlFactsHealth(t, next)
	if health.hasGap("CONTROL_FACTS_UNAVAILABLE") || health.ControlFacts == nil || health.ControlFacts.Source != "record" ||
		health.ControlFacts.Replica != "long-lived-replica-b" {
		t.Fatalf("new Leader's view after its first control record = %+v (gaps %+v)", health.ControlFacts, health.Gaps)
	}

	want := []int64{}
	for n := int64(0); n <= 4; n++ {
		want = append(want, base+n*period)
	}
	if got := detected("1001"); !equalTimes(got, want) {
		t.Fatalf("strategy 1001 detections = %v, want each of the five rounds exactly once %v", got, want)
	}
	// Published one second into round 2's window, the second strategy's
	// Segment is queried from round 2, but that window opened before the
	// Segment started, so the Slot completes with a partial gap under
	// CONFIG_DRIFT rather than judging a window the strategy did not cover.
	// Named here so a change that drops the round silently fails this test.
	// It is detected from round 3, the first whole window after the
	// publication, and every round after it exactly once, on either side of
	// the takeover.
	roundTwo := base + 2*period
	var drift []observability.Observation
	completionsMu.Lock()
	for _, completion := range completions {
		if completion.Trace.EvaluationTime == roundTwo && completion.Trace.ScheduleSegmentStart > roundTwo-period {
			drift = append(drift, completion)
		}
	}
	completionsMu.Unlock()
	if len(drift) != 1 || drift[0].Result != observability.ResultDegraded || drift[0].ReasonCode != "CONFIG_DRIFT" ||
		drift[0].SlotCompletionKind != "COMPLETED_WITH_PARTIAL_GAP" {
		t.Fatalf("second strategy's round-2 completions = %+v, want one degraded CONFIG_DRIFT completion with a partial gap", drift)
	}
	if got := detected("1002"); !equalTimes(got, want[3:]) {
		t.Fatalf("strategy 1002 detections = %v, want each round from its first whole window exactly once %v", got, want[3:])
	}
	if calls := uqCalls.Load(); calls != 8 {
		t.Fatalf("UQ calls = %d, want one per Query Group per round its Segment holds (5 + 3)", calls)
	}
}

func equalTimes(got, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
