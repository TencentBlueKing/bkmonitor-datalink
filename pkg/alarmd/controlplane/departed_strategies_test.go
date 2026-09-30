// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func planOf(tenant, business, strategy string, revision int64) FrozenPlan {
	return FrozenPlan{
		Identity: execution.PlanIdentity{TenantID: tenant, BusinessID: business, StrategyID: strategy},
		Plan:     contract.EvaluationPlanV2{StrategyRef: contract.StrategyRefV2{TenantID: tenant, StrategyID: strategy, SnapshotRevision: revision}},
	}
}

func groupsOf(plans ...FrozenPlan) []QueryGroup {
	return []QueryGroup{{Identity: execution.QueryGroupIdentity("qg"), Plans: plans}}
}

// The identity a close needs exists only while the strategy does. The
// moment it leaves the catalog is the last moment both facts - that it is
// gone, and who it was - are in hand together.
func TestAStrategyLeavingTheCatalogIsRememberedWithTheIdentityItHad(t *testing.T) {
	memory := newDepartedMemory()
	at := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	memory.record(groupsOf(planOf("system", "2", "10", 7), planOf("system", "2", "11", 9)),
		groupsOf(planOf("system", "2", "11", 9)), at)
	departed := memory.departed()
	if len(departed) != 1 || departed[0].StrategyID != "10" {
		t.Fatalf("the strategy that left was not remembered: %+v", departed)
	}
	if departed[0].BusinessID != 2 || departed[0].Revision != 7 || !departed[0].At.Equal(at) {
		t.Fatalf("the departure lost what only this moment knew: %+v", departed[0])
	}
}

// A strategy the catalog publishes again is not gone. Forgetting it here is
// what makes a strategy deleted, restored and deleted again remembered from
// its second departure rather than its first.
func TestAStrategyThatComesBackIsForgotten(t *testing.T) {
	memory := newDepartedMemory()
	at := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	memory.record(groupsOf(planOf("system", "2", "10", 7)), nil, at)
	if len(memory.departed()) != 1 {
		t.Fatal("the departure was not recorded")
	}
	memory.record(nil, groupsOf(planOf("system", "2", "10", 8)), at.Add(time.Minute))
	if len(memory.departed()) != 0 {
		t.Fatalf("a strategy the catalog publishes again is still remembered as gone: %+v", memory.departed())
	}
	memory.record(groupsOf(planOf("system", "2", "10", 8)), nil, at.Add(2*time.Minute))
	departed := memory.departed()
	if len(departed) != 1 || departed[0].Revision != 8 || !departed[0].At.Equal(at.Add(2*time.Minute)) {
		t.Fatalf("the second departure did not replace the first: %+v", departed)
	}
}

// A departure older than the retention is dropped. The memory is a window,
// not a second catalog.
func TestADepartureOlderThanTheRetentionIsDropped(t *testing.T) {
	memory := newDepartedMemory()
	at := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	memory.record(groupsOf(planOf("system", "2", "10", 7)), nil, at)
	memory.record(nil, nil, at.Add(DepartedStrategyRetention+time.Minute))
	if len(memory.departed()) != 0 {
		t.Fatalf("a departure outlived its retention: %+v", memory.departed())
	}
}

// The bound is a reading, not a silence: a departure the memory would not
// hold can never be closed, so how many were refused has to be answerable.
func TestDeparturesRefusedByTheBoundAreCounted(t *testing.T) {
	memory := newDepartedMemory()
	memory.entries = make(map[string]DepartedStrategy, MaxDepartedStrategies)
	for i := 0; i < MaxDepartedStrategies; i++ {
		memory.entries[departedKey("system", "filler-"+string(rune('a'+i%26))+itoaTest(i))] = DepartedStrategy{At: time.Now()}
	}
	memory.record(groupsOf(planOf("system", "2", "10", 7)), nil, time.Now())
	if memory.refusedCount() != 1 {
		t.Fatalf("a departure the bound refused was not counted: refused=%d", memory.refusedCount())
	}
}

func itoaTest(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}

// A strategy whose business does not parse is still recorded. Leaving it
// out would have the difference report "nothing remembers this strategy",
// which is a different and wrong sentence from "its business is unusable".
func TestAStrategyWithAnUnparsableBusinessIsStillRemembered(t *testing.T) {
	memory := newDepartedMemory()
	at := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	memory.record(groupsOf(planOf("system", "not-a-number", "10", 7)), nil, at)
	departed := memory.departed()
	if len(departed) != 1 || departed[0].BusinessID != 0 || departed[0].Revision != 7 {
		t.Fatalf("the departure was dropped instead of recorded with what parsed: %+v", departed)
	}
}
