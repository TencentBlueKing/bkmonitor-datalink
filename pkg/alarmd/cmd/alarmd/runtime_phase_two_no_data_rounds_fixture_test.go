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
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
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
)

// The fixture below runs the production bundle on a real redis-server over one
// strategy compiled from its document, as the follows-output cases do, and
// lets a case decide what the query answers round by round: which series
// report, the value they report, and whether the answer is complete. It is
// what the cases about rounds in which no-data is not judged need, because
// every one of them has to show three things in the same round: what the
// no-data round did, what it left in the store, and that the threshold
// detection beside it went on.

// hostThresholdValue is a value at or above the threshold of the shared
// strategy document (80, testdata/g1_full_threshold_strategy.json), and
// hostQuietValue one below it. A round that reports the first raises the
// reporting series' threshold alert, which is how a case shows the threshold
// detection ran in a round whose no-data was skipped.
const (
	hostThresholdValue = 90
	hostQuietValue     = 5
)

// roundsSeries is one series the query answers: its group keys and values.
type roundsSeries struct {
	keys   []string
	values []string
}

// ipSeries is a host series by address, the shape a static IP target and its
// no-data groups are keyed by.
func ipSeries(ip string) roundsSeries {
	return roundsSeries{keys: []string{"bk_target_cloud_id", "bk_target_ip"}, values: []string{"0", ip}}
}

// hostIDSeries is a host series by host id, the shape a target plan read by
// host identity is keyed by.
func hostIDSeries(hostID string) roundsSeries {
	return roundsSeries{keys: []string{"bk_host_id"}, values: []string{hostID}}
}

type roundsOptions struct {
	// hosts seeds the CMDB host cache with hosts A (id 1) and B (id 2) under
	// the strategy's business. Without it the index the bundle builds at
	// startup holds no host.
	hosts bool
	// item edits the strategy's only item after the defaults, which are the
	// follows-output strategy's: a static target of hosts A and B, no-data on
	// the host dimensions at a continuous count of one.
	item func(item map[string]any)
	// groupPrefix, when set, is rendered as the dynamic group cache prefix.
	groupPrefix string
	// address and client run the bundle on a redis-server the case brings,
	// which already holds the strategy: one a previous bundle of the case ran
	// on, or one the case wrote other platform caches into first. base is the
	// previous bundle's first round, so round numbers carry across the two.
	address string
	client  *redis.Client
	base    int64
}

type roundsFixture struct {
	*followsFixture
	address string

	seriesMu sync.Mutex
	series   []roundsSeries
	value    atomic.Int64
	partial  atomic.Bool
	// stopBundle shuts the bundle down before the case ends, for a case that
	// opens another on the same store.
	stopBundle func()
}

// startRoundsFixture opens the production bundle over the strategy the options
// describe. The query answers host A alone at a quiet value until a case says
// otherwise.
func startRoundsFixture(t *testing.T, options roundsOptions) *roundsFixture {
	t.Helper()
	address, redisClient := options.address, options.client
	if redisClient == nil {
		address, redisClient = startPhaseTwoRedis(t)
	}
	ctx := context.Background()
	cfg := validGoAccessRuntimeConfig()
	if options.hosts {
		followsSeedHosts(t, ctx, redisClient, cfg.PlatformKeyPrefix())
	}
	if options.client == nil {
		installRoundsStrategy(t, ctx, redisClient, 7, options.item)
	}

	const interval = int64(60)
	base := time.Now().Unix()
	base += interval - base%interval
	if options.base != 0 {
		base = options.base
	}
	fixture := &roundsFixture{
		followsFixture: &followsFixture{t: t, base: base, interval: interval, clock: &atomic.Int64{}, redis: redisClient},
		address:        address,
		series:         []roundsSeries{ipSeries(followsHostA)},
	}
	fixture.value.Store(hostQuietValue)
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
		fixture.seriesMu.Lock()
		reporting := append([]roundsSeries(nil), fixture.series...)
		fixture.seriesMu.Unlock()
		encoded := make([]string, 0, len(reporting))
		for index, one := range reporting {
			keys, _ := json.Marshal(one.keys)
			values, _ := json.Marshal(one.values)
			encoded = append(encoded, `{"name":"_result`+strconv.Itoa(index)+`","columns":["_time","_result"],`+
				`"types":["int64","float64"],"group_keys":`+string(keys)+`,"group_values":`+string(values)+`,`+
				`"values":[[`+strconv.FormatInt((end-1)*1000, 10)+`,`+strconv.FormatInt(fixture.value.Load(), 10)+`]]}`)
		}
		_, _ = writer.Write([]byte(`{"series":[` + strings.Join(encoded, ",") + `],"status":null,` +
			`"trace_id":"no-data-rounds","is_partial":` + strconv.FormatBool(fixture.partial.Load()) +
			`,"result_table_id":["system.cpu"]}`))
	}))
	t.Cleanup(uqServer.Close)

	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd-no-data-rounds"
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
	if options.groupPrefix != "" {
		prefix := options.groupPrefix
		cfg.PlatformCache.DynamicGroupKeyPrefix = &prefix
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
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		if shutdownErr := bundle.Shutdown(context.Background()); shutdownErr != nil {
			t.Errorf("phase-two production Shutdown() error = %v", shutdownErr)
		}
	}
	t.Cleanup(stop)
	fixture.stopBundle = stop
	if len(bundle.queryGroups) != 1 {
		t.Fatalf("Query Groups = %v, want one", bundle.queryGroups)
	}
	fixture.runner = settledRunner(bundle, bundle.queryGroups[0])
	fixture.queryGroup = bundle.queryGroups[0]
	fixture.bundle = bundle
	fixture.catalog = bundle.dependencies.Ownership.(*productionPhaseTwoOwnership).dependencies.Catalog
	schedule, err := fixture.catalog.ReadInitialFrozenSchedule(ctx, fixture.queryGroup)
	if err != nil || len(schedule.Plans) != 1 {
		t.Fatalf("initial schedule = %+v, error = %v; want the one Plan the strategy compiles to", schedule, err)
	}
	return fixture
}

// installRoundsStrategy writes the follows-output strategy, edited by item,
// at a revision: a later write has to carry a later one to be read as a change.
func installRoundsStrategy(t *testing.T, ctx context.Context, client *redis.Client, revision int, edit func(map[string]any)) {
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
	document["name"] = "no data rounds"
	document["scenario"] = "os"
	item := document["items"].([]any)[0].(map[string]any)
	item["name"] = "CPU usage"
	query := item["query_configs"].([]any)[0].(map[string]any)
	query["agg_interval"] = 60
	query["agg_dimension"] = []any{"bk_target_ip", "bk_target_cloud_id"}
	item["target"] = []any{[]any{map[string]any{"field": "bk_target_ip", "method": "eq", "value": []any{
		map[string]any{"bk_target_ip": followsHostA, "bk_target_cloud_id": 0},
		map[string]any{"bk_target_ip": followsHostB, "bk_target_cloud_id": 0},
	}}}}
	item["no_data_config"] = map[string]any{
		"is_enabled": true, "continuous": 1, "level": noDataConfiguredLevelID,
		"agg_dimension": []any{"bk_target_ip", "bk_target_cloud_id"},
	}
	for _, detect := range document["detects"].([]any) {
		detect.(map[string]any)["trigger_config"].(map[string]any)["check_window"] = 1
	}
	if edit != nil {
		edit(item)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
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

// report sets the series the query answers from the next round on.
func (fixture *roundsFixture) report(series ...roundsSeries) {
	fixture.seriesMu.Lock()
	defer fixture.seriesMu.Unlock()
	fixture.series = append([]roundsSeries(nil), series...)
}

// run runs one attempt of round n and fails the case when nothing was due.
func (fixture *roundsFixture) run(round int64) {
	fixture.t.Helper()
	if !fixture.attempt(round, 0) {
		fixture.t.Fatalf("round %d was not attempted", round)
	}
}

// mark is a position in the observations, for a case that reads one round's.
func (fixture *roundsFixture) mark() int {
	fixture.observationsMu.Lock()
	defer fixture.observationsMu.Unlock()
	return len(fixture.observations)
}

// since is every observation after a mark.
func (fixture *roundsFixture) since(mark int) []observability.Observation {
	fixture.observationsMu.Lock()
	defer fixture.observationsMu.Unlock()
	return append([]observability.Observation(nil), fixture.observations[mark:]...)
}

// outcomesSince is the no-data outcomes reported after a mark, summed.
func (fixture *roundsFixture) outcomesSince(mark int) map[string]int {
	outcomes := map[string]int{}
	for _, observation := range fixture.since(mark) {
		if observation.NoDataSlot != nil {
			outcomes[observation.NoDataSlot.Outcome] += observation.NoDataSlot.Plans
		}
	}
	return outcomes
}

// stallsSince is the persistent-skip reports after a mark, by outcome.
func (fixture *roundsFixture) stallsSince(mark int) []string {
	var stalls []string
	for _, observation := range fixture.since(mark) {
		if observation.NoDataStall != nil {
			stalls = append(stalls, observation.NoDataStall.Outcome)
		}
	}
	return stalls
}

// written is every event the sink wrote, in order.
func (fixture *roundsFixture) written() []contract.TriggerEventV1 {
	fixture.sink.mu.Lock()
	defer fixture.sink.mu.Unlock()
	return append([]contract.TriggerEventV1(nil), fixture.sink.written...)
}

// eventsAbout is the written events whose record names the dimension at the
// value, split into the no-data ones and the threshold ones.
func eventsAbout(events []contract.TriggerEventV1, dimension, value string) (noData, threshold []contract.TriggerEventV1) {
	for _, event := range events {
		var got string
		raw, named := event.RecordRef.Dimensions[dimension]
		if !named {
			continue
		}
		if json.Unmarshal(raw, &got) != nil {
			got = string(raw)
		}
		if got != value {
			continue
		}
		if noDataTagged(event) {
			noData = append(noData, event)
		} else {
			threshold = append(threshold, event)
		}
	}
	return noData, threshold
}

// noDataRecord is the Plan's stored no-data memory as the store holds it: the
// key and every field. A case compares two of these to say whether a round
// touched the record at all.
func (fixture *roundsFixture) noDataRecord() (string, map[string]string) {
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
		fields, err := fixture.redis.HGetAll(ctx, key).Result()
		if err != nil {
			fixture.t.Fatal(err)
		}
		if _, held := fields["_meta"]; held {
			return key, fields
		}
	}
	return "", nil
}

// sameRecord fails the case when the record moved between two reads.
func (fixture *roundsFixture) sameRecord(what string, before, after map[string]string) {
	fixture.t.Helper()
	if !reflect.DeepEqual(before, after) {
		fixture.t.Fatalf("%s: the no-data record changed:\nbefore %v\nafter  %v", what, before, after)
	}
}

// firstRoundUnder edits the strategy, has the control plane publish the edit, and
// returns the first round whose Segment carries it. Every round before it
// still runs the strategy as it was.
func (fixture *roundsFixture) firstRoundUnder(revision int, edit func(map[string]any)) int64 {
	fixture.t.Helper()
	ctx := context.Background()
	first, err := fixture.catalog.ReadFrozenSchedule(ctx, fixture.queryGroup, execution.EvaluationTime(fixture.evaluationAt(1)))
	if err != nil {
		fixture.t.Fatal(err)
	}
	installRoundsStrategy(fixture.t, ctx, fixture.redis, revision, edit)
	// The control plane reads the change on a refresh and publishes it on the one
	// that confirms it, which is what its own loop does on its cadence.
	for confirm := 0; confirm < 2; confirm++ {
		if err := fixture.bundle.refreshAndReconcile(ctx, true); err != nil {
			fixture.t.Fatalf("refresh after the strategy edit: %v", err)
		}
	}
	changed := int64(0)
	// waitFor is the package's ten-second watchdog for things the bundle
	// does on its own time.
	waitFor(fixture.t, "a Segment carrying the edited strategy", func() bool {
		for round := int64(2); round <= 30; round++ {
			schedule, err := fixture.catalog.ReadFrozenSchedule(ctx, fixture.queryGroup, execution.EvaluationTime(fixture.evaluationAt(round)))
			if err == nil && schedule.Segment.ObjectDigest != first.Segment.ObjectDigest {
				changed = round
				return true
			}
		}
		return false
	})
	return changed
}
