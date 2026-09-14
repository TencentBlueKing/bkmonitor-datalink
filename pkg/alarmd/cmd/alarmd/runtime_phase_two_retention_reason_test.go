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
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

func retentionTestCatalog(strategyID string, intervalSeconds int64) controlplane.Catalog {
	return controlplane.Catalog{QueryGroups: []controlplane.QueryGroup{{Plans: []controlplane.FrozenPlan{{
		Identity:     execution.PlanIdentity{TenantID: "tenant-a", BusinessID: "2", StrategyID: strategyID},
		ScheduleSpec: execution.ScheduleSpec{EvaluationIntervalSeconds: intervalSeconds, Timezone: "UTC"},
	}}}}}
}

// The two refusals are different problems for different people: one
// strategy whose schedule does not clear the downstream reserve, and a
// deployment whose Catalog TTL is shorter than its own longest plan needs.
// A refused round publishes nothing and leaves only this text behind, so
// the text has to say which one, and with what numbers.
func TestCatalogRetentionValidatorSaysWhichRefusalItIs(t *testing.T) {
	t.Run("one plan does not clear the reserve", func(t *testing.T) {
		cfg := validGoAccessRuntimeConfig()
		reserve := cfg.PhaseTwo.Access.DownstreamExecutionReserve.Duration()
		// An evaluation interval no longer than the reserve: the plan
		// completes no later than the reserve wants to start.
		interval := int64(reserve / time.Second)
		if interval <= 0 {
			t.Fatalf("downstream execution reserve %s is under a second; the fixture cannot express this", reserve)
		}
		err := phaseTwoCatalogRetentionValidator(cfg)(retentionTestCatalog("4711", interval))
		if !errors.Is(err, scheduler.ErrSnapshotRetentionInsufficient) {
			t.Fatalf("error=%v, want it to still match the sentinel every caller tests for", err)
		}
		for _, want := range []string{"4711", "downstream execution reserve", reserve.String()} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q does not name %q", err, want)
			}
		}
		if strings.Contains(err.Error(), "Catalog TTL") {
			t.Fatalf("error %q reads as the deployment's TTL, which is the other refusal", err)
		}
	})

	t.Run("a plan is evaluated less often than the retention covers", func(t *testing.T) {
		cfg := validGoAccessRuntimeConfig()
		supported := int64(phaseTwoMaxSupportedEvaluationInterval / time.Second)
		// The cadence the deployment actually ran into was one day, which the
		// retention now covers exactly; this is the next Plan along.
		if err := phaseTwoCatalogRetentionValidator(cfg)(retentionTestCatalog("4712", supported)); err != nil {
			t.Fatalf("a plan at the supported cadence must fit: %v", err)
		}
		err := phaseTwoCatalogRetentionValidator(cfg)(retentionTestCatalog("4713", supported+60))
		if !errors.Is(err, scheduler.ErrSnapshotRetentionInsufficient) {
			t.Fatalf("error=%v, want it to still match the sentinel every caller tests for", err)
		}
		for _, want := range []string{"Catalog retention", "4713", "publication delay allowance", "evaluation cadences up to"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q does not name %q", err, want)
			}
		}
		if strings.Contains(err.Error(), "downstream execution reserve") {
			t.Fatalf("error %q reads as the per-plan refusal, which is the other one", err)
		}
	})
}

// The retention a deployment keeps has to cover the longest cadence it
// says it supports. It was a 24 hour constant, and one strategy evaluated
// once a day needed thirteen minutes more than that -- so every Catalog
// build was refused, for every strategy, while the deployment ran on the
// last good Catalog and reported healthy.
func TestCatalogRetentionCoversTheLongestSupportedCadence(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	daily := phaseTwoMaxSupportedEvaluationInterval - cfg.PhaseTwo.Access.DownstreamExecutionReserve.Duration()
	required := phaseTwoSnapshotMinimumRetention(cfg, daily)
	if got := phaseTwoCatalogRetention(cfg); got < required {
		t.Fatalf("retention %s is shorter than the %s a plan at the supported cadence needs", got, required)
	}
	if phaseTwoCatalogRetention(cfg) < cfg.PhaseTwo.Control.CatalogTTL.Duration() {
		t.Fatalf("retention %s is under the configured floor %s",
			phaseTwoCatalogRetention(cfg), cfg.PhaseTwo.Control.CatalogTTL.Duration())
	}
}
