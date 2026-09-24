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
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func lossGroups(reports []CheckReport) map[string]int {
	groups := map[string]int{}
	for _, report := range reports {
		if report.Code != CheckDetectionAbandoned {
			continue
		}
		for _, group := range report.Groups {
			groups[group.Key] = group.Objects
		}
	}
	return groups
}

// A record that says a cooldown held its Slot is the cooldown's whether or
// not the object is still in the pool when it is read: in the pool it is
// the line's consequence as before; out of the pool it is AFTER_COOLDOWN,
// current on the record line and not this deployment's work -- not ONGOING,
// which is what an object whose cooldown ran out thirty seconds earlier read
// as on a live deployment. A record that names no holder is read as before,
// and a cooldown's record older than the window is history like any other.
func TestARecordHeldByACooldownIsTheCooldownsAfterTheObjectLeavesThePool(t *testing.T) {
	at := time.Date(2026, 9, 21, 3, 8, 0, 0, time.UTC)
	view := &View{
		PerReplica: []ReplicaView{{Replica: "pod-a", StartedAt: at.Add(-time.Hour)}},
		Demoted: []Anomaly{{QueryGroup: "qg-still-cooling", Kind: KindQueryCooldown, DemotedSince: at.Add(-20 * time.Minute),
			Finding: Finding{Check: CheckQueryTargetMissing}}},
		GapSkips: map[string]SkippedSpan{
			// Held by the cooldown, object still in the pool: the line's consequence.
			"qg-still-cooling": {FirstSlot: 1, LastSlot: 1, Slots: 1, At: at.Add(-time.Minute), Replica: "pod-a", HeldBy: "query_cooldown"},
			// Held by the cooldown, object out of the pool: the cooldown's, after.
			"qg-left-pool": {FirstSlot: 1, LastSlot: 1, Slots: 1, At: at.Add(-time.Minute), Replica: "pod-a", HeldBy: "query_cooldown"},
			// Nothing held it: a loss in progress on this deployment.
			"qg-plain": {FirstSlot: 1, LastSlot: 1, Slots: 1, At: at.Add(-time.Minute), Replica: "pod-a"},
			// Held by something other than the cooldown: not the cooldown's.
			"qg-deferred": {FirstSlot: 1, LastSlot: 1, Slots: 1, At: at.Add(-time.Minute), Replica: "pod-a", HeldBy: observability.HeldByReadinessDeferred},
			// The cooldown's, but older than the window: history.
			"qg-old-cooldown": {FirstSlot: 1, LastSlot: 1, Slots: 1, At: at.Add(-30 * time.Minute), Replica: "pod-a", HeldBy: "query_cooldown"},
		}}
	columns := [][]Anomaly{nil, {view.Demoted[0]}, nil, nil}
	reports := ReportChecks(columns, nil, view, at)
	groups := lossGroups(reports)
	if groups[string(LossAfterCooldown)] != 1 || groups[string(LossOngoing)] != 2 || groups[string(LossHistorical)] != 1 || groups[string(LossWhileDemoted)] != 0 {
		t.Fatalf("groups = %v, want 1 AFTER_COOLDOWN, 2 ONGOING (plain, deferred), 1 HISTORICAL, and the pooled object's record on its own line", groups)
	}
	var abandoned CheckReport
	for _, report := range reports {
		if report.Code == CheckDetectionAbandoned {
			abandoned = report
		}
		if report.Code == CheckQueryTargetMissing && (report.Consequence == nil || report.Consequence.Skipped != 1) {
			t.Errorf("the pooled object's record is not its line's consequence: %+v", report.Consequence)
		}
	}
	if abandoned.Current != 3 || abandoned.Retained != 1 {
		t.Errorf("DETECTION_ABANDONED current/retained = %d/%d, want 3/1: after-cooldown is current on the line, not retained", abandoned.Current, abandoned.Retained)
	}
	todo := SummarizeTodo(reports, columns, view, at)
	if todo.AfterCooldown != 1 || todo.Ongoing != 2 || todo.Objects != 2 || todo.WhileDemoted != 1 {
		t.Fatalf("todo = %+v, want after_cooldown 1, ongoing 2 (and only those two objects ours), while_demoted 1", todo)
	}
	// The row itself says what it is.
	rows := UnderCheck(CheckDetectionAbandoned, string(LossAfterCooldown), view, at)
	if len(rows) != 1 || rows[0].QueryGroup != "qg-left-pool" || rows[0].Loss != LossAfterCooldown || rows[0].Skip == nil || rows[0].Skip.HeldBy != "query_cooldown" {
		t.Fatalf("rows under AFTER_COOLDOWN = %+v, want the object that left the pool with its held-by", rows)
	}
	// The load reading counts it in progress and names it apart.
	load := LoadOf(view, at)
	if load.Loss.State != LossInProgress || load.Loss.AfterCooldown != 1 || load.Loss.Ongoing != 2 {
		t.Fatalf("load loss = %+v", load.Loss)
	}
	// And the census the metric family exports agrees with the lines.
	census, unknown := LossCensus(view, at)
	if census[LossAfterCooldown] != 1 || census[LossOngoing] != 2 || census[LossHistorical] != 1 || census[LossWhileDemoted] != 1 || census[LossAfterRestart] != 0 || unknown != 0 {
		t.Fatalf("census = %v unknown %d", census, unknown)
	}
}

// The restart grace is judged from two anchors -- the replica's process start
// and when it first saw the object -- because a replica starts working an
// object a lease expiry, a catalog load and a reconcile round after its
// process starts, and a real catch-up fell past a grace anchored at start
// alone. Either anchor within the grace is the restart's; the record carries
// both offsets so a reader can see which. A record with neither anchor is
// judged by age alone and counted as such, not read as "not the restart's"
// in silence.
func TestTheRestartGraceIsAnchoredAtTheProcessStartAndAtTheFirstSightOfTheObject(t *testing.T) {
	at := time.Date(2026, 9, 21, 3, 20, 0, 0, time.UTC)
	started := at.Add(-9 * time.Minute)
	view := &View{
		PerReplica: []ReplicaView{{Replica: "pod-a", StartedAt: started}, {Replica: "pod-old"}},
		GapSkips: map[string]SkippedSpan{
			// Seven minutes after the process start, but two minutes after
			// the replica first saw the object: the restart's.
			"qg-late-takeover": {FirstSlot: 1, LastSlot: 1, Slots: 1, At: started.Add(7 * time.Minute), Replica: "pod-a",
				FirstSeenAt: started.Add(5 * time.Minute)},
			// Seven minutes after both: not the restart's.
			"qg-long-after": {FirstSlot: 1, LastSlot: 1, Slots: 1, At: started.Add(7 * time.Minute), Replica: "pod-a",
				FirstSeenAt: started.Add(10 * time.Second)},
			// Two minutes after the start with no first sight published: the
			// restart's on the start alone, as before.
			"qg-start-only": {FirstSlot: 1, LastSlot: 1, Slots: 1, At: started.Add(2 * time.Minute), Replica: "pod-a"},
			// A publisher with neither anchor: judged by age, and counted.
			"qg-unjudged": {FirstSlot: 1, LastSlot: 1, Slots: 1, At: at.Add(-2 * time.Minute), Replica: "pod-old"},
			// Neither anchor and old: history, not counted as unjudged.
			"qg-unjudged-old": {FirstSlot: 1, LastSlot: 1, Slots: 1, At: at.Add(-40 * time.Minute), Replica: "pod-old"},
			// No process start published, but the record says when the
			// replica first saw the object: judged on that anchor alone --
			// the restart's, and not counted as unjudged.
			"qg-sight-only": {FirstSlot: 1, LastSlot: 1, Slots: 1, At: at.Add(-2 * time.Minute), Replica: "pod-old",
				FirstSeenAt: at.Add(-3 * time.Minute)},
		}}
	columns := [][]Anomaly{nil, nil, nil, nil}
	reports := ReportChecks(columns, nil, view, at)
	groups := lossGroups(reports)
	if groups[string(LossAfterRestart)] != 3 || groups[string(LossOngoing)] != 2 || groups[string(LossHistorical)] != 1 {
		t.Fatalf("groups = %v, want 3 AFTER_RESTART (late takeover, start only, sight only), 2 ONGOING (long after, unjudged), 1 HISTORICAL", groups)
	}
	todo := SummarizeTodo(reports, columns, view, at)
	if todo.RestartGraceUnknown != 1 || todo.AfterRestart != 3 || todo.Ongoing != 2 {
		t.Fatalf("todo = %+v, want exactly one recent record judged without an anchor", todo)
	}
	byObject := map[string]Anomaly{}
	for _, row := range UnderCheck(CheckDetectionAbandoned, "", view, at) {
		byObject[row.QueryGroup] = row
	}
	late := byObject["qg-late-takeover"].Skip
	if late == nil || late.RestartOffsetSeconds == nil || *late.RestartOffsetSeconds != 420 || late.TakeoverOffsetSeconds == nil || *late.TakeoverOffsetSeconds != 120 {
		t.Fatalf("late takeover offsets = %+v, want 420 s after start and 120 s after first sight on the row", late)
	}
	if only := byObject["qg-start-only"].Skip; only == nil || only.RestartOffsetSeconds == nil || *only.RestartOffsetSeconds != 120 || only.TakeoverOffsetSeconds != nil {
		t.Fatalf("start-only offsets = %+v, want 120 s after start and no takeover offset", only)
	}
	if unjudged := byObject["qg-unjudged"].Skip; unjudged == nil || unjudged.RestartOffsetSeconds != nil || unjudged.TakeoverOffsetSeconds != nil {
		t.Fatalf("unjudged offsets = %+v, want neither", unjudged)
	}
	if sight := byObject["qg-sight-only"].Skip; sight == nil || sight.RestartOffsetSeconds != nil || sight.TakeoverOffsetSeconds == nil || *sight.TakeoverOffsetSeconds != 60 {
		t.Fatalf("sight-only offsets = %+v, want only the takeover offset, 60 s", sight)
	}
	census, unknown := LossCensus(view, at)
	if census[LossAfterRestart] != 3 || census[LossOngoing] != 2 || unknown != 1 {
		t.Fatalf("census = %v unknown %d, want the recent unjudged record counted once", census, unknown)
	}
}

// The tracker writes what held the Slot and when it first saw the object onto
// the record, from the completion's own facts: the held-by decision of the
// round before the skip, "none" left off, and the first sight from the first
// observation of the object in this process -- not from the skip.
func TestTheSkipRecordCarriesWhatHeldTheSlotAndWhenTheObjectWasFirstSeen(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	firstSeen := at.at
	tracker.Observe(context.Background(), completion("qg-cooled", "FULL_COMPLETED", "4101"))
	at.at = at.at.Add(3 * time.Minute)
	held := completion("qg-cooled", "GAP_SKIPPED", "4101")
	held.HeldBy = &observability.HeldByFacts{Decision: "query_cooldown", QueryCooldownFailures: 7}
	tracker.Observe(context.Background(), held)
	record := tracker.GapSkips()["qg-cooled"]
	if record.HeldBy != "query_cooldown" || !record.FirstSeenAt.Equal(firstSeen) || !record.At.Equal(at.at) {
		t.Fatalf("record = %+v, want held by the cooldown, first seen at the first observation, made now", record)
	}
	// A skip nothing held leaves the field off; the holder can also arrive on
	// the replay expiry's facts.
	tracker.Observe(context.Background(), completion("qg-plain", "GAP_SKIPPED", "4102"))
	if record := tracker.GapSkips()["qg-plain"]; record.HeldBy != "" || record.FirstSeenAt.IsZero() {
		t.Fatalf("plain record = %+v, want no holder and a first sight", record)
	}
	none := completion("qg-none", "GAP_SKIPPED", "4103")
	none.HeldBy = &observability.HeldByFacts{Decision: observability.HeldByNothing}
	tracker.Observe(context.Background(), none)
	if record := tracker.GapSkips()["qg-none"]; record.HeldBy != "" {
		t.Fatalf("record held by none = %+v, want the field off", record)
	}
	expiry := completion("qg-expiry", "GAP_SKIPPED", "4104")
	expiry.ReplayExpiry = &observability.ReplayExpiryFacts{HeldBy: &observability.HeldByFacts{Decision: "query_cooldown"}}
	tracker.Observe(context.Background(), expiry)
	if record := tracker.GapSkips()["qg-expiry"]; record.HeldBy != "query_cooldown" {
		t.Fatalf("record with the holder on the expiry facts = %+v, want it read", record)
	}
}
