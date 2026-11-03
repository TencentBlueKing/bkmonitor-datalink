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
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/readhold"
)

// Groups read the same data when their queries name the same tables and
// data sources, whatever order they name them in; a query that names none
// is its own source.
func TestGroupsOnTheSameTablesAreOneSource(t *testing.T) {
	cpu := execution.QueryPlanFacts{QueryList: []execution.QueryClause{{DataSource: "bk_monitor", TableID: "system.cpu"}, {DataSource: "bk_monitor", TableID: "system.mem"}}}
	reordered := execution.QueryPlanFacts{QueryList: []execution.QueryClause{{DataSource: "bk_monitor", TableID: "system.mem"}, {DataSource: "bk_monitor", TableID: "system.cpu"}}}
	if readHoldSource(cpu, "a") != readHoldSource(reordered, "b") {
		t.Fatalf("one set of tables read as two sources: %q %q", readHoldSource(cpu, "a"), readHoldSource(reordered, "b"))
	}
	disk := execution.QueryPlanFacts{QueryList: []execution.QueryClause{{DataSource: "bk_monitor", TableID: "system.disk"}}}
	if readHoldSource(cpu, "a") == readHoldSource(disk, "a") {
		t.Fatal("two tables read as one source")
	}
	if readHoldSource(execution.QueryPlanFacts{}, "a") == readHoldSource(execution.QueryPlanFacts{}, "b") {
		t.Fatal("two queries naming no table read as one source")
	}
}

// A measured arrival suggests a time_delay only past the cold-start wait,
// and a seeded one suggests nothing.
func TestOnlyAMeasuredArrivalPastTheColdStartSuggestsADelay(t *testing.T) {
	basis := readHoldBasis{delay: 0, step: time.Minute, settlingWait: 10 * time.Second, coldStart: 30 * time.Second, known: true}
	if got := basis.suggestion(readhold.Record{ArrivalAgeMillis: 25_000}); got != 0 {
		t.Fatalf("a source landing in 25 s suggested %d s", got)
	}
	if got := basis.suggestion(readhold.Record{ArrivalAgeMillis: 95_000}); got <= 0 {
		t.Fatalf("a source landing in 95 s suggested %d s", got)
	}
	if got := basis.suggestion(readhold.Record{ArrivalAgeMillis: 95_000, ArrivalAgeSeed: readhold.SeedPeer}); got != 0 {
		t.Fatalf("a seeded arrival suggested %d s", got)
	}
}

// seedControl is a control store holding read-hold records by group.
type seedControl struct {
	values map[execution.QueryGroupIdentity][]byte
}

func (store seedControl) ReadControlBatch(_ context.Context, groups []execution.QueryGroupIdentity, _ string) ([]ownership.ControlRead, error) {
	reads := make([]ownership.ControlRead, len(groups))
	for index, qg := range groups {
		raw, found := store.values[qg]
		reads[index] = ownership.ControlRead{Raw: raw, Missing: !found}
	}
	return reads, nil
}

func (store seedControl) FencedCompareAndSet(context.Context, ownership.FencedCASRequest) (ownership.FencedCASStatus, error) {
	return ownership.FencedCASApplied, nil
}

// A group with no record starts from its peers on the same data: the 90th
// percentile of what they measured, each net of its own time_delay, plus its
// own. A peer whose arrival is itself a seed is no measurement and is not
// read; a source nobody has measured starts from the cold start.
func TestANewGroupIsSeededFromItsPeersOnTheSameData(t *testing.T) {
	values := map[execution.QueryGroupIdentity][]byte{}
	arrivals := []int64{12_000, 14_000, 16_000, 18_000, 20_000, 22_000, 24_000, 26_000, 28_000, 60_000}
	groups := map[execution.QueryGroupIdentity]*productionReadHoldGroup{}
	for i, arrival := range arrivals {
		qg := execution.QueryGroupIdentity(fmt.Sprintf("peer-%d", i))
		raw, _ := json.Marshal(readhold.Record{SinceSlot: 1, ArrivalAgeMillis: arrival + 5_000})
		values[qg] = raw
		groups[qg] = &productionReadHoldGroup{querySource: "bk_monitor/system.cpu", queryDelay: 5 * time.Second}
	}
	// A seeded peer far below the others: counted, it would move the 90th
	// percentile down to the 28 s one.
	seeded, _ := json.Marshal(readhold.Record{SinceSlot: 1, ArrivalAgeMillis: 1_000, ArrivalAgeSeed: readhold.SeedColdStart})
	values["seeded-peer"] = seeded
	groups["seeded-peer"] = &productionReadHoldGroup{querySource: "bk_monitor/system.cpu"}
	groups["new"] = &productionReadHoldGroup{querySource: "bk_monitor/system.cpu", queryDelay: time.Minute}
	groups["alone"] = &productionReadHoldGroup{querySource: "bk_monitor/system.disk"}
	controller, err := readhold.NewController(readhold.Options{Control: seedControl{values}, Prefix: "schedule", MaxHold: 10 * time.Minute,
		Owner: func(execution.QueryGroupIdentity) (readhold.Owner, error) { return readhold.Owner{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	var restore []execution.QueryGroupIdentity
	for qg := range values {
		if err := controller.Configure(readhold.GroupSpec{QueryGroup: qg, Plans: []readhold.PlanRef{{Key: execution.PlanKey{
			PlanIdentity: execution.PlanIdentity{TenantID: "t", BusinessID: "2", StrategyID: string(qg)}}}}}); err != nil {
			t.Fatal(err)
		}
		restore = append(restore, qg)
	}
	if err := controller.RestoreBatch(context.Background(), restore); err != nil {
		t.Fatal(err)
	}
	holds := &productionReadHolds{controller: controller, groups: map[execution.QueryGroupIdentity]*productionReadHoldGroup{}}
	for qg, group := range groups {
		source := group.querySource
		group.querySource = ""
		holds.groups[qg] = group
		holds.indexSourceLocked(qg, source)
		group.querySource = source
	}
	// Nine peers measured 12 to 28 s and one 60 s, net of their 5 s delay:
	// the 90th percentile of the ten, sorted, is the tenth - the 60 s one.
	if seed, origin := holds.seedArrival("new", time.Minute, 30*time.Second, time.Hour); origin != readhold.SeedPeer || seed != time.Minute+60*time.Second {
		t.Fatalf("seed %v from %q, want the peers' 90th percentile past the group's own minute", seed, origin)
	}
	if seed, origin := holds.seedArrival("alone", 0, 30*time.Second, time.Hour); origin != readhold.SeedColdStart || seed != 30*time.Second {
		t.Fatalf("a source nobody measured seeded %v from %q, want the 30 s cold start", seed, origin)
	}
}

// The source index follows the groups: a group that moves to other data
// leaves its old source's peers, one forgotten leaves the index, and a
// source with no group left is gone.
func TestTheSourceIndexFollowsTheGroups(t *testing.T) {
	holds := &productionReadHolds{groups: map[execution.QueryGroupIdentity]*productionReadHoldGroup{}}
	for _, qg := range []execution.QueryGroupIdentity{"a", "b"} {
		holds.groups[qg] = &productionReadHoldGroup{}
		holds.indexSourceLocked(qg, "cpu")
		holds.groups[qg].querySource = "cpu"
	}
	holds.indexSourceLocked("a", "disk")
	holds.groups["a"].querySource = "disk"
	if _, still := holds.sources["cpu"]["a"]; still || len(holds.sources["cpu"]) != 1 || len(holds.sources["disk"]) != 1 {
		t.Fatalf("after a moved to disk: %v", holds.sources)
	}
	holds.indexSourceLocked("b", "")
	delete(holds.groups, "b")
	if _, left := holds.sources["cpu"]; left {
		t.Fatalf("a source with no group stayed: %v", holds.sources)
	}
}

// Binding and forgetting a group keep the index in step: a group a Runner
// releases is no longer anyone's peer.
func TestAForgottenGroupIsNoLongerAPeer(t *testing.T) {
	controller, err := readhold.NewController(readhold.Options{Control: seedControl{map[execution.QueryGroupIdentity][]byte{}}, Prefix: "schedule",
		MaxHold: 10 * time.Minute, Owner: func(execution.QueryGroupIdentity) (readhold.Owner, error) { return readhold.Owner{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	holds := &productionReadHolds{controller: controller, groups: map[execution.QueryGroupIdentity]*productionReadHoldGroup{}}
	session := &ownership.Session{}
	holds.bind("a", session)
	holds.mu.Lock()
	holds.indexSourceLocked("a", "cpu")
	holds.groups["a"].querySource = "cpu"
	holds.mu.Unlock()
	holds.forget("a", session)
	if len(holds.sources) != 0 {
		t.Fatalf("a forgotten group stayed in the index: %v", holds.sources)
	}
	holds.bind("b", session)
	holds.mu.Lock()
	holds.indexSourceLocked("b", "cpu")
	holds.groups["b"].querySource = "cpu"
	holds.mu.Unlock()
	holds.bind("b", &ownership.Session{})
	if len(holds.sources) != 0 {
		t.Fatalf("a group bound again kept its old source in the index: %v", holds.sources)
	}
}

// A peer seed stays inside the group's own read budget. One-minute peers on
// the same table that measured a 30-second landing seed a one-minute group
// at 30 s, but a ten-second group - its Slot due 30 s after the window, 25 s
// of it for the query - at no more than its budget past the 10 s floor:
// 15 s. A group with Plans of both is held to the ten-second one's budget,
// the one a late read misses first.
func TestAPeerSeedStaysInsideTheGroupsOwnReadBudget(t *testing.T) {
	values := map[execution.QueryGroupIdentity][]byte{}
	groups := map[execution.QueryGroupIdentity]*productionReadHoldGroup{}
	for i := 0; i < 5; i++ {
		qg := execution.QueryGroupIdentity(fmt.Sprintf("minute-peer-%d", i))
		raw, _ := json.Marshal(readhold.Record{SinceSlot: 1, ArrivalAgeMillis: 30_000})
		values[qg] = raw
		groups[qg] = &productionReadHoldGroup{querySource: "bk_monitor/system.cpu"}
	}
	for _, qg := range []execution.QueryGroupIdentity{"ten-second", "one-minute", "both", "inert"} {
		groups[qg] = &productionReadHoldGroup{querySource: "bk_monitor/system.cpu"}
	}
	groups["alone-both"] = &productionReadHoldGroup{querySource: "bk_monitor/system.disk"}
	controller, err := readhold.NewController(readhold.Options{Control: seedControl{values}, Prefix: "schedule", MaxHold: 10 * time.Minute,
		Owner: func(execution.QueryGroupIdentity) (readhold.Owner, error) { return readhold.Owner{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	var restore []execution.QueryGroupIdentity
	for qg := range values {
		if err := controller.Configure(readhold.GroupSpec{QueryGroup: qg, Plans: []readhold.PlanRef{{Key: execution.PlanKey{
			PlanIdentity: execution.PlanIdentity{TenantID: "t", BusinessID: "2", StrategyID: string(qg)}}}}}); err != nil {
			t.Fatal(err)
		}
		restore = append(restore, qg)
	}
	if err := controller.RestoreBatch(context.Background(), restore); err != nil {
		t.Fatal(err)
	}
	holds := &productionReadHolds{cfg: validGoAccessRuntimeConfig(), controller: controller, groups: map[execution.QueryGroupIdentity]*productionReadHoldGroup{}}
	holds.cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(30 * time.Second)
	holds.cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(5 * time.Second)
	for qg, group := range groups {
		source := group.querySource
		group.querySource = ""
		holds.groups[qg] = group
		holds.indexSourceLocked(qg, source)
		group.querySource = source
	}
	for _, c := range []struct {
		qg      execution.QueryGroupIdentity
		periods []int64
		want    time.Duration
	}{
		{"one-minute", []int64{60}, 30 * time.Second},
		{"ten-second", []int64{10}, 15 * time.Second},
		{"both", []int64{60, 10}, 15 * time.Second},
	} {
		seed, origin := holds.seedFor(c.qg, 0, seedCeilingSchedule(t, c.qg, c.periods...))
		if origin != readhold.SeedPeer || seed != c.want {
			t.Fatalf("%s on %v s schedules seeded %v from %q, want %v from its peers", c.qg, c.periods, seed, origin, c.want)
		}
	}
	// A schedule whose budget does not reach past the floor has no room at
	// all: its peer seed is its own time_delay, never less.
	inert := seedCeilingSchedule(t, "inert", 60)
	inert.Plans[0].Spec.CompletionDeadlineOffsetSeconds = 12
	if seed, origin := holds.seedFor("inert", 7*time.Second, inert); origin != readhold.SeedPeer || seed != 7*time.Second {
		t.Fatalf("a schedule with no room past its floor seeded %v from %q, want its own 7 s time_delay", seed, origin)
	}
	// The cold start is taken from the tightest Plan too: a source nobody
	// measured starts a group with a one-minute and a ten-second Plan at the
	// ten-second one's 10 s, not the one-minute one's 30 s.
	if seed, origin := holds.seedFor("alone-both", 0, seedCeilingSchedule(t, "alone-both", 60, 10)); origin != readhold.SeedColdStart || seed != 10*time.Second {
		t.Fatalf("a group on unmeasured data with 60 s and 10 s Plans seeded %v from %q, want the 10 s cold start", seed, origin)
	}
}

// seedCeilingSchedule is a schedule of the group with one Plan per period,
// each with the schedule spec a strategy of that period derives.
func seedCeilingSchedule(t *testing.T, qg execution.QueryGroupIdentity, periods ...int64) execution.FrozenQueryGroupSchedule {
	t.Helper()
	var plans []execution.FrozenPlanSchedule
	for index, period := range periods {
		spec := execution.DeriveScheduleSpec(period)
		revision, err := execution.DerivePlanScheduleRevision(spec)
		if err != nil {
			t.Fatal(err)
		}
		plans = append(plans, execution.FrozenPlanSchedule{Identity: execution.PlanIdentity{TenantID: "t", BusinessID: "2",
			StrategyID: fmt.Sprintf("%d", index+1)}, ScheduleRevision: revision, Spec: spec})
	}
	return execution.FrozenQueryGroupSchedule{Segment: execution.ScheduleSegmentFact{QueryGroup: qg}, Plans: plans}
}
