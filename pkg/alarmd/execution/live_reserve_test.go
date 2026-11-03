// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"context"
	"testing"
	"time"
)

func withLiveReserve(reserve time.Duration) *LiveReserve {
	live := &LiveReserve{}
	live.Set(reserve)
	return live
}

// consumer: deadline at 60 s, frozen reserve 5 s, so the frozen query deadline is 55 s.
var liveReserveConsumer = DataRequirementConsumer{ConsumerDeadlineUnixMilli: 60_000, DownstreamExecutionReserveMilliSec: 5_000}

const liveReserveGroup QueryGroupIdentity = "light"

// A heavy group at 0.5% of the Slots: its 3 s tails are under the pooled p99 of 100 ms tails, and its query still
// runs under its own longest tail.
func TestAHeavyGroupsQueryRunsUnderItsOwnLongestTail(t *testing.T) {
	live := withLiveReserve(128 * time.Millisecond) // the pooled p99 of 100 ms tails, at its bucket's upper edge
	at := time.UnixMilli(0)
	for i := 0; i < 199; i++ {
		live.RecordGroupTail(liveReserveGroup, at, 100*time.Millisecond)
	}
	live.RecordGroupTail("heavy", at, 3*time.Second)
	deadline, fellBack := live.QueryDeadlineUnixMilli(liveReserveConsumer, "heavy", 10_000)
	if fellBack || liveReserveConsumer.ConsumerDeadlineUnixMilli-deadline < 3_000 {
		t.Fatalf("heavy group deadline %d (%d ms before its consumer deadline), fellBack=%v; want at least 3000 ms", deadline,
			liveReserveConsumer.ConsumerDeadlineUnixMilli-deadline, fellBack)
	}
	if deadline, _ := live.QueryDeadlineUnixMilli(liveReserveConsumer, liveReserveGroup, 10_000); deadline != 60_000-128 {
		t.Fatalf("light group deadline %d, want the pooled reserve's 59872", deadline)
	}
	if budget, _ := live.QueryBudgetMilli(liveReserveConsumer, "heavy", 60_000); budget != 57_000 {
		t.Fatalf("heavy group recovery budget %d, want 57000", budget)
	}
	// a group's own tail never takes it off the frozen reserve before the pooled one is measured
	none := &LiveReserve{}
	none.RecordGroupTail("heavy", at, 100*time.Millisecond)
	if deadline, _ := none.QueryDeadlineUnixMilli(liveReserveConsumer, "heavy", 10_000); deadline != 55_000 {
		t.Fatalf("before a pooled tail: %d, want the frozen 55000", deadline)
	}
}

// A group's tails are its last LiveReserveGroupTailsKept, and a group not seen for a day is dropped.
func TestGroupTailsAreRecentAndAgeOut(t *testing.T) {
	live := &LiveReserve{}
	at := time.UnixMilli(0)
	live.RecordGroupTail("g", at, 3*time.Second)
	for i := 0; i < LiveReserveGroupTailsKept; i++ {
		live.RecordGroupTail("g", at, 200*time.Millisecond)
	}
	if got := live.GroupTail("g"); got != 200*time.Millisecond {
		t.Fatalf("after %d newer tails: %v, want 200ms", LiveReserveGroupTailsKept, got)
	}
	live.RecordGroupTail("other", at.Add(LiveReserveGroupTailAge+time.Second), time.Second)
	if got := live.GroupTail("g"); got != 0 {
		t.Fatalf("a day after its last tail: %v, want dropped", got)
	}
	// the sweep runs at most once a minute: a group aged out within a minute of the last sweep is dropped by the next
	swept := at.Add(LiveReserveGroupTailAge + time.Second)
	live.RecordGroupTail("late", swept.Add(-LiveReserveGroupTailAge-time.Second), time.Second)
	live.RecordGroupTail("other", swept.Add(30*time.Second), time.Second)
	if live.GroupTail("late") == 0 {
		t.Fatal("swept again within a minute of the last sweep")
	}
	live.RecordGroupTail("other", swept.Add(liveReservePruneEvery), time.Second)
	if live.GroupTail("late") != 0 {
		t.Fatal("not swept a minute after the last sweep")
	}
	var nilLive *LiveReserve
	nilLive.RecordGroupTail("g", at, time.Second)
	nilLive.CountOverrun()
	if nilLive.GroupTail("g") != 0 || nilLive.Overruns() != 0 {
		t.Fatal("nil live reserve")
	}
}

func TestStageMarkerKeepsTheEarliestCompletionDeadline(t *testing.T) {
	marker := &StageMarker{}
	marker.Begin("execute")
	marker.LowerCompletionDeadline(time.UnixMilli(60_000))
	marker.LowerCompletionDeadline(time.UnixMilli(70_000))
	marker.LowerCompletionDeadline(time.UnixMilli(50_000))
	if got := marker.CompletionDeadline(); !got.Equal(time.UnixMilli(50_000)) {
		t.Fatalf("got %v, want the earliest", got)
	}
	marker.Begin("execute")
	if !marker.CompletionDeadline().IsZero() {
		t.Fatal("a new execution starts with none")
	}
}

func TestQueryDeadlineUsesTheFrozenReserveUntilATailIsMeasured(t *testing.T) {
	live := withLiveReserve(0)
	deadline, fellBack := live.QueryDeadlineUnixMilli(liveReserveConsumer, liveReserveGroup, 10_000)
	if deadline != 55_000 || fellBack {
		t.Fatalf("got %d fellBack=%v, want the frozen 55000", deadline, fellBack)
	}
}

func TestQueryDeadlineFollowsTheMeasuredTail(t *testing.T) {
	// a tail shorter than today's fixed reserve gives the query more time ...
	live := withLiveReserve(2 * time.Second)
	if deadline, fellBack := live.QueryDeadlineUnixMilli(liveReserveConsumer, liveReserveGroup, 10_000); deadline != 58_000 || fellBack {
		t.Fatalf("short tail: got %d fellBack=%v, want 58000", deadline, fellBack)
	}
	// ... and a longer one keeps more of the period for what follows the query
	live.Set(12 * time.Second)
	if deadline, fellBack := live.QueryDeadlineUnixMilli(liveReserveConsumer, liveReserveGroup, 10_000); deadline != 48_000 || fellBack {
		t.Fatalf("long tail: got %d fellBack=%v, want 48000", deadline, fellBack)
	}
}

func TestQueryDeadlineFallsBackWhenTheLiveReserveLeavesNoTime(t *testing.T) {
	// a 55 s tail would put the deadline at 5 s, before the query can start at 10 s
	live := withLiveReserve(55 * time.Second)
	deadline, fellBack := live.QueryDeadlineUnixMilli(liveReserveConsumer, liveReserveGroup, 10_000)
	if deadline != 55_000 || !fellBack {
		t.Fatalf("got %d fellBack=%v, want the frozen 55000 and a fallback", deadline, fellBack)
	}
	// exactly at the start is no time either
	live.Set(50 * time.Second)
	if _, fellBack := live.QueryDeadlineUnixMilli(liveReserveConsumer, liveReserveGroup, 10_000); !fellBack {
		t.Fatal("a deadline equal to the start must fall back")
	}
}

func TestQueryBudgetFollowsTheTailAndFallsBack(t *testing.T) {
	live := withLiveReserve(0)
	if budget, fellBack := live.QueryBudgetMilli(liveReserveConsumer, liveReserveGroup, 60_000); budget != 55_000 || fellBack {
		t.Fatalf("no tail: got %d fellBack=%v", budget, fellBack)
	}
	live.Set(3 * time.Second)
	if budget, fellBack := live.QueryBudgetMilli(liveReserveConsumer, liveReserveGroup, 60_000); budget != 57_000 || fellBack {
		t.Fatalf("measured tail: got %d fellBack=%v", budget, fellBack)
	}
	live.Set(70 * time.Second)
	if budget, fellBack := live.QueryBudgetMilli(liveReserveConsumer, liveReserveGroup, 60_000); budget != 55_000 || !fellBack {
		t.Fatalf("tail past the interval: got %d fellBack=%v, want the frozen budget", budget, fellBack)
	}
}

// Each runtime owns its reserve: one runtime's tail never reaches another's deadlines, and a runtime with none (nil)
// runs every query under the frozen reserve.
func TestLiveReservesAreIndependentAndNilIsFrozen(t *testing.T) {
	slow, fast := withLiveReserve(20*time.Second), withLiveReserve(time.Second)
	if deadline, _ := slow.QueryDeadlineUnixMilli(liveReserveConsumer, liveReserveGroup, 10_000); deadline != 40_000 {
		t.Fatalf("slow runtime: %d", deadline)
	}
	if deadline, _ := fast.QueryDeadlineUnixMilli(liveReserveConsumer, liveReserveGroup, 10_000); deadline != 59_000 {
		t.Fatalf("fast runtime: %d", deadline)
	}
	var none *LiveReserve
	if deadline, fellBack := none.QueryDeadlineUnixMilli(liveReserveConsumer, liveReserveGroup, 10_000); deadline != 55_000 || fellBack {
		t.Fatalf("no reserve: %d fellBack=%v", deadline, fellBack)
	}
	none.Set(time.Second)
	none.CountFallback("qg", time.Now())
	if none.Reserve() != 0 || none.Fallbacks() != 0 {
		t.Fatal("a nil reserve holds nothing")
	}
}

func TestStageMarkerQueryReturnedIsPerExecution(t *testing.T) {
	marker := &StageMarker{}
	marker.Begin(SlotStageSource)
	ctx := WithStageMarker(context.Background(), marker)
	if !marker.QueryReturnedAt().IsZero() {
		t.Fatal("no query returned yet")
	}
	at := time.Unix(100, 0)
	MarkQueryReturned(ctx, at)
	if !marker.QueryReturnedAt().Equal(at) {
		t.Fatalf("got %v", marker.QueryReturnedAt())
	}
	marker.Begin(SlotStageSource)
	if !marker.QueryReturnedAt().IsZero() {
		t.Fatal("the next execution must not inherit the last one's query return")
	}
	MarkQueryReturned(context.Background(), at) // no marker: no effect, no panic
}

// A runtime names a group as tight for an hour after its last fallback, most recent first, at most
// LiveReserveTightGroupsKept of them; the count stays exact.
func TestTightQueryGroupsAgeOutAndAreBounded(t *testing.T) {
	live := &LiveReserve{}
	at := time.Unix(100_000, 0)
	live.CountFallback("old", at)
	live.CountFallback("new", at.Add(30*time.Minute))
	if got := live.TightQueryGroups(at.Add(30 * time.Minute)); len(got) != 2 || got[0] != "new" || got[1] != "old" {
		t.Fatalf("got %v, want new then old", got)
	}
	if got := live.TightQueryGroups(at.Add(61 * time.Minute)); len(got) != 1 || got[0] != "new" {
		t.Fatalf("an hour after its last fallback a group is no longer tight: %v", got)
	}
	for i := 0; i < LiveReserveTightGroupsKept+5; i++ {
		live.CountFallback(QueryGroupIdentity(time.Duration(i).String()), at.Add(40*time.Minute))
	}
	if got := live.TightQueryGroups(at.Add(40 * time.Minute)); len(got) != LiveReserveTightGroupsKept {
		t.Fatalf("listed %d, want the bound %d", len(got), LiveReserveTightGroupsKept)
	}
	if live.Fallbacks() != uint64(LiveReserveTightGroupsKept+7) {
		t.Fatalf("the count is exact: %d", live.Fallbacks())
	}
}
