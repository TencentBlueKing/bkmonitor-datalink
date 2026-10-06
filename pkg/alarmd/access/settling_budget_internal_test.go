// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package access

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// productionExecutionReserve is the deployment's DownstreamExecutionReserve
// default. The inequality this file guards holds between three numbers and
// two of them are configuration, so the sweep runs over the settling waits a
// deployment can hold rather than over one pair.
const productionExecutionReserve = 5 * time.Second

// Every interval whose schedule can hold a settling wait must leave its Slot
// more completion budget than the wait consumes. A Slot given less is not
// slow, it is inert: its readiness boundary lands past its own deadline, so
// every consumer fails that comparison on every round and is bound
// unavailable -- the strategy compiles, is scheduled, executes, and detects
// nothing, forever, with no failure anywhere to read.
//
// This is the test that was missing. The budget came from the compiler and
// the wait came from access, neither read the other, and each was written by
// listing the interval values someone had in mind. Intervals of 16 to 35
// seconds were on neither list -- and the strategy serializer accepts them,
// because it validates agg_interval as a non-negative integer with no tier
// list behind it. A sweep states the relation instead of the list, so a
// future interval cannot fall between two enumerations again.
func TestEverySchedulableIntervalAffordsItsSettlingWait(t *testing.T) {
	for _, configured := range []time.Duration{
		5 * time.Second, minimumSettlingWait, 30 * time.Second, time.Minute, 5 * time.Minute,
	} {
		for interval := int64(1); interval <= 3600; interval++ {
			spec := execution.DeriveScheduleSpec(interval)
			if !spec.AffordsSettlingWait() {
				continue // Named, with its consequence, by the test below.
			}
			wait := settlingWaitWithinBudget(spec, configured, productionExecutionReserve)
			budget := time.Duration(spec.CompletionOffsetSeconds()) * time.Second
			if wait+productionExecutionReserve >= budget {
				t.Fatalf("interval=%ds configured=%s: settling %s plus reserve %s does not fit the %s budget; every consumer on this Slot is bound unavailable forever",
					interval, configured, wait, productionExecutionReserve, budget)
			}
		}
	}
}

// The intervals that remain inert, named rather than left to be rediscovered.
// Their whole period is shorter than the wait the data needs, so no settling
// wait fits and no schedule this compiler derives can make one fit; they are
// left as they are because the fix -- widening the completion budget floor --
// is a decision about how many Slots may overlap, not about this inequality.
//
// This test exists to fail when that decision is taken: it is the record of
// what is still broken, so closing the gap cannot happen silently.
func TestIntervalsShorterThanTheSettlingWaitRemainInert(t *testing.T) {
	inert := map[int64]bool{}
	for interval := int64(1); interval <= 3600; interval++ {
		if !execution.DeriveScheduleSpec(interval).AffordsSettlingWait() {
			inert[interval] = true
		}
	}
	// 10 and 15 clear the budget because the compiler gives them an explicit
	// thirty-second deadline. Every other interval at or below the budget
	// carries its own period as its deadline, and cannot.
	for interval := int64(1); interval <= execution.MinimumCompletionBudgetSeconds; interval++ {
		want := interval != 10 && interval != 15
		if inert[interval] != want {
			t.Fatalf("interval=%ds inert=%v, want %v", interval, inert[interval], want)
		}
		delete(inert, interval)
	}
	if len(inert) != 0 {
		t.Fatalf("intervals past the budget are inert: %v", inert)
	}
}

// The wait the released deployment gives each period is unchanged. The fix
// removes two branches that named interval values; had it also moved any of
// those values, a released cohort would silently start reading its data at a
// different age -- a detection change wearing a refactor's clothes.
func TestSettlingWaitIsUnchangedForTheReleasedPeriods(t *testing.T) {
	const deploymentConfigured = 30 * time.Second
	for _, testCase := range []struct {
		interval int64
		want     time.Duration
	}{
		{10, minimumSettlingWait},
		{15, minimumSettlingWait},
		{30, minimumSettlingWait},
		{60, deploymentConfigured},
		{120, deploymentConfigured},
		{300, deploymentConfigured},
		{3600, deploymentConfigured},
	} {
		spec := execution.DeriveScheduleSpec(testCase.interval)
		if got := settlingWaitWithinBudget(spec, deploymentConfigured, productionExecutionReserve); got != testCase.want {
			t.Fatalf("interval=%ds settling=%s, want the released %s", testCase.interval, got, testCase.want)
		}
	}
}

// The intervals that were silently inert and are now scheduled. Each one is a
// strategy a user can save today: 20 and 25 seconds are ordinary choices, and
// each produced a Slot whose wait outlasted its budget.
func TestIntervalsBetweenTheTwoListsAreNowSchedulable(t *testing.T) {
	const deploymentConfigured = 30 * time.Second
	for _, interval := range []int64{16, 20, 25, 29, 31, 35} {
		spec := execution.DeriveScheduleSpec(interval)
		wait := settlingWaitWithinBudget(spec, deploymentConfigured, productionExecutionReserve)
		budget := time.Duration(spec.CompletionOffsetSeconds()) * time.Second
		if wait+productionExecutionReserve >= budget {
			t.Fatalf("interval=%ds is still inert: settling %s, budget %s", interval, wait, budget)
		}
		if wait != minimumSettlingWait {
			t.Fatalf("interval=%ds settling=%s, want the minimum %s", interval, wait, minimumSettlingWait)
		}
	}
}
