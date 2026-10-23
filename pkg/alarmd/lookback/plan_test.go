// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lookback

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// plannedFixture is a fresh process - nothing learned about qg yet - with a
// supplement to hand directed reads to, given a plan that qg read late
// series at rung 2.
func plannedFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	recorder := &supplementRecorder{outcome: SupplementOutcome{Ran: true}}
	f.engine.options.Supplement = recorder.run
	f.engine.ApplyPlans([]Plan{{QueryGroup: "qg", Rung: 2, ExpiresAt: f.clock.now().Add(time.Hour)}})
	return f
}

// A process that has never sampled a Query Group the control plane saw read
// late series reads it directed from its first read, at the plan's rung: a
// rollout or a new owner no longer loses an hour of its supplements.
func TestAPlannedQueryGroupIsReadDirectedFromItsFirstRead(t *testing.T) {
	f := plannedFixture(t)
	read := f.engine.Begin(query("qg", f.clock.now().Unix(), minute, sourceLog))
	if read == nil || read.directed == nil || read.directed.slot.rung != 2 {
		t.Fatalf("the first read of a planned Query Group = %+v, want it kept for a directed read at rung 2", read)
	}
	if late := f.group("qg").seriesLate; late == nil || !late.seeded || late.rung != 2 {
		t.Fatalf("the planned Query Group's state = %+v, want it seeded at rung 2", late)
	}
	if stats := f.engine.Stats(); stats.Plans[PlanSeeded] != 1 || stats.PlanEntries != 1 {
		t.Fatalf("seeded plans = %d, entries held %d; want 1 and 1", stats.Plans[PlanSeeded], stats.PlanEntries)
	}
	// The next read of it is still the one entry: counted once, and a newer
	// set's rung taken while no sample here has seen late series.
	f.engine.ApplyPlans([]Plan{{QueryGroup: "qg", Rung: 3, ExpiresAt: f.clock.now().Add(time.Hour)}})
	f.engine.Begin(query("qg", f.clock.now().Unix()+60, minute, sourceLog))
	if got := f.engine.Stats().Plans[PlanSeeded]; got != 1 {
		t.Fatalf("seeded plans after a second read = %d, want the one entry counted once", got)
	}
	if late := f.group("qg").seriesLate; late == nil || late.rung != 3 {
		t.Fatalf("after a newer set moved the rung = %+v, want rung 3", late)
	}
	// Unplanned, the same first read is not read directed.
	if other := f.engine.Begin(query("qg-b", f.clock.now().Unix(), minute, sourceLog)); other == nil || other.directed != nil {
		t.Fatalf("an unplanned Query Group's first read = %+v, want no directed read", other)
	}
}

// A Query Group sampled here is decided by its own samples: a plan neither
// starts directed reads for one found complete nor moves the rung of one
// found late.
func TestAPlanNeverOverridesWhatTheQueryGroupsOwnSamplesFound(t *testing.T) {
	clean := newFixture(t)
	steady := func(slot int64) []*execution.Dataset { return []*execution.Dataset{point(slot, "1")} }
	clean.classSample(0, []*execution.Dataset{point(clean.clock.now().Unix(), "1")}, steady)
	clean.engine.ApplyPlans([]Plan{{QueryGroup: "qg", Rung: 2, ExpiresAt: clean.clock.now().Add(time.Hour)}})
	if late := clean.group("qg").seriesLate; late != nil {
		t.Fatalf("a Query Group its own sample found complete = %+v, want it left as it is", late)
	}
	clean.engine.Begin(query("qg", clean.clock.now().Unix()+60, minute, sourceLog))
	if got := clean.engine.Stats().Plans; got[PlanKeptLocal] != 1 || got[PlanSeeded] != 0 {
		t.Fatalf("plan outcomes after another read = %v, want one kept_local and none seeded", got)
	}

	late, _ := directedFixture(t, SupplementOutcome{Ran: true})
	own := late.group("qg").seriesLate.rung
	late.engine.ApplyPlans([]Plan{{QueryGroup: "qg", Rung: own + 2, ExpiresAt: late.clock.now().Add(time.Hour)}})
	if got := late.group("qg").seriesLate; got == nil || got.seeded || got.rung != own {
		t.Fatalf("a Query Group its own samples found late = %+v, want its own rung %d kept", got, own)
	}
}

// A seeded Query Group none of whose samples here saw late series leaves
// directed reading when its entry goes: withdrawn from the next set, or
// expired with no set since - a lost stream, a fallback Leader. One whose
// own sample has seen late series since stays.
func TestASeededQueryGroupLeavesWithItsPlanUnlessItsOwnSamplesConfirmIt(t *testing.T) {
	withdrawn := plannedFixture(t)
	withdrawn.engine.Begin(query("qg", withdrawn.clock.now().Unix(), minute, sourceLog))
	withdrawn.engine.ApplyPlans(nil)
	if late := withdrawn.group("qg").seriesLate; late != nil {
		t.Fatalf("after its entry was withdrawn = %+v, want directed reading ended", late)
	}
	if stats := withdrawn.engine.Stats(); stats.Plans[PlanExpired] != 1 || stats.PlanEntries != 0 {
		t.Fatalf("expired plans = %d, entries held %d; want 1 and none", stats.Plans[PlanExpired], stats.PlanEntries)
	}

	expired := plannedFixture(t)
	expired.engine.Begin(query("qg", expired.clock.now().Unix(), minute, sourceLog))
	expired.set(func() { expired.clock.at = expired.clock.at.Add(time.Hour) })
	expired.engine.Step(context.Background())
	if late := expired.group("qg").seriesLate; late != nil {
		t.Fatalf("after its entry expired with no new set = %+v, want directed reading ended", late)
	}

	confirmed := plannedFixture(t)
	confirmed.engine.Begin(query("qg", confirmed.clock.now().Unix(), minute, sourceLog))
	confirmed.engine.mu.Lock()
	confirmed.engine.groups["qg"].seriesLate.seen = 1
	confirmed.engine.mu.Unlock()
	confirmed.engine.ApplyPlans(nil)
	if late := confirmed.group("qg").seriesLate; late == nil {
		t.Fatal("a seeded Query Group its own sample confirmed left directed reading with its plan")
	}
}

// Entries no Worker could act on are not plans: past their expiry, or at a
// rung this build does not read.
func TestAPlanPastItsExpiryOrRungsIsNoPlan(t *testing.T) {
	f := newFixture(t)
	f.engine.options.Supplement = (&supplementRecorder{outcome: SupplementOutcome{Ran: true}}).run
	f.engine.ApplyPlans([]Plan{
		{QueryGroup: "qg", Rung: 1, ExpiresAt: f.clock.now()},
		{QueryGroup: "qg-b", Rung: len(RungSteps), ExpiresAt: f.clock.now().Add(time.Hour)},
		{QueryGroup: "qg-c", Rung: -1, ExpiresAt: f.clock.now().Add(time.Hour)},
	})
	for _, queryGroup := range []string{"qg", "qg-b", "qg-c"} {
		if read := f.engine.Begin(query(queryGroup, f.clock.now().Unix(), minute, sourceLog)); read == nil || read.directed != nil {
			t.Fatalf("%s's first read under an unusable entry = %+v, want no directed read", queryGroup, read)
		}
	}
	// An entry that expired since the last maintenance tick is no plan for
	// the first read that comes before the next one.
	between := newFixture(t)
	between.engine.options.Supplement = (&supplementRecorder{outcome: SupplementOutcome{Ran: true}}).run
	between.engine.ApplyPlans([]Plan{{QueryGroup: "qg", Rung: 1, ExpiresAt: between.clock.now().Add(time.Minute)}})
	between.set(func() { between.clock.at = between.clock.at.Add(2 * time.Minute) })
	if read := between.engine.Begin(query("qg", between.clock.now().Unix(), minute, sourceLog)); read == nil || read.directed != nil {
		t.Fatalf("a first read after its entry expired, before a tick = %+v, want no directed read", read)
	}
}

// What the control plane plans from is this process's own evidence: a
// Query Group read directed because its samples here saw late series, by
// their rung; never one read directed only because a plan said so, which
// would keep the plan alive on its own word. A seeded one is reported once
// its own sample has seen late series too.
func TestOnlyOwnEvidenceIsReportedForPlanning(t *testing.T) {
	own, _ := directedFixture(t, SupplementOutcome{Ran: true})
	if rungs := own.engine.SeriesLateRungs(); len(rungs) != 1 || rungs["qg"] != own.group("qg").seriesLate.rung {
		t.Fatalf("reported %v, want qg at its own rung", rungs)
	}
	seeded := plannedFixture(t)
	seeded.engine.Begin(query("qg", seeded.clock.now().Unix(), minute, sourceLog))
	if rungs := seeded.engine.SeriesLateRungs(); len(rungs) != 0 {
		t.Fatalf("reported %v for a Query Group read directed only on its plan, want nothing", rungs)
	}
	seeded.engine.mu.Lock()
	seeded.engine.groups["qg"].seriesLate.seen = 1
	seeded.engine.mu.Unlock()
	if rungs := seeded.engine.SeriesLateRungs(); len(rungs) != 1 || rungs["qg"] != 2 {
		t.Fatalf("reported %v once its own sample confirmed it, want qg at rung 2", rungs)
	}
}
