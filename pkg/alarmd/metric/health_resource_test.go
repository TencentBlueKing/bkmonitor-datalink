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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestHealthAndResourceCollectorsUseBoundedSnapshots(t *testing.T) {
	t.Parallel()

	now := time.Unix(123, 0)
	health := observability.NewHealthTracker(observability.HealthSnapshot{
		State:             observability.HealthDegraded,
		ConfigLoaded:      true,
		SchemaReady:       true,
		AssignmentReady:   true,
		RuntimeStateReady: true,
		OutputSinkReady:   true,
		Reasons: []observability.ReasonCode{
			observability.ReasonWorkerQueue,
			observability.ReasonCode(contract.ReasonKafkaUnavailable),
		},
		AssignedClaims:     2,
		InflightMessages:   3,
		WorkerQueueDepth:   4,
		WorkerQueueBytes:   5,
		ConsumerLagKnown:   true,
		ConsumerLagRecords: 6,
		LastProgressStage:  observability.StageTriggerCompleted,
		LastProgressAt:     now,
		LastRecoveryAt:     now,
	})
	resources, err := observability.NewResourceGovernor(observability.ResourceGovernorConfig{})
	if err != nil {
		t.Fatalf("NewResourceGovernor() error = %v", err)
	}
	resources.Observe(observability.ResourceSnapshot{
		ObservedAt: now, CPUCores: 1.5, RSSBytes: 100, HeapBytes: 80,
		GCPauseSeconds: 0.1, WorkerQueueDepth: 4, WorkerQueueBytes: 50,
		InflightMessages: 3, InflightBytes: 30, ConsumerLagRecords: 6, StateBytes: 70,
	})

	recorder := NewRecorder(BuildInfo{})
	if err := recorder.BindHealth(health); err != nil {
		t.Fatalf("BindHealth() error = %v", err)
	}
	if err := recorder.BindResources(resources); err != nil {
		t.Fatalf("BindResources() error = %v", err)
	}
	got := scrape(t, recorder)
	for _, want := range []string{
		"bkmonitor_alarmd_health_ready 1",
		`bkmonitor_alarmd_health_state{health_state="degraded"} 1`,
		`bkmonitor_alarmd_health_reason{reason_code="resource_worker_queue"} 1`,
		`bkmonitor_alarmd_health_reason{reason_code="contract_retryable"} 1`,
		"bkmonitor_alarmd_health_consumer_lag_records 6",
		`bkmonitor_alarmd_health_last_progress_timestamp_seconds{stage="trigger_completed"} 123`,
		`bkmonitor_alarmd_resource_state{resource_state="normal"} 1`,
		"bkmonitor_alarmd_resource_cpu_cores 1.5",
		"bkmonitor_alarmd_resource_state_bytes 70",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("metrics output does not contain %q:\n%s", want, got)
		}
	}
}

func TestHealthAndResourceBindingsAreRequiredAndSingleUse(t *testing.T) {
	t.Parallel()

	recorder := NewRecorder(BuildInfo{})
	if err := recorder.BindHealth(nil); err == nil {
		t.Fatal("BindHealth(nil) returned nil")
	}
	if err := recorder.BindResources(nil); err == nil {
		t.Fatal("BindResources(nil) returned nil")
	}
	health := observability.NewHealthTracker(observability.HealthSnapshot{})
	if err := recorder.BindHealth(health); err != nil {
		t.Fatalf("first BindHealth() error = %v", err)
	}
	if err := recorder.BindHealth(health); err == nil {
		t.Fatal("second BindHealth() returned nil")
	}
	resources, err := observability.NewResourceGovernor(observability.ResourceGovernorConfig{})
	if err != nil {
		t.Fatalf("NewResourceGovernor() error = %v", err)
	}
	if err := recorder.BindResources(resources); err != nil {
		t.Fatalf("first BindResources() error = %v", err)
	}
	if err := recorder.BindResources(resources); err == nil {
		t.Fatal("second BindResources() returned nil")
	}
}
