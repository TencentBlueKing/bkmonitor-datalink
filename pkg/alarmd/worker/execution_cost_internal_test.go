package worker

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestIncrementalEffectsKeepLoadedFactsAndRejectedMergeSeparate(t *testing.T) {
	co, _, request := gapReservationFixture()
	co.budget.MaxStateMutations = 2
	stream := &streamedExecution{coordinator: co}
	if _, err := stream.loadGapFacts(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	first := sideEffectTestResult("state", "qg")
	if err := stream.mergeProvisional(context.Background(), first, 0); err != nil {
		t.Fatal(err)
	}
	beforeBytes := stream.retained
	tooMany := sideEffectTestResult("state", "qg")
	tooMany.Plans[0].StateResults = append(tooMany.Plans[0].StateResults, execution.StateEvaluation{})
	if err := stream.mergeProvisional(context.Background(), tooMany, 100); err == nil {
		t.Fatal("local limit accepted")
	}
	if stream.effects.states != 1 || co.reservations.states != 1 || stream.retained != beforeBytes || len(stream.evaluated.Plans[0].StateResults) != 1 {
		t.Fatal("failed reservation mutated owner")
	}
	if err := stream.mergeProvisional(context.Background(), first, 0); err != nil {
		t.Fatal(err)
	}
	if stream.effects != countEffects(stream.evaluated) || stream.gapFacts != 1 || co.reservations.gapFacts != 1 || co.reservations.gaps != 0 {
		t.Fatal("facts and effects conflated")
	}
	stream.releaseProvisional()
	stream.releaseProvisional()
	if co.reservations.states != 0 || co.reservations.gapFacts != 0 || co.reservations.retainedBytes != 0 {
		t.Fatal("release leaked")
	}
}

func TestIncrementalEffectsPreserveGapIdentityAndDigest(t *testing.T) {
	co := &SlotExecutionCoordinator{budget: sideEffectTestBudget("state")}
	stream := &streamedExecution{coordinator: co}
	defer stream.releaseProvisional()
	first := sideEffectTestResult("gap", "qg")
	first.Plans[0].GuardBeforeEvents[0].MutationDigest = "one"
	if err := stream.mergeProvisional(context.Background(), first, 0); err != nil {
		t.Fatal(err)
	}
	next := sideEffectTestResult("gap", "qg")
	one := first.Plans[0].GuardBeforeEvents[0]
	two := one
	two.MutationDigest = "two"
	other := one
	other.Identity.StateGeneration = "another"
	next.Plans[0].GuardBeforeEvents = []execution.PlanGapMutation{one, one, two, two, other}
	if err := stream.mergeProvisional(context.Background(), next, 0); err != nil {
		t.Fatal(err)
	}
	if stream.effects.gaps != 3 || !reflect.DeepEqual(stream.evaluated.Plans[0].GuardBeforeEvents, []execution.PlanGapMutation{one, two, other}) {
		t.Fatal("identity/digest dedup changed")
	}
	if stream.effects != countEffects(stream.evaluated) {
		t.Fatal("increment diverged")
	}
}

func TestStatePreflightIndexPreservesFindAndCallerData(t *testing.T) {
	id := execution.StateKeyIdentity{Plan: execution.PlanIdentity{TenantID: "tenant", BusinessID: "1", StrategyID: "2"}, StateGeneration: "generation", SeriesIdentityDigest: "series"}
	result := execution.StatePreflightResult{Items: []execution.RuntimeStateView{{Identity: id, BlobRevision: 1}, {Identity: id, BlobRevision: 2}}}
	for _, change := range []func(*execution.StateKeyIdentity){
		func(i *execution.StateKeyIdentity) { i.Plan.TenantID = "other" }, func(i *execution.StateKeyIdentity) { i.Plan.BusinessID = "other" }, func(i *execution.StateKeyIdentity) { i.Plan.StrategyID = "other" }, func(i *execution.StateKeyIdentity) { i.StateGeneration = "other" }, func(i *execution.StateKeyIdentity) { i.SeriesIdentityDigest = "other" },
	} {
		other := id
		change(&other)
		result.Items = append(result.Items, execution.RuntimeStateView{Identity: other, BlobRevision: 3})
	}
	before := append([]execution.RuntimeStateView(nil), result.Items...)
	index := indexStatePreflight(result)
	for _, item := range result.Items {
		old, ok := result.Find(item.Identity)
		position, found := index[item.Identity]
		if ok != found || !reflect.DeepEqual(old, result.Items[position]) {
			t.Fatal("first-match semantics changed")
		}
	}
	if _, found := index[execution.StateKeyIdentity{}]; found {
		t.Fatal("missing identity invented")
	}
	if !reflect.DeepEqual(before, result.Items) {
		t.Fatal("caller data changed")
	}
}

func BenchmarkStatePreflightIndex(b *testing.B) {
	for _, size := range []int{4065, 8192} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			result := execution.StatePreflightResult{Items: make([]execution.RuntimeStateView, size)}
			for i := range result.Items {
				result.Items[i].Identity.SeriesIdentityDigest = execution.SeriesIdentityDigest(fmt.Sprint(i))
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				index := indexStatePreflight(result)
				for _, item := range result.Items {
					if _, ok := index[item.Identity]; !ok {
						b.Fatal("missing")
					}
				}
			}
		})
	}
}
