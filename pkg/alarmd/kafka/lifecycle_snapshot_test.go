// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"context"
	"testing"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lifecycle"
)

func TestAssignmentSnapshotTracksOnlyKnownCurrentGenerationLag(t *testing.T) {
	t.Parallel()

	state := newAssignmentLifecycle()
	session := newFakeSession(context.Background(), &[]string{})
	session.claims = map[string][]int32{"trigger-input": {0, 1}}
	first := newFakeClaim("trigger-input", 0, nil)
	first.highWater = 15
	second := newFakeClaim("trigger-input", 1, nil)
	second.highWater = 40

	if err := state.Setup(session); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if err := state.ClaimInitialized(session, first); err != nil {
		t.Fatalf("ClaimInitialized(first) error = %v", err)
	}
	if err := state.ClaimInitialized(session, second); err != nil {
		t.Fatalf("ClaimInitialized(second) error = %v", err)
	}
	assertAssignmentSnapshot(t, state.Snapshot(), assignmentSnapshot{
		ready: true, assignedClaims: 2,
	})

	if !state.TryBeginObservedRecord(session, first, 10) {
		t.Fatal("TryBeginObservedRecord(first) rejected an active claim")
	}
	state.EndObservedRecord(session, first, 11, true)
	if snapshot := state.Snapshot(); snapshot.consumerLagKnown {
		t.Fatalf("lag became known before every claim had a cursor: %+v", snapshot)
	}

	if !state.TryBeginObservedRecord(session, second, 30) {
		t.Fatal("TryBeginObservedRecord(second) rejected an active claim")
	}
	assertAssignmentSnapshot(t, state.Snapshot(), assignmentSnapshot{
		ready: true, assignedClaims: 2, inflightRecords: 1, consumerLagRecords: 14, consumerLagKnown: true,
	})
	state.EndObservedRecord(session, second, 31, true)
	assertAssignmentSnapshot(t, state.Snapshot(), assignmentSnapshot{
		ready: true, assignedClaims: 2, consumerLagRecords: 13, consumerLagKnown: true,
	})
	second.highWater = 50
	assertAssignmentSnapshot(t, state.Snapshot(), assignmentSnapshot{
		ready: true, assignedClaims: 2, consumerLagRecords: 23, consumerLagKnown: true,
	})

	first.highWater = 20
	if !state.TryBeginObservedRecord(session, first, 11) {
		t.Fatal("TryBeginObservedRecord(retry) rejected an active claim")
	}
	state.EndObservedRecord(session, first, 12, false)
	assertAssignmentSnapshot(t, state.Snapshot(), assignmentSnapshot{
		ready: true, assignedClaims: 2, consumerLagRecords: 28, consumerLagKnown: true,
	})

	if err := state.Cleanup(session); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	assertAssignmentSnapshot(t, state.Snapshot(), assignmentSnapshot{})
}

func TestObservedRecordRejectsUntrustworthyHighWaterMark(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		highWater int64
	}{
		{name: "negative", highWater: -1},
		{name: "not beyond delivered offset", highWater: 5},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := newAssignmentLifecycle()
			session := newFakeSession(context.Background(), &[]string{})
			claim := newFakeClaim("trigger-input", 0, nil)
			claim.highWater = test.highWater
			session.claims = map[string][]int32{"trigger-input": {0}}
			if err := state.Setup(session); err != nil {
				t.Fatalf("Setup() error = %v", err)
			}
			if err := state.ClaimInitialized(session, claim); err != nil {
				t.Fatalf("ClaimInitialized() error = %v", err)
			}
			if !state.TryBeginObservedRecord(session, claim, 5) {
				t.Fatal("observability edge rejected the business record")
			}
			if snapshot := state.Snapshot(); snapshot.consumerLagKnown {
				t.Fatalf("invalid high water became known lag: %+v", snapshot)
			}
		})
	}
}

func TestZeroClaimAssignmentHasKnownZeroLag(t *testing.T) {
	t.Parallel()

	state := newAssignmentLifecycle()
	session := newFakeSession(context.Background(), &[]string{})
	session.claims = map[string][]int32{}
	if err := state.Setup(session); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	assertAssignmentSnapshot(t, state.Snapshot(), assignmentSnapshot{
		ready: true, consumerLagKnown: true,
	})
}

func TestServiceSnapshotIsReadySourceAndRecordsOneDrainOutcome(t *testing.T) {
	t.Parallel()

	group := newFakeConsumerGroup(func(context.Context, []string, sarama.ConsumerGroupHandler) error { return nil })
	service := newTestService(t, group, &fakeServiceClient{}, noopProcessorFactory(), fakeSyncOffsetCommitter{}, time.Second)
	service.running.Store(true)
	session := newFakeSession(context.Background(), &[]string{})
	session.claims = map[string][]int32{}
	if err := service.handler.Setup(session); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if !service.Ready() || !service.LifecycleSnapshot().Ready {
		t.Fatal("Service.Ready and lifecycle snapshot diverged")
	}

	service.beginDrain()
	snapshot := service.LifecycleSnapshot()
	if !snapshot.Draining || snapshot.Ready {
		t.Fatalf("draining snapshot = %+v, want draining and not ready", snapshot)
	}
	service.recordDrain(lifecycle.DrainTimeout)
	service.recordDrain(lifecycle.DrainFailed)
	service.beginDrain()
	snapshot = service.LifecycleSnapshot()
	if snapshot.Draining || snapshot.DrainTotal[lifecycle.DrainTimeout] != 1 || snapshot.DrainTotal[lifecycle.DrainFailed] != 0 {
		t.Fatalf("terminal drain snapshot = %+v, want exactly one timeout", snapshot)
	}
}

func assertAssignmentSnapshot(t *testing.T, got, want assignmentSnapshot) {
	t.Helper()
	if got != want {
		t.Fatalf("assignment snapshot = %+v, want %+v", got, want)
	}
}
