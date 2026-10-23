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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// The settling wait and downstream reserve a production deployment runs
// with: a Query Group read every minute has 60 s less the reserve from its
// Slot, of which the first 30 s is the settling wait, so a normal round's
// query has 25 s, and a retry or replay 55 s from its arrival.
const (
	poolBudgetSettle  = 30 * time.Second
	poolBudgetReserve = 5 * time.Second
	// poolSlowerThanANormalRound is a backend that answers past every
	// normal round's budget - the minute less the reserve and a read wait of
	// at least the 10 s floor, at most 45 s - and inside a replay's 55 s.
	poolSlowerThanANormalRound = 50 * time.Second
	// poolPassSlack is how far past a Slot's readiness a round's pass runs:
	// at the instant itself the Slot is not yet due.
	poolPassSlack = 500 * time.Millisecond
)

// poolBackend is a query service that answers after a set latency on the
// test's clock: within the request's deadline it moves the clock by the
// latency and answers; past it, the clock moves to the deadline and the
// request times out, as a backend that did not answer in time does. Every
// request is kept with the time it had left.
type poolBackend struct {
	clock   atomic.Int64 // unix milliseconds
	latency atomic.Int64 // milliseconds
	// cut, when set, is the backend's own limit: a query that would take
	// that long or longer is answered unavailable when it is reached, as a
	// query service answers when its storage client gives up.
	cut      atomic.Int64 // milliseconds
	mu       sync.Mutex
	requests []poolRequest
}

type poolRequest struct {
	remaining time.Duration
	answered  bool
}

func (backend *poolBackend) now() time.Time { return time.UnixMilli(backend.clock.Load()) }

func (backend *poolBackend) take() []poolRequest {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	taken := backend.requests
	backend.requests = nil
	return taken
}

func (backend *poolBackend) roundTrip(request *http.Request) (*http.Response, error) {
	deadline, ok := request.Context().Deadline()
	if !ok {
		return nil, fmt.Errorf("a Slot query without a deadline")
	}
	payload, err := decodeControlledG4UQRequest(request)
	if err != nil {
		return nil, err
	}
	now := backend.clock.Load()
	remaining := deadline.UnixMilli() - now
	latency := backend.latency.Load()
	if cut := backend.cut.Load(); cut > 0 && latency >= cut && cut < remaining {
		backend.mu.Lock()
		backend.requests = append(backend.requests, poolRequest{remaining: time.Duration(remaining) * time.Millisecond})
		backend.mu.Unlock()
		backend.clock.Store(now + cut)
		return &http.Response{StatusCode: http.StatusInternalServerError, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(bytes.NewBufferString(`{"error":"context deadline exceeded"}`)), Request: request}, nil
	}
	answered := latency < remaining
	backend.mu.Lock()
	backend.requests = append(backend.requests, poolRequest{remaining: time.Duration(remaining) * time.Millisecond, answered: answered})
	backend.mu.Unlock()
	if !answered {
		backend.clock.Store(now + remaining)
		return nil, fmt.Errorf("simulated backend did not answer: %w", context.DeadlineExceeded)
	}
	backend.clock.Store(now + latency)
	end, err := strconv.ParseInt(payload.EndTime, 10, 64)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(map[string]any{"series": []any{map[string]any{
		"name": "_result0", "columns": []string{"_time", "_value"}, "types": []string{"float", "float"},
		"group_keys": []string{"host"}, "group_values": []string{"host-a"},
		"values": []any{[]any{(end - 60) * 1000, 1}}}}, "status": nil, "trace_id": "pool", "is_partial": false}); err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(&buf), Request: request}, nil
}

// poolProbeHarness is one Query Group read every minute through the
// production bundle on a real Redis, its backend answering in whatever the
// case sets, and every pool transition it went through.
type poolProbeHarness struct {
	t       *testing.T
	ctx     context.Context
	bundle  *phaseTwoWorkerBundle
	client  redis.UniversalClient
	backend *poolBackend
	cfg     config.Config
	// recorder is the bundle's metrics.
	recorder *metric.Recorder
	base     int64
	mu       sync.Mutex
	events   []observability.QueryCooldownFacts
	// completed counts the Slots completed since the last pass took it: a
	// held Slot closed without a query moves the Query Group on as much as
	// one that sent a query.
	completed int
}

func newPoolProbeHarness(t *testing.T, latency time.Duration) *poolProbeHarness {
	t.Helper()
	return newPoolProbeHarnessWith(t, latency, poolBudgetSettle)
}

// newPoolProbeHarnessWith is newPoolProbeHarness with the deployment's ready
// delay settle. The query client times each query on the test's clock, as
// production times it on the same clock its budgets are read from, so a
// query's elapsed time is the latency the backend had and its lateness is
// read against the same instants as its deadline.
func newPoolProbeHarnessWith(t *testing.T, latency, settle time.Duration) *poolProbeHarness {
	t.Helper()
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	document := controlledG4StrategyDocument(t, 7401, "Threshold", "usage", "system.cpu_summary", []string{"host"},
		[]any{[]any{map[string]any{"method": "gte", "threshold": 1_000_000}}})
	if err := client.Set(ctx, "alarm-config.strategy_ids", "[7401]", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "alarm-config.strategy_7401", []byte(document), 0).Err(); err != nil {
		t.Fatal(err)
	}
	// A minute ahead of the wall clock: a query's deadline is a wall-clock
	// instant, and minute 0's must not have passed before its round runs.
	h := &poolProbeHarness{t: t, ctx: ctx, client: client, backend: &poolBackend{}, base: controlledG4Base(t) + 60}
	h.backend.clock.Store(h.base * 1000)
	h.backend.latency.Store(latency.Milliseconds())
	cfg := controlledG4RuntimeConfig(address, "http://controlled-uq", "alarmd-pool-budget")
	cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(settle)
	cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(poolBudgetReserve)
	// The clock jumps minutes within a second of real time, faster than any
	// renewal ticks: registrations and leases are given hours so a cooldown's
	// worth of jumps does not read as the owner having gone.
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(6 * time.Hour)
	cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(6 * time.Hour)
	cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(6 * time.Hour)
	h.cfg = cfg
	h.recorder = metric.NewRecorder(metric.BuildInfo{})
	bundle, err := openProductionPhaseTwoBundleWithDependencies(ctx, cfg, h.recorder,
		observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		}, phaseTwoProductionExternalDependencies{
			Now: h.backend.now, QueryClock: h.backend.now, HTTPClient: &http.Client{Transport: controlledRoundTripper(h.backend.roundTrip)},
			AdditionalObserver: observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
				if o.Stage == observability.StageSlotCompleted {
					h.mu.Lock()
					h.completed++
					h.mu.Unlock()
				}
				if o.QueryCooldown == nil {
					return
				}
				h.mu.Lock()
				defer h.mu.Unlock()
				facts := *o.QueryCooldown
				h.events = append(h.events, facts)
			}),
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			}),
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bundle.Shutdown(context.Background()) })
	h.bundle = bundle
	return h
}

// readWait is how long after its Slot the harness's one Query Group is read:
// the deployment's ready delay, at which its seeded read hold puts the read.
func (h *poolProbeHarness) readWait() time.Duration {
	h.t.Helper()
	if len(h.bundle.queryGroups) != 1 {
		h.t.Fatalf("Query Groups = %v, want one", h.bundle.queryGroups)
	}
	return firstReadWaitOf(h.cfg, h.bundle.queryGroups[0], 60)
}

// normalBudget is what a normal round's query has from its read to its
// deadline: the minute less the reserve and the read wait before its hold,
// since the hold moves the deadline with the read.
func (h *poolProbeHarness) normalBudget() time.Duration {
	return time.Minute - poolBudgetReserve - readWaitOf(h.cfg, h.bundle.queryGroups[0], 60)
}

// round runs the minute's round half a second after its Slot is ready, and
// returns the queries it sent and the pool transitions it made.
func (h *poolProbeHarness) round(minute int64) ([]poolRequest, []observability.QueryCooldownFacts) {
	h.t.Helper()
	h.backend.clock.Store((h.base+minute*60)*1000 + h.readWait().Milliseconds() + 500)
	if err := runScheduledOnceSettled(h.ctx, h.bundle); err != nil {
		h.t.Fatalf("minute %d: %v", minute, err)
	}
	h.mu.Lock()
	events := h.events
	h.events = nil
	h.mu.Unlock()
	return h.backend.take(), events
}

// enterPool runs normal rounds against a backend slower than their budget
// until the third timeout puts the Query Group in the pool, and returns the
// next minute.
func (h *poolProbeHarness) enterPool() int64 {
	h.t.Helper()
	for minute := int64(0); minute <= 2; minute++ {
		requests, events := h.round(minute)
		if len(requests) != 1 || requests[0].answered {
			h.t.Fatalf("minute %d, a backend slower than the budget: requests %+v, want one that timed out", minute, requests)
		}
		if minute == 2 && (len(events) != 1 || events[0].Event != "entered") {
			h.t.Fatalf("the third timeout made %+v, want the Query Group entered", events)
		}
	}
	return 3
}

// pass runs one scheduler pass at the given unix millisecond, and returns
// the queries it sent and the pool transitions it made.
func (h *poolProbeHarness) pass(at int64) ([]poolRequest, []observability.QueryCooldownFacts) {
	requests, events, _ := h.passCounting(at)
	return requests, events
}

// passCounting is pass, and how many Slots it completed.
func (h *poolProbeHarness) passCounting(at int64) ([]poolRequest, []observability.QueryCooldownFacts, int) {
	h.t.Helper()
	h.backend.clock.Store(at)
	if err := runScheduledOnceSettled(h.ctx, h.bundle); err != nil {
		h.t.Fatalf("pass at base+%d ms: %v", at-h.base*1000, err)
	}
	h.mu.Lock()
	events, completed := h.events, h.completed
	h.events, h.completed = nil, 0
	h.mu.Unlock()
	return h.backend.take(), events, completed
}

// runnerReadyAt is when the Query Group's Runner can next make an attempt of
// its own - a retry's backoff - in unix milliseconds; 0 when it can now.
func (h *poolProbeHarness) runnerReadyAt() int64 {
	h.t.Helper()
	h.bundle.mu.RLock()
	defer h.bundle.mu.RUnlock()
	for _, lifecycle := range h.bundle.runners {
		group, ok := lifecycle.runner.(*productionPhaseTwoQueryGroup)
		if !ok {
			h.t.Fatalf("the Query Group runs as %T, not a production Query Group", lifecycle.runner)
		}
		if ready := group.runner.NextReadyAt(); !ready.IsZero() {
			return ready.UnixMilli()
		}
	}
	return 0
}

// afterCooldown is the first instant after the Query Group's cooldown runs
// out, or - where it runs out inside a Slot's own window, which depends on
// the wall-clock minute the case started in - just after that window's
// deadline, so the Slot is held and replayed like the rest and every run of a
// case meets the same order.
func (h *poolProbeHarness) afterCooldown() int64 {
	h.t.Helper()
	at := h.poolRecord().Until.UnixMilli() + 1
	// A round's own window runs from its read to its deadline, which its
	// read hold moves with the read and can carry past the next minute's
	// start: outside every such window, a Slot still held is a replay.
	hold := h.readWait() - readWaitOf(h.cfg, h.bundle.queryGroups[0], 60)
	deadline := (time.Minute - poolBudgetReserve + hold).Milliseconds()
	minute := time.Minute.Milliseconds()
	for slot := (at-h.base*1000)/minute - 1; slot <= (at-h.base*1000)/minute; slot++ {
		start := h.base*1000 + slot*minute
		if at >= start+h.readWait().Milliseconds() && at < start+deadline {
			at = start + deadline + 1
		}
	}
	return at
}

// decide runs the Query Group from the moment its cooldown runs out to the
// execution that decides the pool, from afterCooldown: a round deciding
// inside its own window at the cooldown's end is the scheduler's own test
// (TestOnlyANormalExecutionDecidesThePool). It runs passes, each when the last query came back, until one sends
// nothing: the Slots held in the pool, replayed, each leaving the pool as it
// is (a pass that decides there is returned with atEnd). Then it runs the next
// round whose Slot is not yet ready, late into it as a round that queued for
// its permit at second 30 is, and returns that round's query and the
// transition it made. A pass waits on the wall clock for a retry due later on
// the test's, so the harness never repeats an instant.
func (h *poolProbeHarness) decide(late time.Duration) (replays []poolRequest, decider poolRequest, event observability.QueryCooldownFacts, atEnd bool) {
	h.t.Helper()
	at := h.afterCooldown()
	// A one-shot pass advances the Query Group one Slot, where production's
	// dispatcher takes it again on its next turn while it is due. So the
	// passes go on, each a millisecond after the last answer - or at the
	// Runner's own next readiness, a retry's, when that is later: a one-shot
	// pass waits for a later one on the wall clock, which the test's clock
	// never moves - as long as each completes a Slot (a held Slot past the
	// replay distance completes without a query), and stop at the first that
	// completes none. A Runner next ready at or past a Slot's own readiness
	// is ready for that Slot's normal round, which is the round below.
	for i := 0; ; i++ {
		if ready := h.runnerReadyAt(); ready > at {
			if ready >= h.readyAfter(at) {
				break
			}
			at = ready
		}
		requests, events, completed := h.passCounting(at)
		if len(events) != 0 {
			if len(requests) != 1 || len(events) != 1 {
				h.t.Fatalf("at the cooldown's end %+v sent and %+v made, want one round and its transition", requests, events)
			}
			return replays, requests[0], events[0], true
		}
		replays = append(replays, requests...)
		if completed == 0 {
			break
		}
		if i == 32 {
			h.t.Fatalf("still completing held Slots after %d passes", i)
		}
		at = h.backend.clock.Load() + 1
	}
	minute := (at/1000 - h.base) / 60
	if (h.base+minute*60)*1000+h.readWait().Milliseconds() < at {
		minute++
	}
	requests, events := h.pass((h.base+minute*60)*1000 + (h.readWait() + poolPassSlack + late).Milliseconds())
	if len(requests) != 1 || len(events) != 1 {
		h.t.Fatalf("minute %d, %s late: %+v sent and %+v made, want one query and the pool decided", minute, late, requests, events)
	}
	return replays, requests[0], events[0], false
}

// Only the Query Group's next normal round decides the pool, at its own
// second and late as it is: one that queued for its permit until 13 s of its
// budget were left has 13 s. A backend answering just past a normal round's
// whole budget - which a replay at the cooldown's end, with its 55 s,
// answers - keeps the Query Group in; one answering in 10 s takes it out. The held Slot is replayed first, with a replay's 55 s from its
// arrival, for its data, and decides nothing. The probe line says what the
// deciding round had and used.
func TestThePoolIsLeftOnlyOnAnAnswerItsNormalRoundGets(t *testing.T) {
	for _, test := range []struct {
		name    string
		latency func(h *poolProbeHarness) time.Duration
		event   string
		outcome string
	}{
		{"a backend answering just past a normal round", func(h *poolProbeHarness) time.Duration { return h.normalBudget() + time.Second },
			"extended", observability.QueryCooldownProbeUnavailable},
		{"a backend answering in 10 s", func(*poolProbeHarness) time.Duration { return 10 * time.Second },
			"recovered", observability.QueryCooldownProbeAnswered},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newPoolProbeHarness(t, poolSlowerThanANormalRound)
			h.enterPool()
			latency := test.latency(h)
			h.backend.latency.Store(latency.Milliseconds())
			late := h.normalBudget() - poolPassSlack - 13*time.Second
			replays, decider, event, atEnd := h.decide(late)
			// The held Slot is replayed, and the next round decides.
			if atEnd || len(replays) == 0 {
				t.Fatalf("at the cooldown's end: replays %+v, decided %t; want the held Slot replayed and nothing decided", replays, atEnd)
			}
			// The held Slot is replayed with a replay's 55 s and answers; a
			// retry of the round that put the Query Group in may run too,
			// with what its own budget has left. Neither decides.
			if !anyReplayAnswered(replays) {
				t.Fatalf("the held Slots' executions %+v, want the held Slot replayed with 55 s and answered", replays)
			}
			if decider.remaining != h.normalBudget()-poolPassSlack-late {
				t.Fatalf("the deciding round had %s, want its normal round's %s less %s late", decider.remaining, h.normalBudget(), late)
			}
			if event.Event != test.event {
				t.Fatalf("a backend answering in %s made %q, want %q", latency, event.Event, test.event)
			}
			probe := event.LastProbe
			// The elapsed time is what the query took: the backend's latency
			// when it answered, the round's whole budget when it did not.
			elapsed := min(latency, decider.remaining)
			if probe == nil || probe.Operation != string(execution.OperationNormal) || probe.Outcome != test.outcome || !probe.Measured ||
				probe.BudgetMillis != decider.remaining.Milliseconds() || probe.ElapsedMillis != elapsed.Milliseconds() {
				t.Fatalf("the transition's probe = %+v, want a normal round's, %s, with budget %d ms", probe, test.outcome, decider.remaining.Milliseconds())
			}
			// The record a restart or the next owner reads back says the same.
			if record := h.poolRecord(); record.LastProbe == nil || *record.LastProbe != *probe {
				t.Fatalf("the pool record's probe = %+v, want %+v", record.LastProbe, probe)
			}
		})
	}
}

// A backend that answers in 30 s, past a normal round's 25 s and inside a
// replay's 55 s: the shape that went in and out of the pool every few
// minutes, each exit an answer the next normal rounds could not get. Now the
// Query Group enters once and stays, cooldown after cooldown: each cooldown's
// end replays the held Slots, and the backend answers them for their data;
// the normal round that decides times out and extends the pool, one failure
// more each time, with no exit and no re-entry.
func TestABackendSlowerThanANormalRoundStaysInThePoolWithoutReentering(t *testing.T) {
	h := newPoolProbeHarness(t, poolSlowerThanANormalRound)
	h.enterPool()
	failures := h.poolRecord().Failures
	for cooldown := 0; cooldown < 3; cooldown++ {
		replays, decider, event, atEnd := h.decide(0)
		if atEnd {
			t.Fatalf("cooldown %d: a held Slot's replay decided the pool", cooldown)
		}
		if !anyReplayAnswered(replays) {
			t.Fatalf("cooldown %d: the held Slots' executions %+v, want the held Slot replayed with 55 s and answered in 30", cooldown, replays)
		}
		probe := event.LastProbe
		if decider.answered || decider.remaining != h.normalBudget()-poolPassSlack ||
			event.Event != "extended" || event.Reentries != 0 || event.Failures != failures+1 ||
			probe == nil || probe.Operation != string(execution.OperationNormal) || probe.Outcome != observability.QueryCooldownProbeUnavailable {
			t.Fatalf("cooldown %d: the deciding round %+v made %+v (probe %+v), want a normal round that timed out and the pool extended on failure %d",
				cooldown, decider, event, probe, failures+1)
		}
		failures++
	}
}

// A replay that does not decide anything about the pool keeps what a
// recovery has - its interval less the reserve from its arrival - so the
// Slots a Worker catches up on after a rollout are not cut to a normal
// round's budget. Here a round missed while the Query Group is outside the
// pool is replayed with 55 s, and a backend answering in 30 s answers it.
func TestAReplayOutsideThePoolKeepsTheRecoveryBudget(t *testing.T) {
	h := newPoolProbeHarness(t, 0)
	if requests, _ := h.round(0); len(requests) != 1 || !requests[0].answered {
		t.Fatalf("minute 0: %+v, want one answered query", requests)
	}
	h.backend.latency.Store((30 * time.Second).Milliseconds())
	// Minute 1 is missed; its Slot is a replay by minute 3.
	var replays []poolRequest
	for minute := int64(3); minute <= 4; minute++ {
		requests, events := h.round(minute)
		if len(events) != 0 {
			t.Fatalf("minute %d: pool transitions %+v outside the pool", minute, events)
		}
		for _, request := range requests {
			if request.remaining > h.normalBudget() {
				replays = append(replays, request)
			}
		}
	}
	if len(replays) == 0 {
		t.Fatal("no query had more than a normal round's budget: the missed Slots were not replayed with a recovery's")
	}
	for _, replay := range replays {
		if replay.remaining != time.Minute-poolBudgetReserve || !replay.answered {
			t.Fatalf("a replay %+v, want 55 s and answered in 30 s", replay)
		}
	}
}

// poolRecord is the Query Group's pool record as the store reads it back.
func (h *poolProbeHarness) poolRecord() scheduler.QueryCooldownRecord {
	h.t.Helper()
	prefix := queryCooldownPrefix(h.cfg)
	keys, err := h.client.Keys(h.ctx, prefix+":*").Result()
	if err != nil || len(keys) != 1 {
		h.t.Fatalf("pool records %v (%v), want the one Query Group's", keys, err)
	}
	store := newRedisQueryCooldownStore(h.client, prefix, nil, nil, nil)
	record, found, err := store.LoadQueryCooldown(h.ctx, execution.QueryGroupIdentity(keys[0][len(prefix)+1:]))
	if err != nil || !found {
		h.t.Fatalf("pool record (found %t, %v)", found, err)
	}
	return record
}

// readyAfter is the first minute's Slot readiness, in unix milliseconds, at or
// after at.
func (h *poolProbeHarness) readyAfter(at int64) int64 {
	minute := (at/1000 - h.base) / 60
	ready := (h.base+minute*60)*1000 + h.readWait().Milliseconds()
	if ready < at {
		ready += 60_000
	}
	return ready
}

// The held Slot's replay at a cooldown's end can run into the next normal
// round: the Runner is still busy with it when the round's query is ready,
// so the round starts late on the pool's own account, which no round outside
// the pool would. A backend answering 3 s inside a normal round's budget -
// past what the late round has left - would then time out every
// cooldown and stay in for good. The late round's timeout is excused and
// decides nothing; the next round, with nothing of its own ahead of it,
// answers and takes the Query Group out.
func TestARoundLateBehindThePoolsOwnReplayDoesNotKeepTheQueryGroupIn(t *testing.T) {
	h := newPoolProbeHarness(t, poolSlowerThanANormalRound)
	h.enterPool()
	entered := h.poolRecord()
	end := h.afterCooldown()
	ready := h.readyAfter(end)
	// The replay runs from the cooldown's end to 5 s past the next round's
	// readiness.
	h.backend.latency.Store(ready - end + 5_000)
	requests, events := h.pass(end)
	if len(requests) != 1 || !requests[0].answered || requests[0].remaining != time.Minute-poolBudgetReserve || len(events) != 0 {
		t.Fatalf("at the cooldown's end %+v sent and %+v made, want the held Slot's replay answered with 55 s and nothing decided", requests, events)
	}
	h.backend.latency.Store((h.normalBudget() - 3*time.Second).Milliseconds())
	requests, events = h.pass(h.backend.clock.Load() + 1)
	if len(requests) != 1 || requests[0].answered || requests[0].remaining >= h.normalBudget()-4*time.Second || len(events) != 0 {
		t.Fatalf("the round behind the replay: %+v sent and %+v made, want a late round that timed out and decided nothing", requests, events)
	}
	if record := h.poolRecord(); record.Until != entered.Until || record.Failures != 3 {
		t.Fatalf("after the excused round the pool record is %+v, want it as the cooldown left it", record)
	}
	at := h.readyAfter(h.backend.clock.Load()+1) + poolPassSlack.Milliseconds()
	requests, events = h.pass(at)
	if len(requests) != 1 || !requests[0].answered || requests[0].remaining != h.normalBudget()-poolPassSlack || len(events) != 1 || events[0].Event != "recovered" {
		t.Fatalf("the next round: %+v sent and %+v made, want a normal round's whole budget answered and the Query Group out", requests, events)
	}
}

// The excuse is one round for each cooldown's end. Against a backend that
// never answers, the Runner stays busy with its own replays and retries
// round after round; were every late round excused, nothing would extend the
// cooldown, every Slot would run and time out, and the Query Group would be
// as good as out of the pool with no answer to show for it. After the one
// excused round, the next deciding round extends the cooldown.
func TestOnlyOneRoundIsExcusedForEachCooldownsEnd(t *testing.T) {
	h := newPoolProbeHarness(t, poolSlowerThanANormalRound)
	h.enterPool()
	h.backend.latency.Store((5 * time.Minute).Milliseconds())
	entered := h.poolRecord()
	at := h.afterCooldown()
	excused := 0
	for pass := 0; pass < 12; pass++ {
		requests, events := h.pass(at)
		if len(events) != 0 {
			if len(events) != 1 || events[0].Event != "extended" || events[0].Failures != entered.Failures+1 {
				t.Fatalf("pass %d made %+v, want the cooldown extended on one more failure", pass, events)
			}
			if excused > 1 {
				t.Fatalf("%d late rounds were excused before the cooldown was extended, want at most one", excused)
			}
			return
		}
		for _, request := range requests {
			if request.remaining <= h.normalBudget() {
				excused++
			}
		}
		if len(requests) != 0 {
			at = h.backend.clock.Load() + 1
		} else {
			at = h.readyAfter(at+1) + poolPassSlack.Milliseconds()
		}
	}
	t.Fatalf("the cooldown was never extended against a backend that does not answer (%d rounds excused)", excused)
}

// anyReplayAnswered says one of the executions at a cooldown's end was a
// replay with a replay's whole budget, and the backend answered it.
func anyReplayAnswered(requests []poolRequest) bool {
	for _, request := range requests {
		if request.remaining == time.Minute-poolBudgetReserve && request.answered {
			return true
		}
	}
	return false
}

// The production bundle counts a query the service did not answer in time as
// distress, by whether the query had queued for its permit: three rounds
// against a backend slower than the budget, each the only query in flight,
// are three uncontended timeouts.
func TestTheProductionBundleCountsDownstreamDistressByContention(t *testing.T) {
	h := newPoolProbeHarness(t, poolSlowerThanANormalRound)
	h.enterPool()
	families, err := h.recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]float64{}
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_query_downstream_distress_total" {
			continue
		}
		for _, series := range family.GetMetric() {
			key := ""
			for _, label := range series.GetLabel() {
				key += label.GetName() + "=" + label.GetValue() + " "
			}
			counts[key] = series.GetCounter().GetValue()
		}
	}
	if counts["class=timeout contended=false "] != 3 || counts["class=timeout contended=true "] != 0 || len(counts) != 8 {
		t.Fatalf("distress counted %v, want three uncontended timeouts among eight series", counts)
	}
}

// The production dispatcher counts each Slot it queues as made ready, the
// peak the execution slots are sized from: a round that ran its Query Group
// left one in the busiest second.
func TestTheProductionDispatcherCountsTheSlotsItMakesReady(t *testing.T) {
	h := newPoolProbeHarness(t, 10*time.Millisecond)
	requests, _ := h.round(0)
	if len(requests) != 1 {
		t.Fatalf("minute 0 sent %+v, want one query", requests)
	}
	shaper := h.bundle.shaper
	shaper.mu.Lock()
	peak := shaper.ready.Peak(shaper.now())
	shaper.mu.Unlock()
	if peak < 1 {
		t.Fatalf("a round ran and the ready peak is %d", peak)
	}
}

// A new Query Group on a source nobody here has measured starts from the
// cold start, and the production bundle's metrics say so by origin: its
// first Slot is held to the deployment's ready delay and counted once as
// cold_start, never as peer.
func TestTheProductionBundleCountsHowEachGroupWasSeeded(t *testing.T) {
	h := newPoolProbeHarness(t, 10*time.Millisecond)
	if requests, _ := h.round(0); len(requests) != 1 {
		t.Fatalf("minute 0 sent %+v, want one query", requests)
	}
	families, err := h.recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	seeds := map[string]float64{}
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_read_hold_seeds_total" {
			continue
		}
		for _, series := range family.GetMetric() {
			for _, label := range series.GetLabel() {
				seeds[label.GetValue()] = series.GetCounter().GetValue()
			}
		}
	}
	if seeds["cold_start"] != 1 || seeds["peer"] != 0 || len(seeds) != 2 {
		t.Fatalf("seeds counted %v, want one cold start and no peer", seeds)
	}
}
