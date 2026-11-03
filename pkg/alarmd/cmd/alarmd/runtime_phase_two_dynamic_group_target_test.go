// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

// A strategy whose target is a dynamic group, as the platform's strategy
// cache keeps it - the group id, not its hosts - is evaluated over the hosts
// the platform's group hash says the group holds, read for the strategy's
// tenant, as Python reads them when it matches. Production wiring end to end:
// the strategy cache, the compiled scope, the Slot's reading of the group on
// the CMDB connection, admission, and the events written.

import (
	"context"
	"encoding/json"
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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

const dynamicGroupTargetID = "03c79170-7ee1-11ee-a820-5e22272a2c60"

type dynamicGroupFixture struct {
	t        *testing.T
	sink     *followsSink
	recorder *metric.Recorder
	runner   phaseTwoQueryGroupRuntime
	clock    *atomic.Int64
	base     int64

	mu           sync.Mutex
	observations []observability.Observation
}

// startDynamicGroupFixture opens the production bundle over one strategy of
// tenant-a whose target is one dynamic group, data arriving for hosts A and B
// both over the threshold, and the group hashes as given: field the group id,
// per hash key.
func startDynamicGroupFixture(t *testing.T, groups map[string]string) *dynamicGroupFixture {
	t.Helper()
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	cfg := validGoAccessRuntimeConfig()
	followsSeedHosts(t, ctx, client, cfg.PlatformKeyPrefix())
	for key, value := range groups {
		if err := client.HSet(ctx, strings.ReplaceAll(key, "<prefix>", cfg.PlatformKeyPrefix()), dynamicGroupTargetID, value).Err(); err != nil {
			t.Fatal(err)
		}
	}
	installDynamicGroupStrategy(t, ctx, client)

	const interval = int64(60)
	base := time.Now().Unix()
	base += interval - base%interval
	fixture := &dynamicGroupFixture{t: t, clock: &atomic.Int64{}, base: base, recorder: metric.NewRecorder(metric.BuildInfo{})}
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
		point := strconv.FormatInt((end-1)*1000, 10)
		series := make([]string, 0, 2)
		for _, ip := range []string{followsHostA, followsHostB} {
			series = append(series, `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],`+
				`"group_keys":["bk_target_cloud_id","bk_target_ip"],"group_values":["0","`+ip+`"],`+
				`"values":[[`+point+`,95]]}`)
		}
		_, _ = writer.Write([]byte(`{"series":[` + strings.Join(series, ",") + `],"status":null,"trace_id":"dynamic-group",` +
			`"is_partial":false,"result_table_id":["system.cpu"]}`))
	}))
	t.Cleanup(uqServer.Close)

	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd-dynamic-group"
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

	fixture.sink = &followsSink{mode: followsOK}
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, fixture.recorder, observability.Discard(observability.ComponentRuntime),
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
				fixture.mu.Lock()
				defer fixture.mu.Unlock()
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
		t.Fatalf("Query Groups = %v, want one: the strategy was not admitted", bundle.queryGroups)
	}
	fixture.runner = settledRunner(bundle, bundle.queryGroups[0])
	return fixture
}

// installDynamicGroupStrategy writes the strategy with its target as the
// platform's strategy cache keeps a dynamic group target.
func installDynamicGroupStrategy(t *testing.T, ctx context.Context, client *redis.Client) {
	t.Helper()
	raw, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["update_time"] = 1725000007
	document["strategy_revision"] = 7
	document["scenario"] = "os"
	item := document["items"].([]any)[0].(map[string]any)
	query := item["query_configs"].([]any)[0].(map[string]any)
	query["agg_interval"] = 60
	query["agg_dimension"] = []any{"bk_target_ip", "bk_target_cloud_id"}
	item["target"] = []any{[]any{map[string]any{"field": "dynamic_group", "method": "eq",
		"value": []any{map[string]any{"dynamic_group_id": dynamicGroupTargetID}}}}}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]any{
		"alarm-config.strategy_ids":                  `[` + followsStrategyID + `]`,
		"alarm-config.strategy_" + followsStrategyID: encoded,
		"alarm-config.last_updated":                  "1725000007",
	} {
		if err := client.Set(ctx, key, value, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
}

// runRound runs the Slot one period after the base, half a period past its
// boundary.
func (fixture *dynamicGroupFixture) runRound() {
	fixture.t.Helper()
	fixture.clock.Store((fixture.base+30)*1000 + 60*1000)
	if _, _, err := fixture.runner.RunOne(context.Background()); err != nil {
		fixture.t.Fatalf("RunOne error = %v", err)
	}
}

// eventsFor counts the threshold events written for one host.
func (fixture *dynamicGroupFixture) eventsFor(ip string) int {
	fixture.sink.mu.Lock()
	defer fixture.sink.mu.Unlock()
	count := 0
	for _, event := range fixture.sink.written {
		var got string
		if !noDataTagged(event) && json.Unmarshal(event.RecordRef.Dimensions["bk_target_ip"], &got) == nil && got == ip {
			count++
		}
	}
	return count
}

// groupSelector is the group's state and reason as the Slot reported its
// target scope's groups.
func (fixture *dynamicGroupFixture) groupSelector() (string, string, bool) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	for _, observation := range fixture.observations {
		facts := observation.TargetResolution
		if facts == nil || !facts.ScopeGroups || facts.StrategyID != followsStrategyID {
			continue
		}
		for _, selector := range facts.Selectors {
			if selector.Kind == "dynamic_group" && selector.ID == dynamicGroupTargetID {
				return selector.State, selector.Reason, true
			}
		}
	}
	return "", "", false
}

// admissions is series_admission_total for the target scope by result and
// reason.
func (fixture *dynamicGroupFixture) admissions() map[string]float64 {
	fixture.t.Helper()
	families, err := fixture.recorder.Gatherer().Gather()
	if err != nil {
		fixture.t.Fatal(err)
	}
	counts := map[string]float64{}
	for _, family := range families {
		if !strings.HasSuffix(family.GetName(), "series_admission_total") {
			continue
		}
		for _, series := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range series.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["filter"] == "target_scope" && series.GetCounter().GetValue() > 0 {
				counts[labels["result"]+"/"+labels["reason"]] += series.GetCounter().GetValue()
			}
		}
	}
	return counts
}

func TestADynamicGroupTargetAlertsOnTheHostsItsTenantsGroupHolds(t *testing.T) {
	// Host 1 is A, host 2 is B (followsSeedHosts). The default tenant's hash
	// names B under the same id: reading it instead of tenant-a's would
	// alert on B and not on A.
	fixture := startDynamicGroupFixture(t, map[string]string{
		"tenant-a.<prefix>.cache.cmdb.dynamic_group": `{"bk_biz_id":2,"bk_inst_ids":[1],"bk_obj_id":"host","name":"a","id":"` + dynamicGroupTargetID + `"}`,
		"<prefix>.cache.cmdb.dynamic_group":          `{"bk_biz_id":2,"bk_inst_ids":[2],"bk_obj_id":"host","name":"b","id":"` + dynamicGroupTargetID + `"}`,
	})
	fixture.runRound()
	if a, b := fixture.eventsFor(followsHostA), fixture.eventsFor(followsHostB); a == 0 || b != 0 {
		t.Fatalf("threshold events: host A %d, host B %d; want A's only", a, b)
	}
	if state, reason, found := fixture.groupSelector(); !found || state != "OK" || reason != "none" {
		t.Fatalf("the group as the Slot read it: %q %q (reported %v), want OK none", state, reason, found)
	}
	if counts := fixture.admissions(); counts["rejected/out_of_scope"] == 0 || counts["rejected/dynamic_group_unavailable"] != 0 {
		t.Fatalf("target scope admissions %v: host B is outside the group, decided", counts)
	}
}

func TestADynamicGroupTargetWhoseGroupIsNotInItsTenantsHashAlertsOnNoHost(t *testing.T) {
	// The group is only in the default tenant's hash; tenant-a's has another.
	fixture := startDynamicGroupFixture(t, map[string]string{
		"<prefix>.cache.cmdb.dynamic_group": `{"bk_biz_id":2,"bk_inst_ids":[1,2],"bk_obj_id":"host","name":"ab","id":"` + dynamicGroupTargetID + `"}`,
	})
	fixture.runRound()
	if a, b := fixture.eventsFor(followsHostA), fixture.eventsFor(followsHostB); a != 0 || b != 0 {
		t.Fatalf("threshold events: host A %d, host B %d; want none", a, b)
	}
	if state, reason, found := fixture.groupSelector(); !found || state != "Unavailable" || reason != "key_missing" {
		t.Fatalf("the group as the Slot read it: %q %q (reported %v), want Unavailable key_missing", state, reason, found)
	}
	if counts := fixture.admissions(); counts["rejected/dynamic_group_unavailable"] < 2 || counts["rejected/out_of_scope"] != 0 {
		t.Fatalf("target scope admissions %v: both hosts wait on the group, neither is decided outside", counts)
	}
}
