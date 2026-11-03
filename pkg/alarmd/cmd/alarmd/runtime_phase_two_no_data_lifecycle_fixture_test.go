// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
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
)

// The lifecycle fixture runs the production bundle over a real redis-server
// and can put a new bundle - a new process, for everything the process keeps
// in memory - on the same server between any two rounds: under the same
// Worker identity for a restart, under another for a handover. What must
// survive is what the server holds; anything a case reads after the swap
// came out of the server, because the bundle that wrote it is gone.
//
// The strategy is compiled from its stored document by the production
// compiler, the query answers come from a fake query service the case
// steers round by round, and the event sink is the one the bundles share,
// as the broker outlives the processes that write to it.

const (
	lifecycleHostA      = "192.0.2.1"
	lifecycleHostB      = "192.0.2.2"
	lifecycleStrategyID = "1001"
	// lifecycleInterval is the item's aggregation interval, which is the
	// period no-data is judged at.
	lifecycleInterval = int64(60)
	// lifecycleWatchdog is how long the follows-output sink holds a write
	// whose gate a case never opens, before letting it go. No wait on the
	// bundle is bounded by it: those run to the test's deadline (testwait).
	lifecycleWatchdog = 10 * time.Second
)

// lifecycleStrategy is what the stored strategy says about no-data and its
// target. The rest is the threshold fixture as the platform writes it.
type lifecycleStrategy struct {
	revision   int
	continuous int
	// targets are the static hosts of the old-form target; nil leaves the
	// item without one. dimensions are the no-data dimensions.
	targets    []string
	dimensions []string
	// horizon is the item's own tracking horizon in seconds, written into
	// the no-data section; zero inherits the deployment's.
	horizon int64
	// noDataOff leaves the no-data section out.
	noDataOff bool
	// unrevisioned leaves strategy_revision out of the document, as the
	// platform's legacy document has none.
	unrevisioned bool
	// edit is applied to the item last, for a target shape the fields above
	// do not express.
	edit func(item map[string]any)
	// statusSetter, when set, is every level's recovery_config.status_setter.
	statusSetter string
	// disableRecovery writes every level's recovery_config.disable_recovery
	// as true.
	disableRecovery bool
	// global makes it a global business strategy (is_global_strategy).
	global bool
}

type lifecycleFixture struct {
	t       *testing.T
	address string
	redis   *redis.Client
	// published are the alerts publishOpenAlert made active in the link.
	published []string
	cfg       config.Config
	base      int64
	interval  int64
	clock     *atomic.Int64
	sink      *followsSink
	retry     time.Duration

	// What the query service answers: the hosts whose series arrive, the
	// value they carry, and whether the answer is partial. byHostID names a
	// series by bk_host_id rather than by address and cloud.
	mu        sync.Mutex
	reporting []string
	value     float64
	partial   bool
	byHostID  bool
	// intercept, when set, sees each query before it is answered and
	// answers it itself by returning true: a case that holds a query on a
	// gate, or fails it, sets it with interceptQueries.
	intercept func(http.ResponseWriter, *http.Request) bool

	bundle     *phaseTwoWorkerBundle
	runner     phaseTwoQueryGroupRuntime
	queryGroup execution.QueryGroupIdentity
	workerID   string
	// queryGroups is how many Query Groups a bundle the fixture opens owns
	// once it is ready; zero is one. runner and queryGroup are the first of
	// them in identity order.
	queryGroups int

	observationsMu sync.Mutex
	observations   []observability.Observation
}

// startLifecycleFixture installs the strategy and the host records and opens
// the first bundle under Worker identity worker-0.
func startLifecycleFixture(t *testing.T, strategy lifecycleStrategy, hostRecords map[string]string) *lifecycleFixture {
	t.Helper()
	return startLifecycleFixtureWith(t, strategy, hostRecords, nil)
}

// startLifecycleFixtureWith is the fixture with the deployment's
// configuration adjusted by configure before the first bundle opens.
func startLifecycleFixtureWith(t *testing.T, strategy lifecycleStrategy, hostRecords map[string]string,
	configure func(*config.Config)) *lifecycleFixture {
	t.Helper()
	address, client := startPhaseTwoRedis(t)
	return startLifecycleFixtureOn(t, address, client, strategy, hostRecords, configure)
}

// startLifecycleFixtureOn is the fixture on a redis-server the case started,
// for a case that writes what the first bundle reads at its start.
func startLifecycleFixtureOn(t *testing.T, address string, client *redis.Client, strategy lifecycleStrategy,
	hostRecords map[string]string, configure func(*config.Config)) *lifecycleFixture {
	t.Helper()
	fixture := &lifecycleFixture{t: t, address: address, redis: client, interval: lifecycleInterval,
		clock: &atomic.Int64{}, sink: &followsSink{mode: followsOK}, reporting: []string{lifecycleHostA}, value: 5}
	base := time.Now().Unix()
	base += fixture.interval - base%fixture.interval
	fixture.base = base
	fixture.clock.Store(base * 1000)

	uqServer := httptest.NewServer(http.HandlerFunc(fixture.answerQuery))
	t.Cleanup(uqServer.Close)

	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd-no-data-lifecycle"
	withCompatibilityOutput(&cfg, address)
	cfg.PhaseTwo.Output.Protocol = config.OutputProtocolNative
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(6 * time.Hour)
	cfg.PhaseTwo.Worker.RegistrationRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(6 * time.Hour)
	cfg.PhaseTwo.Ownership.ControlLeaderRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(6 * time.Hour)
	cfg.PhaseTwo.Ownership.LeaseRenewInterval = config.Duration(time.Minute)
	// The link's control plane a deployment on the consumer's protocol has.
	// Without it the open alert copy is not configured and asks nothing, so
	// the cases that publish an open alert and wait for it to be read need it.
	link := httptest.NewServer(lifecycleLink(client))
	t.Cleanup(link.Close)
	cfg.PhaseTwo.Linkd.ControlPlane = config.LinkdControlPlaneConfig{URL: link.URL, JWTSecret: "example-secret"}
	if configure != nil {
		configure(&cfg)
	}
	fixture.cfg = cfg
	fixture.retry = cfg.PhaseTwo.Scheduler.RetryMinDelay.Duration()

	ctx := context.Background()
	if hostRecords == nil {
		hostRecords = lifecycleHostRecords("")
	}
	// Under the strategy's tenant's CMDB key, where its Plans read the hosts,
	// and under the default tenant's, which the replica waits on to be ready.
	for _, hosts := range []string{cfg.PlatformKeyPrefix() + ".cache.cmdb.host", "tenant-a." + cfg.PlatformKeyPrefix() + ".cache.cmdb.host"} {
		for field, record := range hostRecords {
			if err := client.HSet(ctx, hosts, field, record).Err(); err != nil {
				t.Fatal(err)
			}
		}
	}
	fixture.install(strategy)
	fixture.open("alarmd-worker-0", uqServer.Client())
	return fixture
}

// lifecycleHostRecords are host A and host B of the strategy's business, each
// under its address and its identifier, with topoLink as their topology when
// one is given.
func lifecycleHostRecords(topoLink string) map[string]string {
	records := map[string]string{}
	for id, ip := range map[int]string{1: lifecycleHostA, 2: lifecycleHostB} {
		record := `{"bk_host_id":` + strconv.Itoa(id) + `,"bk_host_innerip":"` + ip + `","bk_cloud_id":0,"bk_biz_id":2`
		if topoLink != "" {
			record += `,"topo_link":` + topoLink
		}
		record += "}"
		records[ip+"|0"] = record
		records[strconv.Itoa(id)] = record
	}
	return records
}

// answerQuery is the query service: one series per reporting host, at the
// window's last second, with the fixture's value; partial when the case says.
func (fixture *lifecycleFixture) answerQuery(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	fixture.mu.Lock()
	intercept := fixture.intercept
	fixture.mu.Unlock()
	if intercept != nil && intercept(writer, request) {
		return
	}
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
	fixture.mu.Lock()
	reporting, value, partial, byHostID := append([]string(nil), fixture.reporting...), fixture.value, fixture.partial, fixture.byHostID
	fixture.mu.Unlock()
	series := make([]string, 0, len(reporting))
	for _, host := range reporting {
		group := `"group_keys":["bk_target_cloud_id","bk_target_ip"],"group_values":["0","` + host + `"]`
		if byHostID {
			group = `"group_keys":["bk_host_id"],"group_values":["` + host + `"]`
		}
		series = append(series, `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],`+
			group+`,"values":[[`+strconv.FormatInt((end-1)*1000, 10)+`,`+strconv.FormatFloat(value, 'f', -1, 64)+`]]}`)
	}
	_, _ = writer.Write([]byte(`{"series":[` + strings.Join(series, ",") + `],"status":null,"trace_id":"no-data-lifecycle",` +
		`"is_partial":` + strconv.FormatBool(partial) + `,"result_table_id":["system.cpu"]}`))
}

// interceptQueries puts intercept in front of the query service from now on;
// nil takes it away.
func (fixture *lifecycleFixture) interceptQueries(intercept func(http.ResponseWriter, *http.Request) bool) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.intercept = intercept
}

// serve sets what the query service answers from now on.
func (fixture *lifecycleFixture) serve(partial bool, value float64, reporting ...string) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.reporting, fixture.value, fixture.partial = append([]string(nil), reporting...), value, partial
}

// install writes the strategy at its revision with the change signal beside
// it, which is how the platform publishes a strategy it changed.
func (fixture *lifecycleFixture) install(strategy lifecycleStrategy) {
	t := fixture.t
	t.Helper()
	raw, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["update_time"] = 1725000000 + strategy.revision
	document["strategy_revision"] = strategy.revision
	if strategy.unrevisioned {
		delete(document, "strategy_revision")
	}
	document["name"] = "no data lifecycle"
	if strategy.global {
		document["is_global_strategy"] = true
	}
	document["scenario"] = "os"
	item := document["items"].([]any)[0].(map[string]any)
	item["name"] = "CPU usage"
	query := item["query_configs"].([]any)[0].(map[string]any)
	query["agg_interval"] = lifecycleInterval
	query["agg_dimension"] = []any{"bk_target_ip", "bk_target_cloud_id"}
	if strategy.targets != nil {
		values := make([]any, 0, len(strategy.targets))
		for _, ip := range strategy.targets {
			values = append(values, map[string]any{"bk_target_ip": ip, "bk_target_cloud_id": 0})
		}
		item["target"] = []any{[]any{map[string]any{"field": "bk_target_ip", "method": "eq", "value": values}}}
	}
	if !strategy.noDataOff {
		dimensions := make([]any, 0, len(strategy.dimensions))
		for _, dimension := range strategy.dimensions {
			dimensions = append(dimensions, dimension)
		}
		section := map[string]any{"is_enabled": true, "continuous": strategy.continuous, "level": noDataConfiguredLevelID,
			"agg_dimension": dimensions}
		if strategy.horizon > 0 {
			section["tracking_horizon_seconds"] = strategy.horizon
		}
		item["no_data_config"] = section
	}
	for _, detect := range document["detects"].([]any) {
		detect.(map[string]any)["trigger_config"].(map[string]any)["check_window"] = 1
		if strategy.statusSetter != "" {
			detect.(map[string]any)["recovery_config"].(map[string]any)["status_setter"] = strategy.statusSetter
		}
		if strategy.disableRecovery {
			detect.(map[string]any)["recovery_config"].(map[string]any)["disable_recovery"] = true
		}
	}
	if strategy.edit != nil {
		strategy.edit(item)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]any{
		"alarm-config.strategy_ids":                    `[` + lifecycleStrategyID + `]`,
		"alarm-config.strategy_" + lifecycleStrategyID: encoded,
		"alarm-config.last_updated":                    strconv.Itoa(1725000000 + strategy.revision),
	} {
		if err := fixture.redis.Set(context.Background(), key, value, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
}

// open starts a bundle under workerID. Every bundle gets its own address, as
// every process listens on its own.
func (fixture *lifecycleFixture) open(workerID string, client *http.Client) {
	t := fixture.t
	t.Helper()
	ctx := context.Background()
	cfg := fixture.cfg
	cfg.PhaseTwo.Worker.ID = workerID
	cfg.HTTP.Listen = reserveBundleAddress()
	if client == nil {
		client = http.DefaultClient
	}
	now := func() time.Time { return time.UnixMilli(fixture.clock.Load()) }
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, HTTPClient: client,
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return fixture.sink, nil
			}),
			AdditionalObserver: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				fixture.observationsMu.Lock()
				defer fixture.observationsMu.Unlock()
				fixture.observations = append(fixture.observations, observation)
			}),
		},
	)
	if err != nil {
		t.Fatalf("open the bundle of %s: %v", workerID, err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("start the bundle of %s: %v", workerID, err)
	}
	fixture.bundle, fixture.workerID = bundle, workerID
	t.Cleanup(func() { fixture.closeBundle(bundle) })
	// The Query Group is owned once the Leader has placed it on this Worker
	// and the Worker holds its lease, which the bundle's own loops do on the
	// wall clock.
	want := max(fixture.queryGroups, 1)
	deadline := testwait.Deadline(fixture.t)
	for {
		bundle.mu.RLock()
		groups := append([]execution.QueryGroupIdentity(nil), bundle.queryGroups...)
		bundle.mu.RUnlock()
		sort.Slice(groups, func(left, right int) bool { return groups[left] < groups[right] })
		if len(groups) == want {
			settled := true
			for _, group := range groups {
				settled = settled && settledRunner(bundle, group) != nil
			}
			if settled {
				fixture.queryGroup, fixture.runner = groups[0], settledRunner(bundle, groups[0])
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the bundle of %s owns %v at the test's deadline, want %d Query Groups", workerID, groups, want)
		}
		if err := bundle.refreshAndReconcile(ctx, true); err != nil {
			t.Fatalf("reconcile the bundle of %s: %v", workerID, err)
		}
		time.Sleep(20 * time.Millisecond) // fixed-wait: the poll interval of a condition wait bounded by the test deadline
	}
}

// closeBundle shuts a bundle down the way a process stopping does: it
// drains, releases its leases and closes its connections. Its control
// stream listener goes with it.
func (fixture *lifecycleFixture) closeBundle(bundle *phaseTwoWorkerBundle) {
	if err := bundle.Shutdown(context.Background()); err != nil {
		fixture.t.Errorf("shutdown: %v", err)
	}
	if stream, served := controlStreamServers.LoadAndDelete(bundle); served {
		stream.(*testControlStream).stop()
	}
}

// replace stops the current bundle and starts another under workerID.
func (fixture *lifecycleFixture) replace(workerID string) {
	fixture.t.Helper()
	fixture.closeBundle(fixture.bundle)
	fixture.open(workerID, nil)
}

// evaluationAt is the evaluation time of the Slot round n runs: an attempt
// half a period after round n's boundary runs the Slot that boundary closed.
func (fixture *lifecycleFixture) evaluationAt(round int64) int64 {
	return fixture.base + (round-1)*fixture.interval
}

// attempt runs one attempt of round n, at the round's time plus delay, and
// says whether a Slot was attempted.
func (fixture *lifecycleFixture) attempt(round int64, delay time.Duration) bool {
	fixture.t.Helper()
	fixture.clock.Store(roundReadAtMillis(fixture.cfg, fixture.queryGroup, fixture.evaluationAt(round), fixture.interval) + delay.Milliseconds())
	_, attempted, err := fixture.runner.RunOne(context.Background())
	if err != nil {
		fixture.t.Fatalf("round %d RunOne error = %v", round, err)
	}
	return attempted
}

// mustAttempt runs round n and fails the case when no Slot was attempted.
func (fixture *lifecycleFixture) mustAttempt(round int64) {
	fixture.t.Helper()
	if !fixture.attempt(round, 0) {
		fixture.t.Fatalf("round %d was not attempted by %s", round, fixture.workerID)
	}
}

// noDataEventsFor is every written no-data event about one host, in order.
func (fixture *lifecycleFixture) noDataEventsFor(ip string) []contract.TriggerEventV1 {
	return fixture.sink.noDataEventsFor(ip)
}

// thresholdEventsFor is every written event about one host that is not a
// no-data event, in order.
func (fixture *lifecycleFixture) thresholdEventsFor(ip string) []contract.TriggerEventV1 {
	fixture.sink.mu.Lock()
	defer fixture.sink.mu.Unlock()
	var matched []contract.TriggerEventV1
	for _, event := range fixture.sink.written {
		if noDataTagged(event) {
			continue
		}
		var got string
		if json.Unmarshal(event.RecordRef.Dimensions["bk_target_ip"], &got) == nil && got == ip {
			matched = append(matched, event)
		}
	}
	return matched
}

// allNoDataEvents is every written no-data event.
func (fixture *lifecycleFixture) allNoDataEvents() []contract.TriggerEventV1 {
	fixture.sink.mu.Lock()
	defer fixture.sink.mu.Unlock()
	var matched []contract.TriggerEventV1
	for _, event := range fixture.sink.written {
		if noDataTagged(event) {
			matched = append(matched, event)
		}
	}
	return matched
}

// memoryKeys are the keys holding a Plan's no-data memory: hashes with the
// header field.
func (fixture *lifecycleFixture) memoryKeys() []string {
	fixture.t.Helper()
	ctx := context.Background()
	keys, err := fixture.redis.Keys(ctx, fixture.cfg.Redis.StatePrefix+"*").Result()
	if err != nil {
		fixture.t.Fatal(err)
	}
	var held []string
	for _, key := range keys {
		if kind, _ := fixture.redis.Type(ctx, key).Result(); kind != "hash" {
			continue
		}
		if present, _ := fixture.redis.HExists(ctx, key, "_meta").Result(); present {
			held = append(held, key)
		}
	}
	sort.Strings(held)
	return held
}

// memoryRecord is the whole stored record of the one memory key: its header
// and every group field, exactly as stored.
func (fixture *lifecycleFixture) memoryRecord() map[string]string {
	fixture.t.Helper()
	keys := fixture.memoryKeys()
	if len(keys) != 1 {
		fixture.t.Fatalf("no-data memory keys = %v, want exactly one", keys)
	}
	record, err := fixture.redis.HGetAll(context.Background(), keys[0]).Result()
	if err != nil {
		fixture.t.Fatal(err)
	}
	return record
}

// absenceOf is a group's stored absence: its first absent round and its
// suppression, both zero when the group is held as present or not held.
func (fixture *lifecycleFixture) absenceOf(ip string) (firstAbsent, suppressedAt int64, held bool) {
	fixture.t.Helper()
	value, found := fixture.memoryRecord()["g:"+followsGroupKey(ip)]
	if !found {
		return 0, 0, false
	}
	if value == "p" {
		return 0, 0, true
	}
	var absence struct {
		FirstAbsent  int64 `json:"first_absent"`
		SuppressedAt int64 `json:"suppressed_at"`
	}
	if err := json.Unmarshal([]byte(value), &absence); err != nil {
		fixture.t.Fatalf("memory for %s = %q, not an absence: %v", ip, value, err)
	}
	return absence.FirstAbsent, absence.SuppressedAt, true
}

// absenceOfGroup is absenceOf for a group named by its whole key.
func (fixture *lifecycleFixture) absenceOfGroup(groupKey string) (firstAbsent int64, held bool) {
	fixture.t.Helper()
	value, found := fixture.memoryRecord()["g:"+groupKey]
	if !found {
		return 0, false
	}
	if value == "p" {
		return 0, true
	}
	var absence struct {
		FirstAbsent int64 `json:"first_absent"`
	}
	if err := json.Unmarshal([]byte(value), &absence); err != nil {
		fixture.t.Fatalf("memory for %s = %q, not an absence: %v", groupKey, value, err)
	}
	return absence.FirstAbsent, true
}

// noDataEventsWhere is every written no-data event whose dimension name is
// value, in order.
func (fixture *lifecycleFixture) noDataEventsWhere(name, value string) []contract.TriggerEventV1 {
	fixture.sink.mu.Lock()
	defer fixture.sink.mu.Unlock()
	var matched []contract.TriggerEventV1
	for _, event := range fixture.sink.written {
		if !noDataTagged(event) {
			continue
		}
		var got string
		if json.Unmarshal(event.RecordRef.Dimensions[name], &got) == nil && got == value {
			matched = append(matched, event)
		}
	}
	return matched
}

// runtimeStateKeys are the keys of the runtime state the Plan's series hold.
func (fixture *lifecycleFixture) runtimeStateKeys() []string {
	fixture.t.Helper()
	keys, err := fixture.redis.Keys(context.Background(), fixture.cfg.Redis.StatePrefix+":runtime3:*").Result()
	if err != nil {
		fixture.t.Fatal(err)
	}
	sort.Strings(keys)
	return keys
}

// noDataOutcomes sums the per-Slot no-data outcomes observed so far.
func (fixture *lifecycleFixture) noDataOutcomes() map[string]int {
	fixture.observationsMu.Lock()
	defer fixture.observationsMu.Unlock()
	outcomes := map[string]int{}
	for _, observation := range fixture.observations {
		if observation.NoDataSlot != nil {
			outcomes[observation.NoDataSlot.Outcome] += observation.NoDataSlot.Plans
		}
	}
	return outcomes
}

// memoryWrites is every no-data memory write outcome observed so far.
func (fixture *lifecycleFixture) memoryWrites() []string {
	fixture.observationsMu.Lock()
	defer fixture.observationsMu.Unlock()
	var outcomes []string
	for _, observation := range fixture.observations {
		if observation.NoDataMemoryWrite != nil {
			outcomes = append(outcomes, observation.NoDataMemoryWrite.Outcome)
		}
	}
	return outcomes
}

// change installs a changed strategy and has the Leader publish it, then
// returns the first round whose Slot the new content applies to: the Leader
// starts the new Segment at a Slot of its choosing, and every round before it
// still runs the old content.
func (fixture *lifecycleFixture) change(strategy lifecycleStrategy, after int64) int64 {
	t := fixture.t
	t.Helper()
	ctx := context.Background()
	catalog := fixture.bundle.dependencies.Ownership.(*productionPhaseTwoOwnership).dependencies.Catalog
	first, err := catalog.ReadFrozenSchedule(ctx, fixture.queryGroup, execution.EvaluationTime(fixture.evaluationAt(after)))
	if err != nil {
		t.Fatal(err)
	}
	fixture.install(strategy)
	// The Leader reads the change on one refresh and publishes it on the one
	// that confirms it.
	for confirm := 0; confirm < 2; confirm++ {
		if err := fixture.bundle.refreshAndReconcile(ctx, true); err != nil {
			t.Fatalf("refresh after the change: %v", err)
		}
	}
	for deadline := testwait.Deadline(fixture.t); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) { // fixed-wait: the poll interval of a condition wait bounded by the test deadline
		for round := after + 1; round <= after+30; round++ {
			schedule, err := catalog.ReadFrozenSchedule(ctx, fixture.queryGroup, execution.EvaluationTime(fixture.evaluationAt(round)))
			if err == nil && schedule.Segment.ObjectDigest != first.Segment.ObjectDigest {
				return round
			}
		}
	}
	t.Fatal("no Segment carried the changed strategy before the test's deadline")
	return 0
}

// segmentAt is the Segment the round's Slot is frozen in.
func (fixture *lifecycleFixture) segmentAt(round int64) execution.ScheduleSegmentFact {
	fixture.t.Helper()
	catalog := fixture.bundle.dependencies.Ownership.(*productionPhaseTwoOwnership).dependencies.Catalog
	schedule, err := catalog.ReadFrozenSchedule(context.Background(), fixture.queryGroup, execution.EvaluationTime(fixture.evaluationAt(round)))
	if err != nil {
		fixture.t.Fatal(err)
	}
	return schedule.Segment
}

// progressKey is the key holding the Query Group's Progress.
func (fixture *lifecycleFixture) progressKey() string {
	fixture.t.Helper()
	keys, err := fixture.redis.Keys(context.Background(), fixture.cfg.Redis.StatePrefix+":*:schedule:progress").Result()
	if err != nil {
		fixture.t.Fatal(err)
	}
	if len(keys) != 1 {
		fixture.t.Fatalf("progress keys = %v, want one", keys)
	}
	return keys[0]
}

// publishOpenAlert makes one alert active in the link for the fixture's
// strategy: the link's search (lifecycleLink) finds it from then on. The
// alert is kept in the set "<lifecycleLinkPrefix>:<tenant>:<strategy>" on
// the fixture's redis-server, the stub's store.
func (fixture *lifecycleFixture) publishOpenAlert(dedupeMD5 string) {
	fixture.t.Helper()
	if err := fixture.redis.SAdd(context.Background(), lifecycleLinkPrefix+":tenant-a:"+lifecycleStrategyID, dedupeMD5).Err(); err != nil {
		fixture.t.Fatal(err)
	}
	fixture.published = append(fixture.published, dedupeMD5)
}

// withdrawOpenAlert ends one alert in the link, as a close by hand does: the
// search no longer finds it.
func (fixture *lifecycleFixture) withdrawOpenAlert(dedupeMD5 string) {
	fixture.t.Helper()
	if err := fixture.redis.SRem(context.Background(), lifecycleLinkPrefix+":tenant-a:"+lifecycleStrategyID, dedupeMD5).Err(); err != nil {
		fixture.t.Fatal(err)
	}
}

// linkCloses are the closes by filter (not dry runs) the link stub took, as
// requested.
func (fixture *lifecycleFixture) linkCloses() []string {
	fixture.t.Helper()
	closes, err := fixture.redis.LRange(context.Background(), lifecycleLinkPrefix+":closes", 0, -1).Result()
	if err != nil {
		fixture.t.Fatal(err)
	}
	return closes
}

// activeCopyPort is the current process's open alert port.
func (fixture *lifecycleFixture) activeCopyPort() *openAlertCopyPort {
	fixture.t.Helper()
	port, ok := fixture.bundle.workerPorts.OpenAlerts.(*openAlertCopyPort)
	if !ok || port.copy == nil {
		fixture.t.Fatalf("the open alert port is %T without a copy", fixture.bundle.workerPorts.OpenAlerts)
	}
	return port
}

// passActiveCopy runs one pass of the current process's copy, the one its
// own loop runs every 10 s of wall time, at the fixture's clock.
func (fixture *lifecycleFixture) passActiveCopy() {
	fixture.t.Helper()
	fixture.activeCopyPort().copy.Pass(context.Background())
}

// waitOpenAlertSetRead waits until the current process's copy judges every
// published alert held: its own word (an acknowledged send) or a search hit.
// A lookup with no word queues the fingerprint; the wait then runs the pass
// the copy's loop would run. round is the round the wait follows; the
// fixture's clock is not moved.
func (fixture *lifecycleFixture) waitOpenAlertSetRead(round int64) {
	fixture.t.Helper()
	for _, fingerprint := range fixture.published {
		fixture.waitOpenAlertJudged(round, fingerprint, true)
	}
}

// waitOpenAlertJudged waits until the current process's copy judges one
// fingerprint as held or not, as wanted, running a pass after each lookup
// that is not yet that judgement.
func (fixture *lifecycleFixture) waitOpenAlertJudged(round int64, fingerprint string, held bool) {
	fixture.t.Helper()
	port := fixture.activeCopyPort()
	for deadline := testwait.Deadline(fixture.t); ; {
		if got, judged := port.copy.Holds("tenant-a", lifecycleStrategyID, fingerprint); judged && got == held {
			return
		}
		port.copy.Pass(context.Background())
		time.Sleep(20 * time.Millisecond) // fixed-wait: the poll interval of a condition wait bounded by the test deadline
		if time.Now().After(deadline) {
			fixture.t.Fatalf("after round %d the copy does not judge %s held=%v by the test's deadline: %+v", round, fingerprint, held, port.copy.Stats())
		}
	}
}

// progressSlots is the Query Group's committed Progress: the next Slot due
// and the last Slot completed in full.
func (fixture *lifecycleFixture) progressSlots() (next, lastFull int64) {
	fixture.t.Helper()
	raw, err := fixture.redis.Get(context.Background(), fixture.progressKey()).Result()
	if err != nil {
		fixture.t.Fatal(err)
	}
	var stored struct {
		Progress struct {
			NextSlot     int64
			LastFullSlot int64
		} `json:"progress"`
	}
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		fixture.t.Fatalf("Progress %q does not decode: %v", raw, err)
	}
	return stored.Progress.NextSlot, stored.Progress.LastFullSlot
}

// heldForNoOpenAlert is how many RECOVERY records the open alert gate has held
// so far because the consumer holds no alert on the series, as the rounds
// reported them.
func (fixture *lifecycleFixture) heldForNoOpenAlert() uint64 {
	fixture.observationsMu.Lock()
	defer fixture.observationsMu.Unlock()
	var held uint64
	for _, observation := range fixture.observations {
		for _, gate := range observation.OpenAlertGates {
			if gate.Outcome == observability.OpenAlertGateHeldNoOpenAlert {
				held += gate.Records
			}
		}
	}
	return held
}

// lifecycleLinkPrefix is where the fixture's link stub keeps each
// strategy's active alerts, on the runtime redis-server.
const lifecycleLinkPrefix = "hook:open"

// lifecycleLink is the link's control plane over the fixture's store: the
// search answers the active alerts of "<lifecycleLinkPrefix>:<tenant>:<strategy>"
// that match the query's fingerprint and strategy terms (must_not honoured),
// each under the default event source; a close by filter matches the same
// way over its filter, and a close that is not a dry run ends the matched
// alerts and appends its request to "<lifecycleLinkPrefix>:closes". Any other
// route is a 404: the deployment reads nothing else of the link.
func lifecycleLink(client *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			TenantID string          `json:"bk_tenant_id"`
			Query    json.RawMessage `json:"query"`
			Filter   json.RawMessage `json:"filter"`
			DryRun   bool            `json:"dry_run"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &request)
		switch r.URL.Path {
		case "/api/v1/alerts/search":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": lifecycleLinkSearch(r.Context(), client, request.TenantID, request.Query), "next": ""})
		case "/api/v1/alerts/close":
			matched := lifecycleLinkSearch(r.Context(), client, request.TenantID, request.Filter)
			items := make([]map[string]any, 0, len(matched))
			for _, alert := range matched {
				items = append(items, map[string]any{"alert_id": alert["alert_id"], "status": http.StatusOK})
				if !request.DryRun {
					strategy := alert["labels"].(map[string]any)["strategy_id"].(string)
					_ = client.SRem(r.Context(), lifecycleLinkPrefix+":"+request.TenantID+":"+strategy, alert["fingerprint"]).Err()
				}
			}
			if !request.DryRun {
				_ = client.RPush(r.Context(), lifecycleLinkPrefix+":closes", string(body)).Err()
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "next": "", "dry_run": request.DryRun})
		default:
			http.NotFound(w, r)
		}
	}
}

// lifecycleLinkSearch is the stub's search over the store.
func lifecycleLinkSearch(ctx context.Context, client *redis.Client, tenant string, query json.RawMessage) []map[string]any {
	var node any
	_ = json.Unmarshal(query, &node)
	must, mustNot := map[string]map[string]bool{}, map[string]map[string]bool{}
	var walk func(node any, negated bool)
	walk = func(node any, negated bool) {
		switch value := node.(type) {
		case map[string]any:
			for key, child := range value {
				switch key {
				case "must_not":
					walk(child, true)
				case "term", "terms":
					fields, _ := child.(map[string]any)
					for field, raw := range fields {
						target := must
						if negated {
							target = mustNot
						}
						if target[field] == nil {
							target[field] = map[string]bool{}
						}
						switch v := raw.(type) {
						case []any:
							for _, one := range v {
								target[field][fmt.Sprint(one)] = true
							}
						default:
							target[field][fmt.Sprint(v)] = true
						}
					}
				default:
					walk(child, negated)
				}
			}
		case []any:
			for _, child := range value {
				walk(child, negated)
			}
		}
	}
	walk(node, false)
	allowed := func(field, value string) bool {
		if mustNot[field][value] {
			return false
		}
		return must[field] == nil || must[field][value]
	}
	items := []map[string]any{}
	keys, _ := client.Keys(ctx, lifecycleLinkPrefix+":"+tenant+":*").Result()
	if node == nil {
		return []map[string]any{}
	}
	sort.Strings(keys)
	for _, key := range keys {
		strategy := strings.TrimPrefix(key, lifecycleLinkPrefix+":"+tenant+":")
		if !allowed("labels.strategy_id", strategy) || !allowed("status", "active") || !allowed("event_source_id", "built_in_bk") {
			continue
		}
		members, _ := client.SMembers(ctx, key).Result()
		sort.Strings(members)
		for _, fingerprint := range members {
			if !allowed("fingerprint", fingerprint) {
				continue
			}
			items = append(items, map[string]any{"alert_id": "alert-" + fingerprint, "bk_tenant_id": tenant, "event_source_id": "built_in_bk",
				"fingerprint": fingerprint, "status": "active", "severity": "warning", "labels": map[string]any{"strategy_id": strategy}})
		}
	}
	return items
}
