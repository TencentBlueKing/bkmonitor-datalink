// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// retainedSeed puts a fixture's starting retention on the input phase, which is
// where a real execution's first bytes come from. Cases that only care about
// the total say so by using this rather than naming a phase themselves.
func retainedSeed(bytes uint64) [retainPhaseCount]uint64 {
	return [retainPhaseCount]uint64{retainPhaseInput: bytes}
}

// Each phase's bytes are charged to that phase and to no other.
//
// Asserted one path at a time with every other phase held at zero. A split
// where two paths share a counter still adds up to the right total and still
// moves when either path runs, so a case that only checks the total, or only
// checks that the phase it drove went up, passes on a split that answers
// nothing. The question this reporting exists for is which phase to go and
// change, and an answer that names the wrong one is worse than no answer.
func TestEachPhaseIsChargedForItsOwnBytesAndNobodyElses(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		phase retainPhase
		drive func(t *testing.T, stream *streamedExecution)
	}{{
		name: "targets retained before the gap markers are read", phase: retainPhaseGap,
		drive: func(t *testing.T, stream *streamedExecution) {
			t.Helper()
			if err := stream.retainTargetBytes(context.Background(), 1, 64); err != nil {
				t.Fatalf("retain targets: %v", err)
			}
		},
	}, {
		// Two cases over one call, because that call carries two quantities:
		// what the caller already held when it asked, and what this result
		// adds. They used to be summed into the output phase, where the
		// first one dominated and was read as output.
		name: "the side effects this round produced", phase: retainPhaseOutput,
		drive: func(t *testing.T, stream *streamedExecution) {
			t.Helper()
			if err := stream.mergeProvisional(context.Background(), sideEffectTestResult("state", "qg"), 0); err != nil {
				t.Fatalf("merge provisional: %v", err)
			}
		},
	}, {
		name: "the Runtime State this round loaded", phase: retainPhaseState,
		drive: func(t *testing.T, stream *streamedExecution) {
			t.Helper()
			if err := stream.mergeProvisional(context.Background(), execution.EvaluationResult{}, 128); err != nil {
				t.Fatalf("merge provisional: %v", err)
			}
		},
	}} {
		t.Run(testCase.name, func(t *testing.T) {
			coordinator := &SlotExecutionCoordinator{budget: sideEffectTestBudget("")}
			stream := &streamedExecution{coordinator: coordinator, began: true}
			defer stream.releaseProvisional()
			testCase.drive(t, stream)

			if stream.retainedByPhase[testCase.phase] == 0 {
				t.Fatalf("phase %d was charged nothing by the path that retains for it: %v",
					testCase.phase, stream.retainedByPhase)
			}
			for phase := retainPhaseInput; phase < retainPhaseCount; phase++ {
				if phase != testCase.phase && stream.retainedByPhase[phase] != 0 {
					t.Fatalf("phase %d was charged %d bytes by a path that retains for phase %d: %v",
						phase, stream.retainedByPhase[phase], testCase.phase, stream.retainedByPhase)
				}
			}
		})
	}
}

// Releasing an execution gives the pool back every phase's bytes.
//
// The pool is charged one total and this execution now holds three numbers. A
// release that hands back one phase leaks the other two: nothing fails at the
// time, the replica just refuses admissions later for memory no execution
// holds, and the refusals name whichever object is unlucky enough to be next.
func TestReleasingAnExecutionGivesBackEveryPhaseItHeld(t *testing.T) {
	coordinator := &SlotExecutionCoordinator{budget: sideEffectTestBudget("")}
	stream := &streamedExecution{coordinator: coordinator, began: true}
	if err := stream.reserveProvisional(context.Background(), 1, 100); err != nil {
		t.Fatalf("reserve input: %v", err)
	}
	stream.series++
	stream.retainBytes(retainPhaseInput, 100)
	if err := stream.retainTargetBytes(context.Background(), 1, 64); err != nil {
		t.Fatalf("retain targets: %v", err)
	}
	if err := stream.mergeProvisional(context.Background(), sideEffectTestResult("state", "qg"), 128); err != nil {
		t.Fatalf("merge provisional: %v", err)
	}
	held := stream.retainedTotal()
	sum := uint64(0)
	for phase := retainPhaseInput; phase < retainPhaseCount; phase++ {
		sum += stream.retainedByPhase[phase]
	}
	if held != sum {
		t.Fatalf("the total %d is not the sum of the phases %v", held, stream.retainedByPhase)
	}
	if stream.retainedByPhase[retainPhaseInput] == 0 || stream.retainedByPhase[retainPhaseGap] == 0 ||
		stream.retainedByPhase[retainPhaseOutput] == 0 || stream.retainedByPhase[retainPhaseState] == 0 {
		t.Fatalf("a phase this case drove holds nothing, so a partial release would not show: %v", stream.retainedByPhase)
	}

	stream.releaseProvisional()

	coordinator.reservations.mu.Lock()
	defer coordinator.reservations.mu.Unlock()
	if coordinator.reservations.retainedBytes != 0 {
		t.Fatalf("the pool still holds %d bytes of an execution that released %d and had %d",
			coordinator.reservations.retainedBytes, held, stream.retainedTotal())
	}
}
