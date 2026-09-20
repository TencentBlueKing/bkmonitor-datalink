package worker

import (
	"context"
	"errors"
	"testing"
)

func TestPreparationSharesRetainedAccountWithoutWaiting(t *testing.T) {
	co := &SlotExecutionCoordinator{budget: ProvisionalBudget{MaxSeries: 10, MaxRetainedBytes: 100}}
	release, err := co.reservePreparationBytes(context.Background(), 60)
	if err != nil {
		t.Fatal(err)
	}
	if err := co.acquireProvisional(1, 30, nil, "normal_input"); err != nil {
		t.Fatal(err)
	}
	_, err = co.reservePreparationBytes(context.Background(), 11)
	var exceeded *provisionalBudgetExceededError
	if !errors.As(err, &exceeded) || co.reservations.retainedBytes != 90 {
		t.Fatalf("failed incremental reservation mutated account: bytes=%d err=%v", co.reservations.retainedBytes, err)
	}
	release()
	release()
	if co.reservations.retainedBytes != 30 {
		t.Fatal(co.reservations.retainedBytes)
	}
	co.releaseProvisional(1, 30)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := co.reservePreparationBytes(ctx, 50); !errors.Is(err, context.Canceled) || co.reservations.retainedBytes != 0 {
		t.Fatalf("cancelled preparation retained bytes: %d %v", co.reservations.retainedBytes, err)
	}
}
