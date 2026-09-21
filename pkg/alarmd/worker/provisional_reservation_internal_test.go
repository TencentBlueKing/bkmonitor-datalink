// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
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
					results <- coordinator.acquireProvisional(test.series, test.retained, nil, "query_free")
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
			if err := coordinator.acquireProvisional(test.series, test.retained, nil, "query_free"); err != nil {
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

func TestStreamedExecutionReleaseProvisionalIsIdempotentAndReusable(t *testing.T) {
	coordinator := &SlotExecutionCoordinator{budget: ProvisionalBudget{MaxSeries: 1, MaxRetainedBytes: 100}}
	stream := &streamedExecution{coordinator: coordinator}
	if err := stream.reserveProvisional(context.Background(), 1, 100); err != nil {
		t.Fatal(err)
	}
	stream.series, stream.retained = 1, 100
	stream.releaseProvisional()
	stream.releaseProvisional()

	coordinator.reservations.mu.Lock()
	series, retained := coordinator.reservations.series, coordinator.reservations.retainedBytes
	coordinator.reservations.mu.Unlock()
	if series != 0 || retained != 0 {
		t.Fatalf("repeated stream release leaked or underflowed series/bytes=%d/%d", series, retained)
	}
	if err := coordinator.acquireProvisional(1, 100, nil, "query_free"); err != nil {
		t.Fatalf("budget is not reusable after repeated stream release: %v", err)
	}
	coordinator.releaseProvisional(1, 100)
}

func TestStreamedExecutionReleasesAcceptedReservationAfterLaterBudgetRejection(t *testing.T) {
	coordinator := &SlotExecutionCoordinator{budget: ProvisionalBudget{MaxSeries: 10, MaxRetainedBytes: 1_000}}
	stream := &streamedExecution{coordinator: coordinator}
	if err := stream.reserveProvisional(context.Background(), 1, 600); err != nil {
		t.Fatal(err)
	}
	stream.series, stream.retained = 1, 600
	if err := stream.reserveProvisional(context.Background(), 1, 600); err == nil {
		t.Fatal("second reservation unexpectedly fit the retained-byte budget")
	}

	stream.releaseProvisional()
	coordinator.reservations.mu.Lock()
	series, retained := coordinator.reservations.series, coordinator.reservations.retainedBytes
	coordinator.reservations.mu.Unlock()
	if series != 0 || retained != 0 {
		t.Fatalf("reservation after rejection=%d/%d, want 0/0", series, retained)
	}
}
