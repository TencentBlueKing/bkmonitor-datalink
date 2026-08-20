// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"strings"
	"sync"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/lifecycle"
)

func TestLifecycleCollectorPullsOneConsistentSnapshot(t *testing.T) {
	t.Parallel()

	source := &mutableLifecycleSource{snapshot: lifecycle.Snapshot{
		Ready: true, AssignedClaims: 2, FatalTotal: 1, Draining: true,
		DrainTotal:         [lifecycle.DrainResultCount]uint64{1, 2, 3, 4},
		InflightRecords:    3,
		ConsumerLagRecords: 17,
		ConsumerLagKnown:   true,
	}}
	recorder := NewRecorder(BuildInfo{})
	if err := recorder.BindLifecycle(source); err != nil {
		t.Fatalf("BindLifecycle() error = %v", err)
	}
	got := scrape(t, recorder)
	if calls := source.Calls(); calls != 1 {
		t.Fatalf("LifecycleSnapshot() calls after one scrape = %d, want 1", calls)
	}
	for _, want := range []string{
		"bkmonitor_alarm_engine_ready 1",
		"bkmonitor_alarm_engine_assigned_claims 2",
		"bkmonitor_alarm_engine_fatal_total 1",
		"bkmonitor_alarm_engine_draining 1",
		`bkmonitor_alarm_engine_drain_total{result="success"} 1`,
		`bkmonitor_alarm_engine_drain_total{result="timeout"} 2`,
		`bkmonitor_alarm_engine_drain_total{result="failed"} 3`,
		`bkmonitor_alarm_engine_drain_total{result="_other"} 4`,
		"bkmonitor_alarm_engine_inflight_records 3",
		"bkmonitor_alarm_engine_consumer_lag_records 17",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("metrics output does not contain %q:\n%s", want, got)
		}
	}

	source.Set(lifecycle.Snapshot{
		AssignedClaims: 1, FatalTotal: 1,
		DrainTotal: [lifecycle.DrainResultCount]uint64{1, 2, 3, 4},
	})
	got = scrape(t, recorder)
	if calls := source.Calls(); calls != 2 {
		t.Fatalf("LifecycleSnapshot() calls after two scrapes = %d, want 2", calls)
	}
	if strings.Contains(got, "bkmonitor_alarm_engine_consumer_lag_records") {
		t.Fatalf("unknown lag was exported as a real series:\n%s", got)
	}
	if !strings.Contains(got, "bkmonitor_alarm_engine_ready 0") || !strings.Contains(got, "bkmonitor_alarm_engine_assigned_claims 1") {
		t.Fatalf("collector did not pull the updated snapshot:\n%s", got)
	}
}

func TestLifecycleBindingIsRequiredAndSingleUse(t *testing.T) {
	t.Parallel()

	recorder := NewRecorder(BuildInfo{})
	if err := recorder.BindLifecycle(nil); err == nil {
		t.Fatal("BindLifecycle(nil) returned nil")
	}
	if err := recorder.BindLifecycle(&mutableLifecycleSource{}); err != nil {
		t.Fatalf("first BindLifecycle() error = %v", err)
	}
	if err := recorder.BindLifecycle(&mutableLifecycleSource{}); err == nil {
		t.Fatal("second BindLifecycle() returned nil")
	}
}

func TestLifecycleCollectorOmitsInvalidNegativeGauges(t *testing.T) {
	t.Parallel()

	recorder := NewRecorder(BuildInfo{})
	if err := recorder.BindLifecycle(&mutableLifecycleSource{snapshot: lifecycle.Snapshot{
		AssignedClaims: -1, InflightRecords: -1, ConsumerLagRecords: -1, ConsumerLagKnown: true,
	}}); err != nil {
		t.Fatalf("BindLifecycle() error = %v", err)
	}
	got := scrape(t, recorder)
	for _, name := range []string{
		"bkmonitor_alarm_engine_assigned_claims",
		"bkmonitor_alarm_engine_inflight_records",
		"bkmonitor_alarm_engine_consumer_lag_records",
	} {
		if strings.Contains(got, name) {
			t.Fatalf("invalid negative gauge %s was exported:\n%s", name, got)
		}
	}
}

type mutableLifecycleSource struct {
	mu       sync.Mutex
	snapshot lifecycle.Snapshot
	calls    int
}

func (s *mutableLifecycleSource) LifecycleSnapshot() lifecycle.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.snapshot
}

func (s *mutableLifecycleSource) Set(snapshot lifecycle.Snapshot) {
	s.mu.Lock()
	s.snapshot = snapshot
	s.mu.Unlock()
}

func (s *mutableLifecycleSource) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}
