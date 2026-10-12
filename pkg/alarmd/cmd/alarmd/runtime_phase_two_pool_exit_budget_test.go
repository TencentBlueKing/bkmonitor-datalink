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
	poolNormalBudget  = time.Minute - poolBudgetReserve - poolBudgetSettle
)

// poolBackend is a query service that answers after a set latency on the
// test's clock: within the request's deadline it moves the clock by the
// latency and answers; past it, the clock moves to the deadline and the
// request times out, as a backend that did not answer in time does. Every
// request is kept with the time it had left.
type poolBackend struct {
	clock    atomic.Int64 // unix milliseconds
	latency  atomic.Int64 // milliseconds
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
	base    int64
	mu      sync.Mutex
	events  []observability.QueryCooldownFacts
}

func newPoolProbeHarness(t *testing.T, latency time.Duration) *poolProbeHarness {
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
	cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(poolBudgetSettle)
	cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(poolBudgetReserve)
	h.cfg = cfg
	bundle, err := openProductionPhaseTwoBundleWithDependencies(ctx, cfg, metric.NewRecorder(metric.BuildInfo{}),
		observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		}, phaseTwoProductionExternalDependencies{
			Now: h.backend.now, HTTPClient: &http.Client{Transport: controlledRoundTripper(h.backend.roundTrip)},
			AdditionalObserver: observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
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

// round runs the minute's round half a second after its Slot is ready, and
// returns the queries it sent and the pool transitions it made.
func (h *poolProbeHarness) round(minute int64) ([]poolRequest, []observability.QueryCooldownFacts) {
	h.t.Helper()
	h.backend.clock.Store((h.base+minute*60)*1000 + poolBudgetSettle.Milliseconds() + 500)
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

// probe runs minutes until the pool lets an execution run, and returns that
// execution's query and the transition it made.
func (h *poolProbeHarness) probe(from int64) (int64, poolRequest, observability.QueryCooldownFacts) {
	h.t.Helper()
	for minute := from; minute < from+12; minute++ {
		requests, events := h.round(minute)
		if len(requests) == 0 {
			if len(events) != 0 {
				h.t.Fatalf("minute %d: a held round made %+v", minute, events)
			}
			continue
		}
		if len(requests) != 1 || len(events) != 1 {
			h.t.Fatalf("minute %d: the pool let %+v run and made %+v, want one query and one transition", minute, requests, events)
		}
		return minute, requests[0], events[0]
	}
	h.t.Fatal("the pool let nothing run in twelve minutes")
	return 0, poolRequest{}, observability.QueryCooldownFacts{}
}

// The execution the pool lets run decides whether the Query Group leaves it,
// and it has what a normal round has - not the whole interval a retry or
// replay gets from its arrival. A backend answering a second inside that
// budget takes the Query Group out; one answering a second past it keeps
// the Query Group in, though a retry's budget would have let it answer. The
// probe line says what the deciding execution had and used either way.
func TestThePoolIsLeftOnlyOnAnAnswerInsideANormalRoundsBudget(t *testing.T) {
	for _, test := range []struct {
		name    string
		latency time.Duration
		event   string
		outcome string
	}{
		{"a second inside", poolNormalBudget - time.Second, "recovered", observability.QueryCooldownProbeAnswered},
		{"a second past", poolNormalBudget + time.Second, "extended", observability.QueryCooldownProbeUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newPoolProbeHarness(t, 30*time.Second)
			next := h.enterPool()
			h.backend.latency.Store(test.latency.Milliseconds())
			minute, request, event := h.probe(next)
			if request.remaining > poolNormalBudget {
				t.Fatalf("minute %d: the deciding execution had %s, want at most a normal round's %s", minute, request.remaining, poolNormalBudget)
			}
			if event.Event != test.event {
				t.Fatalf("minute %d: a backend answering in %s made %q, want %q", minute, test.latency, event.Event, test.event)
			}
			probe := event.LastProbe
			// The elapsed time is the query service client's own measure, on
			// the wall clock; this backend's latency is on the test's, so only
			// its bound is read here (the access package pins the value).
			if probe == nil || probe.Outcome != test.outcome || !probe.Measured ||
				probe.BudgetMillis != request.remaining.Milliseconds() || probe.ElapsedMillis < 0 || probe.ElapsedMillis > probe.BudgetMillis {
				t.Fatalf("the transition's probe = %+v, want %s with budget %d ms and the time it took", probe, test.outcome, request.remaining.Milliseconds())
			}
			// The record a restart or the next owner reads back says the same.
			if record := h.poolRecord(); record.LastProbe == nil || *record.LastProbe != *probe {
				t.Fatalf("the pool record's probe = %+v, want %+v", record.LastProbe, probe)
			}
		})
	}
}

// A backend that answers in 30 s, past a normal round's 25 s and inside a
// retry's 55 s: the shape that went in and out of the pool every few
// minutes, each exit an answer the next normal rounds could not get. Now the
// Query Group enters once and stays: every round in the pool either sends
// nothing or is the pool's own probe, which has a normal round's budget,
// times out and extends the pool. No exit, no re-entry, and the backend is
// asked only at the probe cadence.
func TestABackendSlowerThanANormalRoundStaysInThePoolWithoutReentering(t *testing.T) {
	h := newPoolProbeHarness(t, 30*time.Second)
	next := h.enterPool()
	probes := 0
	for minute := next; minute < next+40; minute++ {
		requests, events := h.round(minute)
		switch {
		case len(requests) == 0 && len(events) == 0:
			continue
		case len(requests) == 1 && len(events) == 1:
		default:
			t.Fatalf("minute %d: %+v sent and %+v made, want nothing or one probe and its transition", minute, requests, events)
		}
		probes++
		event, probe := events[0], events[0].LastProbe
		if requests[0].answered || requests[0].remaining > poolNormalBudget {
			t.Fatalf("minute %d: the probe %+v, want one with a normal round's budget that timed out", minute, requests[0])
		}
		if event.Event != "extended" || event.Reentries != 0 || probe == nil ||
			probe.Outcome != observability.QueryCooldownProbeUnavailable || probe.BudgetMillis != requests[0].remaining.Milliseconds() {
			t.Fatalf("minute %d: the probe made %+v (probe %+v), want the pool extended, no re-entry, the probe's budget on it", minute, event, probe)
		}
	}
	// The cooldown grows from two periods towards five minutes, so forty
	// minutes in the pool hold far fewer probes than rounds: at least one,
	// and no more than one every two minutes.
	if probes == 0 || probes > 20 {
		t.Fatalf("%d probes in forty minutes, want the pool's cadence", probes)
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
			if request.remaining > poolNormalBudget {
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
