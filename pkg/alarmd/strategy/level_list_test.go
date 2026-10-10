// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package strategy

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func twoLevelPlan(t testing.TB) *CompiledPlan {
	t.Helper()
	plan := validPlan()
	plan.StrategyIR.Levels = []contract.LevelIRV2{validLevel(5, 1, "50"), validLevel(1, 20, "50")}
	return mustCompilePlan(t, newTestCompiler(t), plan)
}

func levelIDs(levels LevelList) []uint32 {
	ids := make([]uint32, 0, levels.Len())
	for _, level := range levels.All() {
		ids = append(ids, level.Definition().LevelID)
	}
	return ids
}

// A Plan's Levels read the same through every accessor, in the Plan's order,
// and nothing a caller does to what it was handed changes the Plan: not
// reordering or replacing the Levels of a copy, not changing a Level it was
// given by value.
func TestALevelListCannotChangeThePlan(t *testing.T) {
	compiled := twoLevelPlan(t)
	levels := compiled.Levels()
	if levels.Len() != 2 || levels.At(0).Definition().LevelID != 1 || levels.At(1).Definition().LevelID != 5 {
		t.Fatalf("Levels() = %v, want [1 5]", levelIDs(levels))
	}

	owned := levels.Copy()
	owned[0], owned[1] = owned[1], owned[0]
	owned[0].definition.LevelID = 99
	_ = append(owned[:1], CompiledLevel{})
	given := levels.At(0)
	given.definition.LevelID = 98
	for index, level := range levels.All() {
		given = level
		given.definition.LevelID = 97
		_ = index
	}

	if got := levelIDs(compiled.Levels()); len(got) != 2 || got[0] != 1 || got[1] != 5 {
		t.Fatalf("after the caller changed what it was handed, Levels() = %v, want [1 5]", got)
	}
}

// Reading the Levels allocates nothing: the view is the Plan's own slice
// behind an unexported field, and each Level is handed out by value.
func TestReadingALevelListAllocatesNothing(t *testing.T) {
	compiled := twoLevelPlan(t)
	var sink uint32
	allocations := testing.AllocsPerRun(100, func() {
		levels := compiled.Levels()
		for index := 0; index < levels.Len(); index++ {
			sink += levels.At(index).Definition().LevelID
		}
		for _, level := range levels.All() {
			sink += level.Definition().LevelID
		}
	})
	if allocations != 0 {
		t.Fatalf("reading the Levels allocates %.0f times, want 0", allocations)
	}
	_ = sink
}

// A loop over All that stops early stops the iteration: the callers return
// from inside such loops, and an iterator that went on would panic there.
func TestALoopOverTheLevelsCanStopEarly(t *testing.T) {
	visited := 0
	for range twoLevelPlan(t).Levels().All() {
		visited++
		break
	}
	if visited != 1 {
		t.Fatalf("visited %d Levels after breaking on the first, want 1", visited)
	}
}

// A nil Plan has no Levels to read and none to copy.
func TestANilPlanHasAnEmptyLevelList(t *testing.T) {
	var compiled *CompiledPlan
	levels := compiled.Levels()
	if levels.Len() != 0 || levels.Copy() != nil {
		t.Fatalf("nil Plan Levels: Len %d, Copy %v", levels.Len(), levels.Copy())
	}
	for range levels.All() {
		t.Fatal("a nil Plan yielded a Level")
	}
}
