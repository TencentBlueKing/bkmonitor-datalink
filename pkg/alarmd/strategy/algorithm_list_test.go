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

// twoAlgorithmLevel is a Level detecting with two thresholds, in that order.
func twoAlgorithmLevel(t testing.TB) CompiledLevel {
	t.Helper()
	level := validLevel(1, 1, "50")
	level.DetectPlan.Algorithms = append(level.DetectPlan.Algorithms, contract.AlgorithmIRV2{
		Type: "Threshold", Version: 1, Config: mustJSON(thresholdConfig("80")),
	})
	plan := validPlan()
	plan.StrategyIR.Levels = []contract.LevelIRV2{level}
	return mustCompilePlan(t, newTestCompiler(t), plan).Levels().At(0)
}

func algorithmIDs(algorithms AlgorithmList) []string {
	ids := make([]string, 0, algorithms.Len())
	for _, algorithm := range algorithms.All() {
		ids = append(ids, algorithm.AlgorithmPlanID())
	}
	return ids
}

// A Level's algorithms read the same through every accessor, in the Level's
// order, and nothing a caller does to what it was handed changes the Level:
// not reordering or replacing a copy, not changing an algorithm it was given
// by value.
func TestAnAlgorithmListCannotChangeTheLevel(t *testing.T) {
	level := twoAlgorithmLevel(t)
	algorithms := level.Algorithms()
	before := algorithmIDs(algorithms)
	if algorithms.Len() != 2 || before[0] == before[1] ||
		algorithms.At(0).AlgorithmPlanID() != before[0] || algorithms.At(1).AlgorithmPlanID() != before[1] {
		t.Fatalf("Algorithms() = %v, want two distinct algorithms read alike through At and All", before)
	}

	owned := algorithms.Copy()
	owned[0], owned[1] = owned[1], owned[0]
	owned[0].algorithmPlanID = "replaced"
	_ = append(owned[:1], CompiledAlgorithmPlan{})
	given := algorithms.At(0)
	given.algorithmPlanID = "changed"
	for _, algorithm := range algorithms.All() {
		algorithm.algorithmPlanID = "changed in the loop"
		_ = algorithm
	}

	if after := algorithmIDs(level.Algorithms()); len(after) != 2 || after[0] != before[0] || after[1] != before[1] {
		t.Fatalf("after the caller changed what it was handed, Algorithms() = %v, want %v", after, before)
	}
}

// Reading the algorithms allocates nothing: the view is the Level's own
// slice behind an unexported field, and each algorithm is handed out by
// value.
func TestReadingAnAlgorithmListAllocatesNothing(t *testing.T) {
	level := twoAlgorithmLevel(t)
	var sink int
	allocations := testing.AllocsPerRun(100, func() {
		algorithms := level.Algorithms()
		for index := 0; index < algorithms.Len(); index++ {
			sink += len(algorithms.At(index).Kind())
		}
		for _, algorithm := range algorithms.All() {
			sink += len(algorithm.Kind())
		}
	})
	if allocations != 0 {
		t.Fatalf("reading the algorithms allocates %.0f times, want 0", allocations)
	}
	_ = sink
}

// A loop over All that stops early stops the iteration.
func TestALoopOverTheAlgorithmsCanStopEarly(t *testing.T) {
	visited := 0
	for range twoAlgorithmLevel(t).Algorithms().All() {
		visited++
		break
	}
	if visited != 1 {
		t.Fatalf("visited %d algorithms after breaking on the first, want 1", visited)
	}
}

// A Level with no algorithms has none to read and none to copy.
func TestAnEmptyLevelHasAnEmptyAlgorithmList(t *testing.T) {
	algorithms := CompiledLevel{}.Algorithms()
	if algorithms.Len() != 0 || algorithms.Copy() != nil {
		t.Fatalf("empty Level algorithms: Len %d, Copy %v", algorithms.Len(), algorithms.Copy())
	}
	for range algorithms.All() {
		t.Fatal("an empty Level yielded an algorithm")
	}
}
