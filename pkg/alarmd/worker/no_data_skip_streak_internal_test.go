// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
)

// A stall is reported the round it becomes one, once, and again only after the
// Plan has evaluated in between.
//
// The outcome buckets cannot say this. One Slot's SKIPPED_HOSTS_UNRESOLVED and
// a Plan's thousandth consecutive one are the same increment on the same
// series, so a deployment where one Plan has silently stopped detecting for a
// day reads exactly like one where a hundred Plans each missed a round. A
// count of skips is a rate; this is the count of things that became a state,
// and it is the whole reason the family is not just the buckets.
//
// Reporting every round would undo that: the count would go back to being a
// count of rounds, which is the reading that already exists and cannot be
// acted on.
func TestAStallIsReportedOnceAndAgainOnlyAfterItRecovers(t *testing.T) {
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	var streaks noDataSkipStreaks
	skip := nodata.OutcomeSkippedHostsUnresolved

	for round := 1; round < noDataPersistentSkipRounds; round++ {
		if streaks.record(plan, skip) {
			t.Fatalf("round %d of %d reported a stall; skipping a round is allowed by design and "+
				"happens for ordinary reasons", round, noDataPersistentSkipRounds)
		}
	}
	if !streaks.record(plan, skip) {
		t.Fatalf("round %d did not report a stall; that is the round it stops being occasional",
			noDataPersistentSkipRounds)
	}
	for round := 0; round < 5; round++ {
		if streaks.record(plan, skip) {
			t.Fatal("the same stall was reported again; one Plan that has stopped is one thing that " +
				"has stopped, and counting it every round turns this back into a count of rounds")
		}
	}

	// It evaluates, so the streak is over and the Plan can stall again later.
	if streaks.record(plan, nodata.OutcomeEvaluated) {
		t.Fatal("a round that evaluated reported a stall")
	}
	for round := 1; round < noDataPersistentSkipRounds; round++ {
		if streaks.record(plan, skip) {
			t.Fatalf("round %d after the recovery reported a stall; the count restarts", round)
		}
	}
	if !streaks.record(plan, skip) {
		t.Fatal("a Plan that stalled, recovered and stalled again was not reported the second time")
	}
}

// One Plan's streak is not another's.
//
// They share a map, and a streak counted across Plans would report a stall on
// a deployment where every Plan misses one round in turn -- which is the
// ordinary state this is meant to be able to ignore.
func TestStreaksAreCountedPerPlan(t *testing.T) {
	var streaks noDataSkipStreaks
	skip := nodata.OutcomeSkippedQueryNotFull
	for index := 0; index < noDataPersistentSkipRounds*2; index++ {
		plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: string(rune('a' + index))}
		if streaks.record(plan, skip) {
			t.Fatalf("Plan %s reported a stall on its first skipped round", plan.StrategyID)
		}
	}
}
