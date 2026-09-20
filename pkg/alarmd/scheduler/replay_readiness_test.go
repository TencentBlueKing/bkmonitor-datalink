// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The Slot that was dying in production is dispatched as a replay.
//
// Ten-second period, thirty-second completion deadline, five-second reserve:
// the query deadline is T+25, the worker first reaches the Slot at T+26, and
// it is three grid points behind -- the last distance the replay window
// allows. The readiness rule gives it T+10, which is already past, so there is
// nothing to wait for and the replay runs.
//
// Before the settling wait became one rule, the recovery path asked for T+30
// instead, and T+30 is the fourth grid point: the Slot was told to wait until
// the exact instant at which it would be given up on. That is the shape of the
// defect -- not a Slot that arrived too late, but a Slot made too late by the
// wait it was told to serve.
func TestTheReplayThatWasDyingIsDispatched(t *testing.T) {
	const slot = execution.EvaluationTime(1_700_124_000)
	source, catalog := replayClassificationSource(t, 10, slot, 30*time.Second, testRecoveryLimits())
	deadline := int64(slot)*1000 + 25_000

	operation, facts, err := source.classifyRecovery(context.Background(), slot, deadline, time.Unix(int64(slot)+26, 0))
	if err != nil {
		t.Fatalf("classifyRecovery() error = %v", err)
	}
	if operation != execution.OperationReplay || facts.Disposition != ReplayEligible || facts.Distance != 3 {
		t.Fatalf("ten-second Slot at T+26 = %s %+v, want a dispatched replay at distance 3. A replay held "+
			"until after its own window closes is skipped on every round, and no query is ever issued to "+
			"fail", operation, facts)
	}
	// The guard does not pay for a walk it cannot need. Only replayDistance's
	// own navigation should have happened.
	if got := len(catalog.nextSlotAfterCalls); got != 3 {
		t.Fatalf("NextSlotAfter calls = %d (%v), want only the distance walk's three", got, catalog.nextSlotAfterCalls)
	}
}

// A replay whose readiness rule outlasts its own window is refused where the
// two meet, and says so.
//
// The schedule here is one no compiler this repository ships produces: a
// three-second grid under a Plan whose query deadline is two seconds after the
// evaluation time. It is a shape the stored ScheduleSpec can hold, which is
// the point -- the schedule is read back from Redis and may have been written
// by another version, and the completion budget rule has already changed once.
// Under every schedule DeriveScheduleSpec produces today this cannot happen,
// and the sweep in access proves it; this is what happens if that stops being
// true.
//
// Refused rather than dispatched because dispatching it achieves nothing: the
// Slot waits, is abandoned for having waited, and the round is lost either
// way. The difference is that this way it is counted, named, and carries the
// two instants that disagree.
func TestAReplayHeldPastItsOwnWindowIsRefusedAndNamed(t *testing.T) {
	const slot = execution.EvaluationTime(1_700_124_000)
	for _, test := range []struct {
		name          string
		grid          int64
		replaySlots   uint32
		budgetSeconds int64
		reachedAfter  int64
		wantBoundary  int64
	}{
		// Ready after the boundary: the plainest form of the contradiction.
		{name: "ready after the boundary", grid: 3, replaySlots: 3, budgetSeconds: 2, reachedAfter: 3, wantBoundary: 9},
		// Ready at exactly the boundary, which is the shape the outage had:
		// the ten-second Slot was told to wait until T+30 and T+30 was the
		// instant it was given up on, to the second. A guard that refused only
		// what lands strictly after the boundary would have let that through,
		// so this row is the one that decides the comparison.
		{name: "ready at exactly the boundary", grid: 5, replaySlots: 2, budgetSeconds: 6, reachedAfter: 7, wantBoundary: 10},
	} {
		t.Run(test.name, func(t *testing.T) {
			limits := testRecoveryLimits()
			limits.MaxReplaySlots = test.replaySlots
			source, _ := replayClassificationSource(t, test.grid, slot, 30*time.Second, limits)
			observer := &recordingReplayObserver{}
			source.observer = observer
			deadline := (int64(slot) + test.budgetSeconds) * 1000

			operation, facts, err := source.classifyRecovery(context.Background(), slot, deadline,
				time.Unix(int64(slot)+test.reachedAfter, 0))
			if err != nil {
				t.Fatalf("classifyRecovery() error = %v", err)
			}
			if operation != execution.OperationNormal || facts.Disposition != ReplayExpired ||
				facts.Reason != ReplayExpiredByWait {
				t.Fatalf("classifyRecovery() = %s %+v, want the replay refused as %s", operation, facts, ReplayExpiredByWait)
			}
			// The two instants that disagree, not just the verdict. Without
			// them the report says a rule was broken without saying by how
			// much, and the reader has to reproduce the arithmetic to act.
			wantReady := int64(slot)*1000 + execution.MinimumSettlingWait.Milliseconds()
			wantBoundary := (int64(slot) + test.wantBoundary) * 1000
			if facts.ReadyAtUnixMilli != wantReady || facts.DistanceBoundaryUnixMilli != wantBoundary {
				t.Fatalf("compared instants = ready %d boundary %d, want %d and %d",
					facts.ReadyAtUnixMilli, facts.DistanceBoundaryUnixMilli, wantReady, wantBoundary)
			}
			if len(observer.facts) != 1 || observer.facts[0].Reason != string(ReplayExpiredByWait) ||
				observer.facts[0].ReadyAtUnixMilli != wantReady || observer.facts[0].DistanceBoundaryUnixMilli != wantBoundary {
				t.Fatalf("observed %+v, want one report carrying the reason and both instants", observer.facts)
			}
		})
	}
}

// The two ordinary expiries keep their own names and are counted too.
//
// Not because they are news, but because the population has to divide. A
// deployment reading "replays expired" cannot tell a worker that fell behind
// from a rule that will skip every Slot of a period forever, and those need
// opposite responses.
func TestTheOrdinaryExpiriesAreNamedAndCounted(t *testing.T) {
	const slot = execution.EvaluationTime(1_700_124_000)
	for _, test := range []struct {
		name     string
		at       time.Duration
		want     ReplayExpiryReason
		distance uint32
	}{
		{name: "too many grid points behind", at: 45 * time.Second, want: ReplayExpiredByDistance, distance: 4},
		{name: "older than the replay window", at: 15 * time.Minute, want: ReplayExpiredByAge, distance: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			source, _ := replayClassificationSource(t, 10, slot, 30*time.Second, testRecoveryLimits())
			observer := &recordingReplayObserver{}
			source.observer = observer
			deadline := int64(slot)*1000 + 25_000

			operation, facts, err := source.classifyRecovery(context.Background(), slot, deadline,
				time.Unix(int64(slot), 0).Add(test.at))
			if err != nil {
				t.Fatalf("classifyRecovery() error = %v", err)
			}
			if operation != execution.OperationNormal || facts.Disposition != ReplayExpired ||
				facts.Reason != test.want || facts.Distance != test.distance {
				t.Fatalf("classifyRecovery() = %s %+v, want %s at distance %d", operation, facts, test.want, test.distance)
			}
			if len(observer.facts) != 1 || observer.facts[0].Reason != string(test.want) {
				t.Fatalf("observed %+v, want one %s report", observer.facts, test.want)
			}
			// Only the wait guard compares instants; the others leave them
			// empty rather than reporting a comparison they did not make.
			if observer.facts[0].ReadyAtUnixMilli != 0 || observer.facts[0].DistanceBoundaryUnixMilli != 0 {
				t.Fatalf("observed %+v, want no compared instants on an ordinary expiry", observer.facts[0])
			}
		})
	}
}

// Every reason this package can produce has a series waiting for it.
//
// Scanned out of the source rather than listed here. A hand-kept list is one
// more place to forget, and the cutover reasons had already gone stale by four
// entries the last time one was kept by hand -- which meant an unregistered
// reason failed nothing and its counter did not exist until the first time it
// happened, which is the round nobody is watching.
func TestEveryReplayExpiryReasonHasASeries(t *testing.T) {
	declared := map[string]bool{}
	fileSet := token.NewFileSet()
	packages, err := parser.ParseDir(fileSet, ".", func(info fs.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, parsed := range packages {
		ast.Inspect(parsed, func(node ast.Node) bool {
			spec, ok := node.(*ast.ValueSpec)
			if !ok {
				return true
			}
			identifier, ok := spec.Type.(*ast.Ident)
			if !ok || identifier.Name != "ReplayExpiryReason" {
				return true
			}
			for _, value := range spec.Values {
				literal, ok := value.(*ast.BasicLit)
				if ok && literal.Kind == token.STRING {
					declared[literal.Value[1:len(literal.Value)-1]] = true
				}
			}
			return true
		})
	}
	if len(declared) == 0 {
		t.Fatal("the scan found no ReplayExpiryReason constants, so it proves nothing about the ones there are")
	}
	published := map[string]bool{}
	for _, reason := range observability.ReplayExpiryReasons {
		published[reason] = true
	}
	var missing, extra []string
	for reason := range declared {
		if !published[reason] {
			missing = append(missing, reason)
		}
	}
	for reason := range published {
		if !declared[reason] {
			extra = append(extra, reason)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) != 0 || len(extra) != 0 {
		t.Fatalf("observability.ReplayExpiryReasons and the scheduler's constants disagree: "+
			"declared but unpublished %v, published but not declared %v. An unpublished reason has no "+
			"series until the first time it happens", missing, extra)
	}
}

// replayClassificationSource builds a slot source over a regular grid of the
// given spacing, with the deployment's settling wait, for tests that call
// classifyRecovery with an explicit query deadline.
func replayClassificationSource(
	t *testing.T,
	intervalSeconds int64,
	first execution.EvaluationTime,
	settling time.Duration,
	limits RecoveryLimits,
) (*ProductionSlotSource, *fakeSlotCatalog) {
	t.Helper()
	schedule := schedulerSchedule(t, intervalSeconds, first, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source, err := NewProductionSlotSource("query-group-1", "worker-1",
		&sequenceOwnerSession{fences: []execution.OwnerFence{testFence(7)}}, catalog,
		&fakeProgressReader{result: missingProgress(), catalog: catalog},
		func() time.Time { return time.Unix(int64(first), 0) },
		WithRecoveryLimits(limits), WithPostRecoveryTerminalDelay(time.Minute),
		WithQueryDeadlineReserve(5*time.Second), WithSettlingWait(settling))
	if err != nil {
		t.Fatalf("NewProductionSlotSource() error = %v", err)
	}
	return source, catalog
}

type recordingReplayObserver struct {
	facts []observability.ReplayExpiryFacts
}

func (observer *recordingReplayObserver) Observe(_ context.Context, observation observability.Observation) {
	if observation.ReplayExpiry != nil {
		observer.facts = append(observer.facts, *observation.ReplayExpiry)
	}
}
