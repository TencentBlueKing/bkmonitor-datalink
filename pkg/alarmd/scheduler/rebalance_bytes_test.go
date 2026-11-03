// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

func byteWorker(id string, pool uint64, live time.Time) ownership.WorkerRegistration {
	worker := rebalanceWorker(id, ownership.WorkerReady, live, "shadow")
	if pool > 0 {
		worker.Load = &ownership.WorkerLoad{RetainedPoolBytes: pool}
	}
	return worker
}

func applyByteMoves(owners map[execution.QueryGroupIdentity]string, plan BytePlan) map[execution.QueryGroupIdentity]string {
	after := make(map[execution.QueryGroupIdentity]string, len(owners))
	for queryGroup, owner := range owners {
		after[queryGroup] = owner
	}
	for _, move := range plan.Moves {
		after[move.QueryGroup] = move.To
	}
	return after
}

func peakSums(owners map[execution.QueryGroupIdentity]string, readings ByteReadings) map[string]uint64 {
	sums := map[string]uint64{}
	for queryGroup, owner := range owners {
		sums[owner] += readings.Peak[queryGroup]
	}
	return sums
}

// The incident shape (decision-020 section 5.7): three Query Groups of one
// strategy, each peaking at 35% of the pool, all on one replica. Each one
// is inside its own share; together they are 105% of the pool. The byte
// constraint moves the largest to the Worker with the most headroom, one
// per round, and stops once every Worker is within the share - it is not
// a balance and does not go on to even the bytes out.
func TestRouterPlanByteMovesRelievesAReplicaWhosePeaksSumPastItsPool(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	live := now.Add(time.Minute)
	const pool = 1000
	workers := []ownership.WorkerRegistration{byteWorker("a", pool, live), byteWorker("b", pool, live), byteWorker("c", pool, live)}
	owners := map[execution.QueryGroupIdentity]string{"qg-1": "a", "qg-2": "a", "qg-3": "a"}
	readings := ByteReadings{Pool: map[string]uint64{"a": pool, "b": pool, "c": pool},
		Peak: map[execution.QueryGroupIdentity]uint64{"qg-1": 350, "qg-2": 350, "qg-3": 350}}
	router := NewRouter(nil)

	first := router.PlanByteMoves(owners, workers, readings, now)
	if first.Judged != 3 || len(first.PoolUnknown) != 0 || first.Unread != 0 ||
		!reflect.DeepEqual(first.Overloaded, []string{"a"}) || len(first.Unplaceable) != 0 {
		t.Fatalf("first plan = %+v, want a alone overloaded over three judged workers", first)
	}
	if !reflect.DeepEqual(first.Moves, []ByteMove{{QueryGroup: "qg-1", From: "a", To: "b", Bytes: 350}}) {
		t.Fatalf("first moves = %+v, want the first of the equal peaks to the first of the equal headrooms", first.Moves)
	}
	if first.Sum["a"] != 1050 || first.Sum["b"] != 0 {
		t.Fatalf("sums before the moves = %v", first.Sum)
	}
	after := applyByteMoves(owners, first)
	for workerID, sum := range peakSums(after, readings) {
		if sum > byteLimit(pool) {
			t.Fatalf("after one round %s holds %d, past the share %d", workerID, sum, byteLimit(pool))
		}
	}
	if after["qg-1"] == after["qg-2"] && after["qg-2"] == after["qg-3"] {
		t.Fatal("the three Query Groups are still on one replica after a round")
	}
	// Within the share now: 70% on a, 35% on b. Nothing more moves - a
	// balance would go on, the constraint does not.
	second := router.PlanByteMoves(after, workers, readings, now)
	if len(second.Moves) != 0 || len(second.Overloaded) != 0 {
		t.Fatalf("second plan = %+v, want nothing to move once every worker is within the share", second)
	}
	// The share is 80% of the pool, not the pool: two at 45% are 90%,
	// inside the pool and over the share, and one of them moves.
	over := map[execution.QueryGroupIdentity]string{"qg-1": "a", "qg-2": "a"}
	overReadings := ByteReadings{Pool: readings.Pool, Peak: map[execution.QueryGroupIdentity]uint64{"qg-1": 450, "qg-2": 450}}
	if plan := router.PlanByteMoves(over, workers, overReadings, now); len(plan.Moves) != 1 || !reflect.DeepEqual(plan.Overloaded, []string{"a"}) {
		t.Fatalf("plan at 90%% of the pool = %+v, want a over the share and one move", plan)
	}
}

// What is not known is not judged, and is said: a Worker that registered
// no pool is neither judged for overload nor chosen as a destination, and
// a Query Group with no reported peak counts nothing toward its Worker's
// sum and is counted as unread. Neither reads as "no pressure".
func TestRouterPlanByteMovesJudgesOnlyWhatIsKnownAndSaysWhatIsNot(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	live := now.Add(time.Minute)
	workers := []ownership.WorkerRegistration{byteWorker("a", 1000, live), byteWorker("b", 0, live), byteWorker("c", 1000, live)}
	owners := map[execution.QueryGroupIdentity]string{"qg-1": "a", "qg-2": "a", "qg-unread": "a", "qg-on-b": "b"}
	readings := ByteReadings{Pool: map[string]uint64{"a": 1000, "c": 1000},
		Peak: map[execution.QueryGroupIdentity]uint64{"qg-1": 500, "qg-2": 400, "qg-on-b": 900}}
	plan := NewRouter(nil).PlanByteMoves(owners, workers, readings, now)
	if plan.Judged != 2 || !reflect.DeepEqual(plan.PoolUnknown, []string{"b"}) || plan.Unread != 1 {
		t.Fatalf("plan = %+v, want b unjudged and one unread Query Group", plan)
	}
	if !reflect.DeepEqual(plan.Moves, []ByteMove{{QueryGroup: "qg-1", From: "a", To: "c", Bytes: 500}}) {
		t.Fatalf("moves = %+v, want the largest to c, never to the unjudged b", plan.Moves)
	}
	// The unjudged worker's own 900 is nowhere in the sums.
	if _, judged := plan.Sum["b"]; judged {
		t.Fatalf("sums = %v, want no sum for the unjudged worker", plan.Sum)
	}
}

// A Worker holding a Query Group with no reading has a sum that is a lower
// bound: it can be judged overloaded on its known part, and as a
// destination its unread Query Group is counted at the ninetieth percentile
// of the read peaks - 500 here - rather than at nothing. Two overloaded
// Workers and one that looks empty because nothing on it has reported:
// counted at the estimate it has no room for either, nothing moves to it,
// and the round says which Workers were unsettled.
func TestRouterPlanByteMovesDoesNotLandOnAWorkerWithUnreadQueryGroups(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	live := now.Add(time.Minute)
	workers := []ownership.WorkerRegistration{byteWorker("a", 1000, live), byteWorker("b", 1000, live), byteWorker("c", 1000, live)}
	owners := map[execution.QueryGroupIdentity]string{"a-1": "a", "a-2": "a", "b-1": "b", "b-2": "b", "c-unread": "c"}
	readings := ByteReadings{Pool: map[string]uint64{"a": 1000, "b": 1000, "c": 1000},
		Peak: map[execution.QueryGroupIdentity]uint64{"a-1": 500, "a-2": 400, "b-1": 500, "b-2": 400}}
	plan := NewRouter(nil).PlanByteMoves(owners, workers, readings, now)
	if !reflect.DeepEqual(plan.Overloaded, []string{"a", "b"}) || !reflect.DeepEqual(plan.Unsettled, []string{"c"}) || plan.Unread != 1 {
		t.Fatalf("plan = %+v, want a and b overloaded and c unsettled", plan)
	}
	for _, move := range plan.Moves {
		if move.To == "c" {
			t.Fatalf("moves = %+v: a Query Group landed on the unsettled worker, whose room is unknown", plan.Moves)
		}
	}
	// Neither a nor b has room for the other's, so both are unplaceable
	// this round rather than one of them filling c.
	if len(plan.Moves) != 0 || !reflect.DeepEqual(plan.Unplaceable, []string{"a", "b"}) {
		t.Fatalf("plan = %+v, want no move and both named unplaceable", plan)
	}
	// Once c's Query Group reports, c is a destination like any other.
	readings.Peak["c-unread"] = 100
	settled := NewRouter(nil).PlanByteMoves(owners, workers, readings, now)
	if len(settled.Unsettled) != 0 || len(settled.Moves) != 2 || settled.Moves[0].To != "c" || settled.Moves[1].To != "a" {
		t.Fatalf("plan once c reported = %+v, want a's 500 on c and b's 400 in the room a has left", settled)
	}
	// An overloaded Worker's own unread Query Group does not keep it from
	// being judged: its known part is already past the share.
	owners["a-unread"] = "a"
	judged := NewRouter(nil).PlanByteMoves(owners, workers, readings, now)
	if !reflect.DeepEqual(judged.Overloaded, []string{"a", "b"}) || !reflect.DeepEqual(judged.Unsettled, []string{"a"}) {
		t.Fatalf("plan with a's own unread = %+v, want a still judged overloaded and named unsettled", judged)
	}
}

// Largest first, first that fits: a Query Group too large for any other
// Worker does not leave its Worker stuck behind it; the round moves the
// next largest that fits somewhere. A Worker none of whose Query Groups
// fit anywhere is reported unplaceable, and the round moves nothing for it.
func TestRouterPlanByteMovesTakesTheLargestThatFitsAndNamesTheStuck(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	live := now.Add(time.Minute)
	workers := []ownership.WorkerRegistration{byteWorker("a", 1000, live), byteWorker("b", 1000, live)}
	owners := map[execution.QueryGroupIdentity]string{"qg-huge": "a", "qg-mid": "a", "qg-small": "a"}
	readings := ByteReadings{Pool: map[string]uint64{"a": 1000, "b": 1000},
		Peak: map[execution.QueryGroupIdentity]uint64{"qg-huge": 900, "qg-mid": 300, "qg-small": 100}}
	plan := NewRouter(nil).PlanByteMoves(owners, workers, readings, now)
	if !reflect.DeepEqual(plan.Moves, []ByteMove{{QueryGroup: "qg-mid", From: "a", To: "b", Bytes: 300}}) || len(plan.Unplaceable) != 0 {
		t.Fatalf("plan = %+v, want the largest that fits to move past the one that fits nowhere", plan)
	}
	// b already near its share: nothing of a's fits, and a is named stuck.
	owners["qg-on-b"] = "b"
	readings.Peak["qg-on-b"] = 750
	stuck := NewRouter(nil).PlanByteMoves(owners, workers, readings, now)
	if len(stuck.Moves) != 0 || !reflect.DeepEqual(stuck.Overloaded, []string{"a"}) || !reflect.DeepEqual(stuck.Unplaceable, []string{"a"}) {
		t.Fatalf("plan = %+v, want a overloaded and unplaceable with no move", stuck)
	}
}

// Two overloaded Workers in one round do not both fill the same
// destination: headroom is kept as the round moves, so the second move
// lands where room is left, or nowhere.
func TestRouterPlanByteMovesKeepsHeadroomAcrossTheRoundsMoves(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	live := now.Add(time.Minute)
	workers := []ownership.WorkerRegistration{byteWorker("a", 1000, live), byteWorker("b", 1000, live), byteWorker("c", 1000, live)}
	owners := map[execution.QueryGroupIdentity]string{"a-1": "a", "a-2": "a", "b-1": "b", "b-2": "b"}
	readings := ByteReadings{Pool: map[string]uint64{"a": 1000, "b": 1000, "c": 1000},
		Peak: map[execution.QueryGroupIdentity]uint64{"a-1": 500, "a-2": 400, "b-1": 500, "b-2": 450}}
	plan := NewRouter(nil).PlanByteMoves(owners, workers, readings, now)
	// a's 500 lands on c, leaving c 300 and a 400 of room. Neither of b's
	// fits either: b is unplaceable this round rather than c or a filled
	// past its share.
	if !reflect.DeepEqual(plan.Moves, []ByteMove{{QueryGroup: "a-1", From: "a", To: "c", Bytes: 500}}) ||
		!reflect.DeepEqual(plan.Overloaded, []string{"a", "b"}) || !reflect.DeepEqual(plan.Unplaceable, []string{"b"}) {
		t.Fatalf("plan = %+v, want a's move to c and b left unplaceable rather than a destination filled past its share", plan)
	}
	// With 350 instead of 450, b's smaller one fits the room a has left
	// after its own move - a, with 400 left, over c with 300.
	readings.Peak["b-2"] = 350
	plan = NewRouter(nil).PlanByteMoves(owners, workers, readings, now)
	if len(plan.Moves) != 2 || plan.Moves[1] != (ByteMove{QueryGroup: "b-2", From: "b", To: "a", Bytes: 350}) || len(plan.Unplaceable) != 0 {
		t.Fatalf("plan = %+v, want b's 350 to land in the room a has left after its own move", plan)
	}
}

// A destination the Query Group is not eligible for is not chosen even
// with the most headroom.
func TestRouterPlanByteMovesRespectsEligibility(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	live := now.Add(time.Minute)
	eligibility, err := NewStaticWorkerEligibility(ownership.WorkerCompatibility{DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities"})
	if err != nil {
		t.Fatal(err)
	}
	other := byteWorker("other-profile", 1000, live)
	other.DeploymentProfile = "canary"
	workers := []ownership.WorkerRegistration{byteWorker("a", 1000, live), other, byteWorker("z-same", 1000, live)}
	owners := map[execution.QueryGroupIdentity]string{"qg-1": "a", "qg-2": "a"}
	readings := ByteReadings{Pool: map[string]uint64{"a": 1000, "other-profile": 1000, "z-same": 1000},
		Peak: map[execution.QueryGroupIdentity]uint64{"qg-1": 500, "qg-2": 400}}
	plan := NewRouter(eligibility).PlanByteMoves(owners, workers, readings, now)
	if len(plan.Moves) != 1 || plan.Moves[0].To != "z-same" {
		t.Fatalf("plan = %+v, want the move to the eligible worker", plan)
	}
}

// The count correction that follows the byte moves plans under the same
// readings: it does not move a Query Group to a destination its peak would
// take past the share. That is also what keeps it from taking back what
// the round just moved for bytes - here the correction reaches, first by
// identity, for the big one the byte move just took off a, and a is the
// least owned; without the fit check it would go straight back, and every
// round would move it twice.
func TestRouterPlanRebalanceWithBytesNeitherUndoesAByteMoveNorOverfillsADestination(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	live := now.Add(time.Minute)
	workers := []ownership.WorkerRegistration{byteWorker("a", 1000, live), byteWorker("b", 2000, live), byteWorker("c", 100, live)}
	owners := map[execution.QueryGroupIdentity]string{"a-big": "a"}
	readings := ByteReadings{Pool: map[string]uint64{"a": 1000, "b": 2000, "c": 100}, Peak: map[execution.QueryGroupIdentity]uint64{"a-big": 900}}
	for index := 0; index < 10; index++ {
		queryGroup := execution.QueryGroupIdentity("b-small-" + string(rune('0'+index)))
		owners[queryGroup] = "b"
		readings.Peak[queryGroup] = 10
	}
	router := NewRouter(nil)
	bytePlan := router.PlanByteMoves(owners, workers, readings, now)
	if !reflect.DeepEqual(bytePlan.Moves, []ByteMove{{QueryGroup: "a-big", From: "a", To: "b", Bytes: 900}}) {
		t.Fatalf("byte plan = %+v, want the big one to b, the only worker with room", bytePlan)
	}
	after := applyByteMoves(owners, bytePlan)

	// Counts after the byte move: b 11, a 0, c 0. The correction moves one
	// from b to a, and the first by identity is a-big - which does not fit
	// a, so a small one goes instead.
	blind := router.PlanRebalanceWithBytes(after, workers, ByteReadings{}, now)
	if len(blind.Moves) != 1 || blind.Moves[0].QueryGroup != "a-big" || blind.Moves[0].To != "a" {
		t.Fatalf("plan without readings = %+v: the case needs the count correction to reach for the byte-moved Query Group", blind.Moves)
	}
	plan := router.PlanRebalanceWithBytes(after, workers, readings, now)
	if len(plan.Moves) != 1 || plan.Moves[0].QueryGroup != "b-small-0" || plan.Moves[0].To != "a" {
		t.Fatalf("plan = %+v, want a small one to a and the byte-moved one left where it landed", plan.Moves)
	}

	// A destination without room for the first candidate: c's pool is 100,
	// share 80; a's big one does not fit it and is skipped for the first
	// small one that does.
	crowded := map[execution.QueryGroupIdentity]string{"a-big": "a", "a-s1": "a", "a-s2": "a", "a-s3": "a"}
	crowdedReadings := ByteReadings{Pool: readings.Pool, Peak: map[execution.QueryGroupIdentity]uint64{"a-big": 900, "a-s1": 10, "a-s2": 10, "a-s3": 10}}
	only := []ownership.WorkerRegistration{byteWorker("a", 1000, live), byteWorker("c", 100, live)}
	fitted := router.PlanRebalanceWithBytes(crowded, only, crowdedReadings, now)
	if len(fitted.Moves) != 1 || fitted.Moves[0].QueryGroup != "a-s1" {
		t.Fatalf("plan = %+v, want the first that fits c's share, not the big one first by identity", fitted.Moves)
	}
}

// A Worker holding a Query Group that has not run since the restart is a
// destination when its read sum plus that Query Group at the round's
// ninetieth-percentile peak leaves room. It was refused outright, so after a
// rolling restart every Worker held one of the hourly-to-sixty-hour Query
// Groups, none was a destination for the longest cadence, and an overloaded
// Worker was never relieved.
func TestRouterPlanByteMovesCountsAnUnreadQueryGroupAtTheReadNinetiethPercentile(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	live := now.Add(time.Minute)
	workers := []ownership.WorkerRegistration{byteWorker("a", 1000, live), byteWorker("c", 1000, live)}
	owners := map[execution.QueryGroupIdentity]string{"a-1": "a", "a-2": "a", "c-unread": "c"}
	peaks := map[execution.QueryGroupIdentity]uint64{"a-1": 500, "a-2": 400}
	for index := 0; index < 20; index++ {
		queryGroup := execution.QueryGroupIdentity(fmt.Sprintf("c-%02d", index))
		owners[queryGroup], peaks[queryGroup] = "c", 10
	}
	readings := ByteReadings{Pool: map[string]uint64{"a": 1000, "c": 1000}, Peak: peaks}
	plan := NewRouter(nil).PlanByteMoves(owners, workers, readings, now)
	// 22 read peaks, twenty of them 10: the ninetieth percentile by nearest
	// rank is the twentieth, 10. c counts 200 + 10, and a's 500 fits in 800.
	if plan.UnreadEstimate != 10 || !reflect.DeepEqual(plan.Unsettled, []string{"c"}) || plan.Unread != 1 {
		t.Fatalf("plan = %+v, want the unread Query Group on c counted at 10 and c named unsettled", plan)
	}
	want := []ByteMove{{QueryGroup: "a-1", From: "a", To: "c", Bytes: 500}}
	if !reflect.DeepEqual(plan.Moves, want) {
		t.Fatalf("moves = %+v, want a's largest on c", plan.Moves)
	}
}

// The estimate is a boundary: the destination whose read sum, unread
// Query Groups at the estimate and the moved peak come to exactly its share
// takes the move, and one byte more does not.
func TestRouterPlanByteMovesUnreadEstimateBoundary(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	live := now.Add(time.Minute)
	for _, tc := range []struct {
		name  string
		extra uint64
		moved execution.QueryGroupIdentity
	}{{"exactly the share", 10, "a-1"}, {"a byte past it", 11, "a-2"}} {
		workers := []ownership.WorkerRegistration{byteWorker("a", 1000, live), byteWorker("c", 1000, live)}
		owners := map[execution.QueryGroupIdentity]string{"a-1": "a", "a-2": "a", "c-unread": "c", "c-last": "c"}
		peaks := map[execution.QueryGroupIdentity]uint64{"a-1": 500, "a-2": 400, "c-last": tc.extra}
		for index := 0; index < 28; index++ {
			queryGroup := execution.QueryGroupIdentity(fmt.Sprintf("c-%02d", index))
			owners[queryGroup], peaks[queryGroup] = "c", 10
		}
		plan := NewRouter(nil).PlanByteMoves(owners, workers, ByteReadings{Pool: map[string]uint64{"a": 1000, "c": 1000}, Peak: peaks}, now)
		// 31 read peaks, at least 29 of them 10: the twenty-eighth is 10.
		// c counts 280 + extra + 10 against a share of 800: a's 500 fits at
		// exactly 800, and one byte more leaves only a's 400 fitting.
		if plan.UnreadEstimate != 10 {
			t.Fatalf("%s: estimate = %d, want 10", tc.name, plan.UnreadEstimate)
		}
		if len(plan.Moves) != 1 || plan.Moves[0].QueryGroup != tc.moved || plan.Moves[0].To != "c" {
			t.Fatalf("%s: moves = %+v, want %s on c", tc.name, plan.Moves, tc.moved)
		}
	}
}

func TestUnreadEstimateIsTheNearestRankNinetiethPercentile(t *testing.T) {
	if got := unreadEstimate(nil); got != 0 {
		t.Fatalf("no peak read: estimate = %d, want 0", got)
	}
	if got := unreadEstimate([]uint64{7}); got != 7 {
		t.Fatalf("one peak: estimate = %d, want it", got)
	}
	peaks := make([]uint64, 0, 10)
	for value := uint64(10); value >= 1; value-- {
		peaks = append(peaks, value)
	}
	if got := unreadEstimate(peaks); got != 9 {
		t.Fatalf("1..10: estimate = %d, want the ninth, 9", got)
	}
	if peaks[0] != 10 {
		t.Fatal("the estimate sorted the caller's slice")
	}
}

// The round names what keeps each judged Worker unsettled: how many of its
// Query Groups have no peak read, the first few of them by id, and the read
// peaks the estimate came from. The total alone said every Worker was
// unsettled and not which Query Groups kept it so. A Worker that is not
// judged has no unread count: its Query Groups are in no sum.
func TestRouterPlanByteMovesNamesEachWorkersUnreadQueryGroups(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	live := now.Add(time.Minute)
	workers := []ownership.WorkerRegistration{byteWorker("a", 1<<20, live), byteWorker("b", 1<<20, live), byteWorker("c", 0, live)}
	owners := map[execution.QueryGroupIdentity]string{"b-unread": "b", "c-unread": "c"}
	peaks := map[execution.QueryGroupIdentity]uint64{}
	for index := 6; index >= 0; index-- {
		owners[execution.QueryGroupIdentity(fmt.Sprintf("a-unread-%d", index))] = "a"
	}
	// 200 read peaks, 1 to 200 bytes, half on each judged Worker.
	for value := uint64(1); value <= 200; value++ {
		queryGroup, owner := execution.QueryGroupIdentity(fmt.Sprintf("read-%03d", value)), "a"
		if value > 100 {
			owner = "b"
		}
		owners[queryGroup], peaks[queryGroup] = owner, value
	}
	plan := NewRouter(nil).PlanByteMoves(owners, workers, ByteReadings{Pool: map[string]uint64{"a": 1 << 20, "b": 1 << 20}, Peak: peaks}, now)
	if plan.Unread != 8 || !reflect.DeepEqual(plan.UnreadBy, map[string]int{"a": 7, "b": 1}) {
		t.Fatalf("unread = %d by %v, want 7 on a and 1 on b, none for the unjudged c", plan.Unread, plan.UnreadBy)
	}
	wantSample := map[string][]execution.QueryGroupIdentity{
		"a": {"a-unread-0", "a-unread-1", "a-unread-2", "a-unread-3", "a-unread-4"},
		"b": {"b-unread"},
	}
	if !reflect.DeepEqual(plan.UnreadSample, wantSample) {
		t.Fatalf("unread sample = %v, want each Worker's lowest five by id", plan.UnreadSample)
	}
	// Nearest rank over 1..200: the hundredth, the hundred-and-eightieth,
	// the hundred-and-ninety-eighth, and the largest.
	if want := (PeakDistribution{Count: 200, P50: 100, P90: 180, P99: 198, Max: 200}); plan.ReadPeaks != want {
		t.Fatalf("read peaks = %+v, want %+v", plan.ReadPeaks, want)
	}
	if plan.ReadPeaks.P90 != plan.UnreadEstimate {
		t.Fatalf("read peaks p90 = %d, estimate = %d: the distribution is not the one the estimate came from", plan.ReadPeaks.P90, plan.UnreadEstimate)
	}
}

func TestPeakDistributionIsNearestRankAndLeavesTheCallersSlice(t *testing.T) {
	if got := peakDistribution(nil); got != (PeakDistribution{}) {
		t.Fatalf("no peak read: distribution = %+v, want zero", got)
	}
	peaks := []uint64{4, 1, 3, 2}
	if got, want := peakDistribution(peaks), (PeakDistribution{Count: 4, P50: 2, P90: 4, P99: 4, Max: 4}); got != want {
		t.Fatalf("distribution = %+v, want %+v", got, want)
	}
	if peaks[0] != 4 {
		t.Fatal("the distribution sorted the caller's slice")
	}
}
