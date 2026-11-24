// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Every index runs once, on any number of workers, and a panic in one is
// returned rather than raised on its goroutine, the indices after it left.
func TestSideBySideRunsEveryIndexOnceAndReturnsAPanic(t *testing.T) {
	for _, workers := range []int{1, 2, 7, 64} {
		var ran [100]atomic.Int32
		if recovered := sideBySide(len(ran), workers, func(index int) { ran[index].Add(1) }); recovered != nil {
			t.Fatalf("workers %d: a panic %v from indices that raise none", workers, *recovered)
		}
		for index := range ran {
			if got := ran[index].Load(); got != 1 {
				t.Fatalf("workers %d: index %d ran %d times, want once", workers, index, got)
			}
		}
	}
	recovered := sideBySide(100, 4, panicAtThree)
	wrapped, ok := (*recovered).(*sideBySidePanic)
	if recovered == nil || !ok || wrapped.value != "evaluation" {
		t.Fatalf("recovered %v, want the evaluation's panic", recovered)
	}
	// Raised again on the Slot's goroutine, the value still names the
	// function that panicked on the evaluation's.
	if !strings.Contains(wrapped.Error(), "panicAtThree") || !strings.Contains(wrapped.Error(), "evaluation") {
		t.Fatalf("the raised value does not carry the evaluation's stack: %s", wrapped.Error())
	}
}

// BenchmarkSideBySideFanOut is what sharing a batch out costs over running it
// on the Slot's goroutine, the work itself nothing: the goroutines, the shared
// index and the wait, a batch at a time (ns/op is a batch's). Read beside the
// cost of one series' evaluation (evaluation BenchmarkSeriesSampleEvaluator,
// disabled): sharing out pays when a batch's evaluations take longer, on one
// core, than this.
func BenchmarkSideBySideFanOut(b *testing.B) {
	for _, batch := range []int{1, 2, 16, 256} {
		for _, workers := range []int{1, runtime.GOMAXPROCS(0)} {
			b.Run(fmt.Sprintf("batch_%d/workers_%d", batch, workers), func(b *testing.B) {
				var sink atomic.Int64
				b.ReportAllocs()
				for range b.N {
					sideBySide(batch, workers, func(index int) { sink.Add(int64(index)) })
				}
			})
		}
	}
}

// A requirement a Plan's bindings name more than once - its first slice's
// binding and a later slice's that did not complete - is one input of the
// round: taken once, by the binding that did not complete; a requirement
// named once is taken as it is, and one keep turns away is not taken.
func TestOneBindingARequirementTakesEachRequirementOnceByItsShortestSlice(t *testing.T) {
	full := func(requirement string) execution.NamedInputBinding {
		return execution.NamedInputBinding{RequirementID: execution.RequirementID(requirement), Completeness: execution.CompletenessFull}
	}
	partial := full("primary")
	partial.Completeness = execution.CompletenessPartial
	bindings := []execution.NamedInputBinding{full("primary"), full("history"), partial, full("skipped")}
	taken := oneBindingARequirement(bindings, func(binding execution.NamedInputBinding) bool {
		return binding.RequirementID != "skipped"
	})
	if len(taken) != 2 || taken[0].RequirementID != "primary" || taken[0].Completeness != execution.CompletenessPartial ||
		taken[1].RequirementID != "history" {
		t.Fatalf("taken %+v, want primary once by its PARTIAL binding, then history, and nothing skipped", taken)
	}
}

func panicAtThree(index int) {
	if index == 3 {
		panic("evaluation")
	}
}
