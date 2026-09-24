// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func shardPieceForTest(index int) *execution.ShardRef {
	return &execution.ShardRef{Dimension: "bk_target_ip", Index: index, Count: 2, MatcherDigest: strings.Repeat("c", 64)}
}

// The published forms of a Plan that is not split carry no shard field, so
// no object digest and no activation record in a deployment moves on the
// release that adds the carrier. A piece's forms carry it.
func TestPublishedFormsOfAnUnsplitPlanCarryNoShardField(t *testing.T) {
	identity := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	for name, value := range map[string]any{
		"frozen plan":       FrozenPlan{Identity: identity},
		"query group plan":  buildQueryGroupPlanObject(FrozenPlan{Identity: identity}),
		"activation record": PlanActivationRecord{Fact: execution.PlanActivationFact{Plan: identity, Selection: execution.ActivationCurrent}},
	} {
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToLower(string(payload)), "shard") {
			t.Fatalf("%s of an unsplit Plan serializes a shard field: %s", name, payload)
		}
	}
	for name, value := range map[string]any{
		"frozen plan":      FrozenPlan{Identity: identity, Shard: shardPieceForTest(1)},
		"query group plan": buildQueryGroupPlanObject(FrozenPlan{Identity: identity, Shard: shardPieceForTest(1)}),
		"activation record": PlanActivationRecord{Fact: execution.PlanActivationFact{
			Plan: identity, Selection: execution.ActivationCurrent, Shard: shardPieceForTest(1),
		}},
	} {
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.ToLower(string(payload)), "shard") {
			t.Fatalf("%s of a piece serializes no shard field: %s", name, payload)
		}
	}
}

func pieceRecord(identity execution.PlanIdentity, index int, publication SnapshotPublicationRef) PlanActivationRecord {
	return PlanActivationRecord{
		Fact: execution.PlanActivationFact{
			Plan: identity, Selection: execution.ActivationCurrent, Shard: shardPieceForTest(index),
			Selected: execution.ActivatedPlan{
				Identity: identity, StateGeneration: "g", StateApplyEpoch: 1, ScheduleRevision: "r",
				RequiredFullSlots: 1, Shard: shardPieceForTest(index),
			},
		},
		Publication: publication,
	}
}

// Two pieces of one strategy are two activation records: the state that
// holds both is valid, the map that indexes them keeps both, and only the
// same piece twice is the duplicate. Before pieces were keyed, the second
// record of a split strategy was refused whole - which, on an old build,
// is what a split activation still does, and why splitting is gated on the
// fleet's build (decision-020 section 4.7.7).
func TestAnActivationHoldsTwoPiecesOfOneStrategy(t *testing.T) {
	identity := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	publication := SnapshotPublicationRef{SnapshotRevision: "s1", PublicationEpoch: 1}
	state := ActivationState{
		SchemaVersion: activationSchemaVersion, RecordRevision: 1, Current: publication,
		Plans:          []PlanActivationRecord{pieceRecord(identity, 0, publication), pieceRecord(identity, 1, publication)},
		ActiveQGSetRef: ActiveQueryGroupSetRef{SchemaVersion: activeQueryGroupSetSchemaVersion, Digest: strings.Repeat("d", 64)},
	}
	if err := validateActivationState(state); err != nil {
		t.Fatalf("an activation holding two pieces of one strategy is refused: %v", err)
	}
	indexed, err := activationRecordMap(state.Plans)
	if err != nil {
		t.Fatalf("activationRecordMap() refused two pieces: %v", err)
	}
	if len(indexed) != 2 {
		t.Fatalf("activationRecordMap() kept %d of two pieces: one piece overwrote the other", len(indexed))
	}

	twice := state
	twice.Plans = []PlanActivationRecord{pieceRecord(identity, 1, publication), pieceRecord(identity, 1, publication)}
	if err := validateActivationState(twice); err == nil {
		t.Fatal("the same piece twice was accepted as an activation state")
	}
	if _, err := activationRecordMap(twice.Plans); err == nil {
		t.Fatal("activationRecordMap() accepted the same piece twice")
	}

	// A record whose selection names another piece than the record is
	// refused: the two are one coordinate written twice, and have to agree.
	disagree := state
	disagree.Plans = []PlanActivationRecord{pieceRecord(identity, 0, publication)}
	disagree.Plans[0].Fact.Selected.Shard = shardPieceForTest(1)
	if err := validateActivationState(disagree); err == nil {
		t.Fatal("a record whose selection names another piece was accepted")
	}
}

// Records decoded from the same bytes compare equal, and an unchanged piece
// is not reported as changed outside the affected Query Groups. The shard
// is a pointer on the wire; a comparison by address here would refuse every
// activation transition of a split strategy as a change nobody made.
func TestDecodedPieceRecordsCompareByContent(t *testing.T) {
	identity := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	publication := SnapshotPublicationRef{SnapshotRevision: "s1", PublicationEpoch: 1}
	state := ActivationState{
		SchemaVersion: activationSchemaVersion, RecordRevision: 1, Current: publication,
		Plans:          []PlanActivationRecord{pieceRecord(identity, 0, publication), pieceRecord(identity, 1, publication)},
		ActiveQGSetRef: ActiveQueryGroupSetRef{SchemaVersion: activeQueryGroupSetSchemaVersion, Digest: strings.Repeat("d", 64)},
	}
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ActivationState
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := validateUnchangedActivationRecords(state, decoded, map[execution.PlanKey]struct{}{}); err != nil {
		t.Fatalf("an activation re-read from its own bytes reads as changed: %v", err)
	}
	// And a piece that did change outside the affected set is still caught:
	// the comparison is by content, not by nothing.
	decoded.Plans[1].Fact.Selected.StateApplyEpoch = 2
	if err := validateUnchangedActivationRecords(state, decoded, map[execution.PlanKey]struct{}{}); err == nil {
		t.Fatal("a piece changed outside the affected Query Groups was not reported")
	}
}
