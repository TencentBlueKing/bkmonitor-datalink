// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
)

func overviewHealth() HealthResponse {
	expected := 36
	used, limit := uint64(180<<20), uint64(1<<30)
	ready := true
	success, failure := 5.0, 60.0
	return HealthResponse{
		Health: HealthHealthy, Expected: &expected,
		Builds:         []BuildGroup{{Build: BuildFacts{Version: "0.4.10", Commit: "abc1234"}, Replicas: []string{"replica-a"}}},
		SourceStanding: &SourceStanding{Listed: 56, Accepted: 53, Executing: 36},
		Schedule:       &ScheduleCensus{Completed1h: 7200, OnTime1h: 7200, Late: 0, Overdue: 0},
		PerReplica: []ReplicaView{{Replica: "replica-a", Owned: 36, UptimeSeconds: 600,
			Build:     &BuildFacts{Version: "0.4.10", Commit: "abc1234"},
			Readiness: &ReadinessFacts{Ready: true},
			Capacity:  &Capacity{PermitsHeld: 1, PermitBudget: 16, MemoryUsed: used, MemoryLimit: limit, ThrottledKnown: true}}},
		Dependencies: []Endpoint{
			{Role: "query_backend", Address: "http://198.51.100.7:10205", Configured: true, Ready: &ready, LastSuccessAgeSeconds: &success, LastFailureAgeSeconds: &failure},
			{Role: "state_redis", Address: "192.0.2.10:6379", Configured: true, Ready: &ready, LastSuccessAgeSeconds: &success},
			{Role: "output_kafka", Address: "203.0.113.5:9092", Configured: false},
		},
	}
}

func flowStage(overview Overview, stage string) OverviewStage {
	for _, row := range overview.Flow {
		if row.Stage == stage {
			return row
		}
	}
	return OverviewStage{}
}

// The verdict is the health route's, said once in a sentence, with the
// stage a degradation is about; unknown is its own state.
func TestTheOverviewVerdictIsTheHealthVerdictInOneLine(t *testing.T) {
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	running := map[StateWord]int{StateDetecting: 50, StateDataAbsent: 3}
	ok := overviewOf(overviewHealth(), running, true, at)
	if ok.Verdict.State != OverviewOK || !strings.Contains(ok.Verdict.Line, "53") || !strings.Contains(ok.Verdict.Line, "50") {
		t.Fatalf("healthy verdict = %+v, want ok naming 53 usable and 50 detecting", ok.Verdict)
	}
	degraded := overviewHealth()
	degraded.Health = HealthDegraded
	degraded.Degradations = []Degradation{{Kind: DegradationOpenAlertSetStale, Replica: "replica-a"}}
	if got := overviewOf(degraded, running, true, at).Verdict; got.State != OverviewAttention || got.Stage != "output" {
		t.Fatalf("degraded verdict = %+v, want attention at the output stage", got)
	}
	unknown := overviewHealth()
	unknown.Health = HealthUnknown
	if got := overviewOf(unknown, running, true, at).Verdict; got.State != OverviewUnknown || got.Line == "" {
		t.Fatalf("unknown verdict = %+v", got)
	}
}

// Each dependency hangs on its stage with its state and the instance it is,
// and a number the replicas do not report is unmeasured, never zero.
func TestTheOverviewFlowHangsDependenciesOnTheirStageAndSaysWhatIsUnmeasured(t *testing.T) {
	overview := overviewOf(overviewHealth(), nil, false, time.Now())
	pull := flowStage(overview, "pull")
	if len(pull.Dependencies) != 1 || pull.Dependencies[0].Name != "query_backend" || pull.Dependencies[0].State != OverviewOK {
		t.Fatalf("pull dependencies = %+v, want the query backend, ok", pull.Dependencies)
	}
	if state := flowStage(overview, "state"); len(state.Dependencies) != 1 || state.Dependencies[0].Name != "state_redis" {
		t.Fatalf("state dependencies = %+v", state.Dependencies)
	}
	if output := flowStage(overview, "output"); len(output.Dependencies) != 1 || output.Dependencies[0].State != OverviewNotConfigured {
		t.Fatalf("output dependencies = %+v, want the unconfigured Kafka said so", output.Dependencies)
	}
	if pull.Volume.Measured || pull.Failures.Measured || pull.LatencyP95MS.Measured {
		t.Fatalf("pull numbers = %+v, want unmeasured until the replicas report them", pull)
	}
	evaluate := flowStage(overview, "evaluate")
	if !evaluate.Volume.Measured || evaluate.Volume.Value != 2 || !evaluate.Failures.Measured || evaluate.Failures.Value != 0 {
		t.Fatalf("evaluate = %+v, want 7200 an hour as 2 a second and no late detections", evaluate)
	}
	encoded, err := json.Marshal(overview)
	if err != nil {
		t.Fatal(err)
	}
	if pull.Dependencies[0].Address != "http://198.51.100.7:10205" || flowStage(overview, "state").Dependencies[0].Address != "192.0.2.10:6379" {
		t.Fatalf("dependencies = %+v, want each with its address", overview.Flow)
	}
	if strings.Contains(string(encoded), "password") || strings.Contains(string(encoded), "@") {
		t.Fatalf("the overview carries a credential: %s", encoded)
	}
	if overview.Capability.Funnel.Listed != 56 || overview.Capability.Funnel.Accepted != 53 || overview.Capability.Funnel.Detecting.Measured {
		t.Fatalf("funnel = %+v, want 56 listed, 53 accepted and detecting unmeasured without strategy counts", overview.Capability.Funnel)
	}
}

// Resources per replica as shares of their limits, with what is not known
// said so.
func TestTheOverviewResourcesAreSharesOfTheirLimits(t *testing.T) {
	overview := overviewOf(overviewHealth(), nil, false, time.Now())
	if len(overview.Resources.Replicas) != 1 {
		t.Fatalf("replicas = %+v", overview.Resources.Replicas)
	}
	replica := overview.Resources.Replicas[0]
	if !replica.MemoryRatio.Measured || replica.MemoryRatio.Value < 0.17 || replica.MemoryRatio.Value > 0.18 ||
		replica.QueryPermits.Held != 1 || replica.QueryPermits.Limit != 16 || replica.LiveHeapBytes.Measured {
		t.Fatalf("replica resources = %+v", replica)
	}
	if overview.Resources.Scale.Strategies.Value != 53 || overview.Resources.Scale.MergedQueries.Value != 36 || overview.Resources.Scale.Hosts.Measured {
		t.Fatalf("scale = %+v", overview.Resources.Scale)
	}
	if overview.Runtime.Replicas[0].Version != "0.4.10" || !overview.Runtime.Replicas[0].Ready || overview.Runtime.Replicas[0].State != OverviewOK {
		t.Fatalf("runtime = %+v", overview.Runtime.Replicas[0])
	}
}

// The answer stays within its bound at the largest fleet.
func TestTheOverviewStaysWithinItsBoundAtSixtyFourReplicas(t *testing.T) {
	health := overviewHealth()
	base := health.PerReplica[0]
	health.PerReplica = nil
	for i := 0; i < 64; i++ {
		replica := base
		replica.Replica = fmt.Sprintf("alarmd-worker-%02d-0123456789abcdef", i)
		health.PerReplica = append(health.PerReplica, replica)
	}
	encoded, err := json.Marshal(overviewOf(health, map[StateWord]int{StateDetecting: 1}, true, time.Now()))
	if err != nil || len(encoded) > OverviewMaxBytes {
		t.Fatalf("overview is %d bytes (%v), want at most %d", len(encoded), err, OverviewMaxBytes)
	}
}

// The overview is computed at most once per freshness window, however many
// callers ask at once, and again after the window.
func TestTheOverviewIsComputedOncePerWindow(t *testing.T) {
	clock := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	var computed atomic.Int32
	cache := newOverviewCache(now, func() Overview {
		computed.Add(1)
		time.Sleep(10 * time.Millisecond) // fixed-wait: the computation's own time, which the case sets so the concurrent callers arrive while it runs
		return Overview{GeneratedAt: now()}
	})
	var wait sync.WaitGroup
	for i := 0; i < 100; i++ {
		wait.Add(1)
		go func() { defer wait.Done(); cache.get() }()
	}
	wait.Wait()
	if computed.Load() != 1 {
		t.Fatalf("computed %d times for 100 concurrent callers, want once", computed.Load())
	}
	mu.Lock()
	clock = clock.Add(OverviewMaxAge - time.Second)
	mu.Unlock()
	if cache.get(); computed.Load() != 1 {
		t.Fatalf("recomputed within the window")
	}
	mu.Lock()
	clock = clock.Add(2 * time.Second)
	mu.Unlock()
	if got := cache.get(); computed.Load() != 2 || got.MaxAgeSeconds != int(OverviewMaxAge/time.Second) {
		t.Fatalf("after the window: computed %d, max age %d", computed.Load(), got.MaxAgeSeconds)
	}
}

// The capability matrix is the Leader's published per-strategy count, one
// row per area; the reasons list keeps the five largest and counts the
// rest, each with what it means. Without the count it is unmeasured.
func TestTheOverviewCapabilityIsThePublishedStrategyCount(t *testing.T) {
	health := overviewHealth()
	if got := overviewOf(health, nil, false, time.Now()).Capability; got.Measured || len(got.Matrix) != 0 {
		t.Fatalf("without a published count: %+v, want unmeasured", got)
	}
	cells := []CapabilityCell{}
	for _, area := range CapabilityAreas {
		for _, outcome := range CapabilityOutcomes {
			count := 0
			switch {
			case outcome == CapabilitySupported:
				count = 50
			case area == CapabilitySource && outcome == CapabilityNotYetSupported:
				count = 6
			}
			cells = append(cells, CapabilityCell{Area: area, Outcome: outcome, Strategies: count})
		}
	}
	reasons := []CapabilityReason{}
	for i := 0; i < 7; i++ {
		reasons = append(reasons, CapabilityReason{Code: fmt.Sprintf("QUERY_SOURCE_NOT_MIGRATED_%d", i), Area: CapabilitySource,
			Outcome: CapabilityNotYetSupported, Strategies: 7 - i, SampleIDs: []string{"79"}})
	}
	reasons[0].Code = "QUERY_SOURCE_NOT_MIGRATED"
	health.Source = &SourceFacts{Capability: &SourceCapability{Cells: cells, Reasons: reasons,
		IgnoredByDesign: []CapabilityReason{{Code: "PRIORITY_IGNORED", Strategies: 2}}}}
	got := overviewOf(health, nil, false, time.Now()).Capability
	if !got.Measured || len(got.Matrix) != len(CapabilityAreas) || got.Matrix[0].Area != CapabilitySource ||
		got.Matrix[0].Supported != 50 || got.Matrix[0].NotYetSupported != 6 {
		t.Fatalf("matrix = %+v", got.Matrix)
	}
	if len(got.Reasons) != OverviewReasonRows || got.Reasons[0].Code != "QUERY_SOURCE_NOT_MIGRATED" || got.Reasons[0].What == "" ||
		got.ReasonsOmitted.Kinds != 2 || got.ReasonsOmitted.Strategies != 2+1 {
		t.Fatalf("reasons = %+v omitted %+v", got.Reasons, got.ReasonsOmitted)
	}
	if len(got.IgnoredByDesign) != 1 || got.IgnoredByDesign[0].Strategies != 2 {
		t.Fatalf("ignored = %+v", got.IgnoredByDesign)
	}
}

// What the runtime block says about a replica and the deployment: a
// replica not ready is in attention, one without a known memory limit has
// no memory share, one build is not a rollout and two are, and published
// configuration that has not taken effect past its bound is named.
func TestTheOverviewRuntimeNamesWhatNeedsAPerson(t *testing.T) {
	health := overviewHealth()
	if overviewOf(health, nil, false, time.Now()).Build.Rolling {
		t.Fatal("one build reads as a rollout")
	}
	second := health.PerReplica[0]
	second.Replica, second.Readiness = "replica-b", &ReadinessFacts{Ready: false}
	second.Capacity = &Capacity{PermitBudget: 16}
	health.PerReplica = append(health.PerReplica, second)
	health.Builds = append(health.Builds, BuildGroup{Build: BuildFacts{Version: "0.4.11"}, Replicas: []string{"replica-b"}})
	health.Activation = &ActivationFacts{BehindBeyondBound: true, ConsecutiveFailures: 4}
	overview := overviewOf(health, nil, false, time.Now())
	if !overview.Build.Rolling {
		t.Fatal("two builds do not read as a rollout")
	}
	if overview.Runtime.Replicas[1].State != OverviewAttention || overview.Runtime.Replicas[0].State != OverviewOK {
		t.Fatalf("replica states = %+v, want the one not ready in attention", overview.Runtime.Replicas)
	}
	if overview.Resources.Replicas[1].MemoryRatio.Measured {
		t.Fatalf("a replica without a memory limit has a memory share: %+v", overview.Resources.Replicas[1])
	}
	if len(overview.Runtime.Attention) != 1 || overview.Runtime.Attention[0].Kind != "config_behind" || !strings.Contains(overview.Runtime.Attention[0].Line, "4") {
		t.Fatalf("attention = %+v, want the configuration behind named with its failures", overview.Runtime.Attention)
	}
	// Failing past the bound with nothing newer waiting is said as well, in
	// its own words.
	health.Activation = &ActivationFacts{FailingBeyondBound: true, ConsecutiveFailures: 9}
	overview = overviewOf(health, nil, false, time.Now())
	if len(overview.Runtime.Attention) != 1 || overview.Runtime.Attention[0].Kind != "activation_failing" || !strings.Contains(overview.Runtime.Attention[0].Line, "9") {
		t.Fatalf("attention = %+v, want the failing activation named with its failures", overview.Runtime.Attention)
	}
}

// The route answers the same verdict as the health route, from the same
// replicas.
func TestTheOverviewRouteProjectsTheHealthRoute(t *testing.T) {
	snapshots := idleSnapshots()
	snapshots[0].Source = blockedSource(now.Add(-30 * time.Second))
	db := 8
	snapshots[0].Dependencies = []Endpoint{{Role: EndpointStateRedis, Kind: "redis", Address: "redis.example:6379", DB: &db, Configured: true}}
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 0, Known: true}, replicas())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	var overview Overview
	if err := json.Unmarshal(recorder.Body.Bytes(), &overview); err != nil {
		t.Fatal(err)
	}
	if overview.Verdict.State != OverviewAttention || overview.Capability.Funnel.Listed != 26 || overview.MaxAgeSeconds != 15 ||
		flowStage(overview, "state").Dependencies[0].Name != EndpointStateRedis {
		t.Fatalf("overview = %+v, want the degraded verdict, the 26-strategy round and the state store", overview)
	}
}

// A computation that panics caches nothing and leaves nobody waiting: its
// own caller sees the panic, and the next caller computes again.
func TestAPanickingOverviewComputationDoesNotStrandTheNextCaller(t *testing.T) {
	var calls atomic.Int32
	cache := newOverviewCache(time.Now, func() Overview {
		if calls.Add(1) == 1 {
			panic("first computation fails")
		}
		return Overview{Verdict: OverviewVerdict{State: OverviewOK}}
	})
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panicking computation's caller did not see the panic")
			}
		}()
		cache.get()
	}()
	done := make(chan Overview, 1)
	go func() { done <- cache.get() }()
	select {
	case got := <-done:
		if got.Verdict.State != OverviewOK || calls.Load() != 2 {
			t.Fatalf("after the panic: %+v in %d calls, want a fresh computation", got, calls.Load())
		}
	case <-testwait.Expired(t):
		t.Fatal("the caller after a panicking computation is stranded")
	}
}

// A caller already waiting on a computation that panics is released and
// computes again; it is not left waiting on a computation that never ends.
func TestACallerWaitingOnAPanickingComputationIsReleased(t *testing.T) {
	var calls, clockReads atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	cache := newOverviewCache(func() time.Time { clockReads.Add(1); return time.Now() }, func() Overview {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			panic("first computation fails")
		}
		return Overview{Verdict: OverviewVerdict{State: OverviewOK}}
	})
	// A stale answer, so each caller reads the clock before it looks for the
	// computation in flight: the second read is the waiter about to take it.
	cache.at = time.Now().Add(-2 * OverviewMaxAge)
	go func() {
		defer func() { _ = recover() }()
		cache.get()
	}()
	<-started
	waiter := make(chan Overview, 1)
	go func() { waiter <- cache.get() }()
	testwait.Until(t, "the second caller reaching the computation in flight", func() bool { return clockReads.Load() >= 2 })
	close(release)
	select {
	case got := <-waiter:
		if got.Verdict.State != OverviewOK || calls.Load() != 2 {
			t.Fatalf("the waiter got %+v after %d computations, want its own", got, calls.Load())
		}
	case <-testwait.Expired(t):
		t.Fatal("a caller waiting on the panicking computation is stranded")
	}
}

func flowHealth(at time.Time, started time.Time, flow *FlowFacts) HealthResponse {
	health := overviewHealth()
	health.PerReplica[0].AgeSeconds = 0
	health.PerReplica[0].StartedAt = started
	health.PerReplica[0].Flow = flow
	return health
}

func cumulative(counts ...uint64) []uint64 {
	out := make([]uint64, len(counts))
	var sum uint64
	for index, count := range counts {
		sum += count
		out[index] = sum
	}
	return out
}

// The stages' numbers are the replicas' counters over two computations:
// nothing on the first, a rate and a P95 from the buckets on the second,
// a P95 past the last bound said as at least that bound, and nothing for a
// replica that restarted between them.
func TestTheFlowRatesAreTheCountersOverTwoComputations(t *testing.T) {
	started := time.Date(2026, 10, 13, 11, 0, 0, 0, time.UTC)
	first := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	second := first.Add(time.Minute)
	before := &FlowFacts{Queries: 100, QueryDuration: FlowHistogram{Buckets: make([]uint64, len(FlowQueryBounds)), Count: 100},
		DecisionLag: FlowHistogram{Buckets: make([]uint64, len(FlowDecisionBounds))}, RuntimeRedis: FlowHistogram{Buckets: make([]uint64, len(FlowRedisBounds))},
		Detections: 50, StateWrites: 10, EventsEmitted: 4}
	// 300 more queries: 270 within 0.5 s, 30 cut at 30 s (past the last
	// bound); 6 timed out.
	after := *before
	after.Queries = 400
	after.QueryFailures = map[string]uint64{"timeout": 6}
	after.QueryDuration = FlowHistogram{Buckets: cumulative(0, 0, 0, 270, 0, 0, 0, 0, 0, 0), Count: 400}
	after.Detections, after.StateWrites, after.EventsEmitted, after.EventsRejected = 110, 130, 10, 3
	after.DecisionLag = FlowHistogram{Buckets: cumulative(0, 0, 0, 0, 60, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0), Count: 60}

	flows := newOverviewFlows()
	one := overviewOf(flowHealth(first, started, before), nil, false, first)
	flows.apply(&one, flowHealth(first, started, before), first)
	if pull := flowStage(one, "pull"); pull.Volume.Measured || pull.LatencyP95MS.Measured {
		t.Fatalf("first computation = %+v, want nothing measured with nothing to subtract", pull)
	}
	two := overviewOf(flowHealth(second, started, &after), nil, false, second)
	flows.apply(&two, flowHealth(second, started, &after), second)
	pull := flowStage(two, "pull")
	if !pull.Volume.Measured || pull.Volume.Value != 5 || !pull.Failures.Measured || pull.Failures.Value != 6 ||
		len(pull.FailureCauses) != 1 || pull.FailureCauses[0].Cause != "timeout" || pull.FailureCauses[0].Count != 6 {
		t.Fatalf("pull = %+v, want 5 a second and 6 timeouts a minute", pull)
	}
	if !pull.LatencyP95MS.Measured || !pull.LatencyP95MS.AtLeast || pull.LatencyP95MS.Value != 30000 {
		t.Fatalf("pull P95 = %+v, want at least 30 s: 10%% of the queries were cut", pull.LatencyP95MS)
	}
	evaluate := flowStage(two, "evaluate")
	// All 60 lags in the 2.5-5 s bucket: the 95th of them is 95% of the way
	// through it.
	if evaluate.Volume.Value != 1 || !evaluate.LatencyP95MS.Measured || evaluate.LatencyP95MS.AtLeast ||
		evaluate.LatencyP95MS.Value < 4874.9 || evaluate.LatencyP95MS.Value > 4875.1 {
		t.Fatalf("evaluate = %+v, want 60 decisions a minute as 1 a second and a P95 of 4875 ms", evaluate)
	}
	if state, output := flowStage(two, "state"), flowStage(two, "output"); state.Volume.Value != 2 || output.Volume.Value != 0.1 ||
		output.Failures.Value != 3 || output.LatencyP95MS.Measured {
		t.Fatalf("state %+v output %+v", state, output)
	}
	restarted := second.Add(time.Minute)
	// A new process that has already counted past the old one: higher
	// counters, still not a difference of anything.
	afterRestart := after
	afterRestart.Queries = 1000
	three := overviewOf(flowHealth(restarted, started.Add(time.Hour), &afterRestart), nil, false, restarted)
	flows.apply(&three, flowHealth(restarted, started.Add(time.Hour), &afterRestart), restarted)
	// A restart is a reading that is a difference of nothing: the rate the
	// replica had is carried once, said so.
	if pull := flowStage(three, "pull"); pull.Volume.Value != 5 || three.FlowCoverage != (OverviewCoverage{Counted: 1, Carried: 1, Expected: 1}) {
		t.Fatalf("after a restart = %+v coverage %+v, want the last rate carried once", pull.Volume, three.FlowCoverage)
	}
}

// Each replica's rate is over the time between its own two readings, not
// between the two computations: a summary published 30 seconds before the
// computation is 30 seconds older.
func TestAReplicasRateIsOverItsOwnReadings(t *testing.T) {
	started := time.Date(2026, 10, 13, 11, 0, 0, 0, time.UTC)
	first := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	second := first.Add(time.Minute)
	empty := func(bounds []float64) FlowHistogram { return FlowHistogram{Buckets: make([]uint64, len(bounds))} }
	before := &FlowFacts{Queries: 0, QueryDuration: empty(FlowQueryBounds), DecisionLag: empty(FlowDecisionBounds), RuntimeRedis: empty(FlowRedisBounds)}
	after := *before
	after.Queries = 90
	flows := newOverviewFlows()
	one := overviewOf(flowHealth(first, started, before), nil, false, first)
	flows.apply(&one, flowHealth(first, started, before), first)
	late := flowHealth(second, started, &after)
	late.PerReplica[0].AgeSeconds = 30
	two := overviewOf(late, nil, false, second)
	flows.apply(&two, late, second)
	if pull := flowStage(two, "pull"); !pull.Volume.Measured || pull.Volume.Value != 3 {
		t.Fatalf("pull = %+v, want 90 queries over the 30 seconds between the readings, 3 a second", pull.Volume)
	}
}

// Two replicas, counters as each published them, ages relative to the
// computation.
func twoReplicaHealth(started time.Time, a, b *FlowFacts, ageA, ageB float64) HealthResponse {
	health := overviewHealth()
	first := health.PerReplica[0]
	first.StartedAt, first.Flow, first.AgeSeconds = started, a, ageA
	second := first
	second.Replica, second.Flow, second.AgeSeconds = "replica-b", b, ageB
	health.PerReplica = []ReplicaView{first, second}
	return health
}

func queriesFlow(queries uint64) *FlowFacts {
	empty := func(bounds []float64) FlowHistogram { return FlowHistogram{Buckets: make([]uint64, len(bounds))} }
	return &FlowFacts{Queries: queries, QueryDuration: empty(FlowQueryBounds), DecisionLag: empty(FlowDecisionBounds), RuntimeRedis: empty(FlowRedisBounds)}
}

// A replica that missed one publish contributes the rate it had, said so;
// past one window it is dropped, the flow says how many replicas it covers
// of how many publish, and the verdict is no better than attention while it
// covers fewer.
func TestAReplicaThatStoppedPublishingIsCarriedOnceThenCountedOut(t *testing.T) {
	started := time.Date(2026, 10, 13, 11, 0, 0, 0, time.UTC)
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	flows := newOverviewFlows()
	compute := func(health HealthResponse) Overview {
		overview := overviewOf(health, map[StateWord]int{StateDetecting: 50}, true, at)
		flows.apply(&overview, health, at)
		return overview
	}
	// Both publish: nothing to subtract yet, then 1 and 2 queries a second.
	compute(twoReplicaHealth(started, queriesFlow(0), queriesFlow(0), 0, 0))
	at = at.Add(time.Minute)
	if got := compute(twoReplicaHealth(started, queriesFlow(60), queriesFlow(120), 0, 0)); flowStage(got, "pull").Volume.Value != 3 ||
		got.FlowCoverage != (OverviewCoverage{Counted: 2, Expected: 2}) {
		t.Fatalf("both publishing: volume %+v coverage %+v", flowStage(got, "pull").Volume, got.FlowCoverage)
	}
	// replica-b's summary does not move: its last rate is carried, flagged.
	at = at.Add(time.Minute)
	carried := compute(twoReplicaHealth(started, queriesFlow(120), queriesFlow(120), 0, 60))
	if flowStage(carried, "pull").Volume.Value != 3 || carried.FlowCoverage != (OverviewCoverage{Counted: 2, Carried: 1, Expected: 2}) ||
		carried.Verdict.State != OverviewOK {
		t.Fatalf("one missed publish: volume %+v coverage %+v verdict %+v", flowStage(carried, "pull").Volume, carried.FlowCoverage, carried.Verdict)
	}
	// Still not moved: counted out, and the verdict is held.
	at = at.Add(time.Minute)
	dropped := compute(twoReplicaHealth(started, queriesFlow(180), queriesFlow(120), 0, 120))
	if flowStage(dropped, "pull").Volume.Value != 1 || dropped.FlowCoverage != (OverviewCoverage{Counted: 1, Expected: 2}) {
		t.Fatalf("two missed publishes: volume %+v coverage %+v", flowStage(dropped, "pull").Volume, dropped.FlowCoverage)
	}
	if dropped.Verdict.State != OverviewAttention || !strings.Contains(dropped.Verdict.Line, "1/2") {
		t.Fatalf("verdict = %+v, want attention naming the 1/2 coverage", dropped.Verdict)
	}
	// It publishes again: counted again once it has two readings that move.
	at = at.Add(time.Minute)
	back := compute(twoReplicaHealth(started, queriesFlow(240), queriesFlow(180), 0, 0))
	if back.FlowCoverage != (OverviewCoverage{Counted: 2, Expected: 2}) || back.Verdict.State != OverviewOK {
		t.Fatalf("publishing again: coverage %+v verdict %+v", back.FlowCoverage, back.Verdict)
	}
}

// A replica of a build that does not publish the counters is not one the
// flow is expected to cover, and does not hold the verdict.
func TestAReplicaThatDoesNotPublishTheCountersIsNotExpected(t *testing.T) {
	started := time.Date(2026, 10, 13, 11, 0, 0, 0, time.UTC)
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	flows := newOverviewFlows()
	health := twoReplicaHealth(started, queriesFlow(0), nil, 0, 0)
	first := overviewOf(health, map[StateWord]int{StateDetecting: 50}, true, at)
	flows.apply(&first, health, at)
	at = at.Add(time.Minute)
	health = twoReplicaHealth(started, queriesFlow(60), nil, 0, 0)
	second := overviewOf(health, map[StateWord]int{StateDetecting: 50}, true, at)
	flows.apply(&second, health, at)
	if second.FlowCoverage != (OverviewCoverage{Counted: 1, Expected: 1}) || second.Verdict.State != OverviewOK {
		t.Fatalf("coverage %+v verdict %+v, want the publishing replica alone expected", second.FlowCoverage, second.Verdict)
	}
}

// A replica seen for the first time in this process is warming, not
// missing: the first computation over a healthy fleet, a scale-out and a
// rollout's new pods keep the verdict, and the replica is expected from its
// second computation.
func TestAReplicaSeenForTheFirstTimeIsWarmingNotMissing(t *testing.T) {
	started := time.Date(2026, 10, 13, 11, 0, 0, 0, time.UTC)
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	flows := newOverviewFlows()
	compute := func(health HealthResponse) Overview {
		overview := overviewOf(health, map[StateWord]int{StateDetecting: 50}, true, at)
		flows.apply(&overview, health, at)
		return overview
	}
	first := compute(twoReplicaHealth(started, queriesFlow(0), queriesFlow(0), 0, 0))
	if first.FlowCoverage != (OverviewCoverage{Warming: 2}) || first.Verdict.State != OverviewOK {
		t.Fatalf("first computation: coverage %+v verdict %+v, want both warming and ok", first.FlowCoverage, first.Verdict)
	}
	// One replica of the two, then a second appears.
	flows = newOverviewFlows()
	one := overviewHealth()
	one.PerReplica[0].StartedAt, one.PerReplica[0].Flow = started, queriesFlow(0)
	compute(one)
	at = at.Add(time.Minute)
	scaled := compute(twoReplicaHealth(started, queriesFlow(60), queriesFlow(0), 0, 0))
	if scaled.FlowCoverage != (OverviewCoverage{Counted: 1, Expected: 1, Warming: 1}) || scaled.Verdict.State != OverviewOK {
		t.Fatalf("scale-out: coverage %+v verdict %+v, want the new replica warming and ok", scaled.FlowCoverage, scaled.Verdict)
	}
	at = at.Add(time.Minute)
	if next := compute(twoReplicaHealth(started, queriesFlow(120), queriesFlow(60), 0, 0)); next.FlowCoverage != (OverviewCoverage{Counted: 2, Expected: 2}) {
		t.Fatalf("its second computation: coverage %+v, want it counted", next.FlowCoverage)
	}
}

// The page's one resource line and its runtime count are the server's: the
// fleet's shares, the replica closest to a limit and which limit, and the
// replicas counted by roles, version and readiness.
func TestTheOverviewSaysTheMostConstrainedReplicaAndCountsTheRest(t *testing.T) {
	started := time.Date(2026, 10, 13, 11, 0, 0, 0, time.UTC)
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	health := twoReplicaHealth(started, queriesFlow(0), queriesFlow(0), 0, 0)
	health.PerReplica[0].Capacity = &Capacity{MemoryUsed: 300, MemoryLimit: 1000, PermitsHeld: 2, PermitBudget: 16}
	health.PerReplica[1].Capacity = &Capacity{MemoryUsed: 900, MemoryLimit: 1000, PermitsHeld: 4, PermitBudget: 16}
	health.PerReplica[0].Flow.Roles = []string{"control", "worker"}
	health.PerReplica[1].Flow.Roles = []string{"worker", "control"}
	health.PerReplica[1].Readiness = &ReadinessFacts{Ready: false}
	overview := overviewOf(health, nil, false, at)
	newOverviewFlows().apply(&overview, health, at)
	if most := overview.Resources.MostConstrained; most == nil || most.Replica != "replica-b" || most.Constraint != "memory" ||
		!strings.Contains(overview.Resources.Line, "replica-b") {
		t.Fatalf("most constrained %+v line %q", most, overview.Resources.Line)
	}
	if fleet := overview.Resources.Fleet; !fleet.MemoryRatio.Measured || fleet.MemoryRatio.Value != 0.6 || fleet.QueryPermitRatio.Value != 6.0/32 {
		t.Fatalf("fleet shares %+v", fleet)
	}
	summary := overview.Runtime.Summary
	if len(summary) != 1 || summary[0].Version != "0.4.10" || summary[0].Ready != 1 || summary[0].NotReady != 1 ||
		strings.Join(summary[0].Roles, ",") != "control,worker" {
		t.Fatalf("summary %+v, want one group of the two replicas, roles in order", summary)
	}
	// Nothing near a limit: no replica named, and the line says so.
	health.PerReplica[1].Capacity = &Capacity{MemoryUsed: 400, MemoryLimit: 1000, PermitsHeld: 4, PermitBudget: 16}
	health.PerReplica[1].Readiness = &ReadinessFacts{Ready: true}
	calm := overviewOf(health, nil, false, at)
	newOverviewFlows().apply(&calm, health, at)
	if calm.Resources.MostConstrained != nil || calm.Resources.Line == "" {
		t.Fatalf("calm: most %+v line %q", calm.Resources.MostConstrained, calm.Resources.Line)
	}
	if summary := calm.Runtime.Summary; len(summary) != 1 || summary[0].Ready != 2 || summary[0].NotReady != 0 {
		t.Fatalf("calm summary %+v, want both ready", summary)
	}
}

// A dependency carries the stage's numbers where they measure it alone --
// the pull's latency and failures are the query backend's, the state
// stage's latency the runtime Redis's round trips -- and no borrowed
// number elsewhere.
func TestADependencyCarriesTheNumbersThatMeasureItAlone(t *testing.T) {
	started := time.Date(2026, 10, 13, 11, 0, 0, 0, time.UTC)
	first := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	second := first.Add(time.Minute)
	before := &FlowFacts{Queries: 100, QueryDuration: FlowHistogram{Buckets: make([]uint64, len(FlowQueryBounds)), Count: 100},
		DecisionLag: FlowHistogram{Buckets: make([]uint64, len(FlowDecisionBounds))}, RuntimeRedis: FlowHistogram{Buckets: make([]uint64, len(FlowRedisBounds))}}
	after := *before
	after.Queries = 400
	after.QueryFailures = map[string]uint64{"timeout": 6}
	after.QueryDuration = FlowHistogram{Buckets: cumulative(0, 0, 0, 300, 0, 0, 0, 0, 0, 0), Count: 400}
	after.RuntimeRedis = FlowHistogram{Buckets: cumulative(append([]uint64{120}, make([]uint64, len(FlowRedisBounds)-1)...)...), Count: 120}
	// The CMDB cache hangs on the pull too, and is measured by nothing.
	withCMDB := func(at time.Time, flow *FlowFacts) HealthResponse {
		health := flowHealth(at, started, flow)
		health.Dependencies = append(health.Dependencies, Endpoint{Role: EndpointCMDBCache, Address: "192.0.2.13:6379", Configured: true})
		return health
	}
	flows := newOverviewFlows()
	one := overviewOf(withCMDB(first, before), nil, false, first)
	flows.apply(&one, withCMDB(first, before), first)
	two := overviewOf(withCMDB(second, &after), nil, false, second)
	flows.apply(&two, withCMDB(second, &after), second)
	pull, state, output := flowStage(two, "pull"), flowStage(two, "state"), flowStage(two, "output")
	if cmdb := pull.Dependencies[1]; cmdb.Name != EndpointCMDBCache || cmdb.LatencyP95MS != nil || cmdb.FailuresPerMinute != nil {
		t.Fatalf("cmdb cache = %+v, want none of the query backend's numbers", cmdb)
	}
	// A third computation with no runtime Redis round trips since: the
	// state stage has no P95, and neither has the Redis.
	third := second.Add(time.Minute)
	idle := after
	idle.Queries = 500
	idle.QueryDuration = FlowHistogram{Buckets: cumulative(0, 0, 0, 400, 0, 0, 0, 0, 0, 0), Count: 500}
	three := overviewOf(withCMDB(third, &idle), nil, false, third)
	flows.apply(&three, withCMDB(third, &idle), third)
	if quiet := flowStage(three, "state"); quiet.LatencyP95MS.Measured || quiet.Dependencies[0].LatencyP95MS != nil {
		t.Fatalf("state with no round trips = %+v, want no P95 on the stage or the Redis", quiet)
	}
	backend := pull.Dependencies[0]
	if backend.LatencyP95MS == nil || *backend.LatencyP95MS != pull.LatencyP95MS || backend.FailuresPerMinute == nil ||
		backend.FailuresPerMinute.Value != 6 {
		t.Fatalf("query backend = %+v, want the pull's P95 and its 6 failures a minute", backend)
	}
	redisRow := state.Dependencies[0]
	if !state.LatencyP95MS.Measured || redisRow.LatencyP95MS == nil || *redisRow.LatencyP95MS != state.LatencyP95MS || redisRow.FailuresPerMinute != nil {
		t.Fatalf("state redis = %+v (stage P95 %+v), want the stage's P95 and no failures borrowed from admissions", redisRow, state.LatencyP95MS)
	}
	if kafka := output.Dependencies[0]; kafka.LatencyP95MS != nil || kafka.FailuresPerMinute != nil {
		t.Fatalf("output kafka = %+v, want no numbers: nothing measures it alone", kafka)
	}
	// Before two computations there is nothing to put on any dependency.
	if first := flowStage(one, "pull").Dependencies[0]; first.LatencyP95MS != nil || first.FailuresPerMinute != nil {
		t.Fatalf("first computation's query backend = %+v, want no numbers", first)
	}
}

// The Console's state reads its own word: an operation whose latest call
// failed is attention even when another answered later, and the row says
// which event source the link keys this deployment by.
func TestTheConsoleDependencyReadsTheConsolesOwnWord(t *testing.T) {
	recent, older := 1.0, 100.0
	health := overviewHealth()
	health.Dependencies = append(health.Dependencies, Endpoint{Role: EndpointLinkdConsole, Address: "http://198.51.100.8/api", Configured: true,
		LastSuccessAgeSeconds: &recent, LastFailureAgeSeconds: &older, LastFailure: "roster: status 502",
		Console: &LinkdConsoleFacts{State: LinkdConsoleUnreadable, Reason: "roster", EventSource: &LinkdEventSourceFacts{EventSourceID: "source-7"}}})
	console := flowStage(overviewOf(health, nil, false, time.Now()), "output").Dependencies[1]
	if console.State != OverviewAttention || console.ConsoleState != LinkdConsoleUnreadable || console.EventSourceID != "source-7" ||
		console.LastFailure != "roster: status 502" || console.Address != "http://198.51.100.8/api" {
		t.Fatalf("console = %+v, want attention from its own word, with its event source and latest failure", console)
	}
	for _, word := range []string{LinkdConsoleLinkUnhealthy} {
		health.Dependencies[3].Console.State = word
		if got := flowStage(overviewOf(health, nil, false, time.Now()), "output").Dependencies[1].State; got != OverviewAttention {
			t.Fatalf("console %s reads %s, want attention", word, got)
		}
	}
	health.Dependencies[3].Console.State = LinkdConsoleReachable
	if got := flowStage(overviewOf(health, nil, false, time.Now()), "output").Dependencies[1].State; got != OverviewOK {
		t.Fatalf("a reachable Console whose last failure is older than its last answer reads %s, want ok", got)
	}
}

// A replica whose histograms have another number of buckets than this
// process's bounds - an older build during the rollout that changed them -
// is not added in by position. An old replica whose ten decision-lag buckets
// put every decision in (10, 30] reads a P95 of 29 s against its own bounds;
// read against fifteen it would land at about 14.75 s and pass as measured.
// It is left out, said in coverage, and counted once it has rolled.
func TestAReplicaWithOtherBucketBoundsIsNotAddedByPosition(t *testing.T) {
	started := time.Date(2026, 10, 13, 11, 0, 0, 0, time.UTC)
	first := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	empty := func(n int) FlowHistogram { return FlowHistogram{Buckets: make([]uint64, n)} }
	// The previous build's decision-lag bounds: ten, with nothing between
	// 10 s and 30 s.
	const oldLagBuckets = 10
	facts := func(lagBuckets int, decisions uint64) *FlowFacts {
		lag := empty(lagBuckets)
		// Every decision past 10 s and within 30 s: the bucket for 30 s is
		// index 6 in the old bounds and index 9 in the new.
		thirty := 6
		if lagBuckets == len(FlowDecisionBounds) {
			thirty = 9
		}
		for index := thirty; index < lagBuckets; index++ {
			lag.Buckets[index] = decisions
		}
		lag.Count = decisions
		return &FlowFacts{QueryDuration: empty(len(FlowQueryBounds)), DecisionLag: lag, RuntimeRedis: empty(len(FlowRedisBounds)),
			Detections: decisions}
	}
	flows := newOverviewFlows()
	for step, old := range []bool{true, true, false, false} {
		at := first.Add(time.Duration(step) * time.Minute)
		lagBuckets := len(FlowDecisionBounds)
		if old {
			lagBuckets = oldLagBuckets
		}
		health := flowHealth(at, started, facts(lagBuckets, uint64(60*(step+1))))
		overview := overviewOf(health, nil, false, at)
		flows.apply(&overview, health, at)
		evaluate := flowStage(overview, "evaluate")
		switch step {
		case 1:
			// Old layout on both readings: not counted, and said why.
			if evaluate.LatencyP95MS.Measured || overview.FlowCoverage != (OverviewCoverage{Expected: 1, LayoutMismatched: 1}) ||
				overview.Verdict.State == OverviewOK {
				t.Fatalf("an old-layout replica = P95 %+v coverage %+v verdict %s, want unmeasured, mismatched and not OK",
					evaluate.LatencyP95MS, overview.FlowCoverage, overview.Verdict.State)
			}
		case 2:
			// Rolled between the readings: the pair spans two layouts.
			if evaluate.LatencyP95MS.Measured || overview.FlowCoverage.LayoutMismatched != 1 || overview.FlowCoverage.Counted != 0 {
				t.Fatalf("a replica read across its rollout = P95 %+v coverage %+v, want neither reading added", evaluate.LatencyP95MS, overview.FlowCoverage)
			}
		case 3:
			// Both readings in these bounds: counted, and the P95 is the
			// (25, 30] bucket's, not a position borrowed from another layout.
			if !evaluate.LatencyP95MS.Measured || overview.FlowCoverage != (OverviewCoverage{Counted: 1, Expected: 1}) ||
				evaluate.LatencyP95MS.Value <= 25000 || evaluate.LatencyP95MS.Value > 30000 {
				t.Fatalf("a rolled replica = P95 %+v coverage %+v, want counted with a P95 in (25 s, 30 s]", evaluate.LatencyP95MS, overview.FlowCoverage)
			}
		}
	}
}
