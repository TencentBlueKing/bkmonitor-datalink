// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// pinnedWorkers is the placement a two-worker case injects: each Query Group
// it pins goes to the worker it names, every other one to fallback (any
// worker while fallback is empty). It is the eligibility the Leader asks
// where it places, keeps an incumbent and picks a rebalance's destination,
// so nothing the Leader does moves a pinned group.
type pinnedWorkers struct {
	mu       sync.Mutex
	pins     map[execution.QueryGroupIdentity]string
	fallback string
}

func (placement *pinnedWorkers) Eligible(queryGroup execution.QueryGroupIdentity, worker ownership.WorkerRegistration, _ time.Time) bool {
	placement.mu.Lock()
	defer placement.mu.Unlock()
	if pinned, ok := placement.pins[queryGroup]; ok {
		return pinned == worker.WorkerID
	}
	return placement.fallback == "" || placement.fallback == worker.WorkerID
}

func (placement *pinnedWorkers) pin(worker string, queryGroups ...execution.QueryGroupIdentity) {
	placement.mu.Lock()
	defer placement.mu.Unlock()
	for _, queryGroup := range queryGroups {
		placement.pins[queryGroup] = worker
	}
}

func (placement *pinnedWorkers) fallBackTo(worker string) {
	placement.mu.Lock()
	defer placement.mu.Unlock()
	placement.fallback = worker
}

// workerOfTwo is one of the two live bundles: its worker identity, and what
// it wrote and recorded, apart from the other's.
type workerOfTwo struct {
	id       string
	bundle   *phaseTwoWorkerBundle
	recorder *metric.Recorder
	events   *recordingPhaseTwoEventSink
	observed func() []observability.Observation
}

// twoWorkerFixture is the cutover fixture with a second bundle beside it on
// the same Redis and the same clock: worker-a is the cutover fixture's own
// bundle (and the Control Leader, opened first), worker-b opens once a has
// placed the initial Query Groups. Both stay live to the end, and placement
// decides which holds what.
type twoWorkerFixture struct {
	*cutoverStallFixture
	placement *pinnedWorkers
	a, b      *workerOfTwo
	// value is what every series of every answer carries now: a case sets
	// it before the Slots it runs, so each Slot's points are its choice.
	value *atomic.Int64
}

// valuedUQ is a query service whose answers carry one series, host
// 127.0.0.1, at the value the case set last, at the window's last second -
// the cutover fixture's answer with the value the case's to choose.
func valuedUQ(t *testing.T, value *atomic.Int64) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		var payload struct {
			EndTime string `json:"end_time"`
		}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		end, err := strconv.ParseInt(payload.EndTime, 10, 64)
		if err != nil || end <= 0 {
			end = time.Now().Unix()
		}
		if end > 1_000_000_000_000 {
			end /= 1000
		}
		_, _ = writer.Write([]byte(`{"series":[{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["host"],` +
			`"group_values":["127.0.0.1"],"values":[[` + strconv.FormatInt((end-1)*1000, 10) + `,` + strconv.FormatInt(value.Load(), 10) + `]]}],` +
			`"status":null,"trace_id":"two-workers","is_partial":false,"result_table_id":["system.cpu"]}`))
	}))
	t.Cleanup(server.Close)
	return server
}

// startTwoWorkerFixture opens worker-a on the shared Query Group of
// strategies (installed by before, sharing strategy 1001's query, plans of
// them), pins the initial Query Groups to it, sends every later one to
// worker-b, and opens worker-b.
func startTwoWorkerFixture(t *testing.T, before func(ctx context.Context, client *redis.Client, cfg config.Config), plans int) *twoWorkerFixture {
	t.Helper()
	placement := &pinnedWorkers{pins: map[execution.QueryGroupIdentity]string{}}
	value := &atomic.Int64{}
	value.Store(aboveThreshold)
	uq := valuedUQ(t, value)
	f := openCutoverFixtureSharing(t, func(cfg *config.Config) {
		cfg.PhaseTwo.Worker.ID = "worker-a"
		cfg.PhaseTwo.Access.UQEndpoint = uq.URL
	},
		observability.Discard(observability.ComponentRuntime), before, nil,
		func(external *phaseTwoProductionExternalDependencies) { external.Placement = placement }, plans)
	fixture := &twoWorkerFixture{cutoverStallFixture: f, placement: placement, value: value,
		a: &workerOfTwo{id: "worker-a", bundle: f.bundle, recorder: f.recorder, events: f.events, observed: f.observed}}
	fixture.a.bundle.mu.RLock()
	initial := append([]execution.QueryGroupIdentity(nil), fixture.a.bundle.queryGroups...)
	fixture.a.bundle.mu.RUnlock()
	placement.pin("worker-a", initial...)
	placement.fallBackTo("worker-b")
	fixture.b = fixture.openWorker("worker-b")
	return fixture
}

// openWorker opens and starts another bundle of the deployment under
// workerID, on the fixture's clock and placement, with its own address,
// recorder and event sink.
func (fixture *twoWorkerFixture) openWorker(workerID string) *workerOfTwo {
	t := fixture.t
	t.Helper()
	ctx := context.Background()
	cfg := fixture.cfg
	cfg.PhaseTwo.Worker.ID = workerID
	cfg.HTTP.Listen = reserveBundleAddress()
	worker := &workerOfTwo{id: workerID, recorder: metric.NewRecorder(metric.BuildInfo{}), events: &recordingPhaseTwoEventSink{}}
	var mu sync.Mutex
	var observations []observability.Observation
	worker.observed = func() []observability.Observation {
		mu.Lock()
		defer mu.Unlock()
		return append([]observability.Observation(nil), observations...)
	}
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, worker.recorder, observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: fixture.now, HTTPClient: http.DefaultClient, Placement: fixture.placement,
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return worker.events, nil
			}),
			AdditionalObserver: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				mu.Lock()
				defer mu.Unlock()
				observations = append(observations, observation)
			}),
		},
	)
	if err != nil {
		t.Fatalf("open the bundle of %s: %v", workerID, err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("start the bundle of %s: %v", workerID, err)
	}
	t.Cleanup(func() {
		if err := bundle.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown of %s: %v", workerID, err)
		}
		if stream, served := controlStreamServers.LoadAndDelete(bundle); served {
			stream.(*testControlStream).stop()
		}
	})
	worker.bundle = bundle
	return worker
}

// holds is the Query Groups the worker holds now.
func (worker *workerOfTwo) holds() map[execution.QueryGroupIdentity]bool {
	worker.bundle.mu.RLock()
	defer worker.bundle.mu.RUnlock()
	// The Query Groups the worker runs, as ownedQueryGroups reads them; the
	// bundle's queryGroups is every Query Group it knows of, held or not.
	held := make(map[execution.QueryGroupIdentity]bool, len(worker.bundle.runners))
	for queryGroup := range worker.bundle.runners {
		held[queryGroup] = true
	}
	return held
}

// reconcile runs the Leader's round and each worker's until settled says the
// workers hold what the case wants, or the test's deadline passes.
func (fixture *twoWorkerFixture) reconcile(what string, settled func() bool) {
	fixture.t.Helper()
	ctx := context.Background()
	for deadline := testwait.Deadline(fixture.t); !settled(); time.Sleep(20 * time.Millisecond) { // fixed-wait: the poll interval of a condition wait bounded by the test deadline
		if time.Now().After(deadline) {
			fixture.t.Fatalf("%s: worker-a holds %v, worker-b %v at the test's deadline", what, fixture.a.holds(), fixture.b.holds())
		}
		for _, worker := range []*workerOfTwo{fixture.a, fixture.b} {
			if err := worker.bundle.refreshAndReconcile(ctx, true); err != nil {
				fixture.t.Fatalf("reconcile the bundle of %s: %v", worker.id, err)
			}
		}
	}
}

// edit stores the strategies and publishes them, cut over at the clock's
// next second: the Leader's round, then each worker's until settled.
func (fixture *twoWorkerFixture) edit(strategies map[int64][]byte, what string, settled func() bool) execution.EvaluationTime {
	fixture.t.Helper()
	ctx := context.Background()
	boundary := execution.EvaluationTime(fixture.now().Unix() + 1)
	installSharedGroup(fixture.t, ctx, fixture.redisClient, strategies)
	fixture.cutOverAt(ctx, boundary)
	fixture.reconcile(what, settled)
	return boundary
}

// nextGridAfter is the first Slot on the 60 s grid after the boundary.
func nextGridAfter(boundary execution.EvaluationTime) execution.EvaluationTime {
	return execution.EvaluationTime((int64(boundary)/60 + 1) * 60)
}

// slotEndings is how each of a Query Group's Slots ended on the worker, by
// evaluation time.
func (worker *workerOfTwo) slotEndings(queryGroup execution.QueryGroupIdentity) map[execution.EvaluationTime]string {
	endings := map[execution.EvaluationTime]string{}
	for _, observation := range worker.observed() {
		if observation.Trace.QueryGroupKey == string(queryGroup) && observation.Stage == "slot_completed" && observation.Result != "retrying" {
			endings[execution.EvaluationTime(observation.Trace.EvaluationTime)] = observation.SlotCompletionKind + "/" + string(observation.ReasonCode)
		}
	}
	return endings
}

// runUntilEnded runs the worker's rounds of the Query Group, each at its
// next ready instant on the shared clock, until slot has ended, and returns
// how it ended.
func (fixture *twoWorkerFixture) runUntilEnded(worker *workerOfTwo, queryGroup execution.QueryGroupIdentity, slot execution.EvaluationTime) string {
	fixture.t.Helper()
	ctx := context.Background()
	runner := settledRunner(worker.bundle, queryGroup)
	if runner == nil {
		fixture.t.Fatalf("%s holds no runner of %s; it holds %v", worker.id, queryGroup, worker.holds())
	}
	for attempt := 0; attempt < 40; attempt++ {
		if ended, ok := worker.slotEndings(queryGroup)[slot]; ok && !strings.HasPrefix(ended, "/") {
			return ended
		}
		if nextAt := runner.NextReadyAt(); nextAt.After(fixture.now()) {
			fixture.clock.Store(nextAt.UnixMilli() + 1)
		}
		if _, attempted, err := runner.RunOne(ctx); err != nil {
			fixture.t.Fatalf("%s RunOne for Slot %d: %v", worker.id, slot, err)
		} else if !attempted {
			fixture.clock.Store(max(fixture.now().UnixMilli()+1000, firstRoundAtMillis(fixture.cfg, queryGroup, int64(slot), 60)))
		}
	}
	fixture.t.Fatalf("%s: Slot %d did not end; endings %v", worker.id, slot, worker.slotEndings(queryGroup))
	return ""
}

// eventsFor is the events a strategy wrote on the worker for a Slot.
func (worker *workerOfTwo) eventsFor(slot execution.EvaluationTime, strategy string) []contract.TriggerEventV1 {
	var events []contract.TriggerEventV1
	for _, event := range worker.events.snapshot() {
		if event.EvaluationTime == int64(slot) && event.PlanRef.StrategyID == strategy {
			events = append(events, event)
		}
	}
	return events
}

// cacheReading is one State cache cell the worker's /metrics serves.
func (worker *workerOfTwo) cacheReading(t *testing.T, family, label, value string) float64 {
	t.Helper()
	got, served := servedSample(t, worker.recorder, family, map[string]string{label: value})
	if !served {
		t.Fatalf("%s's /metrics serves no %s{%s=%q}", worker.id, family, label, value)
	}
	return got
}

// The values the case's answers carry, against sharedGroupStrategy's
// threshold of 1 (gte).
const (
	aboveThreshold = 5
	belowThreshold = 0
)

// twoOfTwo makes every detect fire on two abnormal points in two checks,
// the same on every version of the strategy, so no edit changes its state
// generation.
func twoOfTwo(strategy map[string]any) {
	for _, detect := range strategy["detects"].([]any) {
		detect.(map[string]any)["trigger_config"].(map[string]any)["count"] = 2
	}
}

// aggregatedByMax is the query edit that moves strategy 1001's Plan out of
// the shared Query Group: its query facts change, its state generation does
// not.
func aggregatedByMax(strategy map[string]any) {
	twoOfTwo(strategy)
	strategy["items"].([]any)[0].(map[string]any)["query_configs"].([]any)[0].(map[string]any)["agg_method"] = "max"
}

// A strategy moved by a query edit from worker-a's Query Group to a new one
// on worker-b, and moved back, is not served on its first Slot back what
// worker-a cached before it left. worker-a held its group throughout - the
// strategy beside it stayed - so the owner fence the cache keys on never
// moved; only the Plan's activation in the group did (lookup rule 6).
//
// The two histories decide the first evaluated Slot back differently, and
// the events show which one it read. The strategy fires on two points over
// its threshold in two checks. Its last Slot on worker-a was over it; its
// Slots on worker-b were under it; the Slot back is over it. On worker-b's
// history - the store's - the Slot back has one of two and does not fire; on
// the history worker-a cached before it left it has two of two and fires.
// The events are written before the State they come from is applied (the
// Plan's order in the coordinator: admit, events, apply), so a Slot that read
// the stale history has sent its event by the time the State's compare-and-
// set refuses it: the conflict and the retry from the store come after, and
// cannot take the event back.
func TestAStrategyBackOnItsWorkerIsNotServedWhatItCachedBeforeAnotherWorkerHeldIt(t *testing.T) {
	previousWindow := cutoverStallTriggerWindow
	cutoverStallTriggerWindow = 2
	t.Cleanup(func() { cutoverStallTriggerWindow = previousWindow })
	unedited := func() map[int64][]byte {
		return map[int64][]byte{1001: sharedGroupStrategy(t, 1001, 1725000000, twoOfTwo), 1003: sharedGroupStrategy(t, 1003, 1725000000, twoOfTwo)}
	}
	fixture := startTwoWorkerFixture(t, func(ctx context.Context, client *redis.Client, _ config.Config) {
		installSharedGroup(t, ctx, client, unedited())
	}, 2)
	old := fixture.queryGroup
	a, b := fixture.a, fixture.b
	// The last Slot on worker-a before the strategy leaves, over the
	// threshold: what worker-a's cache holds of it.
	left := execution.EvaluationTime(fixture.base + 60)
	fixture.value.Store(aboveThreshold)
	if ended := fixture.runUntilEnded(a, old, left); ended != fullEnding {
		t.Fatalf("worker-a's Slot %d ended %q, want FULL_COMPLETED", left, ended)
	}

	var moved execution.QueryGroupIdentity
	away := fixture.edit(map[int64][]byte{1001: sharedGroupStrategy(t, 1001, 1725000600, aggregatedByMax), 1003: unedited()[1003]},
		"the query edit", func() bool {
			for queryGroup := range b.holds() {
				if settledRunner(b.bundle, queryGroup) != nil {
					moved = queryGroup
				}
			}
			return moved != "" && a.holds()[old]
		})
	// Three Slots on worker-b, under the threshold: the store's history of
	// the strategy is worker-b's, with no point over it in the last two.
	fixture.value.Store(belowThreshold)
	onB := nextGridAfter(away)
	for slot := onB; slot <= onB+120; slot += 60 {
		fixture.runUntilEnded(b, moved, slot)
		if events := b.eventsFor(slot, "1001"); len(events) != 0 {
			t.Fatalf("worker-b wrote %d events of strategy 1001 for Slot %d under the threshold", len(events), slot)
		}
	}
	// worker-a's group goes on with the strategy beside it, to the same
	// point, so the reading below is the strategy coming back alone.
	fixture.runUntilEnded(a, old, onB+120)

	back := fixture.edit(unedited(), "the move back", func() bool {
		return a.holds()[old] && settledRunner(a.bundle, old) != nil
	})
	if b.holds()[old] || a.holds()[moved] {
		t.Fatalf("placement did not hold: worker-a holds %v, worker-b %v", a.holds(), b.holds())
	}
	cache := func(family, label, value string) float64 { return a.cacheReading(t, family, label, value) }
	hits, activations := cache("alarmd_state_cache_lookups_total", "result", "hit"), cache("alarmd_state_cache_lookups_total", "result", "miss_activation")
	conflicts := cache("alarmd_state_cache_departures_total", "reason", "conflict")
	declined := cache("alarmd_state_cache_restore_declined_total", "reason", "newer_version")
	// Back, over the threshold again.
	fixture.value.Store(aboveThreshold)
	first := nextGridAfter(back)
	ended := fixture.runUntilEnded(a, old, first)
	// The first Slot back reads the store for every key of the group: no
	// lookup is served from the cache, the strategy's by its activation.
	if got := cache("alarmd_state_cache_lookups_total", "result", "hit"); got != hits {
		t.Fatalf("the first Slot back (%s) was served %v lookups from the cache, want none", ended, got-hits)
	}
	if got := cache("alarmd_state_cache_lookups_total", "result", "miss_activation"); got <= activations {
		t.Fatalf("state_cache_lookups_total{miss_activation} rose by %v, want the strategy back read from the store", got-activations)
	}
	// The first Slot back that evaluates the strategy: on the store's history
	// it has one point of two over the threshold and writes no event.
	evaluated := first
	for ended != fullEnding && evaluated < first+180 {
		evaluated += 60
		ended = fixture.runUntilEnded(a, old, evaluated)
	}
	if ended != fullEnding {
		t.Fatalf("no Slot back evaluated by %d; endings %v", evaluated, a.slotEndings(old))
	}
	if events := a.eventsFor(evaluated, "1001"); len(events) != 0 {
		t.Fatalf("strategy 1001 wrote %d events for Slot %d, its first evaluated back: it fired on the history worker-a cached before it left, "+
			"not the one worker-b wrote", len(events), evaluated)
	}
	// worker-b's bytes are what worker-a wrote over: no write went in
	// conflict with them, none was declined as older, none restored.
	if got := cache("alarmd_state_cache_departures_total", "reason", "conflict"); got != conflicts {
		t.Fatalf("state_cache_departures_total{conflict} rose by %v, want worker-a's writes made on worker-b's bytes", got-conflicts)
	}
	if got := cache("alarmd_state_cache_restore_declined_total", "reason", "newer_version"); got != declined {
		t.Fatalf("state_cache_restore_declined_total{newer_version} rose by %v, want nothing older written back", got-declined)
	}
	if got := cache("alarmd_state_cache_restored_total", "cause", "missing"); got != 0 {
		t.Fatalf("state_cache_restored_total{missing} = %v, want nothing written back", got)
	}
	if b.holds()[old] || a.holds()[moved] {
		t.Fatalf("placement did not hold: worker-a holds %v, worker-b %v", a.holds(), b.holds())
	}
}
