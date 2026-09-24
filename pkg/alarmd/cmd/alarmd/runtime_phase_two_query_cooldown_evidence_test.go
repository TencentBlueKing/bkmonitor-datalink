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
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

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
	store, _, _ := deploymentReads(cfg, nil, nil, newClient)
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
