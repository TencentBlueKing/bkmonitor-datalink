// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// activate makes publication the one the fleet executes, as the control
// leader's activation step does.
func (harness *changeGateHarness) activate(publication controlplane.SnapshotPublicationRef) {
	harness.t.Helper()
	activator, err := controlplane.NewInitialScheduleActivator(harness.repository, harness.compiler, harness.semantics,
		func() time.Time { return harness.clock })
	if err != nil {
		harness.t.Fatal(err)
	}
	if _, err := activator.Ensure(harness.ctx, publication); err != nil {
		harness.t.Fatal(err)
	}
}

// settleActive takes the harness to the steady state with the publication
// activated, the state a round stands on the previous one's Catalog from.
func (harness *changeGateHarness) settleActive() controlplane.SourceRefreshResult {
	harness.t.Helper()
	settled := harness.settle()
	harness.activate(settled.Publication)
	return settled
}

// controlState is every control key's serialized value: what a round wrote,
// compared byte for byte. TTLs are left out; renewing them is what an
// unchanged round is for, and both rounds renew.
func (harness *changeGateHarness) controlState() map[string]string {
	harness.t.Helper()
	keys, err := harness.client.Keys(harness.ctx, harness.prefix+"*").Result()
	if err != nil {
		harness.t.Fatal(err)
	}
	state := make(map[string]string, len(keys))
	for _, key := range keys {
		dumped, err := harness.client.Dump(harness.ctx, key).Result()
		if err != nil {
			harness.t.Fatal(err)
		}
		state[key] = dumped
	}
	return state
}

func (harness *changeGateHarness) wantBuild(result controlplane.SourceRefreshResult, want controlplane.SourceRefreshBuild) {
	harness.t.Helper()
	if result.Build != want {
		harness.t.Fatalf("build = %q, want %q (%+v)", result.Build, want, result)
	}
}

// A round whose inputs have not moved since the previous round ended
// UNCHANGED stands on that round's Catalog. What it reports and what it
// writes are what building the same Catalog again reports and writes; only
// the build word differs.
func TestAnUnchangedRoundStandsOnThePreviousCatalogAndWritesTheSame(t *testing.T) {
	harness := newChangeGateHarness(t)
	settled := harness.settleActive()
	harness.wantBuild(settled, controlplane.SourceRefreshRebuilt)
	reused := harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0)
	harness.wantBuild(reused, controlplane.SourceRefreshReused)
	afterReuse := harness.controlState()

	harness.reconciler.ForgetReusableRoundForTest()
	rebuilt := harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0)
	harness.wantBuild(rebuilt, controlplane.SourceRefreshRebuilt)
	afterRebuild := harness.controlState()

	reused.Build = rebuilt.Build
	if !reflect.DeepEqual(reused, rebuilt) {
		t.Fatalf("reused round reported\n%+v\nrebuilt round reported\n%+v", reused, rebuilt)
	}
	if len(afterReuse) == 0 || !reflect.DeepEqual(afterReuse, afterRebuild) {
		t.Fatalf("reused round left %d control keys, rebuilt round %d, and they differ", len(afterReuse), len(afterRebuild))
	}
}

// Each thing a round stands on the previous Catalog for, moved on its own:
// the round builds. The side where nothing moved is the round before it.
func TestARoundBuildsWhenWhatItStoodOnMoved(t *testing.T) {
	t.Run("the source was read", func(t *testing.T) {
		harness := newChangeGateHarness(t)
		harness.settleActive()
		harness.wantBuild(harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0), controlplane.SourceRefreshReused)
		harness.signal(harness.clock)
		harness.wantBuild(harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadFull, controlplane.SourceReadChanged, 1), controlplane.SourceRefreshRebuilt)
		harness.wantBuild(harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0), controlplane.SourceRefreshReused)
	})
	t.Run("the round key moved", func(t *testing.T) {
		harness := newChangeGateHarness(t)
		harness.settleActive()
		harness.wantBuild(harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0), controlplane.SourceRefreshReused)
		if err := harness.reconciler.ConfigureNoDataPolicy(func() controlplane.NoDataPolicy {
			return controlplane.NoDataPolicy{TrackingHorizonSeconds: 3600}
		}); err != nil {
			t.Fatal(err)
		}
		result, err := harness.reconciler.Refresh(harness.ctx, harness.source, harness.planner)
		if err != nil || result.ReadMode != controlplane.SourceReadSkipped {
			t.Fatalf("Refresh() = (%+v, %v), want a skipped read", result, err)
		}
		harness.wantBuild(result, controlplane.SourceRefreshRebuilt)
	})
	t.Run("the activation is not at this Catalog", func(t *testing.T) {
		harness := newChangeGateHarness(t)
		harness.settleActive()
		harness.wantBuild(harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0), controlplane.SourceRefreshReused)
		// A new revision is published and confirmed, and not activated: a
		// cutover still to come. The round that confirms it ends UNCHANGED
		// with the fleet on the old revision.
		harness.edit("1002", 1, `"threshold":90`, `"threshold":95`)
		harness.signal(harness.clock)
		harness.settleAfter(controlplane.SourceReadChanged)
		harness.wantBuild(harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0), controlplane.SourceRefreshRebuilt)
	})
	t.Run("the absence grace ran out", func(t *testing.T) {
		harness := newChangeGateHarness(t)
		harness.settleActive()
		// 1002 leaves the source: it is kept under PENDING_REMOVAL for the
		// grace, which starts now.
		graceStart := harness.clock
		if err := harness.client.Set(harness.ctx, "bkmonitor.cache.strategy_ids", `[1001]`, 0).Err(); err != nil {
			t.Fatal(err)
		}
		harness.settleAfter(controlplane.SourceReadChanged)
		// Two minutes before the grace ends, a periodic read rebuilds; the
		// grace's end then falls inside the next six minutes of skipped
		// reads, which is the only place this condition decides anything.
		graceEnd := graceStart.Add(controlplane.AbsenceGracePeriod)
		harness.clock = graceEnd.Add(-2 * time.Minute)
		harness.wantBuild(harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadFull, controlplane.SourceReadPeriodic, 1), controlplane.SourceRefreshRebuilt)
		harness.clock = graceEnd.Add(-time.Second)
		harness.wantBuild(harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0), controlplane.SourceRefreshReused)
		harness.clock = graceEnd
		result, err := harness.reconciler.Refresh(harness.ctx, harness.source, harness.planner)
		if err != nil || result.ReadMode != controlplane.SourceReadSkipped {
			t.Fatalf("Refresh() = (%+v, %v), want a skipped read", result, err)
		}
		harness.wantBuild(result, controlplane.SourceRefreshRebuilt)
		if result.Status == controlplane.SourceRefreshUnchanged {
			t.Fatalf("the round the grace ran out on = %+v, want the Catalog changed by the departure", result)
		}
	})
}

// Two rounds always build whatever the round before them was: the periodic
// full read, and the first round of a new leader term -- a new process, which
// remembers nothing, or one that stepped down and was elected again, which
// forgot the round when it stepped down.
func TestThePeriodicReadAndANewLeaderAlwaysBuild(t *testing.T) {
	harness := newChangeGateHarness(t)
	settled := harness.settleActive()
	harness.wantBuild(harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0), controlplane.SourceRefreshReused)
	harness.clock = harness.clock.Add(controlplane.SourceFullReadInterval)
	harness.wantBuild(harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadFull, controlplane.SourceReadPeriodic, 1), controlplane.SourceRefreshRebuilt)

	successor, err := controlplane.NewSourceReconciler(harness.repository, harness.compiler, harness.semantics)
	if err != nil {
		t.Fatal(err)
	}
	if err := successor.ConfigureClock(func() time.Time { return harness.clock }); err != nil {
		t.Fatal(err)
	}
	harness.reconciler = successor
	first := harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadFull, controlplane.SourceReadElected, 1)
	harness.wantBuild(first, controlplane.SourceRefreshRebuilt)
	if first.Publication != settled.Publication {
		t.Fatalf("the new term's first round published %+v, want it where the fleet already is: %+v", first.Publication, settled.Publication)
	}
	// That round compiled every strategy, with an empty cache, and ended
	// UNCHANGED; the next one stands on it and compiled nothing itself.
	if first.CompiledStrategies == 0 {
		t.Fatalf("setup: the new term's first round compiled nothing: %+v", first)
	}
	reused := harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0)
	harness.wantBuild(reused, controlplane.SourceRefreshReused)
	if reused.CompiledStrategies != 0 || reused.ReusedStrategies != first.CompiledStrategies+first.ReusedStrategies {
		t.Fatalf("reused round reported %d compiled and %d reused, want 0 and %d",
			reused.CompiledStrategies, reused.ReusedStrategies, first.CompiledStrategies+first.ReusedStrategies)
	}

	successor.StepDown()
	harness.wantBuild(harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0), controlplane.SourceRefreshRebuilt)
}

// A step-down can come from a path other than the round's - the lease lost
// while a round is running - and it is safe there: the index goes at once,
// and the reusable round goes with the next round, which builds.
func TestAStepDownDuringARoundIsTakenByTheNextOne(t *testing.T) {
	harness := newChangeGateHarness(t)
	harness.settleActive()
	harness.wantBuild(harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0), controlplane.SourceRefreshReused)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				harness.reconciler.StepDown()
			}
		}
	}()
	result, err := harness.reconciler.Refresh(harness.ctx, harness.source, harness.planner)
	close(stop)
	<-done
	if err != nil || result.Status == "" {
		t.Fatalf("the round under concurrent step-downs = (%+v, %v)", result, err)
	}
	harness.reconciler.StepDown()
	if lookup := harness.reconciler.LookupStrategy("1001"); lookup.Available {
		t.Fatalf("after a step-down the lookup = %+v, want nothing published to answer from", lookup)
	}
	harness.wantBuild(harness.refresh(controlplane.SourceRefreshUnchanged, controlplane.SourceReadSkipped, controlplane.SourceReadUnchanged, 0), controlplane.SourceRefreshRebuilt)
}
