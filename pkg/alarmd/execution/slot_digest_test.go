// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"fmt"
	"sync"
	"testing"
)

func slotDigestContract(group QueryGroupIdentity, at EvaluationTime) FrozenExecutionContractRef {
	return FrozenExecutionContractRef{
		Slot:             SlotIdentity{QueryGroup: group, EvaluationTime: at},
		SnapshotRevision: "snapshot", QueryRevision: "query", ScheduleRevision: "schedule",
		ScheduleSegmentStart: 60, DuePlanSetDigest: "plans",
	}
}

// Every ApplyVersion carries the digest deriving it again gives, byte for
// byte: for the same Slot asked again, for the next Slot of the same Query
// Group, for another Query Group at the same minute, and after the memory
// has been cleared. None of them is handed another Slot's.
func TestARememberedSlotDigestIsTheOneDerivingItGives(t *testing.T) {
	check := func(group QueryGroupIdentity, at EvaluationTime) SlotIdentityDigest {
		t.Helper()
		version, err := BuildApplyVersion(slotDigestContract(group, at), 3)
		if err != nil {
			t.Fatal(err)
		}
		want, err := deriveSlotIdentityDigest(SlotIdentity{QueryGroup: group, EvaluationTime: at})
		if err != nil {
			t.Fatal(err)
		}
		if version.SlotDigest != want || version.EvaluationTime != at || version.StateApplyEpoch != 3 {
			t.Fatalf("ApplyVersion for %s at %d = %+v, want the digest %s derived afresh", group, at, version, want)
		}
		return version.SlotDigest
	}
	first := check("group-a", 1_788_000_060)
	if again := check("group-a", 1_788_000_060); again != first {
		t.Fatalf("the same Slot asked again = %s, want %s", again, first)
	}
	if next := check("group-a", 1_788_000_120); next == first {
		t.Fatal("the next Slot of the same Query Group was handed the previous Slot's digest")
	}
	if other := check("group-b", 1_788_000_060); other == first {
		t.Fatal("another Query Group at the same minute was handed the first one's digest")
	}

	// Past the bound the memory is cleared, never grown, and what is
	// derived again afterwards is still the Slot's own.
	for index := 0; index < 2*slotDigestMemoEntries; index++ {
		check(QueryGroupIdentity(fmt.Sprintf("group-%d", index%700)), EvaluationTime(1_788_000_000+60*(index/700)))
		slotDigests.mu.RLock()
		size := len(slotDigests.digests)
		slotDigests.mu.RUnlock()
		if size > slotDigestMemoEntries {
			t.Fatalf("the memory holds %d Slots, past its bound of %d", size, slotDigestMemoEntries)
		}
	}
	check("group-a", 1_788_000_060)
}

// Slots evaluated at once each get their own digest.
func TestSlotDigestsAreTheirOwnUnderConcurrency(t *testing.T) {
	var wait sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for index := 0; index < 500; index++ {
				slot := SlotIdentity{QueryGroup: QueryGroupIdentity(fmt.Sprintf("group-%d", (worker+index)%50)), EvaluationTime: EvaluationTime(1_788_000_000 + 60*(index%20))}
				got, err := slotIdentityDigest(slot)
				want, wantErr := deriveSlotIdentityDigest(slot)
				if err != nil || wantErr != nil || got != want {
					t.Errorf("Slot %+v = %s (%v), want %s", slot, got, err, want)
					return
				}
			}
		}(worker)
	}
	wait.Wait()
}

// Asked again for a Slot it has, the build derives nothing and allocates
// nothing.
func TestAnApplyVersionForARememberedSlotAllocatesNothing(t *testing.T) {
	contractRef := slotDigestContract("group-a", 1_788_000_060)
	if _, err := BuildApplyVersion(contractRef, 1); err != nil {
		t.Fatal(err)
	}
	if allocations := testing.AllocsPerRun(100, func() {
		if _, err := BuildApplyVersion(contractRef, 1); err != nil {
			t.Fatal(err)
		}
	}); allocations != 0 {
		t.Fatalf("BuildApplyVersion for a remembered Slot allocates %.0f times, want 0", allocations)
	}
}
