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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
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
	// openAlert runs the consumer's protocol with its link Console and, once
	// round 0 has sent the ABNORMAL, puts that alert in the consumer's open
	// alert set and waits for each process's copy to read it: a zero's
	// recovery is counted withheld only where an alert is open.
	openAlert bool
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
	// statusSetter, when set, is every Level's recovery_config.status_setter.
	statusSetter string
	// algorithm, when set, is every Level's algorithm type in place of the
	// threshold, with algorithmConfig as its config: a template-bound count.
	algorithm       string
	algorithmConfig any
}

// eventSpanStrategyID is the strategy the runner writes and publishes.
const eventSpanStrategyID = "7201"

// eventSpanRounds is what each round of a run did.
type eventSpanRounds struct {
	sent  map[int][]string // event kinds the sink acknowledged
	quiet map[int]bool
	// evaluated is the rounds that evaluated a record of the group.
	evaluated map[int]bool
	// outcomes is each round's Level outcomes as "KIND" or "KIND:REASON".
	outcomes map[int][]string
	// tracker read the run's observations, as the replica's own does.
	tracker *fleet.Tracker
	gates   map[int][]observability.OpenAlertGateFact
	// withheld is each round's Level outcomes that met their recovery
	// windows on a count of zero and stayed NORMAL.
	withheld map[int]uint64
	written  []contract.TriggerEventV1
	ranges   [][2]int64
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
			recovery := map[string]any{"check_window": windows[1] + recoveryWider}
			if run.statusSetter != "" {
				recovery["status_setter"] = run.statusSetter
			}
			detects = append(detects, map[string]any{"level": index + 1, "priority": 1, "connector": "and",
				"trigger_config":  map[string]any{"count": 1, "check_window": windows[0]},
				"recovery_config": recovery})
			algorithm := map[string]any{"level": index + 1, "type": "Threshold", "config": threshold}
			if run.algorithm != "" {
				algorithm["type"], algorithm["config"] = run.algorithm, run.algorithmConfig
			}
			algorithms = append(algorithms, algorithm)
		}
		document["detects"], item["algorithms"] = detects, algorithms
		if run.native || run.openAlert {
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
	// The one event is in round 0's accepted period, the minute before base:
	// round 0 runs at base and its primary window ends there. Fixed before
	// any request, so every request of a round - the primary and a Level's
	// previous point alike, in whatever order they come - sees it in the
	// same bucket.
	eventBucket := base - period
	var (
		mu     sync.Mutex
		round  int
		result = eventSpanRounds{sent: map[int][]string{}, quiet: map[int]bool{}, evaluated: map[int]bool{}, outcomes: map[int][]string{},
			gates: map[int][]observability.OpenAlertGateFact{}, withheld: map[int]uint64{}}
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
	var console *httptest.Server
	if run.openAlert {
		console = httptest.NewServer(lifecycleConsole(address, client))
		t.Cleanup(console.Close)
	}
	published := false
	// publishOpen puts the ABNORMAL's alert in the consumer's set and says
	// so on the change channel, as the consumer does on taking it.
	publishOpen := func() {
		for _, event := range sink.snapshot() {
			if event.EventKind != contract.TriggerEventAbnormal {
				continue
			}
			if err := client.SAdd(ctx, lifecycleLinkPrefix+":tenant-a:"+eventSpanStrategyID, event.DedupeMD5).Err(); err != nil {
				t.Fatal(err)
			}
			notice, _ := json.Marshal(map[string]string{"bk_tenant_id": "tenant-a", "strategy_id": eventSpanStrategyID})
			if err := client.Publish(ctx, lifecycleLinkPrefix+":changes", notice).Err(); err != nil {
				t.Fatal(err)
			}
			published = true
			return
		}
		t.Fatal("round 0 sent no ABNORMAL to put in the consumer's set")
	}
	// waitHeld waits until the process's copy holds the published alert. The
	// copy reads on its own loop, which the bundle's clock paces, so the wait
	// moves the clock on a second at a time within the round's half period.
	waitHeld := func(bundle *phaseTwoWorkerBundle, current int) {
		port, ok := bundle.workerPorts.OpenAlerts.(*openAlertCopyPort)
		if !ok {
			t.Fatalf("the open alert port is %T", bundle.workerPorts.OpenAlerts)
		}
		for deadline, step := testwait.Deadline(t), int64(1); ; step++ {
			if stats := port.cache.Stats(); stats.Loaded >= 1 && stats.Members >= 1 {
				return
			}
			if step < period/2 {
				clock.Store(base + int64(current)*period + 1 + step)
			}
			time.Sleep(20 * time.Millisecond) // fixed-wait: the poll interval of a condition wait bounded by the test deadline
			if time.Now().After(deadline) {
				t.Fatalf("round %d: the open alert copy has not read the strategy's set by the test's deadline: %+v", current, port.cache.Stats())
			}
		}
	}
	var observations []observability.Observation
	result.tracker = fleet.NewTracker(nil, "event-span", func() time.Time { return time.Unix(clock.Load(), 0) })
	open := func() *phaseTwoWorkerBundle {
		cfg := controlledG4RuntimeConfig(address, "http://controlled-uq", "alarmd-event-span")
		// Held past the longest run: a round is a minute of this clock, and a
		// registration or lease that lapses mid-run stops the rounds.
		cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(6 * time.Hour)
		cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(6 * time.Hour)
		cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(6 * time.Hour)
		if run.native || run.openAlert {
			cfg.PhaseTwo.Output.Protocol = config.OutputProtocolNative
		}
		if run.openAlert {
			cfg.PhaseTwo.Linkd.ConsoleURL = console.URL + testLinkdAPIRoot
			cfg.PhaseTwo.Linkd.Username, cfg.PhaseTwo.Linkd.Password = "user", "secret"
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
		if run.openAlert {
			if !published {
				publishOpen()
			}
			waitHeld(bundle, current)
		}
		mu.Lock()
		result.sent[current] = decidedEventKinds(observations[seen:])
		for _, observation := range observations[seen:] {
			if observation.PrimaryInput != nil && observation.PrimaryInput.QuietWhenEmpty {
				result.quiet[current] = true
			}
			if observation.Stage == observability.StageEvaluationCompleted {
				result.gates[current] = append(result.gates[current], observation.OpenAlertGates...)
				for _, fact := range observation.RecoveryWithheld {
					result.withheld[current] += fact.Levels
				}
				for _, level := range observation.LevelOutcomes {
					if level.Count > 0 {
						result.evaluated[current] = true
					}
					outcome := level.Outcome
					if level.Reason != "" {
						outcome += ":" + level.Reason
					}
					result.outcomes[current] = append(result.outcomes[current], outcome)
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

// A grouped event count's zeros do not end its alert. The query service
// fills in zeros for a group that had an event inside the range asked, and
// the range is still N + R periods, so the trigger reads what it read
// before: the windows ending on rounds 2, 3 and 4 hold no anomaly, and on
// round 4 the R windows are met. But that round's own count is zero - no
// event, which is no observation - so the Level stays NORMAL, counted as
// withheld, and nothing is sent. This strategy fires on any event (count >=
// 1), so no count above zero can ever recover it: its alert ends by -nodata,
// close, or a person (the recovery spec, section 3.1).
//
// Through the production bundle, with a process restart after the event's
// round: the open alert's history is carried across it on the same store.
// Past the range - round 5 on - the group is not in the answer at all:
// nothing is decided for it, an open alert is not closed by inference, and
// the round's whole, empty primary says quiet.
func TestAGroupedEventCountsZerosDoNotEndItsAlert(t *testing.T) {
	run := runGroupedEventCount(t, eventSpanCase{takeover: 1, rounds: 7, openAlert: true})
	if !containsKind(run.sent[0], contract.TriggerEventAbnormal) {
		t.Fatalf("round 0 decided %v, want the event's ABNORMAL", run.sent[0])
	}
	for round := 1; round <= 6; round++ {
		if containsKind(run.sent[round], contract.TriggerEventRecovery) {
			t.Fatalf("round %d decided %v: a RECOVERY on a count of zero", round, run.sent[round])
		}
		if want := uint64(0); round == 4 {
			if run.withheld[round] != 1 {
				t.Fatalf("round 4 withheld %d, want 1: the R windows met on a zero (all rounds: %v)", run.withheld[round], run.withheld)
			}
		} else if run.withheld[round] != want {
			t.Fatalf("round %d withheld %d, want none before the windows are met or once the group left (all rounds: %v)", round, run.withheld[round], run.withheld)
		}
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

// A template-bound event count - its Level detects with something other
// than a threshold - reads a quiet group as every count family does: the
// lead keeps the group in the answer, its zero is detected, and a ring-ratio
// decrease fires on the first quiet round (0 <= 1 x (100 - floor)%). An
// inferred zero feeds the trigger like any value; only recovery declines
// it. Before the lead the group left the answer with its event and nothing
// was detected that round.
func TestATemplateBoundEventCountsQuietZeroFeedsItsTrigger(t *testing.T) {
	run := runGroupedEventCount(t, eventSpanCase{rounds: 3, levels: [][2]int{{1, 1}},
		algorithm: "SimpleRingRatio", algorithmConfig: map[string]any{"floor": 50, "ceil": nil}})
	if containsKind(run.sent[0], contract.TriggerEventAbnormal) {
		t.Fatalf("round 0 decided %v on the event's increase, want nothing: the ceiling is unset", run.sent[0])
	}
	if !run.evaluated[1] || !containsKind(run.sent[1], contract.TriggerEventAbnormal) {
		t.Fatalf("round 1 evaluated %v and decided %v, want the quiet group's zero detected as a decrease (rounds %v)",
			run.evaluated[1], run.sent[1], run.sent)
	}
	// The primary is asked N + R = 2 periods; the ring ratio's previous
	// point is a query of its own, one period long.
	widest := int64(0)
	for _, asked := range run.ranges {
		widest = max(widest, asked[1]-asked[0])
	}
	if widest != 2*60 {
		t.Fatalf("the widest range asked was %d s, want the primary's N + R = 2 periods (ranges %v)", widest, run.ranges)
	}
}

// The same template family's quiet rounds never end its alert at R = 3. A
// ring-ratio increase (ceil 50) fires on the event itself, round 0 (1
// against the 0 before it); N = 1 and R = 3, so the recovery windows are met
// on round 3, while the lead still keeps the group in the primary's answer.
// Rounds 1 to 3 are evaluated and none sends a RECOVERY; from round 4 the
// event is out of the range and the group with it.
//
// Those rounds are UNKNOWN (QUERY_EMPTY), not NORMAL with the recovery
// withheld: the ring ratio's previous point is a dependency requirement
// without the event count's lead, so it answers nothing once the event has
// left its window, and the Level cannot decide. Python skips the point the
// same way (no history point, HistoryDataNotExists). Giving dependency
// requirements the lead is listed for after 0.4.x in the 004 milestone plan
// ("event counts' template ring ratios: the dependency window has no lead");
// when it lands these rounds become NORMAL with the recovery withheld, and
// this case changes with it.
func TestATemplateBoundEventCountsQuietRoundsNeverRecover(t *testing.T) {
	run := runGroupedEventCount(t, eventSpanCase{rounds: 6, levels: [][2]int{{1, 3}}, openAlert: true,
		algorithm: "SimpleRingRatio", algorithmConfig: map[string]any{"floor": nil, "ceil": 50}})
	if !containsKind(run.sent[0], contract.TriggerEventAbnormal) {
		t.Fatalf("round 0 decided %v, want the increase's ABNORMAL", run.sent[0])
	}
	for round := 1; round < 6; round++ {
		if containsKind(run.sent[round], contract.TriggerEventRecovery) {
			t.Fatalf("round %d decided %v: a RECOVERY on a quiet round (rounds %v)", round, run.sent[round], run.sent)
		}
		if want := round <= 3; run.evaluated[round] != want {
			t.Fatalf("round %d evaluated %v, want %v: the group is in the answer through round 3 (rounds %v)",
				round, run.evaluated[round], want, run.evaluated)
		}
		if round <= 3 && !reflect.DeepEqual(run.outcomes[round], []string{"UNKNOWN:QUERY_EMPTY"}) {
			t.Fatalf("round %d outcomes %v, want UNKNOWN:QUERY_EMPTY: the previous point reads nothing (rounds %v)",
				round, run.outcomes[round], run.outcomes)
		}
	}
	if kinds := controlledEventKinds(run.written); len(kinds) != 1 || kinds[0] != contract.TriggerEventAbnormal {
		t.Fatalf("the sink took %v, want the ABNORMAL alone: the alert stays open", kinds)
	}
}

// An alert open when this build takes over from one that asked one period
// at a time. That build lost the group the round after its event, so the
// rounds between the event and the release left no record: holes in the
// group's history. Those rounds were read whole - the group was quiet, not
// unread - so once past the read hold the recovery walk counts their holes
// as minutes with no anomaly, as Python's recovery check does (trigger
// evaluator_v2, "A settled window answers with what it holds").
//
// So the R windows are met on round 4, the first round whose R windows all
// start after the event, whether the event was in the last round before the
// release (j = 1, no hole) or two rounds before it (j = 2, a hole on round
// 1), and the group is still in the answer then. Neither recovers: the
// round's count is zero. Without the hole the window is whole and the round
// is NORMAL, its recovery withheld; with it the window is not whole, and a
// round that does not recover on such a window is unavailable, never NORMAL
// (decision-022), so nothing is withheld either.
func TestAnEventCountAlertOpenAtTheReleaseMeetsItsWindowsWhetherOrNotItLeftAHole(t *testing.T) {
	for _, arm := range []struct {
		name     string
		until    int
		withheld bool
	}{
		{"event in the last round before the release", 1, true},
		{"event two rounds before the release", 2, false},
	} {
		t.Run(arm.name, func(t *testing.T) {
			run := runGroupedEventCount(t, eventSpanCase{oneWindowUntil: arm.until, takeover: arm.until, rounds: 8, openAlert: true})
			if !containsKind(run.sent[0], contract.TriggerEventAbnormal) {
				t.Fatalf("round 0 decided %v, want the event's ABNORMAL", run.sent[0])
			}
			for round := arm.until; round <= 4; round++ {
				if !run.evaluated[round] {
					t.Fatalf("round %d evaluated nothing, want the group back in the answer (rounds %v)", round, run.evaluated)
				}
			}
			for round := 1; round <= 7; round++ {
				if containsKind(run.sent[round], contract.TriggerEventRecovery) {
					t.Fatalf("round %d decided %v: a RECOVERY on a count of zero (all rounds: %v)", round, run.sent[round], run.sent)
				}
				if got, want := run.withheld[round] > 0, round == 4 && arm.withheld; got != want {
					t.Fatalf("round %d withheld %v, want %v (all rounds: %v)", round, got, want, run.withheld)
				}
				if round >= 5 && len(run.sent[round]) != 0 {
					t.Fatalf("round %d decided %v for a group not in the answer, want nothing", round, run.sent[round])
				}
			}
		})
	}
}

// On the consumer's protocol, after a second process takes the Plan over,
// the zeros still decide no RECOVERY, so the open-alert gate is never asked
// and the consumer's alert stays open: the sink takes the ABNORMAL alone.
// Past the range the group is not in the answer: nothing is decided, nothing
// is sent, the alert is not closed by inference, and the rounds read quiet.
func TestAnEventCountsZerosLeaveTheConsumersAlertOpen(t *testing.T) {
	run := runGroupedEventCount(t, eventSpanCase{takeover: 1, rounds: 8, native: true, openAlert: true})
	if !containsKind(run.sent[0], contract.TriggerEventAbnormal) {
		t.Fatalf("round 0 sent %v, want the event's ABNORMAL", run.sent[0])
	}
	if run.withheld[4] != 1 {
		t.Fatalf("round 4 withheld %d, want 1 (all rounds: %v)", run.withheld[4], run.withheld)
	}
	for round := 1; round <= 7; round++ {
		if len(run.gates[round]) != 0 {
			t.Fatalf("round %d asked the gate %+v, want no RECOVERY to ask about", round, run.gates[round])
		}
		if round >= 5 && (len(run.sent[round]) != 0 || !run.quiet[round]) {
			t.Fatalf("round %d sent %v, quiet %v, want nothing sent and quiet for a group not in the answer", round, run.sent[round], run.quiet[round])
		}
	}
	if kinds := controlledEventKinds(run.written); len(kinds) != 1 || kinds[0] != contract.TriggerEventAbnormal {
		t.Fatalf("the sink took %v, want the ABNORMAL alone: the consumer's alert stays open", kinds)
	}
}

// A grouped event count written to end on no data (recovery-nodata), N = 2
// and R = 3: its zeros are withheld as any event count's are, and the round
// (R + T + 1) periods after the event - round 6 - ends the alert, resolved,
// saying no data ended it. To still have the group in the answer then, the
// range asked reaches back R + T + 1 windows before the accepted one: 7
// periods, not N + R = 5. The window accepted is the same, so every round
// both evaluate the group decides what the same strategy without -nodata
// decides; from round 5 that one's range no longer holds the event and its
// group is not in the answer, while this one's still is, with its zero.
func TestAGroupedEventCountWrittenToEndOnNoDataEndsOnItsZeros(t *testing.T) {
	nodata := runGroupedEventCount(t, eventSpanCase{rounds: 8, native: true, statusSetter: "recovery-nodata"})
	plain := runGroupedEventCount(t, eventSpanCase{rounds: 8, native: true})
	if !plain.quiet[5] || nodata.quiet[5] || nodata.withheld[5] != 1 {
		t.Fatalf("round 5: without -nodata quiet %v, with it quiet %v withheld %d; want the group gone from the one and withheld in the other",
			plain.quiet[5], nodata.quiet[5], nodata.withheld[5])
	}
	for round := 0; round <= 4; round++ {
		if !reflect.DeepEqual(nodata.sent[round], plain.sent[round]) || nodata.withheld[round] != plain.withheld[round] {
			t.Fatalf("round %d: -nodata decided %v withheld %d, without %v withheld %d: the trigger read something else",
				round, nodata.sent[round], nodata.withheld[round], plain.sent[round], plain.withheld[round])
		}
	}
	if containsKind(plain.sent[6], contract.TriggerEventRecovery) {
		t.Fatalf("without -nodata round 6 decided %v, want nothing", plain.sent[6])
	}
	if !containsKind(nodata.sent[6], contract.TriggerEventRecovery) {
		t.Fatalf("round 6 decided %v, want the RECOVERY no data ended (all rounds: %v)", nodata.sent[6], nodata.sent)
	}
	var ended *contract.TriggerEventV1
	for index := range nodata.written {
		if nodata.written[index].EventKind == contract.TriggerEventRecovery {
			ended = &nodata.written[index]
		}
	}
	if ended == nil || ended.LevelResults[0].DecisionWindow.Recovery.EndedBy != contract.RecoveryEndedByNoData {
		t.Fatalf("the sink took %v, want a RECOVERY ended by no data", controlledEventKinds(nodata.written))
	}
	for _, asked := range nodata.ranges {
		if asked[1]-asked[0] != 7*60 {
			t.Fatalf("the query service was asked for %d s, want R + T + 2 = 7 periods of 60 s (ranges %v)", asked[1]-asked[0], nodata.ranges)
		}
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
// section 4, point 6). The zeros meet the recovery windows on round 4 and do
// not end the alert: no event is no observation, so the recovery is withheld.
// Through the fleet's own reading of the run: the rounds after the answers
// stop are never quiet, and an hour of them is listed as no data.
func TestAnUngroupedEventCountAnsweredWithNothingIsNoDataNotQuiet(t *testing.T) {
	run := runGroupedEventCount(t, eventSpanCase{ungrouped: true, emptyFrom: 6, rounds: 68, openAlert: true})
	if containsKind(run.sent[4], contract.TriggerEventRecovery) || run.withheld[4] != 1 {
		t.Fatalf("round 4 decided %v and withheld %d, want no RECOVERY and the zero's recovery withheld (all rounds: %v)", run.sent[4], run.withheld[4], run.sent)
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
	deadline := testwait.Deadline(t)
	var owned []execution.QueryGroupIdentity
	for owned = bundle.ownedQueryGroups(); len(owned) == 0 && time.Now().Before(deadline); owned = bundle.ownedQueryGroups() {
		time.Sleep(10 * time.Millisecond) // fixed-wait: the poll interval of a condition wait bounded by the test deadline
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
