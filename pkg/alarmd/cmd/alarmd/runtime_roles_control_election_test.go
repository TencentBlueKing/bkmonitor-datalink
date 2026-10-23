// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
)

// The Control Leader priority election on the wiring production uses: each
// process opens its own role runtime, which writes the instance record the
// others read, and its own bundle, which contends for the lease on its
// reconcile tick. Leases are judged on the Redis server's clock, so the lease
// TTLs are scaled to real seconds (3 s TTL, 1 s renewal, a tenth of
// production); instance records are judged on the injected clock and keep the
// production 60 s TTL, which is both T and the handback window. The test
// drives every reconcile tick itself.

const electionPeriod = int64(300)

// electionProcess is one process of the fixture: its role runtime, its bundle
// and what it counts.
type electionProcess struct {
	name     string
	bundle   *phaseTwoWorkerBundle
	runtime  *phaseTwoRoleRuntime
	health   *phaseTwoApplicationHealth
	recorder *metric.Recorder
	alive    bool
}

type electionFixture struct {
	t           *testing.T
	ctx         context.Context
	address     string
	redis       *redis.Client
	clockMillis atomic.Int64
	base        int64
	uq          *httptest.Server
	prefix      string
	processes   []*electionProcess
	// controlLeaderTTL is the control lease TTL on the Redis clock. Short
	// where a case waits for a dead Leader's lease to run out; long where a
	// case moves the injected clock in large steps with no real time for
	// the renewals to restate the Leader's deadline on it.
	controlLeaderTTL time.Duration
	// configure, when set, edits every process's configuration last.
	configure func(*config.Config)
	// completed is, by evaluation time, the Query Groups a Slot completed
	// for, on any process.
	mu        sync.Mutex
	completed map[int64]map[string]struct{}
	refused   int
}

func newElectionFixture(t *testing.T, strategies int, prefix string) *electionFixture {
	t.Helper()
	address, redisClient := startPhaseTwoRedis(t)
	fixture := &electionFixture{t: t, ctx: context.Background(), address: address, redis: redisClient,
		base: phaseTwoQueryableSlotBoundary(), prefix: prefix, completed: map[int64]map[string]struct{}{},
		controlLeaderTTL: 3 * time.Second}
	fixture.clockMillis.Store(fixture.base * 1000)
	document, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, strategies)
	aggregations, fields := []string{"avg", "max", "min", "sum"}, []string{"usage", "idle"}
	for index := 0; index < strategies; index++ {
		id := 4001 + index
		strategy := bytes.Replace(document, []byte(`"id": 1001`), []byte(`"id": `+strconv.Itoa(id)), 1)
		strategy = bytes.Replace(strategy, []byte(`"agg_method": "avg"`), []byte(`"agg_method": "`+aggregations[index%4]+`"`), 1)
		strategy = bytes.Replace(strategy, []byte(`"metric_field": "usage"`), []byte(`"metric_field": "`+fields[(index/4)%2]+`"`), 1)
		strategy = bytes.Replace(strategy, []byte(`"query_md5": "g1-full-query-md5"`), []byte(`"query_md5": "election-`+strconv.Itoa(id)+`"`), 1)
		if err := redisClient.Set(fixture.ctx, "alarm-config.strategy_"+strconv.Itoa(id), strategy, 0).Err(); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, strconv.Itoa(id))
	}
	if err := redisClient.Set(fixture.ctx, "alarm-config.strategy_ids", "["+strings.Join(ids, ",")+"]", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(fixture.ctx, "alarm-config.last_updated", strconv.FormatInt(fixture.base-electionPeriod, 10), 0).Err(); err != nil {
		t.Fatal(err)
	}
	fixture.uq = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		var payload struct {
			EndTime string `json:"end_time"`
		}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		end, parseErr := strconv.ParseInt(payload.EndTime, 10, 64)
		if parseErr != nil || end <= 0 {
			end = fixture.clockMillis.Load() / 1000
		}
		_, _ = writer.Write([]byte(`{"series":[{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],` +
			`"group_keys":["host"],"group_values":["127.0.0.1"],"values":[[` + strconv.FormatInt((end-1)*1000, 10) + `,95]]}],` +
			`"status":null,"trace_id":"election","is_partial":false,"result_table_id":["system.cpu"]}`))
	}))
	t.Cleanup(fixture.uq.Close)
	return fixture
}

func (fixture *electionFixture) now() time.Time { return time.UnixMilli(fixture.clockMillis.Load()) }

// advance moves the injected clock and gives the real-clock lease renewals a
// beat to restate their deadlines on it, as production's would have.
func (fixture *electionFixture) advance(step time.Duration) {
	fixture.clockMillis.Add(step.Milliseconds())
}

func (fixture *electionFixture) observer() observability.Observer {
	return observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		if observation.ReasonCode == "VIEW_NOT_EXECUTABLE" {
			fixture.mu.Lock()
			fixture.refused++
			fixture.mu.Unlock()
			return
		}
		if observation.Stage != observability.StageSlotCompleted || observation.Trace.QueryGroupKey == "" {
			return
		}
		at := observation.Trace.EvaluationTime
		if at > 1e11 {
			at /= 1000
		}
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		if fixture.completed[at] == nil {
			fixture.completed[at] = map[string]struct{}{}
		}
		fixture.completed[at][observation.Trace.QueryGroupKey] = struct{}{}
	})
}

func (fixture *electionFixture) configFor(name string, selected roles.Set) config.Config {
	cfg := validGoAccessRuntimeConfig()
	cfg.Roles = selected
	cfg.Redis.Address = fixture.address
	withCompatibilityOutput(&cfg, fixture.address)
	cfg.Redis.StatePrefix = fixture.prefix
	cfg.HTTP.Listen = reserveBundleAddress()
	cfg.PhaseTwo.Worker.ID = name
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Second)
	// The test clock moves 5 s per tick, a reconcile round as production
	// runs it; the handback watch counts a reading more than two rounds
	// after the last as a break, so the interval has to be the tick.
	cfg.PhaseTwo.Control.ReconcileInterval = config.Duration(5 * time.Second)
	cfg.PhaseTwo.Access.UQEndpoint = fixture.uq.URL
	cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(60 * time.Second)
	cfg.PhaseTwo.Worker.RegistrationRenewInterval = config.Duration(time.Second)
	cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(fixture.controlLeaderTTL)
	cfg.PhaseTwo.Ownership.ControlLeaderRenewInterval = config.Duration(time.Second)
	cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(3 * time.Second)
	cfg.PhaseTwo.Ownership.LeaseRenewInterval = config.Duration(time.Second)
	if !selected.Has(roles.Worker) && len(selected) != 0 {
		cfg.PhaseTwo.Coordinator.MaxEvents++ // a control process's resources differ from a Worker's
	}
	if fixture.configure != nil {
		fixture.configure(&cfg)
	}
	return cfg
}

// open assembles a process the way startup does: the role runtime first,
// started on the bundle's health and clock, then the bundle on it.
func (fixture *electionFixture) open(name string, selected roles.Set, start bool) *electionProcess {
	t := fixture.t
	t.Helper()
	cfg := fixture.configFor(name, selected)
	recorder := metric.NewRecorder(metric.BuildInfo{})
	health := newPhaseTwoApplicationHealth()
	runtime, err := openPhaseTwoRoleRuntime(cfg, recorder, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime.now = fixture.now
	runtime.Start(context.Background(), health)
	t.Cleanup(func() { _ = runtime.Close() })
	bundle, err := openProductionPhaseTwoBundleWithDependencies(fixture.ctx, cfg, recorder,
		observability.Discard(observability.ComponentRuntime), health,
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			RoleRuntime: runtime, Now: fixture.now, HTTPClient: fixture.uq.Client(), AdditionalObserver: fixture.observer(),
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			}),
		})
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	process := &electionProcess{name: name, bundle: bundle, runtime: runtime, health: health, recorder: recorder, alive: true}
	t.Cleanup(func() { _ = bundle.Shutdown(context.Background()) })
	fixture.processes = append(fixture.processes, process)
	if start {
		fixture.start(process)
	}
	return process
}

func (fixture *electionFixture) start(process *electionProcess) {
	fixture.t.Helper()
	if err := process.bundle.Start(fixture.ctx); err != nil {
		fixture.t.Fatalf("%s Start: %v", process.name, err)
	}
	serveControlStreamForTest(process.bundle)
	process.runtime.writeInstance(fixture.ctx, process.health, "disabled", false)
}

// crash ends a process without a word: no lease is released, its record and
// registration stop being renewed, its stream stops answering.
func (fixture *electionFixture) crash(process *electionProcess) {
	withholdControlStreamForTest(process.bundle)
	process.bundle.cancelMaintain()
	// A process that crashes writes nothing more: its store connection goes
	// first, so the leaving record a stopping process writes never lands.
	_ = process.runtime.client.Close()
	_ = process.runtime.Close()
	process.alive = false
}

func (fixture *electionFixture) alive() []*electionProcess {
	var out []*electionProcess
	for _, process := range fixture.processes {
		if process.alive {
			out = append(out, process)
		}
	}
	return out
}

// renewRecords restates every live process's registrations on the injected
// clock, which the real-clock renewal loops do not follow.
func (fixture *electionFixture) renewRecords() {
	for _, process := range fixture.alive() {
		_ = process.bundle.register(fixture.ctx, ownership.WorkerReady)
		process.runtime.writeInstance(fixture.ctx, process.health, "disabled", false)
	}
}

// tick is one reconcile tick on every live process, in order, with the
// single-Leader invariant checked after each.
func (fixture *electionFixture) tick() {
	fixture.t.Helper()
	fixture.renewRecords()
	for _, process := range fixture.alive() {
		if err := process.bundle.refreshAndReconcile(fixture.ctx, false); err != nil {
			fixture.t.Logf("%s reconcile: %v", process.name, err)
		}
		fixture.assertOneLeaderAtMost()
	}
}

// leader names the live process that leads, and its epoch, after checking
// that no two do and that the one that does holds the lease Redis names.
func (fixture *electionFixture) leader() (*electionProcess, uint64) {
	fixture.t.Helper()
	var leading []*electionProcess
	for _, process := range fixture.alive() {
		process.bundle.mu.RLock()
		holds := process.bundle.controlLeader
		process.bundle.mu.RUnlock()
		if holds {
			leading = append(leading, process)
		}
	}
	if len(leading) > 1 {
		names := []string{}
		for _, process := range leading {
			names = append(names, process.name)
		}
		fixture.t.Fatalf("two Control Leaders at once: %v", names)
	}
	store, err := ownership.NewRedisStoreWithClient(fixture.redis, productionPhaseTwoPrefix(fixture.prefix, "ownership"))
	if err != nil {
		fixture.t.Fatal(err)
	}
	owner, live, err := store.ReadActiveControlLeader(fixture.ctx)
	if err != nil {
		fixture.t.Fatal(err)
	}
	if len(leading) == 0 {
		return nil, 0
	}
	if !live || owner.OwnerID != leading[0].name {
		fixture.t.Fatalf("%s believes it leads, the lease says %+v (live %v)", leading[0].name, owner, live)
	}
	return leading[0], owner.OwnerEpoch
}

func (fixture *electionFixture) assertOneLeaderAtMost() { fixture.leader() }

func (fixture *electionFixture) transitions(toClass, reason string) float64 {
	total := 0.0
	for _, process := range fixture.processes {
		total += counterValue(fixture.t, process.recorder, "bkmonitor_alarmd_control_leader_transitions_total",
			map[string]string{"to_class": toClass, "reason": reason})
	}
	return total
}

// pass runs the Slot of round r on every live Worker and returns how many
// Query Groups completed it. A dispatcher pass advances a Query Group by at
// most one Slot, so the Workers get a few passes inside the round, as their
// own scheduler ticks would give them; no reconcile tick runs between them.
func (fixture *electionFixture) pass(r int64, settled bool, want int) int {
	fixture.t.Helper()
	fixture.clockMillis.Store((fixture.base + r*electionPeriod + 1) * 1000)
	time.Sleep(1200 * time.Millisecond)
	fixture.tick()
	evaluation := fixture.base + (r-1)*electionPeriod
	completed := func() int {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		return len(fixture.completed[evaluation])
	}
	for attempt := 0; attempt < 4 && completed() < want; attempt++ {
		for _, process := range fixture.alive() {
			if !process.bundle.dependencies.Config.HasRole(roles.Worker) {
				continue
			}
			var err error
			if settled {
				err = runScheduledOnceSettled(fixture.ctx, process.bundle)
			} else {
				err = process.bundle.runScheduledOnce(fixture.ctx)
			}
			if err != nil {
				fixture.t.Logf("%s pass %d: %v", process.name, r, err)
			}
		}
	}
	return completed()
}

// balance ticks until the Workers own every Query Group between them in
// equal shares, which is where the rebalance writer stops moving them.
func (fixture *electionFixture) balance(queryGroups int) {
	fixture.t.Helper()
	for attempt := 0; attempt < 120; attempt++ {
		fixture.advance(time.Second)
		fixture.tick()
		time.Sleep(250 * time.Millisecond)
		var shares []int
		total := 0
		for _, process := range fixture.alive() {
			if !process.bundle.dependencies.Config.HasRole(roles.Worker) {
				continue
			}
			process.bundle.mu.RLock()
			shares = append(shares, len(process.bundle.runners))
			total += len(process.bundle.runners)
			process.bundle.mu.RUnlock()
		}
		even := len(shares) > 0
		for _, share := range shares {
			even = even && share == queryGroups/len(shares)
		}
		if total == queryGroups && even {
			return
		}
	}
	fixture.t.Fatalf("Workers never owned all %d Query Groups in equal shares", queryGroups)
}

// leaderClassSeen has the Leader write its control record and reads it the
// way the fleet view does (the record of the lease's current term, attributed
// through the Leader's instance record), returning the class it states. A
// Worker-only process serves the restricted public surface, which leaves the
// control facts out, so the view's own source is read rather than the route.
func (fixture *electionFixture) leaderClassSeen(leader *electionProcess) string {
	fixture.t.Helper()
	leader.bundle.dependencies.PublishFleet(fixture.ctx)
	registry, err := ownership.NewRedisStoreWithClient(fixture.redis, productionPhaseTwoPrefix(fixture.prefix, "ownership"))
	if err != nil {
		fixture.t.Fatal(err)
	}
	records, err := fleet.NewRedisStore(fixture.redis, productionPhaseTwoPrefix(fixture.prefix, "fleet"), time.Minute, 0)
	if err != nil {
		fixture.t.Fatal(err)
	}
	source, err := fleet.NewControlFactsSource(registry, records)
	if err != nil {
		fixture.t.Fatal(err)
	}
	reading, found, err := source.ControlFacts(fixture.ctx, fixture.now())
	if err != nil || !found || reading.Term != fleet.ControlTermCurrent || reading.Facts.Replica != leader.name {
		fixture.t.Fatalf("control facts as the view reads them = (%+v, %v, %v), want %s's record of this term", reading, found, err, leader.name)
	}
	return reading.Facts.CandidacyClass
}

// TestAWorkerLeadsWhileTheControlProcessIsGoneAndHandsBackOnceItIsSteady is
// the split form end to end: a control process, two Workers. The control
// process dies without releasing; no Worker takes the lease while its record
// is still live, one takes it on the first tick after, and the Workers keep
// completing every Slot. A new control process comes back; the fallback
// Leader keeps the lease until that process has been ready for a whole
// record lifetime, then gives it back once, and the control process takes it
// under the next epoch. No instant has two Leaders.
func TestAWorkerLeadsWhileTheControlProcessIsGoneAndHandsBackOnceItIsSteady(t *testing.T) {
	const queryGroups = 8
	fixture := newElectionFixture(t, queryGroups, "alarmd:phase-two:election-failover")
	control := fixture.open("election-control-1", roles.Set{roles.Control}, true)
	workers := []*electionProcess{
		fixture.open("election-worker-1", roles.Set{roles.Worker}, true),
		fixture.open("election-worker-2", roles.Set{roles.Worker}, true),
	}
	for _, worker := range workers {
		if worker.bundle.candidacy != ownership.ControlCandidacyFallback {
			t.Fatalf("%s candidacy = %q, want fallback", worker.name, worker.bundle.candidacy)
		}
	}
	leader, firstEpoch := fixture.leader()
	if leader != control || firstEpoch == 0 {
		t.Fatalf("leader after start = %v epoch %d, want the control process", leader, firstEpoch)
	}
	fixture.balance(queryGroups)
	if got := fixture.pass(1, true, queryGroups); got != queryGroups {
		t.Fatalf("Slot 0 with the control process up: %d of %d Query Groups completed", got, queryGroups)
	}

	// The control process dies. Its lease runs out on the Redis clock; its
	// record says ready until T after its last write.
	fixture.renewRecords()
	crashedAt := fixture.now()
	fixture.crash(control)
	time.Sleep(3500 * time.Millisecond)
	for _, offset := range []time.Duration{5 * time.Second, 30 * time.Second, 55 * time.Second} {
		fixture.clockMillis.Store(crashedAt.Add(offset).UnixMilli())
		fixture.tick()
		if leader, _ := fixture.leader(); leader != nil {
			t.Fatalf("%s took the lease %s after the control process died, while its record was still live", leader.name, offset)
		}
	}
	// The first tick past the record's lifetime: T plus at most one tick.
	fixture.clockMillis.Store(crashedAt.Add(controlCandidacyWindow(control.bundle.dependencies.Config) + time.Second).UnixMilli())
	fixture.tick()
	fallbackLeader, fallbackEpoch := fixture.leader()
	if fallbackLeader == nil || fallbackLeader == control || fallbackEpoch <= firstEpoch {
		t.Fatalf("leader one tick past T = %v epoch %d, want a Worker under an epoch after %d", fallbackLeader, fallbackEpoch, firstEpoch)
	}
	if got := fixture.transitions("fallback", "fallback_takeover"); got != 1 {
		t.Fatalf("fallback takeovers = %v, want 1", got)
	}
	if class := fixture.leaderClassSeen(fallbackLeader); class != ownership.ControlCandidacyFallback {
		t.Fatalf("leader class while a Worker leads = %q, want fallback", class)
	}
	// Every Worker finds the fallback Leader's view stream through its
	// instance record, as it found the control process's.
	waitPhaseTwoCondition(t, 15*time.Second, "Workers stream from the fallback Leader", func() bool {
		for _, worker := range workers {
			stats := worker.bundle.dependencies.ViewClient.Stats()
			if !stats.Connected || stats.Leader.WorkerID != fallbackLeader.name || stats.Leader.ControlEpoch != fallbackEpoch ||
				stats.Installed.ControlEpoch != fallbackEpoch {
				return false
			}
		}
		return true
	})
	if got := fixture.pass(2, false, queryGroups); got != queryGroups {
		t.Fatalf("Slot 1 across the takeover: %d of %d Query Groups completed", got, queryGroups)
	}

	// A control process comes back. It cannot take a live lease, and the
	// fallback Leader keeps it until the window has passed.
	returned := fixture.open("election-control-2", roles.Set{roles.Control}, true)
	if leader, epoch := fixture.leader(); leader != fallbackLeader || epoch != fallbackEpoch {
		t.Fatalf("leader after the control process returned = %v epoch %d, want %s still", leader, epoch, fallbackLeader.name)
	}
	fixture.tick()
	firstSeen := fixture.now()
	window := controlCandidacyWindow(returned.bundle.dependencies.Config)
	// One reconcile round at a time, as production reads: rounds skipped
	// would read as a break in the window and start it again.
	for offset := 5 * time.Second; offset < window; offset += 5 * time.Second {
		fixture.clockMillis.Store(firstSeen.Add(offset).UnixMilli())
		time.Sleep(1200 * time.Millisecond)
		fixture.tick()
		if leader, epoch := fixture.leader(); leader != fallbackLeader || epoch != fallbackEpoch {
			t.Fatalf("leader %s into the window = %v epoch %d, want the fallback still", offset, leader, epoch)
		}
	}
	fixture.clockMillis.Store(firstSeen.Add(window).UnixMilli())
	time.Sleep(1200 * time.Millisecond)
	fixture.tick()
	if fixture.transitions("preferred", "handback") != 1 {
		t.Fatalf("handbacks once the window passed = %v, want 1", fixture.transitions("preferred", "handback"))
	}
	if leader, _ := fixture.leader(); leader == fallbackLeader {
		t.Fatal("the fallback still leads after handing back")
	}
	fixture.advance(time.Second)
	fixture.tick()
	preferredLeader, preferredEpoch := fixture.leader()
	if preferredLeader != returned || preferredEpoch <= fallbackEpoch {
		t.Fatalf("leader after the handback = %v epoch %d, want %s under an epoch after %d",
			preferredLeader, preferredEpoch, returned.name, fallbackEpoch)
	}
	if class := fixture.leaderClassSeen(returned); class != ownership.ControlCandidacyPreferred {
		t.Fatalf("leader class after the handback = %q, want preferred", class)
	}
	if got := fixture.pass(3, false, queryGroups); got != queryGroups {
		t.Fatalf("Slot 2 across the handback: %d of %d Query Groups completed", got, queryGroups)
	}

	// Steady from here: no further moves of the lease.
	for step := 0; step < 6; step++ {
		fixture.advance(20 * time.Second)
		time.Sleep(1100 * time.Millisecond)
		fixture.tick()
	}
	if leader, epoch := fixture.leader(); leader != returned || epoch != preferredEpoch {
		t.Fatalf("leader after two quiet minutes = %v epoch %d, want %s epoch %d", leader, epoch, returned.name, preferredEpoch)
	}
	if fixture.transitions("preferred", "handback") != 1 || fixture.transitions("fallback", "fallback_takeover") != 1 ||
		fixture.transitions("preferred", "acquire_free") != 2 {
		t.Fatalf("transitions = takeover %v handback %v acquire_free %v, want 1, 1, 2",
			fixture.transitions("fallback", "fallback_takeover"), fixture.transitions("preferred", "handback"),
			fixture.transitions("preferred", "acquire_free"))
	}
	if got := fixture.pass(4, false, queryGroups); got != queryGroups {
		t.Fatalf("Slot 3 after the election settled: %d of %d Query Groups completed", got, queryGroups)
	}
	fixture.mu.Lock()
	refused := fixture.refused
	fixture.mu.Unlock()
	// Nothing was gated on who led: no Slot was refused by the view gate
	// through the takeover or the handback.
	if refused != 0 {
		t.Fatalf("view gate refusals over the whole run = %d, want 0", refused)
	}
}

// flappingPreferred is a control process's role runtime alone, whose
// candidacy readiness the test moves: what a fallback Leader reads of a
// control process that keeps coming and going.
func (fixture *electionFixture) flappingPreferred(name string) (*phaseTwoRoleRuntime, *phaseTwoApplicationHealth) {
	fixture.t.Helper()
	cfg := fixture.configFor(name, roles.Set{roles.Control})
	runtime, err := openPhaseTwoRoleRuntime(cfg, metric.NewRecorder(metric.BuildInfo{}), nil, nil)
	if err != nil {
		fixture.t.Fatal(err)
	}
	runtime.now = fixture.now
	fixture.t.Cleanup(func() { _ = runtime.Close() })
	return runtime, newPhaseTwoApplicationHealth()
}

// TestAControlProcessThatKeepsFlappingNeverTakesTheLeaseBack drives a
// fallback Leader against a control process whose readiness keeps changing
// within the window: no handback, no move of the lease at all. Once it stays
// ready for the window the lease is handed back once, and while that record
// says ready no Worker takes the free lease again; when the record lapses a
// Worker does.
func TestAControlProcessThatKeepsFlappingNeverTakesTheLeaseBack(t *testing.T) {
	fixture := newElectionFixture(t, 2, "alarmd:phase-two:election-flap")
	fixture.controlLeaderTTL = time.Hour
	first := fixture.open("flap-worker-1", roles.Set{roles.Worker}, true)
	fixture.open("flap-worker-2", roles.Set{roles.Worker}, true)
	fallbackLeader, fallbackEpoch := fixture.leader()
	if fallbackLeader != first {
		t.Fatalf("leader with no control process = %v, want the first Worker to start", fallbackLeader)
	}
	flapping, flappingHealth := fixture.flappingPreferred("flap-control")
	write := func(ready bool) {
		flappingHealth.candidacyReady.Store(ready)
		flapping.writeInstance(fixture.ctx, flappingHealth, "disabled", false)
	}
	// Ready for 40 s, not ready for 10 s, over and over for five minutes:
	// never a whole window of readiness.
	for second := 0; second < 300; second += 5 {
		fixture.advance(5 * time.Second)
		write(second%50 < 40)
		fixture.tick()
		if leader, epoch := fixture.leader(); leader != fallbackLeader || epoch != fallbackEpoch {
			t.Fatalf("leader at +%ds of flapping = %v epoch %d, want %s epoch %d unchanged", second, leader, epoch, fallbackLeader.name, fallbackEpoch)
		}
	}
	if got := fixture.transitions("preferred", "handback"); got != 0 {
		t.Fatalf("handbacks while flapping = %v, want 0", got)
	}
	// Steady for the window: handed back, once.
	for second := 0; second <= 65; second += 5 {
		fixture.advance(5 * time.Second)
		write(true)
		fixture.tick()
	}
	if got := fixture.transitions("preferred", "handback"); got != 1 {
		t.Fatalf("handbacks after a steady window = %v, want 1", got)
	}
	if leader, _ := fixture.leader(); leader != nil {
		t.Fatalf("%s took the lease back while the control process's record says ready", leader.name)
	}
	// The control process never takes it, and its record lapses: a Worker
	// takes it again after T, under a later epoch.
	fixture.advance(controlCandidacyWindow(first.bundle.dependencies.Config) + time.Second)
	fixture.tick()
	again, againEpoch := fixture.leader()
	if again == nil || againEpoch <= fallbackEpoch {
		t.Fatalf("leader after the control record lapsed = %v epoch %d, want a Worker after epoch %d", again, againEpoch, fallbackEpoch)
	}
	if got := fixture.transitions("fallback", "fallback_takeover"); got != 2 {
		t.Fatalf("fallback takeovers = %v, want 2", got)
	}
}

// TestWorkersRacingForAFreeLeaseEndWithOneLeader starts three Workers at
// once with nobody leading, then kills the one that won and has the other
// two contend on every tick at the same time.
func TestWorkersRacingForAFreeLeaseEndWithOneLeader(t *testing.T) {
	fixture := newElectionFixture(t, 2, "alarmd:phase-two:election-race")
	processes := []*electionProcess{
		fixture.open("race-worker-1", roles.Set{roles.Worker}, false),
		fixture.open("race-worker-2", roles.Set{roles.Worker}, false),
		fixture.open("race-worker-3", roles.Set{roles.Worker}, false),
	}
	var started sync.WaitGroup
	for _, process := range processes {
		started.Add(1)
		go func(process *electionProcess) {
			defer started.Done()
			if err := process.bundle.Start(fixture.ctx); err != nil {
				t.Errorf("%s Start: %v", process.name, err)
			}
		}(process)
	}
	started.Wait()
	for _, process := range processes {
		serveControlStreamForTest(process.bundle)
	}
	winner, firstEpoch := fixture.leader()
	if winner == nil || fixture.transitions("fallback", "fallback_takeover") != 1 {
		t.Fatalf("after three Workers started at once: leader %v, takeovers %v; want exactly one", winner,
			fixture.transitions("fallback", "fallback_takeover"))
	}
	fixture.crash(winner)
	time.Sleep(3500 * time.Millisecond)
	fixture.advance(controlCandidacyWindow(winner.bundle.dependencies.Config))
	for round := 0; round < 5; round++ {
		fixture.advance(time.Second)
		fixture.renewRecords()
		var ticked sync.WaitGroup
		for _, process := range fixture.alive() {
			ticked.Add(1)
			go func(process *electionProcess) {
				defer ticked.Done()
				_ = process.bundle.refreshAndReconcile(fixture.ctx, false)
			}(process)
		}
		ticked.Wait()
		fixture.assertOneLeaderAtMost()
	}
	second, secondEpoch := fixture.leader()
	if second == nil || second == winner || secondEpoch <= firstEpoch {
		t.Fatalf("after the winner died: leader %v epoch %d, want another Worker after epoch %d", second, secondEpoch, firstEpoch)
	}
	if got := fixture.transitions("fallback", "fallback_takeover"); got != 2 {
		t.Fatalf("fallback takeovers = %v, want 2: one per free lease", got)
	}
}

// TestTheDefaultFormElectsAsBeforeButNeverAProcessThatIsStartingOrStopping
// runs the all-roles form: every process is preferred, the first to finish
// starting leads, one that has not finished starting or is draining never
// takes a free lease, and a successor takes a released lease on its next
// tick rather than its next refresh.
func TestTheDefaultFormElectsAsBeforeButNeverAProcessThatIsStartingOrStopping(t *testing.T) {
	fixture := newElectionFixture(t, 2, "alarmd:phase-two:election-default")
	first := fixture.open("default-1", nil, false)
	if first.bundle.candidacy != ownership.ControlCandidacyPreferred {
		t.Fatalf("candidacy in the default form = %q, want preferred", first.bundle.candidacy)
	}
	// Assembled but not started: it does not contend. Bounded, because an
	// unstarted process that did take the lease has no maintenance context
	// to run the term under and would not return.
	contended := make(chan string, 1)
	go func() {
		leader, err := first.bundle.tryAcquireControlLeader(fixture.ctx)
		if err != nil || leader {
			contended <- fmt.Sprintf("leader %v error %v", leader, err)
			return
		}
		contended <- ""
	}()
	select {
	case outcome := <-contended:
		if outcome != "" {
			t.Fatalf("an unstarted process contended: %s", outcome)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("an unstarted process contended and did not return")
	}
	if leader, _ := fixture.leader(); leader != nil {
		t.Fatalf("%s leads before any process finished starting", leader.name)
	}
	first.runtime.writeInstance(fixture.ctx, first.health, "disabled", false)
	if record, _, _ := first.runtime.store.ReadInstance(fixture.ctx, first.name); record.ControlCandidacy != ownership.ControlCandidacyPreferred ||
		record.ControlCandidacyReady {
		t.Fatalf("record of a process still starting = %+v, want preferred and not ready", record)
	}
	fixture.start(first)
	if leader, _ := fixture.leader(); leader != first {
		t.Fatalf("leader once the first process started = %v", leader)
	}
	// It led at once although it had not been ready for a window: with no
	// fallback candidate in this form there is nobody to wait for.
	if first.bundle.controlCandidacyStable(fixture.now()) {
		t.Fatal("the first process reads as stable the moment it started")
	}
	if record, _, _ := first.runtime.store.ReadInstance(fixture.ctx, first.name); !record.ControlCandidacyReady {
		t.Fatalf("record of a started process = %+v, want ready", record)
	}
	second := fixture.open("default-2", nil, true)
	third := fixture.open("default-3", nil, true)
	if leader, _ := fixture.leader(); leader != first {
		t.Fatalf("leader after two more started = %v, want the first still", leader)
	}
	// The second is stopping when the lease comes free: it does not take it.
	if err := second.bundle.beginDraining(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	second.runtime.writeInstance(fixture.ctx, second.health, "disabled", false)
	if record, _, _ := second.runtime.store.ReadInstance(fixture.ctx, second.name); record.ControlCandidacyReady {
		t.Fatalf("record of a draining process = %+v, want not ready", record)
	}
	if err := first.bundle.Shutdown(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	first.alive = false
	if got := counterValue(t, first.recorder, "bkmonitor_alarmd_control_leader_transitions_total",
		map[string]string{"to_class": "none", "reason": "release"}); got != 1 {
		t.Fatalf("releases counted at shutdown = %v, want 1", got)
	}
	if acquired, err := second.bundle.tryAcquireControlLeader(fixture.ctx); err != nil || acquired {
		t.Fatalf("a draining process took the free lease: %v %v", acquired, err)
	}
	// The third takes it on a reconcile tick, not a refresh tick.
	if err := third.bundle.refreshAndReconcile(fixture.ctx, false); err != nil {
		t.Fatal(err)
	}
	second.alive = false
	if leader, _ := fixture.leader(); leader != third {
		t.Fatalf("leader one reconcile tick after the release = %v, want %s", leader, third.name)
	}
	if got := fixture.transitions("preferred", "acquire_free"); got != 2 {
		t.Fatalf("preferred acquisitions = %v, want 2", got)
	}
	if got := fixture.transitions("fallback", "fallback_takeover"); got != 0 {
		t.Fatalf("fallback takeovers in the default form = %v, want 0", got)
	}
}

// TestAWorkerLeadingAsTheFallbackRunsTheDeletedStrategyClose configures the
// alert link's Console on two Workers and no control process. Closing the
// alerts of a deleted strategy is core control, so both Workers wire the loop;
// the one that holds the lease runs its rounds as the Leader - it reads the
// link's roster and decides on the deleted strategy the roster still lists -
// and the other counts its rounds as not_leader.
func TestAWorkerLeadingAsTheFallbackRunsTheDeletedStrategyClose(t *testing.T) {
	fixture := newElectionFixture(t, 2, "alarmd:phase-two:election-absent-close")
	// The lifecycle fixture's Console, naming the runtime redis-server as
	// the link's target, with the one route it does not answer: the roster,
	// which lists a strategy the strategy cache no longer has.
	link := lifecycleConsole(fixture.address, fixture.redis)
	target := openalerts.TargetBinding{EventSourceID: "source", HookName: "active", KeyPrefix: lifecycleLinkPrefix,
		Address: fixture.address, Database: 0, Sources: []string{"source"}}
	const deletedTenant, deletedStrategy = "system", "9999"
	var rosterReads atomic.Int64
	console := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/local-api/strategy-index/browse" {
			link(w, r)
			return
		}
		rosterReads.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"target": target, "nextCursor": nil, "phase": "sets",
			"health": map[string]any{"lastSuccess": fixture.now().Format(time.RFC3339Nano), "lastAttempt": nil, "error": nil, "pendingCount": 0},
			"rows": []any{map[string]any{"tenantId": deletedTenant, "strategyId": deletedStrategy,
				"key": lifecycleLinkPrefix + ":" + deletedTenant + ":" + deletedStrategy, "members": 1}}})
	}))
	t.Cleanup(console.Close)
	fixture.configure = func(cfg *config.Config) {
		cfg.PhaseTwo.Linkd.ConsoleURL = console.URL
		cfg.PhaseTwo.Linkd.Username, cfg.PhaseTwo.Linkd.Password = "user", "secret"
	}
	leading := fixture.open("absent-worker-1", roles.Set{roles.Worker}, true)
	following := fixture.open("absent-worker-2", roles.Set{roles.Worker}, true)
	if leader, _ := fixture.leader(); leader != leading || leading.bundle.candidacy != ownership.ControlCandidacyFallback {
		t.Fatalf("leader with no control process = %v (candidacy %q), want %s as the fallback", leader, leading.bundle.candidacy, leading.name)
	}
	for _, process := range []*electionProcess{leading, following} {
		if process.bundle.dependencies.RunAbsentClose == nil {
			t.Fatalf("%s, a fallback candidate with the Console configured, has no deleted-strategy close loop", process.name)
		}
	}
	outcome := func(process *electionProcess, word string) float64 {
		return counterValue(t, process.recorder, "bkmonitor_alarmd_absent_strategy_close_total", map[string]string{"outcome": word})
	}
	rounds := func(process *electionProcess) float64 {
		total := 0.0
		for _, refusal := range absentalerts.Refusals {
			total += counterValue(t, process.recorder, "bkmonitor_alarmd_absent_strategy_round_total", map[string]string{"disposition": refusal})
		}
		return total
	}
	// Each loop takes its first round when the process starts; the next is
	// five minutes away. Both have run by the time the counts move.
	waitPhaseTwoCondition(t, 10*time.Second, "the Leader's first deleted-strategy round", func() bool {
		return rounds(leading) >= 1 && outcome(following, absentalerts.OutcomeNotLeader) >= 1
	})
	if got := outcome(leading, absentalerts.OutcomeNotLeader); got != 0 {
		t.Fatalf("the fallback Leader counted %v rounds as not_leader", got)
	}
	if rosterReads.Load() == 0 {
		t.Fatal("the fallback Leader's round never read the link's roster")
	}
	if got := counterValue(t, leading.recorder, "bkmonitor_alarmd_absent_strategy_difference", map[string]string{"side": "roster_strategies"}); got != 1 {
		t.Fatalf("roster strategies the Leader's round read = %v, want the deleted strategy", got)
	}
	if decided := outcome(leading, absentalerts.OutcomeWithinGrace) + outcome(leading, absentalerts.OutcomeCloseDecided); decided < 1 {
		t.Fatalf("the deleted strategy was not decided on by the Leader's round: round %v, within_grace %v, close_decided %v",
			rounds(leading), outcome(leading, absentalerts.OutcomeWithinGrace), outcome(leading, absentalerts.OutcomeCloseDecided))
	}
	if got := rounds(following); got != 0 {
		t.Fatalf("the Worker that does not lead ran %v rounds of the difference", got)
	}
}

// TestAControlProcessInACrashLoopLeavesTheLeaseToAFallbackUntilItIsSteady
// is the split form with a control process that restarts every 20 s, each
// time a new incarnation, and two Workers. A control process that has just
// started holds no fallback off and does not take a free lease while a ready
// fallback is there, so a Worker leads from the first tick the lease is free
// and keeps leading through the restarts, and the Workers keep completing
// every Slot. When the control process stops restarting, the fallback hands
// the lease back once, after it has been ready for the window, and the
// control process takes it under the next epoch. No instant has two Leaders.
func TestAControlProcessInACrashLoopLeavesTheLeaseToAFallbackUntilItIsSteady(t *testing.T) {
	const queryGroups, restartEvery = 4, 20 * time.Second
	fixture := newElectionFixture(t, queryGroups, "alarmd:phase-two:election-crash-loop")
	const controlName = "crash-loop-control"
	control := fixture.open(controlName, roles.Set{roles.Control}, true)
	workers := []*electionProcess{
		fixture.open("crash-loop-worker-1", roles.Set{roles.Worker}, true),
		fixture.open("crash-loop-worker-2", roles.Set{roles.Worker}, true),
	}
	leader, firstEpoch := fixture.leader()
	if leader != control {
		t.Fatalf("leader after start = %v, want the control process", leader)
	}
	fixture.balance(queryGroups)
	if got := fixture.pass(1, true, queryGroups); got != queryGroups {
		t.Fatalf("Slot 0 with the control process up: %d of %d Query Groups completed", got, queryGroups)
	}
	// One round at a time on the injected clock, with a beat of real time
	// for the lease renewals to restate their deadlines on it.
	round := func() {
		fixture.advance(5 * time.Second)
		time.Sleep(1200 * time.Millisecond)
		fixture.tick()
	}
	restart := func() {
		fixture.crash(control)
		control = fixture.open(controlName, roles.Set{roles.Control}, true)
	}

	// The control process dies holding the lease, and its next incarnation
	// starts once the lease has run out on the Redis clock: it finds the
	// lease free, has not been ready for a window, and a ready fallback is
	// there, so it leaves the lease to the fallback.
	fixture.renewRecords()
	fixture.crash(control)
	time.Sleep(3500 * time.Millisecond)
	control = fixture.open(controlName, roles.Set{roles.Control}, true)
	if leader, _ := fixture.leader(); leader != nil {
		t.Fatalf("%s took the free lease on starting; a control process not yet stable waits for a ready fallback", leader.name)
	}
	round()
	fallbackLeader, fallbackEpoch := fixture.leader()
	if fallbackLeader == nil || fallbackLeader == control || fallbackEpoch <= firstEpoch {
		t.Fatalf("leader one round after the lease came free = %v epoch %d, want a Worker after epoch %d", fallbackLeader, fallbackEpoch, firstEpoch)
	}
	sinceRestart := 5 * time.Second
	for restarts := 0; restarts < 5; restarts++ {
		for ; sinceRestart < restartEvery; sinceRestart += 5 * time.Second {
			round()
			if leader, epoch := fixture.leader(); leader != fallbackLeader || epoch != fallbackEpoch {
				t.Fatalf("leader while the control process keeps restarting = %v epoch %d, want %s epoch %d",
					leader, epoch, fallbackLeader.name, fallbackEpoch)
			}
		}
		restart()
		sinceRestart = 0
	}
	if got := fixture.pass(2, false, queryGroups); got != queryGroups {
		t.Fatalf("Slot 1 while the control process keeps restarting: %d of %d Query Groups completed", got, queryGroups)
	}
	// The pass moved the clock a whole period; a restart now starts the
	// window again rather than letting the jump count as readiness.
	restart()
	if fixture.transitions("preferred", "handback") != 0 {
		t.Fatalf("handbacks while the control process kept restarting = %v, want 0", fixture.transitions("preferred", "handback"))
	}

	// It stops restarting. The fallback keeps the lease until the control
	// process has been ready for the window, then gives it back once.
	window := controlCandidacyWindow(control.bundle.dependencies.Config)
	for elapsed := time.Duration(0); fixture.transitions("preferred", "handback") == 0; elapsed += 5 * time.Second {
		if elapsed > window+30*time.Second {
			t.Fatalf("no handback %s after the control process stopped restarting", elapsed)
		}
		round()
		if fixture.transitions("preferred", "handback") == 0 && elapsed+5*time.Second < window {
			if leader, epoch := fixture.leader(); leader != fallbackLeader || epoch != fallbackEpoch {
				t.Fatalf("leader %s into the window = %v epoch %d, want the fallback still", elapsed, leader, epoch)
			}
		}
	}
	round()
	preferredLeader, preferredEpoch := fixture.leader()
	if preferredLeader != control || preferredEpoch <= fallbackEpoch {
		t.Fatalf("leader after the handback = %v epoch %d, want %s after epoch %d", preferredLeader, preferredEpoch, control.name, fallbackEpoch)
	}
	for step := 0; step < 4; step++ {
		round()
	}
	if leader, epoch := fixture.leader(); leader != control || epoch != preferredEpoch {
		t.Fatalf("leader after the election settled = %v epoch %d, want %s epoch %d", leader, epoch, control.name, preferredEpoch)
	}
	if fixture.transitions("preferred", "handback") != 1 || fixture.transitions("fallback", "fallback_takeover") != 1 {
		t.Fatalf("transitions = takeover %v handback %v, want 1 and 1",
			fixture.transitions("fallback", "fallback_takeover"), fixture.transitions("preferred", "handback"))
	}
	if got := fixture.pass(3, false, queryGroups); got != queryGroups {
		t.Fatalf("Slot 2 after the handback: %d of %d Query Groups completed", got, queryGroups)
	}
	for _, worker := range workers {
		if !worker.alive {
			t.Fatalf("%s did not stay up", worker.name)
		}
	}
}
