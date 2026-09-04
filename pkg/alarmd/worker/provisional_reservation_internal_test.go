package worker

import (
	"sync"
	"testing"
)

func TestProcessProvisionalReservationCapsConcurrentSlotsAndReleases(t *testing.T) {
	tests := []struct {
		name       string
		budget     ProvisionalBudget
		series     uint64
		retained   uint64
		wantSeries uint64
		wantBytes  uint64
	}{
		{name: "series", budget: ProvisionalBudget{MaxSeries: 1, MaxRetainedBytes: 1_000},
			series: 1, retained: 100, wantSeries: 1, wantBytes: 100},
		{name: "retained bytes", budget: ProvisionalBudget{MaxSeries: 10, MaxRetainedBytes: 150},
			series: 1, retained: 100, wantSeries: 1, wantBytes: 100},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			coordinator := &SlotExecutionCoordinator{budget: test.budget}
			start := make(chan struct{})
			results := make(chan error, 2)
			var waiting sync.WaitGroup
			waiting.Add(2)
			for range 2 {
				go func() {
					waiting.Done()
					<-start
					results <- coordinator.acquireProvisional(test.series, test.retained)
				}()
			}
			waiting.Wait()
			close(start)
			var accepted, rejected int
			for range 2 {
				if err := <-results; err != nil {
					rejected++
				} else {
					accepted++
				}
			}
			if accepted != 1 || rejected != 1 {
				t.Fatalf("concurrent reservations accepted/rejected=%d/%d, want 1/1", accepted, rejected)
			}
			coordinator.reservations.mu.Lock()
			series, retained := coordinator.reservations.series, coordinator.reservations.retainedBytes
			coordinator.reservations.mu.Unlock()
			if series != test.wantSeries || retained != test.wantBytes {
				t.Fatalf("reserved series/bytes=%d/%d, want %d/%d", series, retained, test.wantSeries, test.wantBytes)
			}

			coordinator.releaseProvisional(test.series, test.retained)
			if err := coordinator.acquireProvisional(test.series, test.retained); err != nil {
				t.Fatalf("reservation after release failed: %v", err)
			}
			coordinator.releaseProvisional(test.series, test.retained)
			coordinator.reservations.mu.Lock()
			series, retained = coordinator.reservations.series, coordinator.reservations.retainedBytes
			coordinator.reservations.mu.Unlock()
			if series != 0 || retained != 0 {
				t.Fatalf("released reservation leaked series/bytes=%d/%d", series, retained)
			}
		})
	}
}
