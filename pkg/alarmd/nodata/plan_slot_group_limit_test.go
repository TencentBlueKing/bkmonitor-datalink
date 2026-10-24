// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package nodata

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The Plan's own group bound reaches the store on the round's mutation, and
// leaves the statement's digest alone: the bound is what the memory is
// checked against, not part of what it says.
func TestAPlansOwnGroupBoundTravelsOnItsMutation(t *testing.T) {
	snapshot := execution.NoDataMemorySnapshot{Identity: planSlotIdentity(), Status: execution.NoDataMemoryMissing,
		Representation: execution.NoDataRepresentationNone}
	plain := planSlotInput(snapshot, presentSeries())
	bounded := planSlotInput(snapshot, presentSeries())
	bounded.MaxGroups = 7
	without, err := EvaluatePlanSlot(plain)
	if err != nil || without.Mutation == nil {
		t.Fatalf("EvaluatePlanSlot() = %+v, %v", without, err)
	}
	with, err := EvaluatePlanSlot(bounded)
	if err != nil || with.Mutation == nil {
		t.Fatalf("EvaluatePlanSlot() = %+v, %v", with, err)
	}
	if without.Mutation.GroupLimit != 0 || with.Mutation.GroupLimit != 7 {
		t.Fatalf("group limits = %d and %d, want 0 without a bound and 7 with one", without.Mutation.GroupLimit, with.Mutation.GroupLimit)
	}
	if with.Mutation.MutationDigest != without.Mutation.MutationDigest {
		t.Fatal("the bound changed the statement's digest")
	}
}
