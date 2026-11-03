// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Waiting for a first round is a narrow thing, decided on the schedule's
// grid: the Slot the object's bound waits for is the first grid point since
// this replica took it over, and it is not yet runnable. When it becomes
// runnable - the read wait, a stagger phase or a read hold - does not move
// that point, so an object whose first read comes late after takeover is
// still waiting. Each bound is tested on both sides.
func TestAwaitingFirstRoundIsTheFirstGridPointSinceTakeover(t *testing.T) {
	owned := now.Add(-10 * time.Second)
	wake := func(interval int64, slotIn, runnableIn time.Duration) WakeFacts {
		return WakeFacts{Known: true, IntervalSeconds: interval, NextSlot: now.Add(slotIn), DueAt: now.Add(runnableIn)}
	}
	held := wake(60, 20*time.Second, 20*time.Second+3*time.Minute)
	held.Deferred, held.ReadinessHold = true, true
	for _, tc := range []struct {
		name  string
		wake  WakeFacts
		owned time.Time
		want  bool
	}{
		{"a minute object before its first Slot since takeover", wake(60, 30*time.Second, 30*time.Second), owned, true},
		{"a two-hour object before its first Slot since takeover", wake(7200, time.Hour, time.Hour), owned, true},
		// The first read comes 35 s after the grid point (a stagger phase):
		// the Slot is the same one, so the object is still waiting.
		{"a phased minute object read 35 s after its grid point", wake(60, 20*time.Second, 55*time.Second), owned, true},
		// A 3 min read hold on the first Slot: deferred for readiness, not
		// for a failure, and still the first Slot since takeover.
		{"a held minute object", held, owned, true},
		{"a Slot one second before one period after takeover", wake(60, 49*time.Second, 49*time.Second), owned, true},
		{"a Slot one period after takeover: the second since then", wake(60, 50*time.Second, 50*time.Second), owned, false},
		{"a Slot pending from before takeover, not yet runnable", wake(60, -30*time.Second, 5*time.Second), owned, true},
		{"runnable now", wake(60, 0, 0), owned, false},
		{"past its runnable time: a replica that ran nothing", wake(60, -5*time.Second, -5*time.Second), owned, false},
		{"a failure backoff", WakeFacts{Known: true, IntervalSeconds: 60, NextSlot: now.Add(20 * time.Second), DueAt: now.Add(time.Minute), Deferred: true}, owned, false},
		{"a query cooldown", WakeFacts{Known: true, IntervalSeconds: 60, NextSlot: now.Add(20 * time.Second), DueAt: now.Add(time.Minute), Cooling: true}, owned, false},
		{"no Slot on the bound", WakeFacts{Known: true, IntervalSeconds: 60, DueAt: now.Add(30 * time.Second)}, owned, false},
		{"no takeover time", wake(60, 30*time.Second, 30*time.Second), time.Time{}, false},
		{"no entry", WakeFacts{}, owned, false},
	} {
		if got := AwaitingFirstRound(tc.wake, tc.owned, now); got != tc.want {
			t.Errorf("%s: awaiting = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Undetermined objects that are only waiting for their first round are said
// as such and do not hold the verdict at UNKNOWN; any other undetermined
// object still does, and only it is counted unknown.
func TestObjectsAwaitingTheirFirstRoundDoNotMakeTheVerdictUnknown(t *testing.T) {
	waits := []FirstRoundWait{
		{QueryGroup: "a", IntervalSeconds: 7200, DueAt: now.Add(time.Hour)},
		{QueryGroup: "b", IntervalSeconds: 14400, DueAt: now.Add(2 * time.Hour)},
		{QueryGroup: "c", IntervalSeconds: 14400, DueAt: now.Add(3 * time.Hour)},
		{QueryGroup: "d", IntervalSeconds: 216000, DueAt: now.Add(50 * time.Hour)},
	}
	snapshots := healthySnapshots()
	snapshots[0].Owned, snapshots[0].Determined = 504, 500
	snapshots[0].AwaitingFirstRound, snapshots[0].AwaitingFirstRoundTotal = waits, 4
	view := Aggregate(Expectation{QueryGroups: 953, Known: true}, snapshots, replicas(), now, freshness)
	if view.Health != HealthHealthy || view.Unknown != 0 || view.AwaitingFirstRound == nil || view.AwaitingFirstRound.Objects != 4 {
		t.Fatalf("health %s unknown %d awaiting %+v gaps %+v, want HEALTHY with four awaiting", view.Health, view.Unknown, view.AwaitingFirstRound, view.Gaps)
	}
	for _, want := range []string{"4 个对象在等第一轮", "周期 2 h 的 1 个", "周期 4 h 的 2 个", "周期 60 h 的 1 个，最迟 09-11 14:00Z 到期"} {
		if !strings.Contains(view.AwaitingFirstRound.Line, want) {
			t.Errorf("line %q lacks %q", view.AwaitingFirstRound.Line, want)
		}
	}
	// One more undetermined object that is not waiting: UNKNOWN, and it alone
	// is unknown.
	snapshots[0].Owned = 505
	view = Aggregate(Expectation{QueryGroups: 954, Known: true}, snapshots, replicas(), now, freshness)
	if view.Health != HealthUnknown || view.Unknown != 1 {
		t.Fatalf("health %s unknown %d, want UNKNOWN with the one object not waiting", view.Health, view.Unknown)
	}
}

// fleet.get names which objects unknown counts: the replicas' undetermined
// objects together, longest owned first, each with its replica, within the
// bound, and the total of all of them.
func TestTheDeploymentNamesItsUndeterminedObjects(t *testing.T) {
	snapshots := healthySnapshots()
	var many []UndeterminedObject
	for index := 0; index < 100; index++ {
		many = append(many, UndeterminedObject{QueryGroup: fmt.Sprintf("qg-%03d", index), IntervalSeconds: 60, OwnedSince: now.Add(time.Duration(index) * time.Second)})
	}
	snapshots[0].Owned, snapshots[0].Determined = 600, 500
	snapshots[0].Undetermined, snapshots[0].UndeterminedTotal = many[:MaxUndeterminedObjects], 100
	snapshots[1].Owned++
	snapshots[1].Undetermined, snapshots[1].UndeterminedTotal = []UndeterminedObject{{QueryGroup: "earliest", IntervalSeconds: 300, OwnedSince: now.Add(-time.Minute)}}, 1
	view := Aggregate(Expectation{QueryGroups: 1054, Known: true}, snapshots, replicas(), now, freshness)
	if view.UndeterminedTotal != 101 || len(view.Undetermined) != MaxUndeterminedObjects || view.Undetermined[0].QueryGroup != "earliest" ||
		view.Undetermined[0].Replica != snapshots[1].Replica || view.Undetermined[1].Replica != snapshots[0].Replica {
		t.Fatalf("undetermined %d total %d, first %+v second %+v", len(view.Undetermined), view.UndeterminedTotal, view.Undetermined[0], view.Undetermined[1])
	}
	health, _ := json.Marshal(healthOf(&view, ReplicaPart{}, now))
	for _, key := range []string{`"undetermined_objects"`, `"undetermined_objects_total":101`} {
		if !strings.Contains(string(health), key) {
			t.Fatalf("fleet.get lacks %s", key)
		}
	}
}
