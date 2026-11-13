// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

var _ execution.PlanNoDataStager = (*ExecutionStore)(nil)

// stageAhead stages groups of a Plan's whole memory ahead of its statement,
// a segment a call, as the windows of a first write stage them.
func stageAhead(
	t *testing.T, store *ExecutionStore, attempt execution.NoDataWriteAttempt, presentAsOf int64,
	staged *execution.NoDataStaged, set []execution.NoDataGroupDelta,
) execution.NoDataStageResult {
	t.Helper()
	result, err := store.StageNoData(context.Background(), execution.NoDataStageRequest{
		Contract: frozenRef(), Identity: noDataIdentityV2(), Attempt: attempt, PresentAsOf: presentAsOf,
		Set: set, Staged: staged,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// A first write staged in part ahead of its statement - two segments as the
// windows landed them - and the rest with it is the record a write of the
// whole statement at once makes: the statement is the same, the groups the
// same, and the staging key goes with the swap.
func TestAFirstWriteStagedAheadIsTheRecordWrittenAtOnceOnARealServer(t *testing.T) {
	server := startNoDataServer(t)
	store := noDataStoreOver(t, server.backend)
	attempt := execution.NoDataWriteAttempt{OwnerEpoch: 4, AttemptNo: 1}
	memory := stagedMemory(2*execution.NoDataFirstWriteSegmentGroups + 300)
	whole := firstNoDataWrite(t, 0, memory...)
	var staged *execution.NoDataStaged
	for start := 0; start < 2*execution.NoDataFirstWriteSegmentGroups; start += execution.NoDataFirstWriteSegmentGroups {
		result := stageAhead(t, store, attempt, whole.PresentAsOf, staged,
			whole.Set[start:start+execution.NoDataFirstWriteSegmentGroups])
		if result.Status != execution.NoDataApplied {
			t.Fatalf("segment at %d staged %+v, want it applied", start, result)
		}
		staged = result.Staged
	}
	if exists := server.client.Exists(context.Background(), noDataHashKey(t)).Val(); exists != 0 {
		t.Fatal("the formal key exists before the statement")
	}
	statement := whole
	statement.Set, statement.Staged = whole.Set[2*execution.NoDataFirstWriteSegmentGroups:], staged
	if err := statement.ValidateDigest(); err != nil {
		t.Fatalf("the statement with groups staged ahead is not the whole statement: %v", err)
	}
	if got := storeNoDataAs(t, store, statement, attempt); got.Status != execution.NoDataApplied {
		t.Fatalf("the statement after its groups were staged = %+v, want it applied", got)
	}
	loaded := loadOneNoDataMemory(t, server.store(t))
	if loaded.Status != execution.NoDataMemoryFound || len(loaded.Groups) != len(memory) {
		t.Fatalf("the record is %s with %d groups, want it found with %d", loaded.Status, len(loaded.Groups), len(memory))
	}
	for index, group := range loaded.Groups {
		if group != memory[index] {
			t.Fatalf("group %d is %+v, want %+v", index, group, memory[index])
		}
	}
	if loaded.PersistedMutationDigest != whole.MemoryDigest {
		t.Fatalf("the record's digest is %s, want the whole statement's %s", loaded.PersistedMutationDigest, whole.MemoryDigest)
	}
}

// A group staged twice - a window and the statement both carrying it - and
// one left out fold into another memory than the statement states, though
// the count is right: nothing is put in place.
func TestStagedGroupsThatAreNotTheStatedMemoryAreNotPutInPlaceOnARealServer(t *testing.T) {
	server := startNoDataServer(t)
	store := noDataStoreOver(t, server.backend)
	attempt := execution.NoDataWriteAttempt{OwnerEpoch: 4, AttemptNo: 1}
	whole := firstNoDataWrite(t, 0, stagedMemory(10)...)
	staged := stageAhead(t, store, attempt, whole.PresentAsOf, nil, whole.Set[:3]).Staged
	// Counted right - three staged and seven sent is ten - so only the fold
	// tells: the third group is sent again, and the last is sent by neither.
	statement := whole
	statement.Set, statement.Staged = whole.Set[2:9], staged
	got := storeNoDataAs(t, store, statement, attempt)
	if got.Status != execution.NoDataRejected || got.ReasonCode != execution.ReasonCode(contract.ReasonStateCorrupt) {
		t.Fatalf("applied %+v, want it refused as corrupt", got)
	}
	if exists := server.client.Exists(context.Background(), noDataHashKey(t)).Val(); exists != 0 {
		t.Fatal("a memory other than the statement's was put in place")
	}
}

// A round that ends without its statement takes back what it staged; a
// discard under another write's marker leaves that write's key.
func TestADiscardedStageTakesBackOnlyItsOwnKeyOnARealServer(t *testing.T) {
	server := startNoDataServer(t)
	store := noDataStoreOver(t, server.backend)
	attempt := execution.NoDataWriteAttempt{OwnerEpoch: 4, AttemptNo: 1}
	whole := firstNoDataWrite(t, 0, stagedMemory(10)...)
	stageKey, err := PlanNoDataStageKeyV1("alarmd", noDataIdentityV2(), attempt)
	if err != nil {
		t.Fatal(err)
	}
	stale := stageAhead(t, store, attempt, whole.PresentAsOf, nil, whole.Set[:5]).Staged
	// Started over under the same name by another write.
	current := stageAhead(t, store, attempt, whole.PresentAsOf, nil, whole.Set[:5]).Staged
	discard := func(staged *execution.NoDataStaged) {
		if err := store.DiscardNoDataStage(context.Background(), execution.NoDataStageDiscard{
			Identity: noDataIdentityV2(), Attempt: attempt, Staged: staged,
		}); err != nil {
			t.Fatal(err)
		}
	}
	discard(stale)
	if exists := server.client.Exists(context.Background(), stageKey).Val(); exists != 1 {
		t.Fatal("a discard under another write's marker took its key")
	}
	discard(current)
	if exists := server.client.Exists(context.Background(), stageKey).Val(); exists != 0 {
		t.Fatal("the staging key is still there after its own write discarded it")
	}
}
