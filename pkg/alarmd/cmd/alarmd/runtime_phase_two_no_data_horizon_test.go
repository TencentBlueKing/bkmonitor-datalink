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

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/platformsettings"
)

// The horizon an operator wrote in the config reaches a Plan the running
// process compiled.
//
// This is the assembly's own case, and it is here because the defect it guards
// is the one this feature already had: every layer below carried the horizon
// and nothing at the top ever set it, so the whole feature sat idle behind a
// zero that means "track indefinitely" - indistinguishable from a deployment
// that chose not to configure it. The reconciler's case covers the layer below;
// it passes with the single line in the production assembly deleted, because
// that line is the only place the configured value enters the process at all.
//
// So this case starts where the process starts: a real config, the production
// bundle, and the Plan read back off the frozen Slot contract the Worker would
// execute.
func TestTheConfiguredNoDataHorizonReachesACompiledPlan(t *testing.T) {
	const horizon = 600
	if got := noDataHorizonOfFirstDuePlan(t, horizon, ""); got != horizon {
		t.Fatalf("the compiled Plan carries horizon %d, want the configured %d. The value is read from "+
			"phase_two.no_data.tracking_horizon_seconds and has to survive every step between there and the "+
			"Slot contract, which is where detection reads it", got, horizon)
	}

	// The control, and the contract's default: a deployment configuring no
	// horizon compiles one day. Without this the case above passes on an
	// assembly that hands every Plan the configured number from anywhere.
	if got := noDataHorizonOfFirstDuePlan(t, 0, ""); got != 86400 {
		t.Fatalf("a deployment configuring no horizon compiled %d, want the contract's one day (86400): "+
			"every group gets a finite horizon unless someone states another", got)
	}

	// The dynamic layer, as the platform's distribution publishes it, over
	// the deployment's values: 3600 published beats 600 configured, all the
	// way into the Plan the Slot runs.
	if got := noDataHorizonOfFirstDuePlan(t, horizon, "3600"); got != 3600 {
		t.Fatalf("with 3600 published under base_config.domains.strategy the compiled Plan carries %d, "+
			"want the dynamic value over the configured %d", got, horizon)
	}
}

// noDataHorizonOfFirstDuePlan opens the production bundle against a config
// stating this horizon and returns the horizon frozen into the first due Plan.
//
// published, when not empty, is the raw JSON the platform's distribution
// carries for the dynamic horizon; the distribution is then read from the
// strategy cache's own Redis.
func noDataHorizonOfFirstDuePlan(t *testing.T, horizon int64, published string) int64 {
	t.Helper()
	ctx := context.Background()
	fixture := startCutoverFixtureWith(t,
		func(cfg *config.Config) {
			// Absent rather than zero for the unconfigured deployment: the
			// leaf is read by presence, and a written zero is refused.
			if horizon != 0 {
				cfg.PhaseTwo.NoData.TrackingHorizonSeconds = &horizon
			}
			if published != "" {
				connection := cfg.StrategySourceRedis()
				cfg.PlatformCache.DynamicConfig = &connection
			}
		},
		observability.Discard(observability.ComponentRuntime),
		func(ctx context.Context, client *redis.Client, cfg config.Config) {
			enableNoDataOnCutoverStrategies(ctx, client, cfg)
			if published == "" {
				return
			}
			prefix := cfg.PhaseTwo.PlatformSettings.RedisKeyPrefix
			key := platformsettings.ConfigKey(prefix, platformsettings.Tenant,
				platformsettings.FieldNoDataTrackingHorizonSeconds.DBKey())
			if err := client.Set(ctx, key, published, 0).Err(); err != nil {
				panic("no_data horizon fixture: publish " + key + ": " + err.Error())
			}
			if err := client.Set(ctx, platformsettings.RevisionKey(prefix), "horizon-test", 0).Err(); err != nil {
				panic("no_data horizon fixture: publish revision: " + err.Error())
			}
		},
	)

	schedule, err := fixture.production.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, fixture.queryGroup)
	if err != nil {
		t.Fatal(err)
	}
	slot, ok := schedule.FirstSlot()
	if !ok {
		t.Fatal("the initial Segment holds no Slot, so there is no Plan to read a horizon off")
	}
	frozen, err := fixture.production.dependencies.Catalog.FreezeSlotContract(ctx, execution.FreezeSlotContractRequest{
		QueryGroup: fixture.queryGroup, ScheduleRevision: schedule.Segment.ScheduleRevision,
		ScheduleSegmentStart: schedule.Segment.Start, EvaluationTime: slot,
		DuePlans: schedule.DuePlanRefs(slot),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(frozen.DuePlans) == 0 {
		t.Fatal("no Plan is due in the first Slot")
	}
	noData := frozen.DuePlans[0].CompiledPlan.NoData()
	if noData == nil {
		// The fixture's strategy detects no-data only because the hook below
		// turned it on. Without this the horizon would read zero for a reason
		// that has nothing to do with the config, and both branches of the
		// case above would agree for the wrong reason.
		t.Fatal("the compiled Plan detects no no-data at all, so it carries no horizon to read")
	}
	return noData.TrackingHorizonSeconds
}

// enableNoDataOnCutoverStrategies turns on no-data detection for the strategies
// the cutover fixture installs, which configure none of their own.
//
// Written against the stored document rather than by building a new one: the
// horizon is frozen out of whatever the compiler read, so a document this test
// invented would prove the compiler freezes the horizon into documents this
// test invents.
func enableNoDataOnCutoverStrategies(ctx context.Context, client *redis.Client, _ config.Config) {
	for _, key := range []string{"alarm-config.strategy_1001", "alarm-config.strategy_1002"} {
		raw, err := client.Get(ctx, key).Bytes()
		if err != nil {
			panic("no_data horizon fixture: read " + key + ": " + err.Error())
		}
		var document map[string]any
		if err := json.Unmarshal(raw, &document); err != nil {
			panic("no_data horizon fixture: decode " + key + ": " + err.Error())
		}
		for _, item := range document["items"].([]any) {
			item.(map[string]any)["no_data_config"] = map[string]any{"is_enabled": true, "continuous": 3}
		}
		encoded, err := json.Marshal(document)
		if err != nil {
			panic("no_data horizon fixture: encode " + key + ": " + err.Error())
		}
		if err := client.Set(ctx, key, encoded, 0).Err(); err != nil {
			panic("no_data horizon fixture: write " + key + ": " + err.Error())
		}
	}
}
