// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A10. A host target this process cannot resolve is skipped by name, not
// judged as an item that expects nothing.
//
// The two look identical from inside the roster: both give an empty expected
// set. They are opposite answers. A target whose hosts have all gone really
// does expect nothing, and the item then speaks about itself; a target that
// could not be resolved is not known to be empty, and treating it as empty
// sends a no-data alert under the whole-item identity while the item's own
// host groups keep their absences open with nothing left to close them.
//
// A cold index is not a rare state. It is every replica for the first refresh
// after every restart, and every replica for as long as a CMDB read keeps
// failing.
func TestEvaluateSlotSkipsAHostTargetItCannotResolve(t *testing.T) {
	scope := hostScope("10.0.0.1|0", "10.0.0.2|0")
	memory := map[string]GroupMemory{
		hostTargetGroup(HostIdentity{IP: "10.0.0.2", CloudID: "0"}).Key(): {FirstAbsent: 940},
	}

	result, outcome, err := EvaluateSlot(SlotInput{
		Plan:           slotPlan(scope, []string{HostIPDimension, HostCloudDimension}),
		EvaluationTime: 1000, PeriodSeconds: 60, Completeness: execution.CompletenessFull,
		// No index: every declared host reads as one CMDB has never heard of,
		// which is what an unbuilt index answers about everything.
		KnownHosts: knownHosts(), HostsResolved: false,
		Memory: memory,
	})

	if err != nil {
		t.Fatalf("EvaluateSlot() error = %v; an unresolved index is a state, not a failure", err)
	}
	if outcome != OutcomeSkippedHostsUnresolved {
		t.Fatalf("outcome = %q, want %q. An empty expected set here would be read as an item that "+
			"expects nothing, which is the one reading that is certainly wrong",
			outcome, OutcomeSkippedHostsUnresolved)
	}
	if len(result.Verdicts) != 0 {
		t.Fatalf("Verdicts = %v, want none: a round with no expected set has judged nothing", result.Verdicts)
	}
	if len(result.Memory) != 0 {
		t.Fatalf("Memory = %v, want nothing written back: the absences this item has open are still "+
			"open, and a round that could not look must not move their clocks", result.Memory)
	}
}

// And the legitimate empty target is still judged. This is the case the skip
// must not swallow: the index answered, and what it said is that none of the
// declared hosts is in this business any more.
func TestEvaluateSlotStillJudgesATargetThatResolvedToNoHost(t *testing.T) {
	scope := hostScope("10.0.0.1|0", "10.0.0.2|0")

	result, outcome, err := EvaluateSlot(SlotInput{
		Plan:           slotPlan(scope, []string{HostIPDimension, HostCloudDimension}),
		EvaluationTime: 1000, PeriodSeconds: 60, Completeness: execution.CompletenessFull,
		KnownHosts: knownHosts(), HostsResolved: true,
		Memory: map[string]GroupMemory{},
	})

	if err != nil || outcome != OutcomeEvaluated {
		t.Fatalf("EvaluateSlot() = %q, %v; want the round judged. The index answered, and an empty "+
			"answer is an answer", outcome, err)
	}
	if got := result.Verdicts[WholeItemGroup().Key()]; got != VerdictAnomaly {
		t.Fatalf("whole-item verdict = %q, want %q: nothing is expected and nothing arrived",
			got, VerdictAnomaly)
	}
	if result.Facts.RosterSource != RosterTargetStatic {
		t.Fatalf("RosterSource = %q, want it left as declared: a target that resolved to nobody is not "+
			"the same as an item that expects nothing by design, and only the source says which",
			result.Facts.RosterSource)
	}
}

// The classes that do not look at hosts are not affected. A history roster
// comes out of the memory and a whole-item one expects nothing by design, and
// neither becomes less true because the host index is cold -- so gating them
// on it would stop detection that was never at risk.
func TestEvaluateSlotJudgesHistoryAndWholeItemWithNoHostIndex(t *testing.T) {
	seen := hostGroup(t, "10.0.0.9")
	history := map[string]GroupMemory{seen.Key(): {LastSeen: 880}}

	result, outcome, err := EvaluateSlot(SlotInput{
		Plan:           slotPlan(nil, []string{HostIPDimension, HostCloudDimension}),
		EvaluationTime: 1000, PeriodSeconds: 60, Completeness: execution.CompletenessFull,
		HostsResolved: false, Memory: history,
	})
	if err != nil || outcome != OutcomeEvaluated {
		t.Fatalf("history roster: EvaluateSlot() = %q, %v; it reads the memory, not the index", outcome, err)
	}
	if got := result.Verdicts[seen.Key()]; got != VerdictAnomaly {
		t.Fatalf("history group verdict = %q, want %q", got, VerdictAnomaly)
	}

	whole, outcome, err := EvaluateSlot(SlotInput{
		// No-data dimensions that do not name the host make this a whole-item
		// item whatever the target says.
		Plan:           slotPlan(hostScope("10.0.0.1|0"), []string{"device"}),
		EvaluationTime: 1000, PeriodSeconds: 60, Completeness: execution.CompletenessFull,
		HostsResolved: false, Memory: map[string]GroupMemory{},
	})
	if err != nil || outcome != OutcomeEvaluated {
		t.Fatalf("whole-item roster: EvaluateSlot() = %q, %v; it expects nothing by design", outcome, err)
	}
	if got := whole.Verdicts[WholeItemGroup().Key()]; got != VerdictAnomaly {
		t.Fatalf("whole-item verdict = %q, want %q", got, VerdictAnomaly)
	}
}
