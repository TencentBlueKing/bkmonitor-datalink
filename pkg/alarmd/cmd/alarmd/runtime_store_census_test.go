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
	"fmt"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
)

// The Control Leader takes a census of each store it writes to, reported by
// family on the scrape and counted under the store_census caller of the
// store's client; a replica that does not lead reports none, and one that
// stops leading forgets its last.
func TestTheLeaderReportsWhatEachStoreHoldsByFamily(t *testing.T) {
	_, server := startPhaseTwoRedis(t)
	recorder := metric.NewRecorder(metric.BuildInfo{Version: "test"})
	client := redis.NewClient(server.Options())
	client.AddHook(recorder.RedisHook("source"))
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	for index := range 40 {
		if err := client.Set(ctx, fmt.Sprintf("alarmd:state:%064x", index), "value", 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	census := &storeCensus{now: time.Now, stores: []censusStore{{name: "source", client: client}}}
	if err := recorder.BindStoreCensus(census.read); err != nil {
		t.Fatal(err)
	}
	gather := func() map[string]float64 {
		t.Helper()
		families, err := recorder.Gatherer().Gather()
		if err != nil {
			t.Fatal(err)
		}
		readings := map[string]float64{}
		for _, family := range families {
			for _, series := range family.GetMetric() {
				labels := ""
				for _, label := range series.GetLabel() {
					labels += "/" + label.GetValue()
				}
				readings[family.GetName()+labels] = series.GetGauge().GetValue()
			}
		}
		return readings
	}

	census.measure(ctx, false)
	if readings := gather(); readings["bkmonitor_alarmd_store_census_keys/source"] != 0 {
		t.Fatalf("a replica that does not lead reported %v", readings)
	}
	census.measure(ctx, true)
	readings := gather()
	if readings["bkmonitor_alarmd_store_census_keys/source"] != 40 || readings["bkmonitor_alarmd_store_census_exact/source"] != 1 ||
		readings["bkmonitor_alarmd_store_census_family_keys/source/alarmd:state:*"] != 40 ||
		readings["bkmonitor_alarmd_store_census_family_samples/source/alarmd:state:*"] != 40 ||
		readings["bkmonitor_alarmd_store_census_family_bytes/source/alarmd:state:*"] <= 0 {
		t.Fatalf("the Leader's census = %v, want the store's 40 keys in one family, weighed", readings)
	}
	operations, _ := callerCounts(t, recorder)
	if operations["source/store_census"] == 0 {
		t.Fatalf("operations %v, want the census counted under store_census", operations)
	}
	census.measure(ctx, false)
	if readings := gather(); readings["bkmonitor_alarmd_store_census_keys/source"] != 0 {
		t.Fatalf("a replica that stopped leading still reports %v", readings)
	}
}

// Each store is measured once, under the first name it was given: the
// runtime store when it is the source is the source, the compatibility
// output's service Redis is a third store only when it is neither, and a
// store this process did not open is none.
func TestEachStoreIsMeasuredOnce(t *testing.T) {
	source, runtime, service := redis.NewClient(&redis.Options{}), redis.NewClient(&redis.Options{}), redis.NewClient(&redis.Options{})
	t.Cleanup(func() { _ = source.Close(); _ = runtime.Close(); _ = service.Close() })
	names := func(stores []censusStore) string {
		var joined string
		for _, store := range stores {
			joined += "/" + store.name
		}
		return joined
	}
	at := func(name string, client redis.UniversalClient, address string) storeAt {
		return storeAt{censusStore{name: name, client: client}, config.RedisConnectionConfig{Address: address}}
	}
	for want, candidates := range map[string][]storeAt{
		"/source/runtime/legacy_output": {at("source", source, "a"), at("runtime", runtime, "b"), at("legacy_output", service, "c")},
		"/source/legacy_output":         {at("source", source, "a"), at("runtime", source, "a"), at("legacy_output", service, "c")},
		"/source/runtime":               {at("source", source, "a"), at("runtime", runtime, "b"), at("legacy_output", service, "a")},
		"/source":                       {at("source", source, "a"), at("runtime", source, "a"), at("legacy_output", nil, "c")},
	} {
		if got := names(distinctStores(candidates...)); got != want {
			t.Errorf("stores = %s, want %s", got, want)
		}
	}

	// From the configuration: the compatibility output's service Redis on
	// its own address is a third store; on the runtime store's, with only its
	// timeouts its own, it is the runtime store.
	var cfg config.Config
	cfg.Redis.Address = "runtime:6379"
	strategy := config.RedisConnectionConfig{Address: "strategy:6379"}
	cfg.PlatformCache.Strategy = &strategy
	cfg.Kafka.LegacyAdapter.ServiceRedis = config.RedisConnectionConfig{Address: "service:6379"}
	if got := names(censusStoresOf(cfg, source, runtime, service)); got != "/source/runtime/legacy_output" {
		t.Errorf("stores of three addresses = %s, want all three", got)
	}
	cfg.Kafka.LegacyAdapter.ServiceRedis = config.RedisConnectionConfig{Address: "runtime:6379", ReadTimeout: config.Duration(time.Second)}
	if got := names(censusStoresOf(cfg, source, runtime, service)); got != "/source/runtime" {
		t.Errorf("stores with the service on the runtime address = %s, want it measured as the runtime store", got)
	}
}

// The census names keys with every key prefix the deployment configures,
// as it is spelled: each one alone keeps its segment, where the built-in
// words would fold it.
func TestTheCensusKnowsTheDeploymentsPrefixes(t *testing.T) {
	for name, configure := range map[string]func(*config.Config, string){
		"state":             func(cfg *config.Config, prefix string) { cfg.Redis.StatePrefix = prefix },
		"strategy cache":    func(cfg *config.Config, prefix string) { cfg.PhaseTwo.Control.StrategyCachePrefix = prefix },
		"platform key":      func(cfg *config.Config, prefix string) { cfg.Kafka.LegacyAdapter.SnapshotPrefix = prefix },
		"platform settings": func(cfg *config.Config, prefix string) { cfg.PhaseTwo.PlatformSettings.RedisKeyPrefix = prefix },
		"dynamic groups":    func(cfg *config.Config, prefix string) { cfg.PlatformCache.DynamicGroupKeyPrefix = &prefix },
		"linkd":             func(cfg *config.Config, prefix string) { cfg.PhaseTwo.Linkd.KeyPrefix = prefix },
		"pod cache": func(cfg *config.Config, prefix string) {
			cfg.Kafka.LegacyAdapter.PodCache = &config.LegacyPodCacheConfig{KeyPrefix: prefix}
		},
	} {
		var cfg config.Config
		configure(&cfg, "deployment[x]")
		if got := censusVocabulary(cfg).FamilyOf("deployment[x]:state:1"); got != "deployment[x]:state:*" {
			t.Errorf("%s prefix: family = %q, want it kept", name, got)
		}
	}
}
