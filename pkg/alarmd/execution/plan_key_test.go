// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func pieceForTest(index int) *execution.ShardRef {
	return &execution.ShardRef{Dimension: "bk_target_ip", Index: index, Count: 2, MatcherDigest: strings.Repeat("a", 64)}
}

// A Plan that is not split serializes exactly as it did before pieces
// existed, on every carrier that is persisted or digested. The key embeds
// the identity and omits a zero index; the carriers hold a pointer omitted
// when nil. Both are what keep every record of every deployment where it is
// - a persisted activation, a frozen due-Plan target, a Schedule entry - at
// the bytes an older build wrote and reads.
//
// The other branch matters as much: a piece must serialize its coordinate,
// or the split would be lost at the first restart.
func TestAnUnsplitPlanSerializesWithoutAnyShardField(t *testing.T) {
	identity := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	unsplit := map[string]any{
		"key":            execution.PlanKey{PlanIdentity: identity},
		"activated plan": execution.ActivatedPlan{Identity: identity},
		"activation fact": execution.PlanActivationFact{
			Plan: identity, Selection: execution.ActivationCurrent, Selected: execution.ActivatedPlan{Identity: identity},
		},
		"schedule entry": execution.FrozenPlanSchedule{Identity: identity},
		"schedule ref":   execution.FrozenPlanScheduleRef{Identity: identity},
		"query facts":    execution.QueryPlanFacts{TenantID: "tenant"},
	}
	for name, value := range unsplit {
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToLower(string(payload)), "shard") {
			t.Fatalf("%s of an unsplit Plan serializes a shard field: %s", name, payload)
		}
	}
	// The key of an unsplit Plan is byte for byte the identity.
	keyed, err := json.Marshal(execution.PlanKey{PlanIdentity: identity})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	if string(keyed) != string(plain) {
		t.Fatalf("an unsplit key serializes as %s, want the identity's own %s: a persisted list of Plans would change bytes", keyed, plain)
	}

	split := map[string]any{
		"key":             execution.PlanKey{PlanIdentity: identity, ShardIndex: 1},
		"activated plan":  execution.ActivatedPlan{Identity: identity, Shard: pieceForTest(1)},
		"activation fact": execution.PlanActivationFact{Plan: identity, Shard: pieceForTest(1)},
		"schedule entry":  execution.FrozenPlanSchedule{Identity: identity, Shard: pieceForTest(1)},
		"schedule ref":    execution.FrozenPlanScheduleRef{Identity: identity, Shard: pieceForTest(1)},
		"query facts":     execution.QueryPlanFacts{TenantID: "tenant", Shard: pieceForTest(1)},
	}
	for name, value := range split {
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.ToLower(string(payload)), "shard") {
			t.Fatalf("%s of a piece serializes no shard field: %s; the split would not survive a restart", name, payload)
		}
	}
	// And a piece's key reads back as the piece, not as the strategy's zeroth.
	var decoded execution.PlanKey
	if err := json.Unmarshal([]byte(`{"TenantID":"tenant","BusinessID":"2","StrategyID":"7","ShardIndex":1}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != (execution.PlanKey{PlanIdentity: identity, ShardIndex: 1}) {
		t.Fatalf("decoded key = %+v, want piece 1 of the strategy", decoded)
	}
}

// Two pieces of one strategy are two facts: found apart, unequal, and
// carried apart by the request that asks for them. Compared by content,
// because two records decoded from the same bytes hold different pointers
// and a comparison by address would call every piece changed on every read.
func TestActivationFactsOfTwoPiecesAreTwoFacts(t *testing.T) {
	identity := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	first := execution.PlanActivationFact{Plan: identity, Selection: execution.ActivationCurrent, Shard: pieceForTest(0),
		Selected: execution.ActivatedPlan{Identity: identity, StateGeneration: "g", StateApplyEpoch: 1, ScheduleRevision: "r", RequiredFullSlots: 1, Shard: pieceForTest(0)}}
	second := first
	second.Shard, second.Selected.Shard = pieceForTest(1), pieceForTest(1)
	result := execution.PlanActivationResult{Facts: []execution.PlanActivationFact{first, second}}

	found, ok := result.Find(execution.PlanKey{PlanIdentity: identity, ShardIndex: 1})
	if !ok || !execution.ShardsEqual(found.Shard, pieceForTest(1)) {
		t.Fatalf("Find(piece 1) = %+v, %v; want the second piece's fact", found, ok)
	}
	if _, ok := result.Find(execution.PlanKey{PlanIdentity: identity, ShardIndex: 2}); ok {
		t.Fatal("Find(piece 2) found a fact the strategy has no piece for")
	}
	if first.Equal(second) {
		t.Fatal("two pieces of one strategy compare equal")
	}
	// Same content, different pointers: equal.
	copied := first
	copied.Shard, copied.Selected.Shard = pieceForTest(0), pieceForTest(0)
	if !first.Equal(copied) {
		t.Fatal("a fact decoded into fresh pointers no longer equals itself")
	}

	// The result validator keeps both and refuses a piece named twice.
	contractRef := execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: "q", EvaluationTime: 60}, SnapshotRevision: "s", QueryRevision: "q", ScheduleRevision: "r", ScheduleSegmentStart: 60, DuePlanSetDigest: "d"}
	result.Contract = contractRef
	request := execution.PlanActivationRequest{Contract: contractRef, Plans: []execution.PlanKey{
		{PlanIdentity: identity, ShardIndex: 0}, {PlanIdentity: identity, ShardIndex: 1},
	}}
	if err := result.Validate(request); err != nil {
		t.Fatalf("two pieces of one strategy refused as an activation result: %v", err)
	}
	twice := execution.PlanActivationResult{Contract: contractRef, Facts: []execution.PlanActivationFact{first, first}}
	if err := twice.Validate(execution.PlanActivationRequest{Contract: contractRef, Plans: []execution.PlanKey{
		{PlanIdentity: identity, ShardIndex: 0}, {PlanIdentity: identity, ShardIndex: 1},
	}}); err == nil {
		t.Fatal("the same piece twice was accepted as two facts")
	}
	// A selection that names another piece than its fact is refused: the
	// selection is what runs, the fact is what is indexed.
	disagree := first
	disagree.Selected.Shard = pieceForTest(1)
	if err := (execution.PlanActivationResult{Contract: contractRef, Facts: []execution.PlanActivationFact{disagree}}).Validate(
		execution.PlanActivationRequest{Contract: contractRef, Plans: []execution.PlanKey{{PlanIdentity: identity, ShardIndex: 0}}}); err == nil {
		t.Fatal("a fact whose selection names another piece was accepted")
	}
}

// The gap identity a Worker composes from an activation names the piece: a
// piece's marker is its own, and an activation that stopped carrying the
// piece would load the unsplit marker for every piece of the strategy.
func TestAnActivationFactNamesItsPiecesGapMarker(t *testing.T) {
	identity := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	fact := execution.PlanActivationFact{Plan: identity, Selection: execution.ActivationCurrent, Shard: pieceForTest(1),
		Selected: execution.ActivatedPlan{Identity: identity, StateGeneration: "g", Shard: pieceForTest(1)}}
	due := execution.DuePlan{Identity: identity, StateGeneration: "g", Shard: *pieceForTest(1)}
	if fact.GapIdentity() != due.GapIdentity() {
		t.Fatalf("activation names %+v, the due Plan names %+v: the two readers of one marker disagree on its key",
			fact.GapIdentity(), due.GapIdentity())
	}
	if fact.GapIdentity().Shard.IsZero() {
		t.Fatal("the activation's gap identity dropped the piece")
	}
}
