package worker

import (
	"context"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// PreparationByteAdmission binds preparation to the existing Coordinator account.
func PreparationByteAdmission(coordinator *SlotExecutionCoordinator) func(context.Context, uint64) (func(), error) {
	return coordinator.reservePreparationBytes
}

// reservePreparationBytes uses the same process account as input and output
// retention. It never waits while a caller may already own part of that pool.
// The owner must drop its references before releasing the reservation.
func (coordinator *SlotExecutionCoordinator) reservePreparationBytes(ctx context.Context, size uint64) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := coordinator.acquireProvisional(0, size, nil, "snapshot_prepare"); err != nil {
		coordinator.observeCapacityRejection(ctx, "", observability.CapacityBudgetRetainedBytes, err)
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { coordinator.releaseProvisional(0, size) }) }, nil
}

// PreparationObjectBytes applies the existing DTO bookkeeping convention;
// allocator rounding, transient decoder garbage and GC are measured separately.
func PreparationObjectBytes(value any) uint64 { return retainedObjectBytes(value) }
