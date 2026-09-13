package progress

import (
	"context"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"testing"
)

func TestBeginCoverageFactOnlyAfterActualCAS(t *testing.T) {
	fake := &controlFake{missing: true}
	store := mustStore(t, fake)
	request := execution.ProgressBeginRequest{Identity: execution.ProgressIdentity{QueryGroup: "q"}, OwnerFence: execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}, Projection: progressProjection()}
	var facts []bool
	ctx := execution.WithSlotCoverageCapture(context.Background(), &execution.SlotCoverageCapture{BeginCommitted: func(prior bool) { facts = append(facts, prior) }})
	fake.status = ownership.FencedCASConflict
	if _, err := store.BeginSlot(ctx, request); err != nil {
		t.Fatal(err)
	}
	if len(facts) != 0 {
		t.Fatal("CAS failure emitted first Slot fact")
	}
	fake.status = ownership.FencedCASApplied
	for i := 0; i < 2; i++ {
		if _, err := store.BeginSlot(ctx, request); err != nil {
			t.Fatal(err)
		}
	}
	if len(facts) != 2 || facts[0] || !facts[1] {
		t.Fatal("new vs unfinished", facts)
	}
	fake.status = ownership.FencedCASStaleOwner
	_, _ = store.BeginSlot(ctx, request)
	if len(facts) != 2 {
		t.Fatal("stale owner emitted")
	}
	lost := false
	ctx = execution.WithSlotCoverageCapture(context.Background(), &execution.SlotCoverageCapture{BeginCommitted: func(bool) { panic("capture") }, Lost: func() { lost = true }})
	fake.status = ownership.FencedCASApplied
	result, err := store.BeginSlot(ctx, request)
	if err != nil || result.Status != execution.ProgressCommitted || !lost {
		t.Fatal("capture changed business CAS", result, err, lost)
	}
}
