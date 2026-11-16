// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// pinnedPlacement puts each Query Group it names on the one worker it names
// and leaves the rest alone: a test's placement, through the eligibility the
// Router asks everywhere it places and moves.
type pinnedPlacement map[execution.QueryGroupIdentity]string

func (pins pinnedPlacement) Eligible(queryGroup execution.QueryGroupIdentity, worker ownership.WorkerRegistration, _ time.Time) bool {
	pinned, ok := pins[queryGroup]
	return !ok || pinned == worker.WorkerID
}

// AllEligible leaves out what is nil, and with nothing left is nil - the
// Router's "no eligibility" - never one that admits every worker.
func TestAllEligibleLeavesOutWhatIsNil(t *testing.T) {
	static, err := NewStaticWorkerEligibility(ownership.WorkerCompatibility{DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities"})
	if err != nil {
		t.Fatal(err)
	}
	if got := AllEligible(static, nil); got != static {
		t.Fatalf("AllEligible(static, nil) = %#v, want static itself", got)
	}
	if got := AllEligible(nil, nil); got != nil {
		t.Fatalf("AllEligible(nil, nil) = %#v, want nil", got)
	}
	both := AllEligible(static, pinnedPlacement{"qg": "b"})
	now := time.Unix(1_700_000_000, 0)
	if both.Eligible("qg", rebalanceWorker("a", ownership.WorkerReady, now.Add(time.Minute), "shadow"), now) ||
		!both.Eligible("qg", rebalanceWorker("b", ownership.WorkerReady, now.Add(time.Minute), "shadow"), now) ||
		both.Eligible("qg", rebalanceWorker("b", ownership.WorkerReady, now.Add(time.Minute), "canary"), now) {
		t.Fatal("AllEligible(static, pins) is not eligible exactly where both are")
	}
}

// A Query Group pinned to a worker is placed there and stays: the count
// rebalance and the byte moves look for a destination it is eligible for,
// and the pin leaves none but the worker that holds it. Unpinned, the same
// shapes move - the pin is what holds them.
func TestAPinnedQueryGroupIsPlacedOnItsWorkerAndNeitherRebalanceMovesIt(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	live := now.Add(time.Minute)

	// Placed where it is pinned, whatever rendezvous would choose.
	workers := []ownership.WorkerRegistration{rebalanceWorker("a", ownership.WorkerReady, live, "shadow"), rebalanceWorker("b", ownership.WorkerReady, live, "shadow")}
	chosen, err := NewRouter(nil).Select("qg-pinned", workers, now)
	if err != nil {
		t.Fatal(err)
	}
	other := "a"
	if chosen.WorkerID == "a" {
		other = "b"
	}
	if placed, err := NewRouter(pinnedPlacement{"qg-pinned": other}).Select("qg-pinned", workers, now); err != nil || placed.WorkerID != other {
		t.Fatalf("Select() = (%s, %v), want the pinned worker %s over rendezvous's %s", placed.WorkerID, err, other, chosen.WorkerID)
	}

	// The count rebalance: twelve on a, none on b.
	owners := rebalanceOwners(map[string]int{"a": 12})
	pins := pinnedPlacement{}
	for queryGroup := range owners {
		pins[queryGroup] = "a"
	}
	if plan := NewRouter(nil).PlanRebalance(owners, workers, now); len(plan.Moves) == 0 {
		t.Fatalf("unpinned plan = %+v, want the skew corrected", plan)
	}
	if plan := NewRouter(pins).PlanRebalance(owners, workers, now); len(plan.Moves) != 0 {
		t.Fatalf("pinned plan = %+v, want nothing moved off the worker it is pinned to", plan)
	}

	// The byte moves: three at 35% of the pool on a, over its share.
	const pool = 1000
	byteWorkers := []ownership.WorkerRegistration{byteWorker("a", pool, live), byteWorker("b", pool, live)}
	byteOwners := map[execution.QueryGroupIdentity]string{"qg-1": "a", "qg-2": "a", "qg-3": "a"}
	readings := ByteReadings{Pool: map[string]uint64{"a": pool, "b": pool},
		Peak: map[execution.QueryGroupIdentity]uint64{"qg-1": 350, "qg-2": 350, "qg-3": 350}}
	if plan := NewRouter(nil).PlanByteMoves(byteOwners, byteWorkers, readings, now); len(plan.Moves) == 0 {
		t.Fatalf("unpinned byte plan = %+v, want a relieved", plan)
	}
	bytePins := pinnedPlacement{"qg-1": "a", "qg-2": "a", "qg-3": "a"}
	if plan := NewRouter(bytePins).PlanByteMoves(byteOwners, byteWorkers, readings, now); len(plan.Moves) != 0 {
		t.Fatalf("pinned byte plan = %+v, want nothing moved off the worker it is pinned to", plan)
	}
}
