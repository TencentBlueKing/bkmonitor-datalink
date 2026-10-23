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
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// eventSpanCase is one run of the event count below: one event at round 0,
// nothing after it; N = 2, R = 3 unless levels says otherwise.
type eventSpanCase struct {
	// levels is each Level's trigger and recovery windows, {N, R}.
	levels [][2]int
	// answeredPeriods, when not zero, answers every round as if the range
	// asked were this many periods.
	answeredPeriods int
	// ungrouped counts by no dimension: the query service answers one
	// series of every step, zero where the step holds none, until emptyFrom.
	ungrouped bool
	// emptyFrom, when not zero, answers no series at all from that round on,
	// as the query service does when it cannot find the index.
	emptyFrom int
	// oneWindowUntil: the rounds before it are answered as they were while a
	// build asked one period at a time - the group only on the round whose
	// period holds its event - so a run can start where the build before
	// this one left a strategy.
	oneWindowUntil int
	// takeover is the round from which a second process on the same store
	// runs the Plan, as a release or a move to another replica would.
	takeover int
	// rounds is how many rounds are run, from round 0.
	rounds int
	// native publishes the alert consumer's protocol, on which a RECOVERY
	// asks the open-alert gate; the compatibility protocol is not gated.
	native bool
	// editAtTakeover widens every Level's recovery window by one while no
	// process runs: the second process owns other content than the one the
	// first wrote its record under, on the same Query Group.
	editAtTakeover bool
	// readRestored reads the second process's own row for the Plan's object
	// after it takes the Plan over and before it runs a round: what it
	// restored from the record, not anything it watched.
	readRestored bool
	// readDiagnose reads the strategy's row from the process's own
	// /api/diagnose after the last round.
	readDiagnose bool
	// readQueryRanges reads the object's latest asked ranges from the
	// process's own object detail after the last round.
	readQueryRanges bool
}

// eventSpanRounds is what each round of a run did.
type eventSpanRounds struct {
	sent  map[int][]string // event kinds the sink acknowledged
	quiet map[int]bool
	// evaluated is the rounds that evaluated a record of the group.
	evaluated map[int]bool
	// tracker read the run's observations, as the replica's own does.
	tracker *fleet.Tracker
	gates   map[int][]observability.OpenAlertGateFact
	written []contract.TriggerEventV1
	ranges  [][2]int64
	// restored is the second process's row kind before its first round
	// (readRestored), empty when the object is on no line.
	restored string
	// diagnosed is the strategy's Plans as /api/diagnose names them
	// (readDiagnose).
	diagnosed []diagnosedPlan
	// queryRanges is the object detail's last_query_ranges
	// (readQueryRanges).
	queryRanges *objectQueryRanges
}

// objectQueryRanges is the object detail's account of what its latest
// round's primary queries asked of the provider.
type objectQueryRanges struct {
	Slot   int64 `json:"slot"`
	Total  int   `json:"total"`
	Ranges []struct {
		Digest          string `json:"digest"`
		AskedSeconds    int64  `json:"asked_seconds"`
		AcceptedSeconds int64  `json:"accepted_seconds"`
	} `json:"ranges"`
}

// diagnosedPlan is what a diagnose row says a Plan reads and groups by.
type diagnosedPlan struct {
	SourceSemantics []string `json:"source_semantics"`
	GroupBy         []string `json:"group_by"`
	GroupByTotal    int      `json:"group_by_total"`
	PromQL          bool     `json:"promql"`
}

func runGroupedEventCount(t *testing.T, run eventSpanCase) eventSpanRounds {
	t.Helper()
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	var document map[string]any
	dimensions := []string{"host"}
	if run.ungrouped {
		dimensions = []string{}
	}
	threshold := []any{[]any{map[string]any{"method": "gte", "threshold": 1}}}
	if err := json.Unmarshal(controlledG4StrategyDocument(t, 7201, "Threshold", "usage", "system.cpu", dimensions,
		threshold), &document); err != nil {
		t.Fatal(err)
	}
	item := document["items"].([]any)[0].(map[string]any)
	query := item["query_configs"].([]any)[0].(map[string]any)
	query["data_source_label"], query["data_type_label"], query["agg_method"] = "custom", "event", "COUNT"
	query["custom_event_name"], query["result_table_id"] = "synthetic-event", "system_event"
	levels := run.levels
	if levels == nil {
		levels = [][2]int{{2, 3}}
	}
	writeStrategy := func(recoveryWider int) {
		detects, algorithms := []any{}, []any{}
		for index, windows := range levels {
			detects = append(detects, map[string]any{"level": index + 1, "priority": 1, "connector": "and",
				"trigger_config":  map[string]any{"count": 1, "check_window": windows[0]},
				"recovery_config": map[string]any{"check_window": windows[1] + recoveryWider}})
			algorithms = append(algorithms, map[string]any{"level": index + 1, "type": "Threshold", "config": threshold})
		}
		document["detects"], item["algorithms"] = detects, algorithms
		if run.native {
			// The consumer's protocol names the alert by the frozen revision.
			document["strategy_revision"] = 1 + recoveryWider
		}
		body, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		if err := client.Set(ctx, "alarm-config.strategy_ids", "[7201]", 0).Err(); err != nil {
			t.Fatal(err)
		}
		if err := client.Set(ctx, "alarm-config.strategy_7201", body, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	writeStrategy(0)

	const period = int64(60)
	base := controlledG4Base(t)
	var clock atomic.Int64
	clock.Store(base)
	var (
		mu          sync.Mutex
		round       int
		eventBucket int64 // the bucket the one event falls in: round 0's accepted period
		result      = eventSpanRounds{sent: map[int][]string{}, quiet: map[int]bool{}, evaluated: map[int]bool{},
			gates: map[int][]observability.OpenAlertGateFact{}}
	)
	// The query service as Elasticsearch answers a grouped count: a group
	// with a document in [start, end) comes back with a point for every
	// step, zero where the step holds none; a group with none in the range
	// does not come back at all.
	uq := &http.Client{Transport: controlledRoundTripper(func(request *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			return nil, err
		}
		start, err := strconv.ParseInt(fmt.Sprint(payload["start_time"]), 10, 64)
		if err != nil {
			return nil, err
		}
		end, err := strconv.ParseInt(fmt.Sprint(payload["end_time"]), 10, 64)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		if eventBucket == 0 {
			eventBucket = end - period
		}
		result.ranges = append(result.ranges, [2]int64{start, end})
		event := eventBucket
		if round < run.oneWindowUntil {
			start = end - period
		}
		if run.answeredPeriods != 0 {
			start = end - int64(run.answeredPeriods)*period
		}
		empty := run.emptyFrom != 0 && round >= run.emptyFrom
		mu.Unlock()
		series := []any{}
		if !empty && (run.ungrouped || event >= start && event < end) {
			values := []any{}
			for bucket := start; bucket < end; bucket += period {
				count := 0
				if bucket == event {
					count = 1
				}
				values = append(values, []any{bucket * 1000, count})
			}
			keys, groups := []string{"host"}, []string{"synthetic-a"}
			if run.ungrouped {
				keys, groups = []string{}, []string{}
			}
			series = append(series, map[string]any{"name": "_result0", "columns": []string{"_time", "_value"}, "types": []string{"float", "float"},
				"group_keys": keys, "group_values": groups, "values": values})
		}
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(map[string]any{"series": series, "status": nil, "trace_id": "event-span", "is_partial": false}); err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(&buf), Request: request}, nil
	})}

	sink := &recordingPhaseTwoEventSink{}
	var observations []observability.Observation
	result.tracker = fleet.NewTracker(nil, "event-span", func() time.Time { return time.Unix(clock.Load(), 0) })
	open := func() *phaseTwoWorkerBundle {
		cfg := controlledG4RuntimeConfig(address, "http://controlled-uq", "alarmd-event-span")
		// Held past the longest run: a round is a minute of this clock, and a
		// registration or lease that lapses mid-run stops the rounds.
		cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(6 * time.Hour)
		cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(6 * time.Hour)
		cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(6 * time.Hour)
		if run.native {
			cfg.PhaseTwo.Output.Protocol = config.OutputProtocolNative
		}
		bundle, err := openProductionPhaseTwoBundleWithDependencies(ctx, cfg, metric.NewRecorder(metric.BuildInfo{}),
			observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
			func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
				return controlplane.NewLegacyRedisStrategySource(client, prefix)
			}, phaseTwoProductionExternalDependencies{
				Now: func() time.Time { return time.Unix(clock.Load(), 0) }, HTTPClient: uq,
				AdditionalObserver: observability.ObserverFunc(func(ctx context.Context, o observability.Observation) {
					result.tracker.Observe(ctx, o)
					mu.Lock()
					defer mu.Unlock()
					observations = append(observations, observability.NormalizeObservation(o))
				}),
				PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
					return sink, nil
				}),
			})
		if err != nil {
			t.Fatal(err)
		}
		if err := bundle.Start(ctx); err != nil {
			t.Fatal(err)
		}
		return bundle
	}
	bundle := open()
	for current := 0; current < run.rounds; current++ {
		if current == run.takeover {
			if err := bundle.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			if run.editAtTakeover {
				writeStrategy(1)
			}
			clock.Store(base + int64(current)*period + 1)
			bundle = open()
			if run.readRestored {
				result.restored = restoredRowKind(t, ctx, bundle)
			}
		}
		mu.Lock()
		round = current
		seen := len(observations)
		mu.Unlock()
		clock.Store(base + int64(current)*period + 1)
		if err := runScheduledOnceSettled(ctx, bundle); err != nil {
			t.Fatalf("round %d: %v", current, err)
		}
		mu.Lock()
		result.sent[current] = decidedEventKinds(observations[seen:])
		for _, observation := range observations[seen:] {
			if observation.PrimaryInput != nil && observation.PrimaryInput.QuietWhenEmpty {
				result.quiet[current] = true
			}
			if observation.Stage == observability.StageEvaluationCompleted {
				result.gates[current] = append(result.gates[current], observation.OpenAlertGates...)
				for _, level := range observation.LevelOutcomes {
					if level.Count > 0 {
						result.evaluated[current] = true
					}
				}
			}
		}
		mu.Unlock()
	}
	if run.readDiagnose {
		result.diagnosed = diagnosedPlans(t, bundle, "7201")
	}
	if run.readQueryRanges {
		owned := bundle.ownedQueryGroups()
		if len(owned) != 1 {
			t.Fatalf("the process owns %v, want its one object", owned)
		}
		recorder := httptest.NewRecorder()
		bundle.dependencies.FleetAPI.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/objects/"+string(owned[0]), nil))
		var body struct {
			LastQueryRanges *objectQueryRanges `json:"last_query_ranges"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode the object detail: %v (%s)", err, recorder.Body.String())
		}
		result.queryRanges = body.LastQueryRanges
	}
	if err := bundle.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	result.written = sink.snapshot()
	return result
}

// diagnosedPlans is the given strategy's Plans on the process's own
// /api/diagnose, every page read.
func diagnosedPlans(t *testing.T, bundle *phaseTwoWorkerBundle, strategyID string) []diagnosedPlan {
	t.Helper()
	cursor := ""
	for page := 0; page < 100; page++ {
		target := "/api/diagnose"
		if cursor != "" {
			target += "?cursor=" + url.QueryEscape(cursor)
		}
		recorder := httptest.NewRecorder()
		bundle.dependencies.FleetAPI.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		var body struct {
			Strategies []struct {
				StrategyID string          `json:"strategy_id"`
				Plans      []diagnosedPlan `json:"plans"`
			} `json:"strategies"`
			NextCursor string `json:"next_cursor"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode /api/diagnose: %v (%s)", err, recorder.Body.String())
		}
		for _, row := range body.Strategies {
			if row.StrategyID == strategyID {
				return row.Plans
			}
		}
		if body.NextCursor == "" {
			break
		}
		cursor = body.NextCursor
	}
	t.Fatalf("strategy %s is not on /api/diagnose", strategyID)
	return nil
}

// A grouped event count recovers on the zeros the query service fills in
// for a group that had an event inside the range asked, and it can only do
// so while the event is still inside that range: the query service returns a
// group only when the range holds one of its events. The trigger reads back
// N + R - 1 points to recover, so the range asked is N + R periods, the
// period accepted the newest of them. Here N = 2 and R = 3: the event's round
// opens the alert, the windows ending on rounds 2, 3 and 4 hold no anomaly,
// and RECOVERY is decided on round 4 - not before, and not never, as it was
// while the range was one period and the group vanished on round 1.
//
// Through the production bundle, with a process restart after the event's
// round: the open alert's history is carried across it on the same store.
// Past the range - round 5 on - the group is not in the answer at all:
// nothing is decided for it, an open alert is not closed by inference, and
// the round's whole, empty primary says quiet.
func TestAGroupedEventCountRecoversOnTheQueryServicesZeros(t *testing.T) {
	run := runGroupedEventCount(t, eventSpanCase{takeover: 1, rounds: 7})
	if !containsKind(run.sent[0], contract.TriggerEventAbnormal) {
		t.Fatalf("round 0 decided %v, want the event's ABNORMAL", run.sent[0])
	}
	for round := 1; round <= 3; round++ {
		if containsKind(run.sent[round], contract.TriggerEventRecovery) {
			t.Fatalf("round %d decided %v: a RECOVERY before the R windows after the trigger window passed", round, run.sent[round])
		}
	}
	if !containsKind(run.sent[4], contract.TriggerEventRecovery) {
		t.Fatalf("round 4 decided %v, want RECOVERY at t0 + N + R - 1 (all rounds: %v)", run.sent[4], run.sent)
	}
	for round := 0; round <= 6; round++ {
		if want := round >= 5; run.quiet[round] != want {
			t.Fatalf("round %d quiet %v, want %v: quiet once the event left the range, and only then (rounds %v)", round, run.quiet[round], want, run.quiet)
		}
		if round >= 5 && len(run.sent[round]) != 0 {
			t.Fatalf("round %d decided %v for a group not in the answer, want nothing", round, run.sent[round])
		}
	}
	for _, asked := range run.ranges {
		if asked[1]-asked[0] != 5*60 {
			t.Fatalf("the query service was asked for %d s, want N + R = 5 periods of 60 s (ranges %v)", asked[1]-asked[0], run.ranges)
		}
	}
}

// An alert open when this build takes over from one that asked one period
// at a time. That build lost the group the round after its event, so the
// rounds between the event and the release left no record: holes in the
// group's history. The recovery walk steps over a window holding a hole
// rather than counting it as a miss (trigger evaluator_v2, "A window nobody
// observed"), so R observed misses take R whole windows after the last hole:
// with the first round of this build at t0 + j, RECOVERY comes at
// t0 + j + N + R - 2. The group stays in the answer through t0 + N + R - 1.
//
// So an event in the last round before the release (j = 1) leaves no hole
// and recovers on round 4 as if the release never happened; an event two
// rounds before it (j = 2) would need round 5, where the group has already
// left the range, and the alert stays open with nothing decided for it. That
// is the release's one-time cost, bounded to the grouped event alerts that
// opened 2 to N + R - 1 rounds before it; one opened earlier than that was
// already past recovery under the build before.
func TestAnEventCountAlertOpenAtTheReleaseRecoversOnlyWithoutAHole(t *testing.T) {
	t.Run("event in the last round before the release", func(t *testing.T) {
		run := runGroupedEventCount(t, eventSpanCase{oneWindowUntil: 1, takeover: 1, rounds: 8})
		if !containsKind(run.sent[0], contract.TriggerEventAbnormal) {
			t.Fatalf("round 0 decided %v, want the event's ABNORMAL", run.sent[0])
		}
		for round := 1; round <= 7; round++ {
			if got := containsKind(run.sent[round], contract.TriggerEventRecovery); got != (round == 4) {
				t.Fatalf("round %d RECOVERY %v, want it on round 4 alone (all rounds: %v)", round, got, run.sent)
			}
		}
	})
	t.Run("event two rounds before the release", func(t *testing.T) {
		run := runGroupedEventCount(t, eventSpanCase{oneWindowUntil: 2, takeover: 2, rounds: 8})
		if !containsKind(run.sent[0], contract.TriggerEventAbnormal) {
			t.Fatalf("round 0 decided %v, want the event's ABNORMAL", run.sent[0])
		}
		for round := 2; round <= 4; round++ {
			if !run.evaluated[round] {
				t.Fatalf("round %d evaluated nothing, want the group back in the answer (rounds %v)", round, run.evaluated)
			}
		}
		for round := 1; round <= 7; round++ {
			if containsKind(run.sent[round], contract.TriggerEventRecovery) {
				t.Fatalf("round %d decided %v: the hole on round 1 leaves R whole windows only on round 5, "+
					"after the event left the range (all rounds: %v)", round, run.sent[round], run.sent)
			}
			if round >= 5 && len(run.sent[round]) != 0 {
				t.Fatalf("round %d decided %v for a group not in the answer, want nothing", round, run.sent[round])
			}
		}
		if kinds := controlledEventKinds(run.written); len(kinds) != 1 || kinds[0] != contract.TriggerEventAbnormal {
			t.Fatalf("the sink took %v, want the ABNORMAL alone: the alert stays open", kinds)
		}
	})
}

// A RECOVERY that is decided and held leaves the consumer's alert open, and
// the quiet that follows does not close it. On the consumer's protocol a
// RECOVERY asks the open-alert gate; here a second process takes the Plan
// over after the event's round, its copy of the consumer's set holds nothing
// it did not send - no Console is configured, so the copy is untrusted and
// answers from this process's own record of what it opened - and round 4's
// RECOVERY is held as no open alert. Past the
// range the group is not in the answer: nothing is decided, nothing is sent,
// the alert is not closed by inference, and the rounds read quiet.
func TestAHeldEventCountRecoveryLeavesTheAlertOpenThroughTheQuiet(t *testing.T) {
	run := runGroupedEventCount(t, eventSpanCase{takeover: 1, rounds: 8, native: true})
	if !containsKind(run.sent[0], contract.TriggerEventAbnormal) {
		t.Fatalf("round 0 sent %v, want the event's ABNORMAL", run.sent[0])
	}
	held := []observability.OpenAlertGateFact{{Outcome: observability.OpenAlertGateHeldNoOpenAlert, Records: 1}}
	if !reflect.DeepEqual(run.gates[4], held) {
		t.Fatalf("round 4's gate answered %+v, want %+v: the RECOVERY decided and held", run.gates[4], held)
	}
	for round := 5; round <= 7; round++ {
		if len(run.sent[round]) != 0 || len(run.gates[round]) != 0 {
			t.Fatalf("round %d sent %v and asked the gate %+v for a group not in the answer, want neither", round, run.sent[round], run.gates[round])
		}
		if !run.quiet[round] {
			t.Fatalf("round %d is not quiet (rounds %v)", round, run.quiet)
		}
	}
	if kinds := controlledEventKinds(run.written); len(kinds) != 1 || kinds[0] != contract.TriggerEventAbnormal {
		t.Fatalf("the sink took %v, want the ABNORMAL alone: the consumer's alert stays open", kinds)
	}
}

func containsKind(kinds []string, kind string) bool {
	for _, candidate := range kinds {
		if candidate == kind {
			return true
		}
	}
	return false
}

// The range is the most demanding Level's N + R, read off the compiled Plan:
// with Level 1 at N = 2, R = 3 and Level 2 at N = 1, R = 5 it is 6 periods -
// not the first Level's 5, and not max N + max R = 7.
func TestAnEventCountOfTwoLevelsAsksTheWidestLevelsSpan(t *testing.T) {
	run := runGroupedEventCount(t, eventSpanCase{levels: [][2]int{{2, 3}, {1, 5}}, rounds: 1})
	if len(run.ranges) == 0 {
		t.Fatal("the query service was not asked")
	}
	for _, asked := range run.ranges {
		if asked[1]-asked[0] != 6*60 {
			t.Fatalf("the query service was asked for %d s, want 6 periods of 60 s (ranges %v)", asked[1]-asked[0], run.ranges)
		}
	}
}

// The counterexample to max(N, R) + 1 (design section 1, point 3): answered
// as if 4 periods were asked, the event's bucket leaves the range on round 4,
// the round RECOVERY needs, and the alert never recovers.
func TestAnEventCountAnsweredOverMaxNRPlusOneDoesNotRecover(t *testing.T) {
	run := runGroupedEventCount(t, eventSpanCase{answeredPeriods: 4, rounds: 8})
	if !containsKind(run.sent[0], contract.TriggerEventAbnormal) {
		t.Fatalf("round 0 decided %v, want the event's ABNORMAL", run.sent[0])
	}
	for round := 1; round <= 3; round++ {
		if !run.evaluated[round] {
			t.Fatalf("round %d evaluated nothing, want the group in the answer (rounds %v)", round, run.evaluated)
		}
	}
	for round := 1; round <= 7; round++ {
		if containsKind(run.sent[round], contract.TriggerEventRecovery) {
			t.Fatalf("round %d decided %v over 4 periods (all rounds: %v)", round, run.sent[round], run.sent)
		}
	}
}

// An ungrouped event count is answered with one series of zeros when no event
// came, so an empty answer is the data path failing, not quiet (design
// section 4, point 6). Through the fleet's own reading of the run: the rounds
// after the answers stop are never quiet, and an hour of them is listed as
// no data.
func TestAnUngroupedEventCountAnsweredWithNothingIsNoDataNotQuiet(t *testing.T) {
	run := runGroupedEventCount(t, eventSpanCase{ungrouped: true, emptyFrom: 6, rounds: 68})
	if !containsKind(run.sent[4], contract.TriggerEventRecovery) {
		t.Fatalf("round 4 decided %v, want RECOVERY on the zeros of the one series (all rounds: %v)", run.sent[4], run.sent)
	}
	for round, quiet := range run.quiet {
		if quiet {
			t.Fatalf("round %d is quiet: an ungrouped answer of nothing is never quiet", round)
		}
	}
	var kinds []string
	for _, anomaly := range run.tracker.NoData() {
		kinds = append(kinds, anomaly.Kind)
	}
	if len(kinds) != 1 || kinds[0] != fleet.KindNoData {
		t.Fatalf("the fleet lists %v, want the one object as no data", kinds)
	}
}

// restoredRowKind is the kind of the row a process that has just taken the
// Plan over publishes for its object, before it runs a round: it waits for
// the assignment to reach the process, publishes once - which restores what
// it owns from the records - and reads the object back from its own API.
func restoredRowKind(t *testing.T, ctx context.Context, bundle *phaseTwoWorkerBundle) string {
	t.Helper()
	settleExecutableView(ctx, bundle, 10*time.Second)
	deadline := time.Now().Add(10 * time.Second)
	var owned []execution.QueryGroupIdentity
	for owned = bundle.ownedQueryGroups(); len(owned) == 0 && time.Now().Before(deadline); owned = bundle.ownedQueryGroups() {
		time.Sleep(10 * time.Millisecond)
	}
	if len(owned) != 1 {
		t.Fatalf("the process that took the Plan over owns %v, want its one object", owned)
	}
	bundle.dependencies.PublishFleet(ctx)
	recorder := httptest.NewRecorder()
	bundle.dependencies.FleetAPI.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/objects/"+string(owned[0]), nil))
	var body struct {
		Anomaly *struct {
			Kind string `json:"kind"`
		} `json:"anomaly"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the object route: %v (%s)", err, recorder.Body.String())
	}
	if body.Anomaly == nil {
		return ""
	}
	return body.Anomaly.Kind
}

// A daily event count at rest reads quiet from the moment the replica that
// takes it over restores it, not a day later at its first round there: the
// record's last empty round said so, under the content the new owner runs.
// Here the event is at round 0 with N = 2, R = 3, rounds 5 and 6 are quiet,
// and a second process takes the Plan over at round 7 and is read before it
// runs. With the recovery window widened while no process ran, the Plan
// runs other content on the same Query Group, and the record's word is not
// restored: the row waits for the first round, as before.
func TestAnEventCountAtRestIsQuietOnRestoreUnderTheSameContent(t *testing.T) {
	run := runGroupedEventCount(t, eventSpanCase{takeover: 7, rounds: 8, readRestored: true})
	if !run.quiet[5] || !run.quiet[6] {
		t.Fatalf("rounds 5 and 6 are not quiet before the takeover (rounds %v)", run.quiet)
	}
	if run.restored != fleet.KindQuiet {
		t.Fatalf("the process that took the Plan over reads %q before its first round, want %s", run.restored, fleet.KindQuiet)
	}
	edited := runGroupedEventCount(t, eventSpanCase{takeover: 7, rounds: 8, readRestored: true, editAtTakeover: true})
	if !edited.quiet[5] || !edited.quiet[6] {
		t.Fatalf("rounds 5 and 6 are not quiet before the edit (rounds %v)", edited.quiet)
	}
	if edited.restored == fleet.KindQuiet {
		t.Fatal("a record written under other content restored quiet before the first round under the new content")
	}
	if !edited.quiet[7] {
		t.Fatalf("round 7 under the new content is not quiet (rounds %v)", edited.quiet)
	}
}

// A diagnose row names what each of its Plans reads and groups by, so one
// pass over the rows sorts the strategies by source and grouping: the event
// count here reads custom/event, grouped by the one dimension it is written
// with, and the same count written with none is ungrouped.
func TestADiagnoseRowNamesWhatItsPlanReadsAndGroupsBy(t *testing.T) {
	grouped := runGroupedEventCount(t, eventSpanCase{rounds: 1, readDiagnose: true})
	want := []diagnosedPlan{{SourceSemantics: []string{"custom/event"}, GroupBy: []string{"host"}, GroupByTotal: 1}}
	if !reflect.DeepEqual(grouped.diagnosed, want) {
		t.Fatalf("the grouped count's diagnose row names %+v, want %+v", grouped.diagnosed, want)
	}
	ungrouped := runGroupedEventCount(t, eventSpanCase{ungrouped: true, rounds: 1, readDiagnose: true})
	want = []diagnosedPlan{{SourceSemantics: []string{"custom/event"}}}
	if !reflect.DeepEqual(ungrouped.diagnosed, want) {
		t.Fatalf("the ungrouped count's diagnose row names %+v, want %+v", ungrouped.diagnosed, want)
	}
}

// The object detail says what the event count's primary query asked of the
// query service on its latest round: with N = 2 and R = 3 it asks five
// periods and accepts one, so a group that does not recover can be read
// against its strategy's windows in one step.
func TestTheObjectDetailNamesTheRangeTheEventCountAsked(t *testing.T) {
	run := runGroupedEventCount(t, eventSpanCase{rounds: 2, readQueryRanges: true})
	if run.queryRanges == nil || run.queryRanges.Total != 1 || len(run.queryRanges.Ranges) != 1 ||
		run.queryRanges.Ranges[0].AskedSeconds != 5*60 || run.queryRanges.Ranges[0].AcceptedSeconds != 60 ||
		run.queryRanges.Ranges[0].Digest == "" || run.queryRanges.Slot == 0 {
		t.Fatalf("the object detail names %+v, want one primary query asking 300 s and accepting 60 s at its latest Slot", run.queryRanges)
	}
	if asked := run.ranges[len(run.ranges)-1]; asked[1]-asked[0] != 5*60 {
		t.Fatalf("the query service was asked %v, the range the detail should name", asked)
	}
}
