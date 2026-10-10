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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A strategy evaluated every sixty hours is admitted, and the publication
// asks its content to be kept for what that strategy's frozen Slot needs.
// It was refused for a retention the deployment could give it.
func TestASixtyHourPlanIsAdmittedAndItsPublicationKeepsItsContent(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	reserve := cfg.PhaseTwo.Access.DownstreamExecutionReserve.Duration()
	catalog := retentionTestCatalog(retentionTestPlan("4710", 60), retentionTestPlan("4715", 216000))

	admitted, err := phaseTwoCatalogRetentionAdmission(cfg)(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if published := publishedStrategies(admitted); len(published) != 2 {
		t.Fatalf("published %v, want both: the sixty-hour strategy is served", published)
	}
	if withheld := withheldReasons(admitted); len(withheld) != 0 {
		t.Fatalf("withheld %+v, want none", withheld)
	}
	want := phaseTwoSnapshotMinimumRetention(cfg, 216000*time.Second-reserve)
	if admitted.ObjectRetention != want {
		t.Fatalf("object retention = %s, want the sixty-hour Plan's own %s", admitted.ObjectRetention, want)
	}
	if want <= phaseTwoCatalogRetention(cfg) {
		t.Fatalf("setup: %s does not exceed the catalog retention %s", want, phaseTwoCatalogRetention(cfg))
	}
}

// A catalog with no Plan past a day's cadence keeps today's retention: the
// content of a deployment with no long strategy is kept exactly as long as
// before.
func TestACatalogWithNoLongPlanKeepsTheCatalogRetention(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	catalog := retentionTestCatalog(retentionTestPlan("4710", 60), retentionTestPlan("4714", 86400))
	admitted, err := phaseTwoCatalogRetentionAdmission(cfg)(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if admitted.ObjectRetention != phaseTwoCatalogRetention(cfg) {
		t.Fatalf("object retention = %s, want the catalog retention %s", admitted.ObjectRetention, phaseTwoCatalogRetention(cfg))
	}
}

// The limit is a boundary: a Plan needing exactly the state store's ceiling
// is served, one needing a second more is withheld, by name, with both
// numbers and nothing that tells the operator to raise a parameter.
func TestThePlanAtTheRetentionLimitIsServedAndTheOnePastItIsWithheld(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	reserve := cfg.PhaseTwo.Access.DownstreamExecutionReserve.Duration()
	limit := phaseTwoObjectRetentionLimit(cfg)
	atLimit := limit - phaseTwoSnapshotMinimumRetention(cfg, 0) + reserve
	if atLimit%time.Second != 0 || phaseTwoSnapshotMinimumRetention(cfg, atLimit-reserve) != limit {
		t.Fatalf("setup: an offset of %s does not need exactly the limit %s", atLimit, limit)
	}
	plan := func(strategyID string, offset time.Duration) controlplane.FrozenPlan {
		frozen := retentionTestPlan(strategyID, int64(offset/time.Second))
		frozen.ScheduleSpec = execution.ScheduleSpec{EvaluationIntervalSeconds: int64(offset / time.Second),
			CompletionDeadlineOffsetSeconds: int64(offset / time.Second), Timezone: "UTC"}
		return frozen
	}
	catalog := retentionTestCatalog(plan("9001", atLimit), plan("9002", atLimit+time.Second))

	admitted, err := phaseTwoCatalogRetentionAdmission(cfg)(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if published := publishedStrategies(admitted); len(published) != 1 || published[0] != "9001" {
		t.Fatalf("published %v, want the Plan at the limit and not the one past it", published)
	}
	if admitted.ObjectRetention != limit {
		t.Fatalf("object retention = %s, want the limit %s", admitted.ObjectRetention, limit)
	}
	withheld := withheldReasons(admitted)["9002"]
	if withheld.Reason != contract.ReasonSnapshotRetentionInsufficient {
		t.Fatalf("the Plan past the limit is %+v, want it withheld as %s", withheld, contract.ReasonSnapshotRetentionInsufficient)
	}
}
