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
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
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
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The rule these cases hold the worker to is the contract's commit order for
// a Plan (retention proposal, section 4 item 5): the output is acknowledged,
// then the Plan's State and no-data memory move, then Progress. The memory is
// what the next round decides from. A round whose output was not written must
// leave it where it was, so the round that repeats the decision repeats it
// from the same memory and sends the same events; memory that moved anyway
// makes the repeat decide from a round nobody heard about.
//
// Every case runs the production bundle on a real redis-server, with the Plan
// compiled from a strategy document by the production compiler: the order is
// a property of the wiring between the coordinator, the sink and the store,
// and a case that replaced any of them would test its own replacement.

const (
	followsHostA      = "192.0.2.1"
	followsHostB      = "192.0.2.2"
	followsStrategyID = "1001"
)

// followsGroupKey is the no-data group key of one of the fixture's hosts, as
// the memory stores it.
func followsGroupKey(ip string) string {
	return "bk_target_cloud_id=0,bk_target_ip=" + ip + "," + contract.NoDataDimensionTag + "=true"
}

// followsSink is an event sink whose answer the case decides, one mode at a
// time. Only what it acknowledges is recorded as written.
type followsSink struct {
	mu        sync.Mutex
	mode      string
	attempted []contract.TriggerEventV1
	written   []contract.TriggerEventV1
	closed    bool
	// gate, when set, keeps WriteBatch from going on until it is closed,
	// after saying so on entered: a case that needs a Slot held inside its
	// output write sets both with holdWrites. The hold does not watch the
	// write's context, as an acknowledgement already on its way does not.
	gate    chan struct{}
	entered chan struct{}
}

// holdWrites holds every WriteBatch from now on until gate is closed,
// telling entered (without blocking) each time one is held.
func (sink *followsSink) holdWrites(gate, entered chan struct{}) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.gate, sink.entered = gate, entered
}

const (
	followsOK         = "ok"
	followsACKUnknown = "ack_unknown"
	followsDeferred   = "deferred"
	followsRejected   = "rejected"
	followsPartial    = "partial"
)

func (sink *followsSink) ConfigureStandardOutput(enginekafka.StandardEventConverter) error {
	return nil
}

func (sink *followsSink) ConfigureLegacyOutput(enginekafka.LegacyEventConverter, string, int) error {
	return nil
}

func (sink *followsSink) ProtocolNegotiation() *enginekafka.ProtocolNegotiation { return nil }

func (sink *followsSink) setMode(mode string) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.mode = mode
}

func (sink *followsSink) WriteBatch(_ context.Context, events []contract.TriggerEventV1) error {
	sink.mu.Lock()
	gate, entered := sink.gate, sink.entered
	sink.mu.Unlock()
	if gate != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-gate:
		case <-time.After(lifecycleWatchdog):
		}
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.attempted = append(sink.attempted, events...)
	switch sink.mode {
	case followsACKUnknown:
		return &retryablePhaseTwoEventError{err: errors.New("broker ACK unavailable")}
	case followsDeferred:
		return &enginekafka.OutputDeferredError{Remaining: time.Second, Needed: 2 * time.Second}
	case followsRejected:
		return &enginekafka.OutputRejectedError{Reason: contract.ReasonOutputClientRejected,
			Detail: "refused by the client", StrategyID: followsStrategyID}
	case followsPartial:
		// Host B's no-data event is refused; everything else of the batch
		// is written.
		var refused []enginekafka.RefusedEvent
		for _, event := range events {
			if followsIsNoDataFor(event, followsHostB) {
				refused = append(refused, enginekafka.RefusedEvent{EventID: event.EventID, Rule: "test_rule",
					Detail: "refused for this case", StrategyID: followsStrategyID})
				continue
			}
			sink.written = append(sink.written, event)
		}
		if len(refused) == 0 {
			return nil
		}
		return &enginekafka.OutputPartiallyRejectedError{Rejected: refused}
	}
	sink.written = append(sink.written, events...)
	return nil
}

func (sink *followsSink) Shutdown(context.Context) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.closed = true
	return nil
}

func (sink *followsSink) Close() error { return sink.Shutdown(context.Background()) }

// noDataEventsFor is every written no-data event about one host, in order.
func (sink *followsSink) noDataEventsFor(ip string) []contract.TriggerEventV1 {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return followsNoDataFor(sink.written, ip)
}

// attemptedNoDataFor is every no-data event about one host the sink was asked
// to write, written or not.
func (sink *followsSink) attemptedNoDataFor(ip string) []contract.TriggerEventV1 {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return followsNoDataFor(sink.attempted, ip)
}

func followsNoDataFor(events []contract.TriggerEventV1, ip string) []contract.TriggerEventV1 {
	var matched []contract.TriggerEventV1
	for _, event := range events {
		if followsIsNoDataFor(event, ip) {
			matched = append(matched, event)
		}
	}
	return matched
}

func followsIsNoDataFor(event contract.TriggerEventV1, ip string) bool {
	if !noDataTagged(event) {
		return false
	}
	var got string
	return json.Unmarshal(event.RecordRef.Dimensions["bk_target_ip"], &got) == nil && got == ip
}

type followsFixture struct {
	t        *testing.T
	base     int64
	interval int64
	clock    *atomic.Int64
	redis    *redis.Client
	prefix   string
	sink     *followsSink
	runner   phaseTwoQueryGroupRuntime
	retry    time.Duration
	// catalog and queryGroup read the published timeline, for a case that
	// has to know which Slot a strategy change first applies to.
	catalog    productionPhaseTwoSlotCatalog
	queryGroup execution.QueryGroupIdentity
	bundle     *phaseTwoWorkerBundle

	observationsMu sync.Mutex
	observations   []observability.Observation
}

// startFollowsFixture opens the production bundle over one strategy: two
// static hosts in the target, no-data detected on the host dimensions with a
// continuous count of one, and data that only ever arrives for host A. Both
// hosts are in the strategy's business, so host B is an ordinary absence.
func startFollowsFixture(t *testing.T, maxStateMutations uint64) *followsFixture {
	t.Helper()
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()

	cfg := validGoAccessRuntimeConfig()
	followsSeedHosts(t, ctx, redisClient, cfg.PlatformKeyPrefix())
	followsInstallStrategy(t, ctx, redisClient, 7, []string{followsHostA, followsHostB})

	const interval = int64(60)
	base := time.Now().Unix()
	base += interval - base%interval
	fixture := &followsFixture{t: t, base: base, interval: interval, clock: &atomic.Int64{}, redis: redisClient}
	fixture.clock.Store(base * 1000)
	now := func() time.Time { return time.UnixMilli(fixture.clock.Load()) }

	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
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
		series := `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],` +
			`"group_keys":["bk_target_cloud_id","bk_target_ip"],"group_values":["0","` + followsHostA + `"],` +
			`"values":[[` + strconv.FormatInt((end-1)*1000, 10) + `,5]]}`
		_, _ = writer.Write([]byte(`{"series":[` + series + `],"status":null,"trace_id":"no-data-follows",` +
			`"is_partial":false,"result_table_id":["system.cpu"]}`))
	}))
	t.Cleanup(uqServer.Close)

	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd-no-data-follows"
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
	if maxStateMutations > 0 {
		cfg.PhaseTwo.Coordinator.MaxStateMutations = maxStateMutations
	}
	fixture.prefix = cfg.Redis.StatePrefix
	fixture.retry = cfg.PhaseTwo.Scheduler.RetryMinDelay.Duration()

	fixture.sink = &followsSink{mode: followsOK}
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, HTTPClient: uqServer.Client(),
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
		t.Fatalf("openProductionPhaseTwoBundleWithDependencies() error = %v", err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("phase-two production Start() error = %v", err)
	}
	t.Cleanup(func() {
		if shutdownErr := bundle.Shutdown(context.Background()); shutdownErr != nil {
			t.Errorf("phase-two production Shutdown() error = %v", shutdownErr)
		}
	})
	if len(bundle.queryGroups) != 1 {
		t.Fatalf("Query Groups = %v, want one", bundle.queryGroups)
	}
	fixture.runner = settledRunner(bundle, bundle.queryGroups[0])
	fixture.queryGroup = bundle.queryGroups[0]
	fixture.bundle = bundle
	fixture.catalog = bundle.dependencies.Ownership.(*productionPhaseTwoOwnership).dependencies.Catalog
	return fixture
}

// followsSeedHosts writes the hosts under the strategy's tenant's CMDB keys,
// where a tenant's Plans read them, and under the default tenant's, which
// the replica waits on to be ready.
func followsSeedHosts(t *testing.T, ctx context.Context, client *redis.Client, platformPrefix string) {
	t.Helper()
	for _, hosts := range []string{platformPrefix + ".cache.cmdb.host", "tenant-a." + platformPrefix + ".cache.cmdb.host"} {
		for id, ip := range map[int]string{1: followsHostA, 2: followsHostB} {
			record := `{"bk_host_id":` + strconv.Itoa(id) + `,"bk_host_innerip":"` + ip + `","bk_cloud_id":0,"bk_biz_id":2}`
			if err := client.HSet(ctx, hosts, ip+"|0", record, strconv.Itoa(id), record).Err(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// followsInstallStrategy writes the strategy with the hosts its target names,
// at a revision: a later write has to carry a later one to be read as a change.
func followsInstallStrategy(t *testing.T, ctx context.Context, client *redis.Client, revision int, targets []string) {
	t.Helper()
	raw, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["update_time"] = 1725000000 + revision
	document["strategy_revision"] = revision
	document["name"] = "no data follows its output"
	document["scenario"] = "os"
	item := document["items"].([]any)[0].(map[string]any)
	item["name"] = "CPU usage"
	query := item["query_configs"].([]any)[0].(map[string]any)
	query["agg_interval"] = 60
	query["agg_dimension"] = []any{"bk_target_ip", "bk_target_cloud_id"}
	values := make([]any, 0, len(targets))
	for _, ip := range targets {
		values = append(values, map[string]any{"bk_target_ip": ip, "bk_target_cloud_id": 0})
	}
	item["target"] = []any{[]any{map[string]any{"field": "bk_target_ip", "method": "eq", "value": values}}}
	item["no_data_config"] = map[string]any{
		"is_enabled": true, "continuous": 1, "level": noDataConfiguredLevelID,
		"agg_dimension": []any{"bk_target_ip", "bk_target_cloud_id"},
	}
	for _, detect := range document["detects"].([]any) {
		detect.(map[string]any)["trigger_config"].(map[string]any)["check_window"] = 1
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	// The change signal the platform writes beside a strategy it changed: the
	// leader re-reads the strategies when it moves.
	for key, value := range map[string]any{
		"alarm-config.strategy_ids":                  `[` + followsStrategyID + `]`,
		"alarm-config.strategy_" + followsStrategyID: encoded,
		"alarm-config.last_updated":                  strconv.Itoa(1725000000 + revision),
	} {
		if err := client.Set(ctx, key, value, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
}

// evaluationAt is the evaluation time of the Slot round n runs: an attempt
// half a period after round n's boundary runs the Slot that boundary closed,
// the one a period earlier, which is the last one past its readiness.
func (fixture *followsFixture) evaluationAt(round int64) int64 {
	return fixture.base + (round-1)*fixture.interval
}

// attempt runs one attempt of round n, at the round's time plus delay.
func (fixture *followsFixture) attempt(round int64, delay time.Duration) bool {
	fixture.t.Helper()
	fixture.clock.Store((fixture.evaluationAt(round)+fixture.interval/2)*1000 + delay.Milliseconds())
	_, attempted, err := fixture.runner.RunOne(context.Background())
	if err != nil {
		fixture.t.Fatalf("round %d RunOne error = %v", round, err)
	}
	return attempted
}

// memoryOf is what the Plan's stored no-data memory holds for one group: its
// raw value, and whether the group is there at all.
func (fixture *followsFixture) memoryOf(groupKey string) (string, bool) {
	fixture.t.Helper()
	ctx := context.Background()
	keys, err := fixture.redis.Keys(ctx, fixture.prefix+"*").Result()
	if err != nil {
		fixture.t.Fatal(err)
	}
	for _, key := range keys {
		if kind, _ := fixture.redis.Type(ctx, key).Result(); kind != "hash" {
			continue
		}
		if held, _ := fixture.redis.HExists(ctx, key, "_meta").Result(); !held {
			continue
		}
		value, err := fixture.redis.HGet(ctx, key, "g:"+groupKey).Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			fixture.t.Fatal(err)
		}
		return value, true
	}
	return "", false
}

// firstAbsentOf is the start of the absence the memory holds for a group, or
// zero when it holds none.
func (fixture *followsFixture) firstAbsentOf(groupKey string) int64 {
	fixture.t.Helper()
	value, held := fixture.memoryOf(groupKey)
	if !held || value == "p" {
		return 0
	}
	var absence struct {
		FirstAbsent int64 `json:"first_absent"`
	}
	if err := json.Unmarshal([]byte(value), &absence); err != nil {
		fixture.t.Fatalf("memory for %s = %q, not an absence: %v", groupKey, value, err)
	}
	return absence.FirstAbsent
}

// noDataOutcomes is the per-Slot no-data outcomes observed so far, summed.
func (fixture *followsFixture) noDataOutcomes() map[string]int {
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

// A round whose output did not land leaves the Plan's no-data memory where it
// was, whichever way the output failed, and the round that decides again
// sends what the failed one would have.
//
// Host B is absent from the first round on. Its first absent round opens the
// absence: the memory gains B with that round as its start, and B's anomaly
// goes out. Here that output fails, four ways:
//   - the broker's acknowledgement is unknown, and the Slot is retried;
//   - the sink defers the batch for the lease, and the Slot is retried;
//   - the client rejects the batch, and the Slot completes without it;
//   - the sink writes the batch except host B's event, and the Slot
//     completes without that one.
//
// In every one the memory must not hold B afterwards. The next decision - the
// retry of the same Slot, or the next Slot after a completed one - must then
// send B's anomaly, from the memory as it was.
func TestANoDataRoundWhoseOutputDidNotLandLeavesItsMemoryWhereItWas(t *testing.T) {
	for _, testCase := range []struct {
		mode    string
		retried bool
	}{
		{mode: followsACKUnknown, retried: true},
		{mode: followsDeferred, retried: true},
		{mode: followsRejected, retried: false},
		{mode: followsPartial, retried: false},
	} {
		t.Run(testCase.mode, func(t *testing.T) {
			fixture := startFollowsFixture(t, 0)
			hostB := followsGroupKey(followsHostB)

			fixture.sink.setMode(testCase.mode)
			if !fixture.attempt(1, 0) {
				t.Fatal("round 1 was not attempted")
			}
			if value, held := fixture.memoryOf(hostB); held {
				t.Fatalf("after round 1's output failed (%s), the memory holds host B as %q; want it as it was, "+
					"without B: the round that decides again must decide from there", testCase.mode, value)
			}
			if sent := fixture.sink.noDataEventsFor(followsHostB); len(sent) != 0 {
				t.Fatalf("host B's events written through a failed output: %+v", sent)
			}

			fixture.sink.setMode(followsOK)
			next, want := int64(1), fixture.evaluationAt(1)
			if testCase.retried {
				if !fixture.attempt(1, fixture.retry+time.Second) {
					t.Fatal("the retry of round 1 was not attempted")
				}
			} else {
				next, want = 2, fixture.evaluationAt(2)
				if !fixture.attempt(2, 0) {
					t.Fatal("round 2 was not attempted")
				}
			}
			sent := fixture.sink.noDataEventsFor(followsHostB)
			if len(sent) != 1 || sent[0].EventKind != contract.TriggerEventAbnormal {
				t.Fatalf("after the failed round, round %d sent host B's no-data events %+v; want one anomaly", next, sent)
			}
			if got := fixture.firstAbsentOf(hostB); got != want {
				t.Fatalf("host B's absence starts at %d after round %d; want %d, the first round whose output landed",
					got, next, want)
			}
		})
	}
}

// The control for the case above: with the output landing, the first absent
// round both sends B's anomaly and records B's absence from that round.
func TestANoDataRoundWhoseOutputLandedMovesItsMemory(t *testing.T) {
	fixture := startFollowsFixture(t, 0)
	if !fixture.attempt(1, 0) {
		t.Fatal("round 1 was not attempted")
	}
	sent := fixture.sink.noDataEventsFor(followsHostB)
	if len(sent) != 1 || sent[0].EventKind != contract.TriggerEventAbnormal {
		t.Fatalf("round 1 sent host B's no-data events %+v; want one anomaly", sent)
	}
	if got, want := fixture.firstAbsentOf(followsGroupKey(followsHostB)), fixture.evaluationAt(1); got != want {
		t.Fatalf("host B's absence starts at %d; want %d", got, want)
	}
	if outcomes := fixture.noDataOutcomes(); outcomes["EVALUATED"] != 1 {
		t.Fatalf("no-data outcomes = %v, want one EVALUATED", outcomes)
	}
}

// The case the order exists for: a closing NORMAL that deletes its group.
//
// Host B's absence is open and its alert raised. The target then stops naming
// B, so the first round under the new target closes B's absence with its one
// NORMAL and forgets B (A8): no later round will ever speak about B again.
// That NORMAL's output fails with the acknowledgement unknown, and the Slot is
// retried. The retry has to send it. With the memory applied by the failed
// attempt, the retry finds no B, decides nothing about it, and the alert stays
// open for good.
func TestAClosingNormalWhoseOutputFailedIsSentByTheRetry(t *testing.T) {
	fixture := startFollowsFixture(t, 0)
	ctx := context.Background()
	hostB := followsGroupKey(followsHostB)
	if !fixture.attempt(1, 0) {
		t.Fatal("round 1 was not attempted")
	}
	opened := fixture.sink.noDataEventsFor(followsHostB)
	if len(opened) != 1 || opened[0].EventKind != contract.TriggerEventAbnormal {
		t.Fatalf("round 1 sent host B's no-data events %+v; want one anomaly", opened)
	}
	if got := fixture.firstAbsentOf(hostB); got != fixture.evaluationAt(1) {
		t.Fatalf("host B's absence starts at %d after round 1; want %d", got, fixture.evaluationAt(1))
	}

	// The new target applies from the Segment the leader publishes for it,
	// which starts at a Slot of its choosing; every round before it still
	// runs the old one. Found by reading the timeline, so the case fails the
	// output of exactly the round that closes.
	first, err := fixture.catalog.ReadFrozenSchedule(ctx, fixture.queryGroup, execution.EvaluationTime(fixture.evaluationAt(1)))
	if err != nil {
		t.Fatal(err)
	}
	followsInstallStrategy(t, ctx, fixture.redis, 8, []string{followsHostA})
	// The leader reads the change on a refresh and publishes it on the one
	// that confirms it, which is what its own loop does on its cadence.
	for confirm := 0; confirm < 2; confirm++ {
		if err := fixture.bundle.refreshAndReconcile(ctx, true); err != nil {
			t.Fatalf("refresh after the target change: %v", err)
		}
	}
	closing := int64(0)
	for deadline := time.Now().Add(10 * time.Second); closing == 0 && time.Now().Before(deadline); {
		for round := int64(2); round <= 30 && closing == 0; round++ {
			schedule, err := fixture.catalog.ReadFrozenSchedule(ctx, fixture.queryGroup, execution.EvaluationTime(fixture.evaluationAt(round)))
			if err == nil && schedule.Segment.ObjectDigest != first.Segment.ObjectDigest {
				closing = round
			}
		}
		if closing == 0 {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if closing == 0 {
		t.Fatal("no Segment carried the new target within ten seconds")
	}
	for round := int64(2); round < closing; round++ {
		if !fixture.attempt(round, 0) {
			t.Fatalf("round %d was not attempted", round)
		}
	}

	fixture.sink.setMode(followsACKUnknown)
	if !fixture.attempt(closing, 0) {
		t.Fatalf("round %d was not attempted", closing)
	}
	closingAttempted := false
	for _, event := range fixture.sink.attemptedNoDataFor(followsHostB) {
		closingAttempted = closingAttempted || event.EventKind != contract.TriggerEventAbnormal
	}
	if !closingAttempted {
		t.Fatalf("round %d, the first under the new target, did not attempt host B's closing event", closing)
	}
	if got := fixture.firstAbsentOf(hostB); got != fixture.evaluationAt(1) {
		t.Fatalf("after the closing round's output failed, host B's absence starts at %d; want it still open from %d",
			got, fixture.evaluationAt(1))
	}

	fixture.sink.setMode(followsOK)
	if !fixture.attempt(closing, fixture.retry+time.Second) {
		t.Fatalf("the retry of round %d was not attempted", closing)
	}
	var closed []contract.TriggerEventV1
	for _, event := range fixture.sink.noDataEventsFor(followsHostB) {
		if event.EventKind != contract.TriggerEventAbnormal {
			closed = append(closed, event)
		}
	}
	if len(closed) != 1 || closed[0].EvaluationTime != fixture.evaluationAt(closing) {
		t.Fatalf("the retry wrote host B's closing events %+v; want one, for round %d: without it the alert "+
			"B's absence raised stays open", closed, closing)
	}
	if value, held := fixture.memoryOf(hostB); held {
		t.Fatalf("host B is still remembered as %q after its closing event was written", value)
	}
}
