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
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
)

// The one deployment shape the page went blind on: every platform cache on
// the same instance as the runtime store, so one client serves four roles.
// The list says which, and no field of it carries a credential.
func TestResolvedEndpointsNameSharingAndCarryNoCredential(t *testing.T) {
	cfg := config.Default()
	cfg.Redis.RedisConnectionConfig = config.RedisConnectionConfig{Mode: config.RedisModeSentinel,
		MasterName: "mymaster", SentinelAddress: []string{"sentinel-a:26379", "sentinel-b:26379"},
		Username: "alarmd", Password: "top-secret", SentinelPassword: "sentinel-secret", DB: 8}
	cfg.Redis.StatePrefix = "alarmd:phase2:g2:runtime:v1"
	cfg.Kafka.LegacyAdapter.SnapshotPrefix = "bk_monitorv3.ee.cache"
	cfg.Kafka.Brokers = []string{"kafka-0:9092", "kafka-1:9092"}
	cfg.Kafka.TriggerEvent.Topic = "0bkmonitor_backend_event"
	cfg.PhaseTwo.Access.UQEndpoint = "http://unify-query:10205"
	cache := cfg.Redis.Connection()
	cache.DB = 0
	cfg.PlatformCache.Strategy = &cache
	cfg.PlatformCache.CMDB = &cache
	sharing := endpointSharing{runtimeIsSource: false, cmdbSharedWith: fleet.EndpointStrategyCache, compatOutputPresent: true}
	endpoints := resolveEndpoints(cfg, sharing)
	byRole := map[string]fleet.Endpoint{}
	for _, entry := range endpoints {
		byRole[entry.Role] = entry
	}
	if len(endpoints) != len(fleet.EndpointRoles) {
		t.Fatalf("%d endpoints for %d roles", len(endpoints), len(fleet.EndpointRoles))
	}
	for _, role := range fleet.EndpointRoles {
		if _, present := byRole[role]; !present {
			t.Errorf("no endpoint for role %s", role)
		}
	}
	state := byRole[fleet.EndpointStateRedis]
	if state.Address != "mymaster@sentinel-a:26379,sentinel-b:26379" || state.Mode != config.RedisModeSentinel || state.DB == nil || *state.DB != 8 ||
		state.Prefix != "alarmd:phase2:g2:runtime:v1" || state.SharedWith != "" || !state.Configured {
		t.Errorf("state redis = %+v", state)
	}
	strategy := byRole[fleet.EndpointStrategyCache]
	if strategy.DB == nil || *strategy.DB != 0 || strategy.Prefix != "bk_monitorv3.ee.cache" || strategy.SharedWith != "" {
		t.Errorf("strategy cache = %+v", strategy)
	}
	if cmdb := byRole[fleet.EndpointCMDBCache]; cmdb.SharedWith != fleet.EndpointStrategyCache {
		t.Errorf("cmdb cache does not say it shares the strategy cache's connection: %+v", cmdb)
	}
	if dynamic := byRole[fleet.EndpointDynamicConfig]; dynamic.Configured || dynamic.Address != "" {
		t.Errorf("an unrendered dynamic config reads as configured: %+v", dynamic)
	}
	if kafka := byRole[fleet.EndpointOutputKafka]; kafka.Address != "kafka-0:9092,kafka-1:9092" || kafka.Prefix != "0bkmonitor_backend_event" || !kafka.Configured {
		t.Errorf("kafka = %+v", kafka)
	}
	if query := byRole[fleet.EndpointQueryBackend]; query.Address != "http://unify-query:10205" || !query.Configured {
		t.Errorf("query backend = %+v", query)
	}
	// The red line: nothing that is a credential in the configuration appears
	// anywhere in the published list, whatever field it might be copied into.
	encoded, err := json.Marshal(endpoints)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"top-secret", "sentinel-secret", "alarmd\"", "\"username\"", "\"password\""} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("the endpoint list carries %q: %s", secret, encoded)
		}
	}
	// A standalone address is the address.
	standalone := config.RedisConnectionConfig{Mode: config.RedisModeStandalone, Address: "redis:6379", Password: "x"}
	if got := redisAddress(standalone); got != "redis:6379" {
		t.Errorf("standalone address = %q", got)
	}
}

// Which hook record each role reads: its own client where it has one, the
// client of the role it shares a connection with where it does not.
func TestRedisClientForRoleFollowsTheSharing(t *testing.T) {
	shared := endpointSharing{runtimeIsSource: true, cmdbSharedWith: fleet.EndpointStateRedis, dynamicSharedWith: fleet.EndpointCMDBCache}
	for role, want := range map[string]string{
		fleet.EndpointStrategyCache: "source", fleet.EndpointStateRedis: "source", fleet.EndpointCMDBCache: "source",
		fleet.EndpointDynamicConfig: "source", fleet.EndpointCompatOutput: "legacy_output", fleet.EndpointOutputKafka: "",
	} {
		if got := shared.redisClientForRole(role); got != want {
			t.Errorf("shared %s -> %q, want %q", role, got, want)
		}
	}
	separate := endpointSharing{}
	for role, want := range map[string]string{
		fleet.EndpointStateRedis: "runtime", fleet.EndpointCMDBCache: "cmdb", fleet.EndpointDynamicConfig: "dynamic_config",
	} {
		if got := separate.redisClientForRole(role); got != want {
			t.Errorf("separate %s -> %q, want %q", role, got, want)
		}
	}
}

// The health beside each entry comes from the hook record of the connection
// the role uses, and the writer evidence from the leader's source round.
func TestEndpointFactsReadTheSharedConnectionAndTheSourceRound(t *testing.T) {
	cfg := config.Default()
	cfg.Redis.Address = "redis:6379"
	cfg.Kafka.LegacyAdapter.SnapshotPrefix = "bk_monitorv3.ee.cache"
	recorder := metric.NewRecorder(metric.BuildInfo{})
	moment := time.Unix(5000, 0)
	hook := recorder.RedisHook("source")
	ctx, _ := hook.BeforeProcess(context.Background(), nil)
	ok := redis.NewStringCmd(ctx, "get", "a")
	_ = hook.AfterProcess(ctx, ok)
	sharing := endpointSharing{runtimeIsSource: true, cmdbSharedWith: fleet.EndpointStrategyCache, compatOutputPresent: true}
	age := int64(95)
	source := func() *fleet.SourceFacts {
		facts := fleet.NewSourceFacts(moment, map[string]int{"SOURCE_INCOMPLETE": 81}, nil)
		facts.ChangeSignalPresent, facts.ChangeSignalAgeSeconds = true, &age
		return facts
	}
	// The hook stamps with the wall clock, so the facts read the same clock
	// and the age is asserted small rather than exact.
	facts := endpointFactsSource(cfg, sharing, recorder, nil, nil, source, time.Now)()
	byRole := map[string]fleet.Endpoint{}
	for _, entry := range facts {
		byRole[entry.Role] = entry
	}
	// Three roles on one connection: all three read the one record.
	for _, role := range []string{fleet.EndpointStateRedis, fleet.EndpointStrategyCache, fleet.EndpointCMDBCache} {
		entry := byRole[role]
		if entry.LastSuccessAgeSeconds == nil || *entry.LastSuccessAgeSeconds < 0 || *entry.LastSuccessAgeSeconds > 5 {
			t.Errorf("%s last success age = %v, want a few seconds from the shared source record", role, entry.LastSuccessAgeSeconds)
		}
		if entry.LastFailureAgeSeconds != nil || entry.LastFailure != "" {
			t.Errorf("%s carries a failure nobody recorded: %+v", role, entry)
		}
	}
	// The compat output client has issued nothing: no health, not zero.
	if compat := byRole[fleet.EndpointCompatOutput]; compat.LastSuccessAgeSeconds != nil {
		t.Errorf("a connection that issued nothing reads as recently succeeded: %+v", compat)
	}
	writer := byRole[fleet.EndpointStrategyCache].Writer
	if writer == nil || !writer.Present || writer.Count != 81 || writer.State != "marker_present" || writer.AgeSeconds == nil || *writer.AgeSeconds != 95 {
		t.Errorf("strategy cache writer = %+v, want 81 listed with a 95-second-old marker", writer)
	}
	// A follower has no round and says nothing about the writer.
	none := endpointFactsSource(cfg, sharing, recorder, nil, nil, func() *fleet.SourceFacts { return nil },
		func() time.Time { return moment })()
	for _, entry := range none {
		if entry.Role == fleet.EndpointStrategyCache && entry.Writer != nil {
			t.Errorf("a follower reports writer evidence it never read: %+v", entry.Writer)
		}
	}
}

// The composition becomes the fleet's facts as it is, with the change marker
// beside it; a round without a marker has no age.
func TestSourceFactsOfCarriesTheCompositionAndTheMarker(t *testing.T) {
	at := time.Unix(7000, 0)
	composition := &controlplane.CatalogComposition{
		Objects: map[controlplane.Disposition]int{controlplane.DispositionAccepted: 0, controlplane.DispositionSourceIncomplete: 2},
		WithheldObjects: []controlplane.ObjectDisposition{
			{SourceID: "9", Scope: "STRATEGY", Disposition: controlplane.DispositionSourceIncomplete, Reason: "SOURCE_IDENTITY_UNAVAILABLE"},
			{SourceID: "12", Scope: "LEVEL", LevelID: 3, Disposition: controlplane.DispositionSourceIncomplete, Reason: "SOURCE_OBJECT_INCOMPLETE", FieldPath: "items"},
		},
	}
	facts := sourceFactsOf(phaseTwoControlRefreshResult{Composition: composition, ChangeSignalPresent: true, ChangeSignalAgeSeconds: 42}, at)
	if !facts.At.Equal(at) || facts.Listed != 2 || facts.Accepted != 0 || !facts.Blocked() {
		t.Fatalf("facts = %+v", facts)
	}
	if !facts.ChangeSignalPresent || facts.ChangeSignalAgeSeconds == nil || *facts.ChangeSignalAgeSeconds != 42 {
		t.Errorf("marker = %v/%v, want present at 42", facts.ChangeSignalPresent, facts.ChangeSignalAgeSeconds)
	}
	if len(facts.Withheld) != 2 || facts.Withheld[0].Samples[0].StrategyID != "9" ||
		facts.Withheld[1].Samples[0].LevelID != 3 || facts.Withheld[1].Samples[0].FieldPath != "items" {
		t.Errorf("withheld = %+v", facts.Withheld)
	}
	without := sourceFactsOf(phaseTwoControlRefreshResult{Composition: composition}, at)
	if without.ChangeSignalPresent || without.ChangeSignalAgeSeconds != nil {
		t.Errorf("a round without a marker carries an age: %+v", without)
	}
}
