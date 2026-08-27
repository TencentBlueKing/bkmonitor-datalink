// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestHealthTrackerNormalizesReadinessAndReasons(t *testing.T) {
	t.Parallel()

	tracker := NewHealthTracker(HealthSnapshot{
		State:             HealthDegraded,
		ConfigLoaded:      true,
		SchemaReady:       true,
		AssignmentReady:   true,
		RuntimeStateReady: true,
		OutputSinkReady:   true,
		Reasons:           []ReasonCode{ReasonWorkerQueue, ReasonCPU, ReasonWorkerQueue, "dynamic"},
	})
	got := tracker.HealthSnapshot()
	if !got.Ready || got.State != HealthDegraded {
		t.Fatalf("health = %#v, want ready degraded", got)
	}
	if want := []ReasonCode{ReasonOther, ReasonCPU, ReasonWorkerQueue}; !reflect.DeepEqual(got.Reasons, want) {
		t.Fatalf("reasons = %#v, want %#v", got.Reasons, want)
	}

	tracker.Update(HealthSnapshot{State: HealthDraining})
	got = tracker.HealthSnapshot()
	if got.Ready || got.State != HealthDraining {
		t.Fatalf("health = %#v, want not-ready draining", got)
	}
}

func TestHealthSnapshotReturnsIndependentReasonSlice(t *testing.T) {
	t.Parallel()

	tracker := NewHealthTracker(HealthSnapshot{
		State: HealthReady, Reasons: []ReasonCode{ReasonNone}, ConfigLoaded: true,
		SchemaReady: true, AssignmentReady: true, RuntimeStateReady: true, OutputSinkReady: true,
	})
	first := tracker.HealthSnapshot()
	first.Reasons[0] = ReasonOther
	second := tracker.HealthSnapshot()
	if second.Reasons[0] != ReasonNone {
		t.Fatalf("stored reasons were mutated through snapshot: %#v", second.Reasons)
	}
}

func TestHealthSnapshotRejectsContradictoryReadyState(t *testing.T) {
	t.Parallel()

	got := NormalizeHealthSnapshot(HealthSnapshot{State: HealthReady, ConfigLoaded: true})
	if got.Ready || got.State != HealthNotReady {
		t.Fatalf("contradictory health = %#v, want not-ready", got)
	}
	if want := []ReasonCode{ReasonInternalUnknown}; !reflect.DeepEqual(got.Reasons, want) {
		t.Fatalf("contradictory reasons = %#v, want %#v", got.Reasons, want)
	}

	got = NormalizeHealthSnapshot(HealthSnapshot{
		State: HealthReady, ConfigLoaded: true, SchemaReady: true, AssignmentReady: true,
		RuntimeStateReady: true, OutputSinkReady: true, ResourceState: ResourceHard,
	})
	if !got.Ready || got.State != HealthReady {
		t.Fatalf("observe-only resource hard changed readiness: %#v", got)
	}
}

func TestHealthSnapshotPreservesKnownM0Reason(t *testing.T) {
	t.Parallel()

	got := NormalizeHealthSnapshot(HealthSnapshot{
		State: HealthNotReady, Reasons: []ReasonCode{ReasonCode(contract.ReasonKafkaUnavailable)},
	})
	if want := []ReasonCode{ReasonCode(contract.ReasonKafkaUnavailable)}; !reflect.DeepEqual(got.Reasons, want) {
		t.Fatalf("health reasons = %#v, want %#v", got.Reasons, want)
	}
}

func TestHealthSnapshotBoundsProgressFields(t *testing.T) {
	t.Parallel()

	now := time.Now()
	tracker := NewHealthTracker(HealthSnapshot{
		State:              HealthReady,
		AssignedClaims:     -2,
		InflightMessages:   -3,
		WorkerQueueDepth:   -4,
		WorkerQueueBytes:   -5,
		ConsumerLagKnown:   true,
		ConsumerLagRecords: -6,
		LastProgressStage:  Stage("dynamic"),
		LastProgressAt:     now,
	})
	got := tracker.HealthSnapshot()
	if got.AssignedClaims != -1 || got.InflightMessages != -1 || got.WorkerQueueDepth != -1 || got.WorkerQueueBytes != -1 {
		t.Fatalf("negative gauges were not normalized: %#v", got)
	}
	if got.ConsumerLagKnown || got.ConsumerLagRecords != -1 {
		t.Fatalf("invalid lag was not normalized: %#v", got)
	}
	if got.LastProgressStage != StageOther || !got.LastProgressAt.Equal(now) {
		t.Fatalf("progress = %q/%v", got.LastProgressStage, got.LastProgressAt)
	}
}
