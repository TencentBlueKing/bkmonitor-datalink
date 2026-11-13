package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A stream within its share (three quarters of the 100-byte pool, 75) that
// the pool refuses because a sibling holds 60 of it is refused for what
// others hold, RESOURCE_POOL_HELD_BY_OTHERS, with its own, the pool's and the
// others' bytes on the line; the refusal changes nothing it did not hold.
func TestBudgetRejectionLogsSnapshotWithoutMutatingSibling(t *testing.T) {
	var output bytes.Buffer
	limiter, _ := observability.NewScopedLogLimiter(observability.ScopedLogLimiterConfig{Window: time.Hour, MaxEvents: 10, MaxScopes: 1024})
	policy, _ := observability.NewScopedBoundedLogPolicy(limiter)
	co := &SlotExecutionCoordinator{budget: ProvisionalBudget{MaxSeries: 10, MaxRetainedBytes: 100}, ports: Ports{Observer: observability.NewLoggingObserver(observability.New("alarmd", &output), policy)}}
	sibling := &streamedExecution{coordinator: co, began: true}
	if err := sibling.reserveProvisional(context.Background(), 1, 60); err != nil {
		t.Fatal(err)
	}
	sibling.series, sibling.retainedByPhase = 1, retainedSeed(60)
	defer sibling.releaseProvisional()
	failing := &streamedExecution{coordinator: co, began: true, request: execution.SlotExecutionRequest{Operation: execution.OperationNormal}}
	err := failing.reserveProvisional(context.Background(), 1, 50)
	var exceeded *provisionalBudgetExceededError
	if !errors.As(err, &exceeded) || err.Error() != "alarmd worker: provisional retained_bytes budget exceeded" ||
		!exceeded.others || exceeded.share || !PoolHeldByOthers(err) {
		t.Fatalf("error=%v (others %t), want the pool's refusal for what others hold", err, exceeded != nil && exceeded.others)
	}
	if co.reservations.series != 1 || co.reservations.retainedBytes != 60 || failing.retainedTotal() != 0 {
		t.Fatal("failed reservation mutated state")
	}
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{"capacity_phase": "normal_input", "capacity_own_used": float64(0), "capacity_shared_used": float64(60),
		"capacity_others_used": float64(60), "capacity_requested": float64(50), "capacity_limit": float64(100),
		"reason_code": "RESOURCE_POOL_HELD_BY_OTHERS"} {
		if event[key] != want {
			t.Errorf("%s=%v want %v", key, event[key], want)
		}
	}
	// A rejected stream cannot consume capacity needed by a healthy sibling:
	// the sibling takes the rest of its own share.
	if err := sibling.reserveProvisional(context.Background(), 1, 15); err != nil {
		t.Fatal(err)
	}
	sibling.series++
	sibling.retainBytes(retainPhaseInput, 15)
	sibling.releaseProvisional()
	if co.reservations.series != 0 || co.reservations.retainedBytes != 0 {
		t.Fatal("sibling completion did not release")
	}
	if exceeded.facts.SharedUsed != 60 || *exceeded.facts.OwnUsed != 0 {
		t.Fatal("rejection snapshot drifted after sibling release")
	}
}

func TestBudgetRejectionOutputAndQueryFreeOwnership(t *testing.T) {
	for _, normal := range []bool{true, false} {
		co := &SlotExecutionCoordinator{budget: ProvisionalBudget{MaxSeries: 10, MaxRetainedBytes: 100, MaxStateMutations: 10, MaxEvents: 10, MaxGapMutations: 10}}
		co.reservations.retainedBytes = 80
		// Twenty rather than thirty: one Query Group may hold half the pool, so
		// at thirty this execution crosses its own share and the shared-pool
		// rejection this case is about never happens.
		stream := &streamedExecution{coordinator: co, began: normal, retainedByPhase: retainedSeed(20)}
		err := co.acquireEffects(effectCounts{states: 1}, 25, stream, stream.reservationPhase("normal_output"))
		var exceeded *provisionalBudgetExceededError
		if !errors.As(err, &exceeded) {
			t.Fatal(err)
		}
		facts := exceeded.facts
		if facts.SharedUsed != 80 || facts.Requested != 25 || facts.Limit != 100 {
			t.Fatalf("facts=%+v", facts)
		}
		if normal {
			if facts.Phase != "normal_output" || facts.OwnUsed == nil || *facts.OwnUsed != 20 {
				t.Fatalf("normal=%+v", facts)
			}
		} else if facts.Phase != "query_free" || facts.OwnUsed != nil {
			t.Fatalf("nested owner exposed as entire execution: %+v", facts)
		}
		if co.reservations.retainedBytes != 80 || co.reservations.states != 0 {
			t.Fatal("rejection mutated reservations")
		}
	}
}

// A gap fact within the Slot's share that the pool has no room for - others
// hold all of it but 100 bytes - is refused with the pool as it stood: for a
// Slot that began, for what others hold (its own, the pool's and the
// others' bytes); for a query-free owner, whose usage is not the whole
// execution's, as the pool's own refusal with no usage of its own.
func TestGapLoadBudgetRejectionUsesFailedReservationSnapshot(t *testing.T) {
	for _, normal := range []bool{true, false} {
		co, store, request := gapReservationFixture()
		co.budget.MaxRetainedBytes = 1 << 20
		others := co.budget.MaxRetainedBytes - 100
		co.reservations.retainedBytes = others
		stream := &streamedExecution{coordinator: co, began: normal}
		_, err := stream.loadGapFacts(context.Background(), request)
		var exceeded *provisionalBudgetExceededError
		if !errors.As(err, &exceeded) || exceeded.facts == nil || exceeded.share {
			t.Fatal(err)
		}
		f := exceeded.facts
		if f.SharedUsed != others || f.Requested <= 100 || f.Limit != co.budget.MaxRetainedBytes {
			t.Fatalf("facts=%+v", f)
		}
		if normal {
			if f.Phase != "normal_gap" || f.OwnUsed == nil || *f.OwnUsed != 0 || !exceeded.others ||
				f.OthersUsed == nil || *f.OthersUsed != others {
				t.Fatalf("facts=%+v", f)
			}
		} else if f.Phase != "query_free" || f.OwnUsed != nil || exceeded.others {
			t.Fatalf("facts=%+v", f)
		}
		if store.reads != 1 || co.reservations.gapFacts != 0 || co.reservations.retainedBytes != others || len(stream.gaps.Items) != 0 {
			t.Fatal("failed Gap reservation mutated ownership")
		}
	}
}
