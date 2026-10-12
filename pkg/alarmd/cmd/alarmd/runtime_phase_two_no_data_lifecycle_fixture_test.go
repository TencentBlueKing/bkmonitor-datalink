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
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
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
	// lifecycleWatchdog bounds every wait on something the bundle does in
	// its own goroutines. It is generous because the machine running these is
	// shared; nothing here measures time against it.
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
	// edit is applied to the item last, for a target shape the fields above
	// do not express.
	edit func(item map[string]any)
}

type lifecycleFixture struct {
	t        *testing.T
	address  string
	redis    *redis.Client
	cfg      config.Config
	base     int64
	interval int64
	clock    *atomic.Int64
	sink     *followsSink
	retry    time.Duration

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
	// The link Console a deployment on the consumer's protocol has. Without
	// one the open alert copy is not configured and reads no set, so the
	// cases that publish an open alert and wait for it to be read need it.
	console := httptest.NewServer(lifecycleConsole(address, client))
	t.Cleanup(console.Close)
	cfg.PhaseTwo.Linkd.ConsoleURL = console.URL
	cfg.PhaseTwo.Linkd.Username, cfg.PhaseTwo.Linkd.Password = "user", "secret"
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
	document["name"] = "no data lifecycle"
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
	deadline := time.Now().Add(lifecycleWatchdog)
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
			t.Fatalf("the bundle of %s owns %v after %s, want %d Query Groups", workerID, groups, lifecycleWatchdog, want)
		}
		if err := bundle.refreshAndReconcile(ctx, true); err != nil {
			t.Fatalf("reconcile the bundle of %s: %v", workerID, err)
		}
		time.Sleep(20 * time.Millisecond)
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
	fixture.clock.Store((fixture.evaluationAt(round)+fixture.interval/2)*1000 + delay.Milliseconds())
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
	for deadline := time.Now().Add(lifecycleWatchdog); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		for round := after + 1; round <= after+30; round++ {
			schedule, err := catalog.ReadFrozenSchedule(ctx, fixture.queryGroup, execution.EvaluationTime(fixture.evaluationAt(round)))
			if err == nil && schedule.Segment.ObjectDigest != first.Segment.ObjectDigest {
				return round
			}
		}
	}
	t.Fatalf("no Segment carried the changed strategy within %s", lifecycleWatchdog)
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

// publishOpenAlert puts one alert in the consumer's open alert set for the
// fixture's strategy, the way the consumer publishes an alert it opened:
// the set "<prefix>:<tenant>:<strategy>" holding the alert's dedupe md5
// (no-data tracking retention proposal, section 5), and a change notice for
// the strategy on "<prefix>:changes". The prefix is the deployment's
// default, as the fixture configures no other.
func (fixture *lifecycleFixture) publishOpenAlert(dedupeMD5 string) {
	fixture.t.Helper()
	ctx := context.Background()
	prefix := lifecycleLinkPrefix
	if err := fixture.redis.SAdd(ctx, prefix+":tenant-a:"+lifecycleStrategyID, dedupeMD5).Err(); err != nil {
		fixture.t.Fatal(err)
	}
	notice := `{"bk_tenant_id":"tenant-a","strategy_id":"` + lifecycleStrategyID + `"}`
	if err := fixture.redis.Publish(ctx, prefix+":changes", notice).Err(); err != nil {
		fixture.t.Fatal(err)
	}
}

// waitOpenAlertSetRead waits until the current process's copy of the open
// alert set holds the published member, which its own loop reads once the
// strategy is registered. The loop decides a read is due by the bundle's
// clock, so the wait moves that clock on a second at a time from the round
// it is in, never past the next round's attempt.
func (fixture *lifecycleFixture) waitOpenAlertSetRead(round int64) {
	fixture.t.Helper()
	port, ok := fixture.bundle.workerPorts.OpenAlerts.(*openAlertCopyPort)
	if !ok {
		fixture.t.Fatalf("the open alert port is %T", fixture.bundle.workerPorts.OpenAlerts)
	}
	attemptedAt := (fixture.evaluationAt(round) + fixture.interval/2) * 1000
	for deadline, step := time.Now().Add(lifecycleWatchdog), int64(1); ; step++ {
		if stats := port.cache.Stats(); stats.Loaded >= 1 && stats.Members >= 1 {
			return
		}
		if step < fixture.interval/2 {
			fixture.clock.Store(attemptedAt + step*1000)
		}
		time.Sleep(20 * time.Millisecond)
		if time.Now().After(deadline) {
			fixture.t.Fatalf("the open alert copy has not read the strategy's set after %s: %+v", lifecycleWatchdog, port.cache.Stats())
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

// lifecycleLinkPrefix is where the fixture's link Console says the open alert
// sets are written, on the runtime redis-server.
const lifecycleLinkPrefix = "hook:open"

// lifecycleConsole is a link Console naming the runtime redis-server under
// lifecycleLinkPrefix, its event source keyed by the alert id, and answering
// each reconciliation with what that strategy's set holds, as matched - so a
// calibration agrees with the set the fixture published rather than replacing
// it.
func lifecycleConsole(address string, client *redis.Client) http.HandlerFunc {
	target := openalerts.TargetBinding{EventSourceID: "source", HookName: "active", KeyPrefix: lifecycleLinkPrefix,
		Address: address, Database: 0, Sources: []string{"source"}}
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/local-api/strategy-index/targets":
			_ = json.NewEncoder(w).Encode([]openalerts.TargetBinding{target})
		case "/local-api/event-sources/" + target.EventSourceID:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": target.EventSourceID, "revision": 1, "published": 1,
				"spec": map[string]any{"fingerprint_mode": "field", "fingerprint_field": "source_alert_id"}})
		default:
			q := r.URL.Query()
			tenant, strategy := q.Get("bk_tenant_id"), q.Get("strategy_id")
			key := target.KeyPrefix + ":" + tenant + ":" + strategy
			members, _ := client.SMembers(r.Context(), key).Result()
			rows := make([]any, 0, len(members))
			for _, fingerprint := range members {
				rows = append(rows, map[string]any{"fingerprint": fingerprint, "status": "matched",
					"alerts": []openalerts.Alert{{AlertID: fingerprint, EventSourceID: target.EventSourceID, Fingerprint: fingerprint, Severity: "warning"}}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"target": target, "tenantId": tenant, "strategyId": strategy,
				"key": key, "complete": true, "redis": map[string]any{"complete": true}, "alerts": map[string]any{"complete": true},
				"rows": rows})
		}
	}
}
