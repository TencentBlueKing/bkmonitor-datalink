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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// fixedOverdueWakes is a due index that always reports the same parked objects.
type fixedOverdueWakes []fleet.OverdueWake

func (wakes fixedOverdueWakes) OverdueWakes(time.Time, int) ([]fleet.OverdueWake, int) {
	return wakes, len(wakes)
}

// The defect was in the publisher, not in the helper, so this drives the
// publisher. A test that only called the helper passed against the broken
// wiring -- it could not fail on the thing that actually shipped.
func TestThePublishedSnapshotCountsEachObjectOnce(t *testing.T) {
	at := time.Date(2026, 9, 11, 14, 12, 0, 0, time.UTC)
	now := func() time.Time { return at }
	tracker := fleet.NewTracker(nil, "replica-1", now)
	// Drive one object over the blocked threshold the way the pipeline does.
	for round := 0; round < fleet.DefaultBlockedRounds; round++ {
		tracker.Observe(context.Background(), observability.Observation{
			Trace: observability.TraceFields{QueryGroupKey: "qg-stuck"}, RunOutcome: "source_retry",
		})
	}
	if len(tracker.Anomalies()) != 1 {
		t.Fatalf("setup: tracker reports %d anomalies, want the one blocked object", len(tracker.Anomalies()))
	}

	publisher := fleetPublisher{
		tracker: tracker, replica: "replica-1", now: now,
		owned: func() []execution.QueryGroupIdentity { return []execution.QueryGroupIdentity{"qg-stuck", "qg-parked"} },
		// The same object is also past a wake time, which is the ordinary state
		// during a re-warm, plus one that is only overdue.
		overdue: fixedOverdueWakes{
			{QueryGroup: "qg-stuck", WakeAt: at.Add(-time.Hour), IntervalSeconds: 60},
			{QueryGroup: "qg-parked", WakeAt: at.Add(-time.Hour), IntervalSeconds: 60},
		},
	}
	snapshot := publisher.snapshot(context.Background())

	seen := map[string]int{}
	for _, anomaly := range snapshot.Anomalies {
		seen[anomaly.QueryGroup]++
	}
	if len(seen) != len(snapshot.Anomalies) {
		t.Errorf("published %d rows over %d objects: %v -- a row counted twice makes the columns claim "+
			"more objects than the replica can speak for", len(snapshot.Anomalies), len(seen), seen)
	}
	if snapshot.TotalAnomalies != len(seen) {
		t.Errorf("TotalAnomalies = %d over %d objects; the aggregate subtracts this from what the replica "+
			"can speak for, so it has to be objects rather than rows", snapshot.TotalAnomalies, len(seen))
	}
	if _, listed := seen["qg-parked"]; !listed {
		t.Error("the object that is only overdue was dropped; de-duplicating must not lose it")
	}
}

// An object that is not progressing is usually also past a wake time, and during
// a re-warm nearly all of them are. Appending a row for each door it arrived
// through made the list longer than the set of objects it was about.
//
// That was invisible while the length was only ever displayed. It stopped being
// invisible the moment the columns had to add up against what the replica can
// speak for: a running deployment reported 964 anomalies over 943 objects and
// held itself at UNKNOWN, correctly, on a defect in the counting rather than in
// the deployment.
func TestAnObjectAppearsOnceEvenWhenItIsBothStuckAndOverdue(t *testing.T) {
	at := time.Date(2026, 9, 11, 14, 12, 0, 0, time.UTC)
	tracked := []fleet.Anomaly{
		{QueryGroup: "qg-stuck", Kind: fleet.KindBlockedRun, ReasonCode: "source_retry", Since: at},
		{QueryGroup: "qg-degraded", Kind: fleet.KindDegradedRun, Since: at},
	}
	demoted := []fleet.Anomaly{{QueryGroup: "qg-pooled", Kind: fleet.KindQueryCooldown, Since: at}}
	overdue := []fleet.Anomaly{
		{QueryGroup: "qg-stuck", Kind: fleet.KindOverdueWake, ReasonCode: fleet.ReasonWakeMissed, Since: at},
		{QueryGroup: "qg-pooled", Kind: fleet.KindOverdueWake, ReasonCode: fleet.ReasonWakeMissed, Since: at},
		{QueryGroup: "qg-only-overdue", Kind: fleet.KindOverdueWake, ReasonCode: fleet.ReasonWakeMissed, Since: at},
	}

	kept := onlyUnlisted(overdue, tracked, demoted)
	if len(kept) != 1 {
		t.Fatalf("kept %d overdue rows, want only the object no column already carries: %+v", len(kept), kept)
	}
	if kept[0].QueryGroup != "qg-only-overdue" {
		t.Errorf("kept %q, want qg-only-overdue", kept[0].QueryGroup)
	}

	// The list and the objects it is about have to be the same size, because
	// that length is what the aggregate subtracts from what the replica can
	// speak for.
	published := append(append([]fleet.Anomaly{}, tracked...), kept...)
	seen := map[string]int{}
	for _, anomaly := range published {
		seen[anomaly.QueryGroup]++
	}
	if len(seen) != len(published) {
		t.Errorf("published %d rows over %d objects; a row counted twice makes the columns claim more than exist",
			len(published), len(seen))
	}
}

// The candidates can repeat an object among themselves, and a check that only
// looked at the other columns would let that through.
func TestOverdueRowsDoNotRepeatAnObjectAmongThemselves(t *testing.T) {
	at := time.Date(2026, 9, 11, 14, 12, 0, 0, time.UTC)
	overdue := []fleet.Anomaly{
		{QueryGroup: "qg-a", Kind: fleet.KindOverdueWake, Since: at},
		{QueryGroup: "qg-a", Kind: fleet.KindOverdueWake, Since: at.Add(time.Minute)},
	}
	if kept := onlyUnlisted(overdue); len(kept) != 1 {
		t.Errorf("kept %d rows for one object, want 1", len(kept))
	}
}
