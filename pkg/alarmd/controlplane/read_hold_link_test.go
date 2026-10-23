package controlplane

import (
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A moved Plan is linked when its state generation did not change, so the
// two groups' Slots keep their order on the one state they write; a
// generation that changed starts a state of its own and is not linked; one
// that cannot be read is linked to keep the order. A carried link that names
// the group it is in, or is past its lifetime, is not carried on.
func TestTheCutoverLinksAPlanByItsStateGenerationAndDropsSelfAndExpiredLinks(t *testing.T) {
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "101"}
	record := func(generation execution.StateGeneration) PlanActivationRecord {
		return PlanActivationRecord{Fact: execution.PlanActivationFact{Plan: plan, Selection: execution.ActivationCurrent,
			Selected: execution.ActivatedPlan{Identity: plan, StateGeneration: generation}}}
	}
	key := record("").Fact.Key()
	ref := ReadHoldPredecessorRef{QueryGroup: "old", ClosedAt: 1200, PreviousSlot: 1140, CompletionOffsetMillis: 55_000}
	for _, tc := range []struct {
		name     string
		origin   *readHoldOrigin
		carried  *ReadHoldPredecessorRef
		group    execution.QueryGroupIdentity
		next     execution.StateGeneration
		link     *ReadHoldPredecessorRef
		decision string
	}{
		{"same generation", &readHoldOrigin{ref: ref, generation: "g1"}, nil, "new", "g1", &ref, "linked"},
		{"generation changed", &readHoldOrigin{ref: ref, generation: "g1"}, nil, "new", "g2", nil, "generation_changed"},
		{"generation unknown", &readHoldOrigin{ref: ref}, nil, "new", "g1", &ref, "linked_generation_unknown"},
		{"carried link to itself", &readHoldOrigin{ref: ref, generation: "g1"}, &ref, "old", "g1", nil, "dropped_self"},
		{"carried link expired", nil, &ref, "new", "g1", nil, "dropped_expired"},
		{"carried link kept", nil, &ReadHoldPredecessorRef{QueryGroup: "old", ClosedAt: 5000}, "new", "g1",
			&ReadHoldPredecessorRef{QueryGroup: "old", ClosedAt: 5000}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, count := range []bool{true, false} {
				facts := newCutoverFacts()
				linker := readHoldLinker{origins: map[execution.PlanKey]readHoldOrigin{}, carried: map[execution.PlanKey]PlanActivationRecord{},
					expiredBefore: 1300, facts: facts}
				if tc.origin != nil {
					linker.origins[key] = *tc.origin
				}
				if tc.carried != nil {
					carried := record("g1")
					carried.PreviousReadHold = tc.carried
					linker.carried[key] = carried
				}
				records := []PlanActivationRecord{record(tc.next)}
				linker.carry(records, tc.group, count)
				if !reflect.DeepEqual(records[0].PreviousReadHold, tc.link) {
					t.Fatalf("link %+v, want %+v", records[0].PreviousReadHold, tc.link)
				}
				want := map[string]int{}
				if count && tc.decision != "" {
					want[tc.decision] = 1
				}
				if !reflect.DeepEqual(facts.readHoldLinks, want) {
					t.Fatalf("counted %v with count=%t, want %v", facts.readHoldLinks, count, want)
				}
			}
		})
	}
}
