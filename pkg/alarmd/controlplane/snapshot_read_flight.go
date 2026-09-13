package controlplane

import (
	"context"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// snapshotReadFlight is one complete Snapshot body read, verified and placed
// in the revision cache, that every per-Slot caller of this process needing
// the same revision shares while it is in progress.
//
// A follower's workers all miss the revision cache in the same minute after
// the leader publishes a new revision, and each of them used to read the whole
// body for itself. A hundred-odd concurrent reads of one multi-megabyte value
// saturate the Redis egress, every read overruns the client read timeout, the
// client retries each of them, the Slot layer requeues each of them with a
// fresh complete read, and the burst sustains itself for minutes. The verified
// cache did not prevent it: its mutex serialises the decode, but only after
// each caller has already pulled its own copy over the network.
//
// The flight coalesces the read, not the outcome. It is dropped the moment it
// completes, so a failure is never handed to a caller that arrived after it
// and the next caller starts a fresh read. That keeps the cache's standing
// promise that Redis availability and publication authority are never cached.
// The epoch a taker receives was observed by a read that was in progress for
// the whole of the taker's wait, which is no older than what a read the taker
// issued itself would have observed.
//
// The read runs to completion for whoever is still waiting: a caller whose
// context ends leaves without failing the others, and a read nobody waits for
// any more is cancelled rather than finished for no one. Running the read under
// the first caller's context instead was rejected because that caller's Slot
// deadline would then decide the fate of every sibling behind it.
//
// Client-level retries are left as they are. A retried read is sequential
// inside the one flight this process keeps per revision, so after coalescing
// the retry bound is one body in flight per process per revision and retries
// no longer multiply concurrency; they only lengthen the single flight while
// the client waits out a transient connection failure that is worth waiting
// out for a read of this size. Disabling them for this read alone would need a
// second client or a context deadline copied from the client options, neither
// of which this repository can see.
//
// The flight also performs the decode and cache insertion rather than leaving
// them to its takers. Otherwise a caller arriving between the read completing
// and the first taker inserting the entry would find neither a flight nor a
// cached revision and start a second complete read.
type snapshotReadFlight struct {
	revision execution.SnapshotRevision
	done     chan struct{}
	// cancel stops the read once no caller is left to take its result.
	cancel context.CancelFunc
	// waiting counts callers still going to take the result; guarded by the
	// owning table's mutex. Reaching zero before completion cancels the read.
	// Reaching zero after completion drops the flight's own reference to the
	// allocation, which each taker retained for itself while leaving.
	waiting   int
	completed bool

	payload    string
	epoch      uint64
	allocation *snapshotAllocation
	err        error
}

type snapshotReadFlights struct {
	mu         sync.Mutex
	byRevision map[execution.SnapshotRevision]*snapshotReadFlight
}

// loadSharedSnapshotPayload joins the complete read of revision already in
// progress in this process, or starts one. The result carries one allocation
// reference for the caller, exactly as loadAdmittedSnapshotPayload does.
func (repository *RedisCatalogRepository) loadSharedSnapshotPayload(
	ctx context.Context,
	revision execution.SnapshotRevision,
) (string, uint64, *snapshotAllocation, error) {
	flights := &repository.snapshotFlights
	flights.mu.Lock()
	flight := flights.byRevision[revision]
	if flight == nil {
		flight = repository.startSnapshotReadFlight(ctx, revision)
	} else {
		repository.controlReads.snapshot.shared.Add(1)
	}
	flight.waiting++
	flights.mu.Unlock()

	select {
	case <-flight.done:
	case <-ctx.Done():
		flights.mu.Lock()
		flights.leave(flight)
		flights.mu.Unlock()
		return "", 0, nil, ctx.Err()
	}
	flights.mu.Lock()
	defer flights.mu.Unlock()
	// Taken before leaving: the last taker's leave drops the flight's own
	// reference and forgets the allocation.
	payload, epoch, allocation, err := flight.payload, flight.epoch, flight.allocation, flight.err
	allocation.retain()
	flights.leave(flight)
	return payload, epoch, allocation, err
}

// startSnapshotReadFlight registers and starts the read for revision. The
// table mutex must be held.
func (repository *RedisCatalogRepository) startSnapshotReadFlight(
	ctx context.Context,
	revision execution.SnapshotRevision,
) *snapshotReadFlight {
	flights := &repository.snapshotFlights
	if flights.byRevision == nil {
		flights.byRevision = make(map[execution.SnapshotRevision]*snapshotReadFlight)
	}
	// Values such as the trace fields stay with the read; only the first
	// caller's cancellation is detached, because the read belongs to everyone
	// who joins it.
	readCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	flight := &snapshotReadFlight{revision: revision, done: make(chan struct{}), cancel: cancel}
	flights.byRevision[revision] = flight
	go func() {
		payload, epoch, allocation, err := repository.loadAdmittedSnapshotPayload(readCtx, revision)
		if err == nil {
			var entry verifiedSnapshotCacheEntry
			entry, _, err = repository.snapshotCache.load(readCtx, revision, payload, epoch, allocation)
			entry.allocation.release()
			if err != nil {
				allocation.release()
				payload, epoch, allocation = "", 0, nil
			}
		}
		cancel()
		flights.mu.Lock()
		defer flights.mu.Unlock()
		flights.retire(flight)
		flight.payload, flight.epoch, flight.allocation, flight.err = payload, epoch, allocation, err
		flight.completed = true
		close(flight.done)
		if flight.waiting == 0 {
			flight.allocation = nil
			allocation.release()
		}
	}()
	return flight
}

// leave records that one caller will not take the flight's result. The table
// mutex must be held.
func (flights *snapshotReadFlights) leave(flight *snapshotReadFlight) {
	flight.waiting--
	if flight.waiting != 0 {
		return
	}
	if !flight.completed {
		// Nobody will take this read, so nobody may join it either: a caller
		// arriving now would otherwise inherit a cancellation that was never
		// about Redis. It leaves the table before it finishes so that caller
		// starts its own read.
		flights.retire(flight)
		flight.cancel()
		return
	}
	allocation := flight.allocation
	flight.allocation = nil
	allocation.release()
}

// retire removes flight from the table if the table still refers to it. The
// table mutex must be held.
func (flights *snapshotReadFlights) retire(flight *snapshotReadFlight) {
	if flights.byRevision[flight.revision] == flight {
		delete(flights.byRevision, flight.revision)
	}
}
