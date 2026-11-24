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
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/filterclose"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// An inactive close runs on its own time - a dry run and a real close at a
// request's full budget - cut only where its Plan may be active again: past
// the minute where the Plan reads inactive at the next minute's start too,
// at the minute's end otherwise. One started late in a minute of a Plan
// inactive for an hour sends its requests and is not refused round_spent;
// one with less than a request's budget before the Plan turns active sends
// nothing and is counted close_boundary_near, apart from round_spent; one
// with a request's budget left before it turns active is sent.
func TestAnInactiveCloseRunsOnItsOwnTimeUntilItsPlanMayTurnActive(t *testing.T) {
	for _, tc := range []struct {
		name string
		at   time.Time
		sent bool
	}{
		{"late in a minute, inactive until 09:00", maintenanceTime(8, 0).Add(50 * time.Second), true},
		{"active at the next minute, less than a request's budget left", maintenanceTime(8, 59).Add(50 * time.Second), false},
		{"active at the next minute, a request's budget left", maintenanceTime(8, 59).Add(20 * time.Second), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, tc.at, maintenanceMatches)
			f.m.step(context.Background())
			spent := f.m.filter.closer.Stats()[filterclose.OutcomeRoundSpent]
			stats := f.m.Stats()
			if tc.sent {
				if closes := f.closed(); len(closes) != 1 || spent != 0 || stats[closeOutcomeDone] != 1 || stats[closeOutcomeBoundaryNear] != 0 {
					t.Fatalf("closes %d, round_spent %d, stats %v: want the close sent within its own time", len(closes), spent, nonZeroCounts(stats))
				}
				return
			}
			requests := len(f.stub.requests("/api/v1/alerts/close")) + len(f.stub.requests("/api/v1/alerts/search"))
			if requests != 0 || spent != 0 || stats[closeOutcomeBoundaryNear] != 1 || stats[closeOutcomeDone] != 0 {
				t.Fatalf("requests %d, round_spent %d, stats %v: want nothing sent, close_boundary_near once", requests, spent, nonZeroCounts(stats))
			}
		})
	}
}

// The close's own time holds a dry run and a real close each at a
// request's full budget, and stays under the minute past the one the Plan
// was read in, which is all the next minute's read proves.
func TestAnInactiveClosesTimeHoldsTwoRequestsWithinTheNextMinute(t *testing.T) {
	if inactiveJobTime < 2*filterCloseHTTPTimeout || inactiveJobTime >= time.Minute {
		t.Fatalf("inactiveJobTime %s: want at least two requests of %s and under a minute", inactiveJobTime, filterCloseHTTPTimeout)
	}
}

// A close that starts with a request's budget but runs out of it still
// stops round_spent, before the request it could not wait for: by the
// Plan's next boundary (a dry run that took long enough that its real
// close no longer fits before 09:00), or by its own time (a dry run and a
// close that together took more than inactiveJobTime leaves a request's
// budget, the link saying it has more).
func TestAnInactiveCloseThatRunsOutOfItsTimeStopsRoundSpent(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		at                      time.Time
		dryRunDelay, closeDelay time.Duration
		closeHasMore            bool
		dryRuns, closes         int
	}{
		{"by the Plan's next boundary", maintenanceTime(8, 59).Add(33 * time.Second), 2500 * time.Millisecond, 0, false, 1, 0},
		{"by its own time", maintenanceTime(8, 0), 16 * time.Second, 16 * time.Second, true, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, tc.at, maintenanceMatches)
			f.stub.dryRunDelay, f.stub.closeDelay, f.stub.closeHasMore = tc.dryRunDelay, tc.closeDelay, tc.closeHasMore
			f.m.step(context.Background())
			dryRuns := 0
			for _, body := range f.stub.requests("/api/v1/alerts/close") {
				if strings.Contains(body, `"dry_run":true`) {
					dryRuns++
				}
			}
			spent := f.m.filter.closer.Stats()[filterclose.OutcomeRoundSpent]
			stats := f.m.Stats()
			if dryRuns != tc.dryRuns || len(f.closed()) != tc.closes || spent != 1 || stats[closeOutcomeBoundaryNear] != 0 {
				t.Fatalf("dry runs %d, closes %d, round_spent %d, stats %v: want %d and %d, then round_spent", dryRuns, len(f.closed()),
					spent, nonZeroCounts(stats), tc.dryRuns, tc.closes)
			}
		})
	}
}

// nonZeroCounts is the counts that are not zero.
func nonZeroCounts(counts map[string]uint64) map[string]uint64 {
	out := map[string]uint64{}
	for word, n := range counts {
		if n > 0 {
			out[word] = n
		}
	}
	return out
}

// morePlans adds n copies of the fixture's Plan under other strategies of
// the same Query Group: n more inactive closes for one step to run.
func morePlans(f *maintenanceTestFixture, n int) {
	catalog := f.m.catalog.(*maintenanceTestCatalog)
	for i := 1; i <= n; i++ {
		plan := catalog.plans[0]
		plan.Identity.StrategyID = strconv.Itoa(123 + i)
		catalog.plans = append(catalog.plans, plan)
	}
}

// One step's inactive closes share one time, inactiveJobTime: against a
// link that takes 13 s for every request, the first close (a dry run and a
// close) takes 26 s, the second has 29 s left and is stopped after its dry
// run (round_spent), and the step starts no third, counted
// close_step_spent once. The step ends within inactiveJobTime; each close
// with its own time, the four would have taken some 100 s.
func TestAStepsInactiveClosesShareOneTime(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), maintenanceMatches)
	morePlans(f, inactiveJobsPerStep-1)
	f.stub.dryRunDelay, f.stub.closeDelay = 13*time.Second, 13*time.Second
	// The link finds the alert again after each close, so every close has
	// a dry run and a real close to send.
	f.stub.onClose = func() { f.stub.matches = append([]string(nil), maintenanceMatches...) }
	started := time.Now()
	f.m.step(context.Background())
	took := time.Since(started)
	dryRuns := 0
	for _, body := range f.stub.requests("/api/v1/alerts/close") {
		if strings.Contains(body, `"dry_run":true`) {
			dryRuns++
		}
	}
	stats := f.m.Stats()
	if took > inactiveJobTime+2*time.Second || stats[closeOutcomeStepSpent] != 1 || dryRuns != 2 || len(f.closed()) != 1 ||
		f.m.filter.closer.Stats()[filterclose.OutcomeRoundSpent] != 1 {
		t.Fatalf("step took %s, dry runs %d, closes %d, stats %v, filter close %v: want within %s, two dry runs, one close, "+
			"close_step_spent once", took, dryRuns, len(f.closed()), nonZeroCounts(stats),
			nonZeroCounts(f.m.filter.closer.Stats()), inactiveJobTime)
	}
}

// The step's time is checked again after a close's owner check and
// recheck, which can take its last request budget: the close is then not
// sent - no search, no request the link would be asked for - and the step
// counts close_step_spent once, across its Query Groups, however many
// Plans are left after it.
func TestAStepSpentInItsOwnerCheckSendsNothingMoreAndCountsOnce(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), maintenanceMatches)
	morePlans(f, inactiveJobsPerStep-1)
	for qg := range f.m.bundle.runners {
		f.m.bundle.runners[qg+"-b"] = &phaseTwoQueryGroupLifecycle{runner: f.runner}
		break
	}
	// Each owner check takes 16 s: the second close's leaves 23 s.
	f.runner.check = func(context.Context) error {
		time.Sleep(16 * time.Second) // fixed-wait: the owner check's cost against the step's time is the behaviour under test
		return nil
	}
	started := time.Now()
	f.m.step(context.Background())
	took := time.Since(started)
	stats := f.m.Stats()
	if took > inactiveJobTime || stats[closeOutcomeStepSpent] != 1 || len(f.stub.requests("/api/v1/alerts/search")) != 1 ||
		len(f.closed()) != 1 || f.m.filter.closer.Stats()[filterclose.OutcomeRoundSpent] != 0 {
		t.Fatalf("step took %s, searches %d, closes %d, stats %v, filter close %v: want one close, then close_step_spent once "+
			"and nothing sent", took, len(f.stub.requests("/api/v1/alerts/search")), len(f.closed()), nonZeroCounts(stats),
			nonZeroCounts(f.m.filter.closer.Stats()))
	}
}

// A close too near its Plan's boundary to start is counted once per Plan
// and boundary, with a count of one on its observation as on the counter:
// every step of the last request budget before 09:00 reaches it, and two
// Plans through those sixteen steps count two, not thirty-two; the next
// day's 09:00 is another boundary, counted again.
func TestABoundaryTooNearIsCountedOncePerPlanAndBoundary(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 59).Add(40*time.Second), maintenanceMatches)
	morePlans(f, 1)
	var near []observability.Observation
	f.m.bundle.dependencies.Observer = observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		if observation.ReasonCode == observability.ReasonCode(closeOutcomeBoundaryNear) {
			near = append(near, observation)
		}
	})
	for i := 0; i < 16; i++ {
		f.m.step(context.Background())
		f.advance(time.Second)
	}
	if n := f.m.Stats()[closeOutcomeBoundaryNear]; n != 2 || len(near) != 2 || near[0].Counts.Events != 1 || near[1].Counts.Events != 1 ||
		near[0].Trace.StrategyID == near[1].Trace.StrategyID {
		t.Fatalf("close_boundary_near %d, observations %+v: want one per Plan, each counting one", n, near)
	}
	if requests := len(f.stub.requests("/api/v1/alerts/close")) + len(f.stub.requests("/api/v1/alerts/search")); requests != 0 {
		t.Fatalf("%d requests sent before the boundary", requests)
	}
	// The next boundary of the same inactive period - no step saw the Plans
	// active in between - is counted again, once per Plan.
	f.advance(24*time.Hour - 16*time.Second)
	for i := 0; i < 3; i++ {
		f.m.step(context.Background())
		f.advance(time.Second)
	}
	if n := f.m.Stats()[closeOutcomeBoundaryNear]; n != 4 || len(near) != 4 {
		t.Fatalf("close_boundary_near %d after the next boundary, want 4", n)
	}
}

// nextMinuteUnreadable answers effective time as a static schedule does,
// except a query at the start of the minute after 08:00 while failing is
// set: a calendar that cannot say whether the next minute is inactive.
type nextMinuteUnreadable struct{ failing bool }

func (p *nextMinuteUnreadable) Resolve(ctx context.Context, requests []strategy.EffectiveTimeRequest) ([]strategy.EffectiveTimeFact, error) {
	for _, request := range requests {
		if p.failing && request.EvaluationTime == maintenanceTime(8, 1).Unix() {
			return nil, errors.New("calendar service did not answer")
		}
	}
	return strategy.NewStaticScheduleProvider(strategy.TimezoneResolverFunc(func(context.Context, string, string, string) (*time.Location, error) {
		return time.UTC, nil
	})).Resolve(ctx, requests)
}

// A close not started for its boundary (close_boundary_near) ended with
// no word: it is not held as a close that got nowhere is. Here the next
// minute cannot be read, so the close is held to this minute's last ten
// seconds and not started; once the calendar answers, the next step past
// the boundary sends it, not five minutes later.
func TestACloseNotStartedForItsBoundaryIsNotHeld(t *testing.T) {
	f := newMaintenanceTestFixture(t, "", maintenanceTime(8, 0).Add(50*time.Second), maintenanceMatches)
	calendar := &nextMinuteUnreadable{failing: true}
	f.m.legacy = calendar
	f.m.step(context.Background())
	if stats := f.m.Stats(); stats[closeOutcomeBoundaryNear] != 1 || len(f.closed()) != 0 {
		t.Fatalf("closes %d, stats %v: want the close not started, close_boundary_near once", len(f.closed()), nonZeroCounts(stats))
	}
	calendar.failing = false
	f.advance(15 * time.Second)
	f.m.step(context.Background())
	if closes := f.closed(); len(closes) != 1 {
		t.Fatalf("closes %d past the boundary, stats %v: want the close sent", len(closes), nonZeroCounts(f.m.Stats()))
	}
}
