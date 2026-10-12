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
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// untilUnchanged refreshes until a round finds the publication unchanged and
// returns that round, with the strategy disposition the latest audit gave
// sourceID after each round, in order. How the rounds read the source is
// not asserted: these tests are about what leaves the Catalog and when.
func (harness *changeGateHarness) untilUnchanged(sourceID string) (controlplane.SourceRefreshResult, []controlplane.ObjectDisposition) {
	harness.t.Helper()
	var seen []controlplane.ObjectDisposition
	for round := 0; round < 8; round++ {
		result, err := harness.reconciler.Refresh(harness.ctx, harness.source, harness.planner)
		if err != nil {
			harness.t.Fatalf("round %d: Refresh() error = %v", round, err)
		}
		seen = append(seen, harness.strategyDisposition(sourceID))
		if result.Status == controlplane.SourceRefreshUnchanged {
			return result, seen
		}
	}
	harness.t.Fatal("the source did not settle within eight rounds")
	return controlplane.SourceRefreshResult{}, nil
}

// strategyDisposition is the STRATEGY-scope disposition the latest audit
// gives sourceID, zero when it gives none or there is no audit yet.
func (harness *changeGateHarness) strategyDisposition(sourceID string) controlplane.ObjectDisposition {
	harness.t.Helper()
	audit, err := harness.repository.LoadLatestAudit(harness.ctx)
	if errors.Is(err, controlplane.ErrSnapshotUnavailable) {
		return controlplane.ObjectDisposition{}
	}
	if err != nil {
		harness.t.Fatal(err)
	}
	for _, disposition := range audit.Dispositions {
		if disposition.Scope == "STRATEGY" && disposition.SourceID == sourceID {
			return disposition
		}
	}
	return controlplane.ObjectDisposition{}
}

// publishedPlans is the strategies the publication holds a Plan for.
func (harness *changeGateHarness) publishedPlans(publication controlplane.SnapshotPublicationRef) []string {
	harness.t.Helper()
	snapshot, err := loadPublishedSnapshot(harness.ctx, harness.repository, publication)
	if err != nil {
		harness.t.Fatal(err)
	}
	ids := []string{}
	for _, group := range snapshot.QueryGroups {
		for _, plan := range group.Plans {
			ids = append(ids, plan.Identity.StrategyID)
		}
	}
	sort.Strings(ids)
	return ids
}

// The writer that states hold-last-good takes a strategy out of its list
// only when the strategy is gone. Under its statement an absence is a
// removal: the rounds that read the list without strategy 1002 publish a
// Catalog without its Plan with no time passing at all, and the audit names
// it REMOVED, never PENDING_REMOVAL.
func TestAnAbsenceUnderTheWritersStatementIsRemovedAtOnce(t *testing.T) {
	harness := newChangeGateHarness(t)
	harness.state(harness.signalled(), harnessStrategyIDs)
	settled, _ := harness.untilUnchanged("1002")
	if plans := harness.publishedPlans(settled.Publication); !reflect.DeepEqual(plans, []string{"1001", "1002"}) {
		t.Fatalf("settled plans = %v, want both", plans)
	}

	harness.clock = harness.clock.Add(time.Minute)
	harness.publish(harness.clock, `[1001]`)
	removed, seen := harness.untilUnchanged("1002")
	if plans := harness.publishedPlans(removed.Publication); !reflect.DeepEqual(plans, []string{"1001"}) {
		t.Fatalf("plans after the writer dropped 1002 under its statement = %v, want only 1001 with no time passed", plans)
	}
	wantRemoved := controlplane.ObjectDisposition{SourceID: "1002", Scope: "STRATEGY",
		Disposition: controlplane.DispositionRemoved, Reason: "ABSENT_FROM_ACTIVE_SET"}
	removedSeen := false
	for _, disposition := range seen {
		if disposition.Disposition == controlplane.DispositionPendingRemoval {
			t.Fatalf("audits after the drop = %+v, want no grace under the writer's statement", seen)
		}
		removedSeen = removedSeen || disposition == wantRemoved
	}
	if !removedSeen {
		t.Fatalf("audits after the drop = %+v, want %+v on the round that removed it", seen, wantRemoved)
	}
}

// The writer without a statement drops enabled strategies for minutes at a
// time and lists them again. Without a statement a strategy absent from the
// list keeps its Plan through an absence shorter than AbsenceGracePeriod,
// comes back without a removal, and an absence that outlasts the grace
// removes it.
func TestWithoutAStatementTheGraceRidesOutAFlapAndRemovesAfterIt(t *testing.T) {
	harness := newChangeGateHarness(t)
	harness.untilUnchanged("1002")

	// The list loses 1002 in place, the way the hourly full refresh does.
	dropped := harness.clock
	harness.setActiveSet(`[1001]`)
	graced, _ := harness.untilUnchanged("1002")
	if plans := harness.publishedPlans(graced.Publication); !reflect.DeepEqual(plans, []string{"1001", "1002"}) {
		t.Fatalf("plans on the drop = %v, want 1002 kept under the grace", plans)
	}
	want := controlplane.ObjectDisposition{SourceID: "1002", Scope: "STRATEGY", Disposition: controlplane.DispositionPendingRemoval,
		Reason: "REMOVED_FROM_ACTIVE_SET", AbsentSince: dropped.Unix()}
	if got := harness.strategyDisposition("1002"); got != want {
		t.Fatalf("audit on the drop = %+v, want %+v", got, want)
	}

	// Back a minute before the grace ends: accepted, nothing removed.
	harness.clock = dropped.Add(controlplane.AbsenceGracePeriod - time.Minute)
	harness.setActiveSet(harnessStrategyIDs)
	back, _ := harness.untilUnchanged("1002")
	if plans := harness.publishedPlans(back.Publication); !reflect.DeepEqual(plans, []string{"1001", "1002"}) {
		t.Fatalf("plans after the flap = %v, want both", plans)
	}
	if got := harness.strategyDisposition("1002"); got != (controlplane.ObjectDisposition{}) {
		t.Fatalf("audit after the flap = %+v, want no removal fact", got)
	}

	// Dropped again and absent for the whole grace: removed.
	dropped = harness.clock
	harness.setActiveSet(`[1001]`)
	harness.untilUnchanged("1002")
	harness.clock = dropped.Add(controlplane.AbsenceGracePeriod)
	gone, _ := harness.untilUnchanged("1002")
	if plans := harness.publishedPlans(gone.Publication); !reflect.DeepEqual(plans, []string{"1001"}) {
		t.Fatalf("plans after the grace = %v, want only 1001", plans)
	}
}

// An empty list is a statement like any other: every strategy in it is
// absent. Under the writer's statement the publication that follows has no
// Plan left, with no time passed; without a statement every strategy serves
// the grace and then leaves. Neither is held for as long as the list stays
// empty. The observation of an empty list is an observation of nothing, not
// no observation: the absent-alert close then refuses it by its own word
// (snapshot_empty) rather than as a snapshot it never read.
func TestAnEmptyListRemovesEveryStrategy(t *testing.T) {
	t.Run("under the writer's statement", func(t *testing.T) {
		harness := newChangeGateHarness(t)
		harness.state(harness.signalled(), harnessStrategyIDs)
		harness.untilUnchanged("1001")
		harness.clock = harness.clock.Add(time.Minute)
		harness.publish(harness.clock, `[]`)
		emptied, _ := harness.untilUnchanged("1001")
		if plans := harness.publishedPlans(emptied.Publication); len(plans) != 0 {
			t.Fatalf("plans after the writer published an empty list under its statement = %v, want none", plans)
		}
		observed, ok := harness.reconciler.ObservedSnapshot()
		if !ok || len(observed.Strategies) != 0 || observed.Observation == "" || !observed.HoldsLastGood {
			t.Fatalf("ObservedSnapshot() of an empty list = (%+v, %v), want an observation of nothing under the statement", observed, ok)
		}
	})
	t.Run("without a statement", func(t *testing.T) {
		harness := newChangeGateHarness(t)
		harness.untilUnchanged("1001")
		emptiedAt := harness.clock
		harness.setActiveSet(`[]`)
		graced, _ := harness.untilUnchanged("1001")
		if plans := harness.publishedPlans(graced.Publication); !reflect.DeepEqual(plans, []string{"1001", "1002"}) {
			t.Fatalf("plans when the list was first found empty = %v, want both under the grace", plans)
		}
		want := controlplane.ObjectDisposition{SourceID: "1001", Scope: "STRATEGY", Disposition: controlplane.DispositionPendingRemoval,
			Reason: "REMOVED_FROM_ACTIVE_SET", AbsentSince: emptiedAt.Unix()}
		if got := harness.strategyDisposition("1001"); got != want {
			t.Fatalf("audit when the list was first found empty = %+v, want %+v", got, want)
		}
		harness.clock = emptiedAt.Add(controlplane.AbsenceGracePeriod)
		gone, _ := harness.untilUnchanged("1001")
		if plans := harness.publishedPlans(gone.Publication); len(plans) != 0 {
			t.Fatalf("plans after the list stayed empty for the whole grace = %v, want none", plans)
		}
	})
}
