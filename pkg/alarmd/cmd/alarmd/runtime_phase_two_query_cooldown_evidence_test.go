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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cmdbcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obevidence"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// What an owner writes is what store.inspect reads: the record goes in
// through the store the bundle builds for the Runners,
// and comes out through the store.inspect built for the CLI from the same
// configuration. A reader looking under another key would answer "missing"
// for every Query Group in the pool, which reads as "nothing persisted".
func TestStoreInspectReadsThePoolRecordTheOwnerWrote(t *testing.T) {
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	cfg := config.Default()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd:test:cooldown-evidence"

	entered := time.Unix(1_790_000_000, 0).UTC()
	record := scheduler.QueryCooldownRecord{QueryGroup: "qg-pooled", OwnerEpoch: 7, EnteredAt: entered,
		Until: entered.Add(3 * time.Minute), LastQueryAt: entered.Add(time.Minute), Failures: 5,
		ExitedAt: entered.Add(-time.Hour), ExitReason: scheduler.QueryCooldownRecovered, Reentries: 1}
	writer := newProductionQueryCooldownStore(cfg, client, metric.NewRecorder(metric.BuildInfo{}), observability.NopObserver{})
	fence := execution.OwnerFence{QueryGroup: "qg-pooled", OwnerID: "worker", OwnerEpoch: 7, LeaseToken: "token"}
	if err := writer.SaveQueryCooldown(ctx, fence, record); err != nil {
		t.Fatal(err)
	}

	var clients []redis.UniversalClient
	t.Cleanup(func() {
		for _, c := range clients {
			_ = c.Close()
		}
	})
	newClient := func(connection config.RedisConnectionConfig) redis.UniversalClient {
		c := redis.NewClient(&redis.Options{Addr: connection.Address, DB: connection.DB})
		clients = append(clients, c)
		return c
	}
	store, _, _ := deploymentReads(cfg, nil, nil, newClient, nil)
	inspect := operationNamed(t, store, "store.inspect")

	read := func(queryGroup string) obevidence.Result {
		t.Helper()
		out := inspect.Run(ctx, obchannel.Params{"family": obevidence.FamilyQueryCooldown, "query_group": queryGroup})
		result, ok := out.Value.(obevidence.Result)
		if !ok {
			t.Fatalf("store.inspect answered %T: %+v", out.Value, out)
		}
		return result
	}
	got := read("qg-pooled")
	if got.Status != "ok" || got.Location.Role != "runtime" || got.Location.Key != queryCooldownPrefix(cfg)+":qg-pooled" {
		t.Fatalf("pooled record = %s at %+v, want ok at the key the owner wrote", got.Status, got.Location)
	}
	if got.TTLMS == nil || *got.TTLMS <= 0 {
		t.Fatalf("record TTL = %v, want the store's expiry", got.TTLMS)
	}
	raw, _ := json.Marshal(got.Value)
	var back scheduler.QueryCooldownRecord
	if err := json.Unmarshal(raw, &back); err != nil || back.OwnerEpoch != 7 || back.Failures != 5 || !back.EnteredAt.Equal(entered) ||
		back.ExitReason != scheduler.QueryCooldownRecovered || back.Reentries != 1 || !back.Until.Equal(record.Until) {
		t.Fatalf("record read back = %s (%v), want what the owner wrote", raw, err)
	}
	if missing := read("qg-never-pooled"); missing.Status != "missing" {
		t.Fatalf("a Query Group with no record = %s, want missing", missing.Status)
	}
}

// The host record store.inspect reads is the one the host index loads: the
// CLI's reads are built from the same platform key prefix the admission
// chain's CMDB reader is. Built without it, every host would read as not
// configured.
func TestStoreInspectReadsTheHostRecordTheIndexLoads(t *testing.T) {
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	cfg := config.Default()
	cfg.Redis.Address = address
	cfg.Kafka.LegacyAdapter.SnapshotPrefix = "bk_test.ee"
	indexReader, err := cmdbcache.NewReader(client, cfg.PlatformKeyPrefix())
	if err != nil {
		t.Fatal(err)
	}
	record := `{"bk_host_id":101,"bk_host_innerip":"192.0.2.10","bk_cloud_id":0,"bk_biz_id":2}`
	if err := client.HSet(ctx, indexReader.HostCacheKey(), "101", record).Err(); err != nil {
		t.Fatal(err)
	}
	var clients []redis.UniversalClient
	t.Cleanup(func() {
		for _, c := range clients {
			_ = c.Close()
		}
	})
	newClient := func(connection config.RedisConnectionConfig) redis.UniversalClient {
		c := redis.NewClient(&redis.Options{Addr: connection.Address, DB: connection.DB})
		clients = append(clients, c)
		return c
	}
	store, _, _ := deploymentReads(cfg, nil, nil, newClient, nil)
	out := operationNamed(t, store, "store.inspect").Run(ctx, obchannel.Params{"family": obevidence.FamilyCMDBHost, "host": "101"})
	got, ok := out.Value.(obevidence.Result)
	if !ok || got.Status != "ok" || got.Location.Role != "cmdb_cache" || got.Location.Key != indexReader.HostCacheKey() {
		t.Fatalf("host 101 = %+v, want ok at the key the index loads (%s)", got, indexReader.HostCacheKey())
	}
}

func operationNamed(t *testing.T, ops []obchannel.Operation, id string) obchannel.Operation {
	t.Helper()
	for _, op := range ops {
		if op.ID == id {
			return op
		}
	}
	t.Fatalf("%s is not among the operations", id)
	return obchannel.Operation{}
}

// The CLI's store reads report an unanswered read by its reason through the
// hook the builder is given, on every binding it builds.
func TestTheCLIStoreReadsReportAnUnansweredReadByReason(t *testing.T) {
	cfg := config.Default()
	cfg.Redis.Address = "127.0.0.1:1"
	cfg.Redis.StatePrefix = "alarmd:test:unanswered"
	var clients []redis.UniversalClient
	t.Cleanup(func() {
		for _, c := range clients {
			_ = c.Close()
		}
	})
	newClient := func(connection config.RedisConnectionConfig) redis.UniversalClient {
		c := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
		clients = append(clients, c)
		return c
	}
	var heard []string
	store, _, _ := deploymentReads(cfg, nil, nil, newClient, func(reason string) { heard = append(heard, reason) })
	inspect := operationNamed(t, store, "store.inspect")
	out := inspect.Run(context.Background(), obchannel.Params{"family": obevidence.FamilyQueryCooldown, "query_group": "qg"})
	if out.Error == nil || out.Error.Reason != "connection_refused" || len(heard) != 1 || heard[0] != "connection_refused" {
		t.Fatalf("error %+v heard %v", out.Error, heard)
	}
}

type capturedObservations []observability.Observation

func (captured *capturedObservations) Observe(_ context.Context, observation observability.Observation) {
	*captured = append(*captured, observation)
}

// Every CLI client's unanswered call is counted by its reason; the
// authorization store's is also a limited auth_store line carrying the
// error's text, which its public answer leaves out. An evidence read's text
// is in its own result, so it logs nothing.
func TestCLIRedisFailuresAreCountedAndTheAuthStoresTextLogged(t *testing.T) {
	recorder := metric.NewRecorder(metric.BuildInfo{})
	var observed capturedObservations
	report := cliRedisFailures(recorder, &observed)
	report("auth", "sentinel_unreachable", "redis: all sentinels specified in configuration are unreachable")
	report("evidence", "timeout", "")
	for client, reason := range map[string]string{"auth": "sentinel_unreachable", "evidence": "timeout"} {
		if got := counterValue(t, recorder, "bkmonitor_alarmd_diagnostic_redis_failures_total", map[string]string{"client": client, "reason": reason}); got != 1 {
			t.Fatalf("%s/%s counted %v", client, reason, got)
		}
	}
	if len(observed) != 1 || observed[0].Stage != observability.StageAuthStore ||
		!strings.Contains(observed[0].Err.Error(), "all sentinels specified in configuration are unreachable") {
		t.Fatalf("observed %+v", observed)
	}
}
