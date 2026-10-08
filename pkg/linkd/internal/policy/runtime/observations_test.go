// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	"linkd/internal/policy"
)

type blockedStatistics struct{ entered chan struct{} }

func (s blockedStatistics) RecordPolicyObservation(ctx context.Context, _ policy.Scope, _, _ string, _ time.Time) error {
	s.entered <- struct{}{}
	<-ctx.Done()
	return ctx.Err()
}

type observationMetrics struct {
	mu     sync.Mutex
	values map[string]int
}

func (m *observationMetrics) ObservePolicyMatch(context.Context, string, string, time.Duration) {}

func (m *observationMetrics) ObservePolicyDelay(context.Context, string, time.Duration) {}

func (m *observationMetrics) ObservePolicySample(_ context.Context, key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[key]++
}

func TestStatisticsAreBoundedAsynchronousAndCloseDrains(t *testing.T) {
	metrics := &observationMetrics{values: map[string]int{}}
	writer := blockedStatistics{entered: make(chan struct{}, 4)}
	o := NewObservations(writer, metrics)
	scope := policy.Scope{TenantID: "tenant", Kind: policy.Merge}
	for range 4 {
		o.record(t.Context(), scope, "p", "matched")
	}
	for range 4 {
		select {
		case <-writer.entered:
		case <-time.After(time.Second):
			t.Fatal("writer not started")
		}
	}
	o.record(t.Context(), scope, "p", "matched")
	o.Close()
	o.Close()
	o.record(t.Context(), scope, "p", "matched")
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	if metrics.values["dropped"] != 1 || metrics.values["failed"] != 4 || len(o.slots) != 0 {
		t.Fatalf("observation lifecycle %+v", metrics.values)
	}
}

func TestStatisticsCloseCanRaceWithSubmission(t *testing.T) {
	o := NewObservations(blockedStatistics{entered: make(chan struct{}, 4)}, nil)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 20 {
				o.record(t.Context(), policy.Scope{TenantID: "t", Kind: policy.Merge}, "p", "matched")
			}
		})
	}
	wg.Go(o.Close)
	wg.Wait()
	o.Close()
	if len(o.slots) != 0 {
		t.Fatal("inflight leaked")
	}
}
