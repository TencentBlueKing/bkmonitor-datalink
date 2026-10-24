// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// The census names the objects it counts as late, overdue and never evaluated,
// in the same pass and by the same rule: a page that lists them and a page that
// counts them read one instant, so each list is exactly as long as its count.
// On-time objects are not listed; they are the owned objects in no list.
func TestDueIndexNamesTheObjectsItsCensusCounts(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(10_000, 0)}
	dispatcher := dueIndexDispatcher(clock, metric.NewRecorder(metric.BuildInfo{}), 8, 8,
		map[execution.QueryGroupIdentity]walkRunner{
			"waiting": {}, "cooling": {}, "late": {}, "late-edge": {}, "overdue": {}, "no-period": {},
		})
	index := dispatcher.dueIndex
	record := func(queryGroup execution.QueryGroupIdentity, dueAt, interval int64, cooling bool) {
		index.Record(queryGroup, dispatcher.bundle.runners[queryGroup], index.versionEpoch,
			scheduler.RunnerDueBound{NotDueUntilUnix: dueAt, IntervalSeconds: interval, QueryCooldown: cooling},
			time.Unix(dueAt-1, 0))
	}
	record("waiting", 10_040, 60, false)
	record("cooling", 10_300, 60, true)
	record("late", 9_990, 60, false)
	record("late-edge", 9_940, 60, false)
	record("overdue", 9_800, 60, false)
	record("no-period", 9_000, 0, false)

	owned := []string{"cooling", "late", "late-edge", "never", "no-period", "overdue", "waiting"}
	census, classes := index.CensusWithDueClasses(clock.at, owned)
	if want := index.Census(clock.at, len(owned)); !reflect.DeepEqual(census, want) {
		t.Fatalf("census with classes = %+v, want the census %+v", census, want)
	}
	want := fleet.DueClasses{
		Late:    []string{"late", "late-edge", "no-period"},
		Overdue: []string{"overdue"},
		Never:   []string{"never"},
	}
	if !reflect.DeepEqual(classes, want) {
		t.Fatalf("classes = %+v, want %+v", classes, want)
	}
	if len(classes.Late) != census.Late || len(classes.Overdue) != census.Overdue || len(classes.Never) != census.Never {
		t.Fatalf("lists %d/%d/%d do not match the census %d/%d/%d",
			len(classes.Late), len(classes.Overdue), len(classes.Never), census.Late, census.Overdue, census.Never)
	}
}

// An index with nothing late, overdue or unevaluated names nothing: the three
// lists are empty, not missing, and still match the census.
func TestDueIndexNamesNothingWhenEverythingIsOnTime(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(10_000, 0)}
	dispatcher := dueIndexDispatcher(clock, metric.NewRecorder(metric.BuildInfo{}), 8, 8,
		map[execution.QueryGroupIdentity]walkRunner{"a": {}, "b": {}})
	index := dispatcher.dueIndex
	for _, queryGroup := range []execution.QueryGroupIdentity{"a", "b"} {
		index.Record(queryGroup, dispatcher.bundle.runners[queryGroup], index.versionEpoch,
			scheduler.RunnerDueBound{NotDueUntilUnix: 10_060, IntervalSeconds: 60}, clock.at)
	}
	census, classes := index.CensusWithDueClasses(clock.at, []string{"a", "b"})
	if census.Late != 0 || census.Overdue != 0 || census.Never != 0 {
		t.Fatalf("census = %+v, want nothing late", census)
	}
	if len(classes.Late)+len(classes.Overdue)+len(classes.Never) != 0 {
		t.Fatalf("classes = %+v, want none listed", classes)
	}
}

// The named census is over the objects the replica owns. An entry the index
// still holds for an object it lost -- until the next owned-set change drops
// it -- is in no list and no count, and the never count is the never list.
func TestDueIndexNamesOnlyTheObjectsItOwns(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(10_000, 0)}
	dispatcher := dueIndexDispatcher(clock, metric.NewRecorder(metric.BuildInfo{}), 8, 8,
		map[execution.QueryGroupIdentity]walkRunner{"kept-late": {}, "lost-late": {}, "lost-overdue": {}})
	index := dispatcher.dueIndex
	record := func(queryGroup execution.QueryGroupIdentity, dueAt, interval int64) {
		index.Record(queryGroup, dispatcher.bundle.runners[queryGroup], index.versionEpoch,
			scheduler.RunnerDueBound{NotDueUntilUnix: dueAt, IntervalSeconds: interval}, time.Unix(dueAt-1, 0))
	}
	record("kept-late", 9_990, 60)
	record("lost-late", 9_990, 60)
	record("lost-overdue", 9_000, 60)

	census, classes := index.CensusWithDueClasses(clock.at, []string{"kept-late", "never-1"})
	want := fleet.DueClasses{Late: []string{"kept-late"}, Never: []string{"never-1"}}
	if !reflect.DeepEqual(classes, want) {
		t.Fatalf("classes = %+v, want only the owned objects %+v", classes, want)
	}
	if census.Late != 1 || census.Overdue != 0 || census.Never != len(classes.Never) {
		t.Fatalf("census late %d overdue %d never %d, want 1, 0 and the never list's %d",
			census.Late, census.Overdue, census.Never, len(classes.Never))
	}
}

// A replica that owns nothing still names its objects -- none -- rather than
// publishing counts from entries it no longer owns with no list beside them.
func TestDueIndexNamesNothingWhenItOwnsNothing(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(10_000, 0)}
	dispatcher := dueIndexDispatcher(clock, metric.NewRecorder(metric.BuildInfo{}), 8, 8,
		map[execution.QueryGroupIdentity]walkRunner{"lost": {}})
	index := dispatcher.dueIndex
	index.Record("lost", dispatcher.bundle.runners["lost"], index.versionEpoch,
		scheduler.RunnerDueBound{NotDueUntilUnix: 9_000, IntervalSeconds: 60}, time.Unix(8_999, 0))

	census, classes := index.CensusWithDueClasses(clock.at, []string{})
	if census.Late+census.Overdue+census.Never != 0 || len(classes.Late)+len(classes.Overdue)+len(classes.Never) != 0 {
		t.Fatalf("census %+v classes %+v, want nothing counted or named for an empty owned set", census, classes)
	}
	publisher := fleetPublisher{tracker: fleet.NewTracker(nil, "replica-1", clock.now), replica: "replica-1", now: clock.now,
		owned: func() []execution.QueryGroupIdentity { return nil }, schedule: index}
	snapshot := publisher.snapshot(context.Background())
	if snapshot.Due == nil {
		t.Fatal("a replica owning nothing published a census with no named due classes")
	}
	if snapshot.Schedule == nil || snapshot.Schedule.Late+snapshot.Schedule.Overdue != 0 {
		t.Fatalf("schedule = %+v, want no late or overdue for an empty owned set", snapshot.Schedule)
	}
}
