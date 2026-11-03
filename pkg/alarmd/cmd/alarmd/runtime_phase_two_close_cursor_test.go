// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"regexp"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

var closeStrategy = regexp.MustCompile(`"labels\.strategy_id":"([0-9]+)"`)

// closeStrategies is the strategy of every filtered close request the link
// received, dry runs included, in order.
func closeStrategies(f *maintenanceTestFixture) []string {
	var out []string
	for _, body := range f.stub.requests("/api/v1/alerts/close") {
		if m := closeStrategy.FindStringSubmatch(body); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}

func sent(f *maintenanceTestFixture, strategy string) bool {
	for _, s := range closeStrategies(f) {
		if s == strategy {
			return true
		}
	}
	return false
}

// A step out of close time stops at its Query Group, and the next step's
// closes start there: later Query Groups are not kept waiting by an
// earlier one whose close never settles. The first Query Group's close
// spends the step (its owner check takes 20 s and each request 6 s, 32 s
// of the step's 55; items always busy, so never settled); the second and
// the third, after it by name, both send their requests by the second
// step. Started from the first Query Group every step they never would,
// nor would the second if the step started from the last Query Group it
// stopped at instead of the first.
func TestALaterQueryGroupGetsItsCloseAfterAStepRanOutOfTime(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), maintenanceMatches)
	f.stub.itemBusy = true
	f.stub.dryRunDelay, f.stub.closeDelay = 6*time.Second, 6*time.Second
	catalog := f.m.catalog.(*maintenanceTestCatalog)
	var first execution.QueryGroupIdentity
	for qg := range f.m.bundle.runners {
		first = qg
	}
	catalog.byGroup = map[execution.QueryGroupIdentity][]controlplane.MaintenancePlan{}
	f.m.capacity.GroupBatch = 3
	for i, suffix := range []string{"-b", "-c"} {
		other := catalog.plans[0]
		other.Identity.StrategyID = strconv.Itoa(124 + i)
		catalog.byGroup[first+execution.QueryGroupIdentity(suffix)] = []controlplane.MaintenancePlan{other}
		f.m.bundle.runners[first+execution.QueryGroupIdentity(suffix)] = &phaseTwoQueryGroupLifecycle{runner: &maintenanceTestRunner{
			check: func(context.Context) error { return nil }, scope: "obj-a", revision: 1}}
	}
	f.runner.check = func(context.Context) error {
		if !sent(f, "124") || !sent(f, "125") {
			time.Sleep(20 * time.Second) // fixed-wait: the first Query Group's cost against the step's time is the behaviour under test
		}
		return nil
	}
	for step := 1; step <= 2; step++ {
		f.m.step(context.Background())
		f.advance(time.Second)
	}
	if !sent(f, "124") || !sent(f, "125") || f.m.Stats()[closeOutcomeStepSpent] != 1 {
		t.Fatalf("requests by strategy %v, stats %v: want the later Query Groups' closes sent by the second step",
			closeStrategies(f), nonZeroCounts(f.m.Stats()))
	}
}

// The Plan a step stopped before is the first the next step takes: stopped
// before it was started (the step's time spent by the close before), or
// after its own owner check took the step's last request budget.
func TestThePlanAStepStoppedBeforeIsTheNextStepsFirst(t *testing.T) {
	for _, tc := range []struct {
		name   string
		checks []time.Duration
		// request is what each request costs the link.
		request time.Duration
	}{
		// 16 s, then a dry run and a close of 8 s each: 23 s left.
		{"stopped before it started", []time.Duration{16 * time.Second}, 8 * time.Second},
		// 16 s, a close, then 16 s more: 23 s left after its check.
		{"stopped after its owner check", []time.Duration{16 * time.Second, 16 * time.Second}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), maintenanceMatches)
			morePlans(f, 2)
			f.stub.dryRunDelay, f.stub.closeDelay = tc.request, tc.request
			var calls atomic.Int32
			f.runner.check = func(context.Context) error {
				if n := int(calls.Add(1)); n <= len(tc.checks) {
					time.Sleep(tc.checks[n-1]) // fixed-wait: the owner check's cost against the step's time is the behaviour under test
				}
				return nil
			}
			f.m.step(context.Background())
			before := closeStrategies(f)
			if len(before) == 0 || before[0] != "123" || sent(f, "124") || sent(f, "125") {
				t.Fatalf("first step's requests by strategy %v: want only strategy 123's", before)
			}
			f.matchAgain(maintenanceMatches...)
			f.advance(time.Second)
			f.m.step(context.Background())
			after := closeStrategies(f)[len(before):]
			if len(after) == 0 || after[0] != "124" {
				t.Fatalf("second step's requests by strategy %v: want strategy 124's first", after)
			}
		})
	}
}

// A step stopped at inactiveJobsPerStep stops at its Query Group as one out
// of time does: the first Query Group's four closes, never settled (items
// always busy), fill every step, and the second Query Group's close is
// sent by the second step all the same.
func TestALaterQueryGroupGetsItsCloseAfterAStepRanOutOfCloses(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), maintenanceMatches)
	f.stub.itemBusy = true
	morePlans(f, inactiveJobsPerStep-1)
	catalog := f.m.catalog.(*maintenanceTestCatalog)
	var first execution.QueryGroupIdentity
	for qg := range f.m.bundle.runners {
		first = qg
	}
	second := first + "-b"
	other := catalog.plans[0]
	other.Identity.StrategyID = "200"
	catalog.byGroup = map[execution.QueryGroupIdentity][]controlplane.MaintenancePlan{second: {other}}
	f.m.bundle.runners[second] = &phaseTwoQueryGroupLifecycle{runner: &maintenanceTestRunner{
		check: func(context.Context) error { return nil }, scope: "obj-a", revision: 1}}
	for step := 1; step <= 2; step++ {
		f.m.step(context.Background())
		f.advance(time.Second)
	}
	if !sent(f, "200") {
		t.Fatalf("requests by strategy %v: want the second Query Group's close sent by the second step", closeStrategies(f))
	}
}

// A step stopped inside a Query Group that ran closes starts the next step
// at the Query Group after it, and the stopped one goes on from its own
// Plan when its turn comes. The first Query Group holds six closes that
// never settle (items always busy): the first step runs four of them and
// stops before its fifth; the second step starts with the second Query
// Group's close, then the first's fifth and sixth. Started from the
// stopped Query Group again, the first would take the head of every step
// and the second's close would wait for as long as the first has closes.
func TestAStepStoppedInsideAQueryGroupStartsTheNextAtTheOneAfter(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), maintenanceMatches)
	f.stub.itemBusy = true
	morePlans(f, inactiveJobsPerStep+1)
	catalog := f.m.catalog.(*maintenanceTestCatalog)
	var first execution.QueryGroupIdentity
	for qg := range f.m.bundle.runners {
		first = qg
	}
	second := first + "-b"
	other := catalog.plans[0]
	other.Identity.StrategyID = "200"
	catalog.byGroup = map[execution.QueryGroupIdentity][]controlplane.MaintenancePlan{second: {other}}
	f.m.bundle.runners[second] = &phaseTwoQueryGroupLifecycle{runner: &maintenanceTestRunner{
		check: func(context.Context) error { return nil }, scope: "obj-a", revision: 1}}
	f.m.step(context.Background())
	before := closeStrategies(f)
	if sent(f, "200") || sent(f, "127") {
		t.Fatalf("first step's requests by strategy %v: want the first Query Group's first four closes only", before)
	}
	f.advance(time.Second)
	f.m.step(context.Background())
	after := closeStrategies(f)[len(before):]
	if len(after) < 3 || after[0] != "200" || after[2] != "127" {
		t.Fatalf("second step's requests by strategy %v: want strategy 200's first, then the first Query Group's fifth (127)", after)
	}
}

// A step stopped by a Query Group's own owner check - it took the step's
// last request budget before that Query Group's first close - ran no close
// there, and the next step starts at it, not after it. The first Query
// Group's close (its check 16 s, items always busy, so never settled) and
// the second's check (16 s more) leave 23 s of the step's 55: the second's
// close is not started. The next step's first requests are the second's.
func TestAStepStoppedByAnOwnerCheckStartsTheNextAtThatQueryGroup(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), maintenanceMatches)
	f.stub.itemBusy = true
	catalog := f.m.catalog.(*maintenanceTestCatalog)
	var first execution.QueryGroupIdentity
	for qg := range f.m.bundle.runners {
		first = qg
	}
	second := first + "-b"
	other := catalog.plans[0]
	other.Identity.StrategyID = "200"
	catalog.byGroup = map[execution.QueryGroupIdentity][]controlplane.MaintenancePlan{second: {other}}
	var firstChecks, secondChecks atomic.Int32
	f.runner.check = func(context.Context) error {
		if firstChecks.Add(1) == 1 {
			time.Sleep(16 * time.Second) // fixed-wait: the owner check's cost against the step's time is the behaviour under test
		}
		return nil
	}
	f.m.bundle.runners[second] = &phaseTwoQueryGroupLifecycle{runner: &maintenanceTestRunner{
		check: func(context.Context) error {
			if secondChecks.Add(1) == 1 {
				time.Sleep(16 * time.Second) // fixed-wait: the owner check's cost against the step's time is the behaviour under test
			}
			return nil
		}, scope: "obj-a", revision: 1}}
	f.m.step(context.Background())
	before := closeStrategies(f)
	if len(before) == 0 || sent(f, "200") || f.m.Stats()[closeOutcomeStepSpent] != 1 {
		t.Fatalf("first step's requests by strategy %v, stats %v: want only the first Query Group's, then close_step_spent",
			before, nonZeroCounts(f.m.Stats()))
	}
	f.advance(time.Second)
	f.m.step(context.Background())
	after := closeStrategies(f)[len(before):]
	if len(after) == 0 || after[0] != "200" {
		t.Fatalf("second step's requests by strategy %v: want strategy 200's first", after)
	}
}

// A close whose recheck refused it (its Plan turned active) sent nothing,
// takes none of the step's jobs, and does not make its Query Group one
// that ran a close: the step that then stops in that Query Group for time
// starts the next step at it. The first Query Group's first Plan (active
// from 09:00) is rechecked after a 31 s owner check and found active; its
// second (active from 10:00) finds 24 s of the step's 55, less than a
// request's budget, and the step stops. The next step's first requests are
// that second Plan's, not the second Query Group's.
func TestAStepStoppedAfterARefusedRecheckStartsTheNextAtThatQueryGroup(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 59), maintenanceMatches)
	addMaintenancePlan(t, f, 124, maintenanceReadySnapshot, "10:00")
	catalog := f.m.catalog.(*maintenanceTestCatalog)
	var first execution.QueryGroupIdentity
	for qg := range f.m.bundle.runners {
		first = qg
	}
	second := first + "-b"
	other := catalog.plans[len(catalog.plans)-1]
	other.Identity.StrategyID = "200"
	catalog.byGroup = map[execution.QueryGroupIdentity][]controlplane.MaintenancePlan{second: {other}}
	f.m.bundle.runners[second] = &phaseTwoQueryGroupLifecycle{runner: &maintenanceTestRunner{
		check: func(context.Context) error { return nil }, scope: "obj-a", revision: 1}}
	var checks atomic.Int32
	f.runner.check = func(context.Context) error {
		if checks.Add(1) == 1 {
			time.Sleep(31 * time.Second) // fixed-wait: the owner check's cost against the step's time is the behaviour under test
			f.advance(time.Minute)
		}
		return nil
	}
	f.m.step(context.Background())
	if requests := closeStrategies(f); len(requests) != 0 || f.m.Stats()[closeOutcomeBoundaryActive] != 1 || f.m.Stats()[closeOutcomeStepSpent] != 1 {
		t.Fatalf("first step's requests by strategy %v, stats %v: want none, the recheck refused and the step spent",
			requests, nonZeroCounts(f.m.Stats()))
	}
	f.advance(time.Second)
	f.m.step(context.Background())
	if after := closeStrategies(f); len(after) == 0 || after[0] != "124" {
		t.Fatalf("second step's requests by strategy %v: want strategy 124's first", after)
	}
}
