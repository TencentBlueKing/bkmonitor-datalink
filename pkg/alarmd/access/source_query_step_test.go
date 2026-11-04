// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package access

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// steppedFrozenExecution is frozenExecution for a Plan detected every fifteen
// seconds over one-minute windows, whose query asks for named outputs: its
// primary query and its previous window, a minute before.
func steppedFrozenExecution(t *testing.T) (execution.FrozenExecutionContractRef, FrozenPlan) {
	t.Helper()
	contractRef, frozen := frozenExecution(t)
	facts := frozen.QueryFacts[frozen.Requirements[0].LogicalQueryRef]
	facts.QueryRevision, facts.NotTimeAlign, facts.QueryStepMillis = "", true, 15_000
	facts.NamedOutputs = &execution.NamedOutputs{LegacyRef: "a", Outputs: []execution.NamedOutput{{Ref: "a", Expression: "a"}, {Ref: "raw", Expression: "a"}}}
	stepped, err := execution.BuildQueryPlanFacts(facts)
	if err != nil {
		t.Fatal(err)
	}
	ref := execution.LogicalQueryRef(stepped.QueryRevision)
	primary := frozen.Requirements[0]
	primary.LogicalQueryRef = ref
	previous := primary
	previous.RequirementID, previous.DatasetName, previous.Role = "previous", "previous", execution.InputRoleAlgorithmDependency
	previous.RelativeWindow = execution.RelativeQueryWindow{StartOffsetSeconds: -120, EndOffsetSeconds: -60, HalfOpen: true}
	previous.ReadinessClass = execution.ReadinessFinalizedRequired
	frozen.Requirements = []execution.DataRequirement{primary, previous}
	frozen.QueryFacts = map[execution.LogicalQueryRef]execution.QueryPlanFacts{ref: stepped}
	contractRef.QueryRevision = stepped.QueryRevision
	return bindFrozenDueDigest(t, contractRef, frozen), frozen
}

// Every query of a Plan asked a step apart asks for, and accepts, one step:
// the bucket that starts at its window's start, which is the window. Its
// window stays the requirement's, which readiness is judged by. The re-query
// of its legacy output alone asks for the same step over the same range.
func TestAPlanAskedAStepApartAsksEachWindowForItsOneBucket(t *testing.T) {
	contractRef, frozen := steppedFrozenExecution(t)
	prepared, err := Prepare(contractRef, frozen, time.Second)
	if err != nil {
		t.Fatalf("Prepare() error=%v", err)
	}
	at := int64(contractRef.Slot.EvaluationTime)
	want := map[int64]bool{at - 60: false, at - 120: false}
	if len(prepared.Queries) != 2 {
		t.Fatalf("queries %+v, want the primary and the previous window", prepared.Queries)
	}
	for _, query := range prepared.Queries {
		spec := query.Spec
		start := spec.LogicalWindow.Start
		if _, ok := want[start]; !ok || spec.LogicalWindow.End != start+60 {
			t.Fatalf("logical window %+v, want a requirement's one-minute window", spec.LogicalWindow)
		}
		want[start] = true
		one := execution.QueryWindow{Start: start, End: start + 15}
		if spec.ProviderRange != one || spec.AcceptedRange != one {
			t.Fatalf("asked %+v accepted %+v of %+v, want one 15 s step", spec.ProviderRange, spec.AcceptedRange, spec.LogicalWindow)
		}
		legacy, err := spec.LegacyOutputOnly()
		if err != nil {
			t.Fatalf("LegacyOutputOnly() error=%v", err)
		}
		if legacy.PlanFacts.NamedOutputs != nil || legacy.PlanFacts.RequestStepMillis() != 15_000 ||
			legacy.ProviderRange != one || legacy.AcceptedRange != one || legacy.LogicalWindow != spec.LogicalWindow {
			t.Fatalf("re-query %+v asked %d ms apart over %+v, want the same step over %+v",
				legacy.LogicalWindow, legacy.PlanFacts.RequestStepMillis(), legacy.ProviderRange, one)
		}
	}
	for start, seen := range want {
		if !seen {
			t.Fatalf("no query for the window starting %d", start)
		}
	}
}

// A Plan detected at its aggregation interval asks for its whole window, as
// it always did.
func TestAPlanAtItsAggregationIntervalAsksForItsWholeWindow(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	prepared, err := Prepare(contractRef, frozen, time.Second)
	if err != nil {
		t.Fatalf("Prepare() error=%v", err)
	}
	spec := prepared.Queries[0].Spec
	if spec.ProviderRange != spec.LogicalWindow || spec.AcceptedRange != spec.LogicalWindow {
		t.Fatalf("asked %+v accepted %+v, want the window %+v", spec.ProviderRange, spec.AcceptedRange, spec.LogicalWindow)
	}
}
