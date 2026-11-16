// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import (
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// stagedRound runs a first write's round whose present groups keys were
// staged ahead of the statement, as the worker's windows stage them, and the
// store's fold of them is folded (the store folds the values it wrote; extra
// adds groups to it the round never staged).
func stagedRound(t *testing.T, input PlanSlotInput, keys []string, extra int) SegmentedResult {
	t.Helper()
	set := NewPresentSet(input.NoData.AggDimension)
	for _, series := range input.Series {
		set.Add(series)
	}
	var memory execution.NoDataMemoryDigestAccumulator
	for _, key := range keys {
		set.MarkStaged(key)
		if err := memory.Add(execution.NoDataGroupMemory{GroupKey: key, LastSeen: input.EvaluationTime}); err != nil {
			t.Fatal(err)
		}
	}
	for index := range extra {
		if err := memory.Add(execution.NoDataGroupMemory{GroupKey: fmt.Sprintf("never-%d", index), LastSeen: input.EvaluationTime}); err != nil {
			t.Fatal(err)
		}
	}
	segmented := input
	segmented.Series, segmented.Present = nil, set
	round, err := NewSegmentedRound(segmented)
	if err != nil {
		t.Fatal(err)
	}
	round.Settle()
	round.UseStaged(execution.NewNoDataStaged([]byte("marker"), input.EvaluationTime, memory))
	result, err := round.Finish(input.ApplyVersion)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// A first write whose present groups were staged ahead is the statement the
// whole read makes, its digests the same, with the staged groups left out of
// its Set and carried as staged: the worker holds them no longer. Staged
// groups the round does not account for - here one the store folded that no
// window staged - are not its record: every group goes with the statement
// and the stage is the caller's to discard.
func TestAFirstWriteStagedAheadIsTheWholeStatementWithoutTheStagedGroups(t *testing.T) {
	input := historyPlanSlotInput(execution.NoDataMemorySnapshot{
		Identity: planSlotIdentity(), Status: execution.NoDataMemoryMissing,
		Representation: execution.NoDataRepresentationNone,
	})
	keys := make([]string, 0, 30)
	for index := range 30 {
		ip := fmt.Sprintf("10.0.0.%d", index+1)
		input.Series = append(input.Series, map[string]string{HostIPDimension: ip, HostCloudDimension: "0"})
		keys = append(keys, hostTargetGroup(HostIdentity{IP: ip, CloudID: "0"}).Key())
	}
	sort.Strings(keys)
	want, err := EvaluatePlanSlot(input)
	if err != nil || want.Mutation == nil || len(want.Mutation.Set) != len(keys) {
		t.Fatalf("whole read %+v (%v), want a first write of the %d present groups", want.Mutation, err, len(keys))
	}

	got := stagedRound(t, input, keys[:20], 0)
	if got.StagedDiffers != "" || got.Mutation == nil || got.Mutation.Staged.Groups() != 20 {
		t.Fatalf("staged round %+v, want its 20 staged groups carried as staged", got)
	}
	if !reflect.DeepEqual(got.Mutation.Set, want.Mutation.Set[20:]) {
		t.Fatalf("statement sets %v, want the groups never staged %v", got.Mutation.Set, want.Mutation.Set[20:])
	}
	if got.Mutation.MemoryDigest != want.Mutation.MemoryDigest || got.Mutation.MutationDigest != want.Mutation.MutationDigest ||
		got.Mutation.GroupCount != want.Mutation.GroupCount {
		t.Fatalf("staged statement %+v, want the whole read's digests and count %+v", got.Mutation, want.Mutation)
	}

	differs := stagedRound(t, input, keys[:20], 1)
	if differs.StagedDiffers != StagedDiffersCount || differs.Mutation == nil || differs.Mutation.Staged != nil {
		t.Fatalf("round over a stage it does not account for %+v, want every group with the statement", differs)
	}
	sort.Slice(differs.Mutation.Set, func(left, right int) bool {
		return differs.Mutation.Set[left].GroupKey < differs.Mutation.Set[right].GroupKey
	})
	if !reflect.DeepEqual(differs.Mutation.Set, want.Mutation.Set) || differs.Mutation.MutationDigest != want.Mutation.MutationDigest {
		t.Fatalf("statement %+v, want the whole read's %+v", differs.Mutation, want.Mutation)
	}
}
