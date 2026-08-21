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
	"errors"
	"testing"
	"time"
)

func TestAssignmentReadyAfterEveryClaimInitializes(t *testing.T) {
	t.Parallel()

	lifecycle := newAssignmentLifecycle()
	session := newFakeSession(context.Background(), &[]string{})
	session.claims = map[string][]int32{"trigger-input": {0, 1}}
	if err := lifecycle.Setup(session); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if lifecycle.Ready() {
		t.Fatal("assignment became ready before claims initialized")
	}
	if err := lifecycle.ClaimInitialized(session, newFakeClaim("trigger-input", 0, nil)); err != nil {
		t.Fatalf("ClaimInitialized(0) error = %v", err)
	}
	if lifecycle.Ready() {
		t.Fatal("assignment became ready after only one claim initialized")
	}
	if err := lifecycle.ClaimInitialized(session, newFakeClaim("trigger-input", 1, nil)); err != nil {
		t.Fatalf("ClaimInitialized(1) error = %v", err)
	}
	if !lifecycle.Ready() {
		t.Fatal("assignment did not become ready after all claims initialized")
	}
}

func TestZeroClaimAssignmentIsReady(t *testing.T) {
	t.Parallel()

	lifecycle := newAssignmentLifecycle()
	session := newFakeSession(context.Background(), &[]string{})
	session.claims = map[string][]int32{}
	if err := lifecycle.Setup(session); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if !lifecycle.Ready() {
		t.Fatal("zero-claim assignment is not ready")
	}
}

func TestStaleSessionCancellationDoesNotClearNewAssignment(t *testing.T) {
	t.Parallel()

	lifecycle := newAssignmentLifecycle()
	firstContext, cancelFirst := context.WithCancel(context.Background())
	first := newFakeSession(firstContext, &[]string{})
	first.generation = 1
	first.claims = map[string][]int32{}
	if err := lifecycle.Setup(first); err != nil {
		t.Fatalf("Setup(first) error = %v", err)
	}

	second := newFakeSession(context.Background(), &[]string{})
	second.generation = 2
	second.claims = map[string][]int32{}
	if err := lifecycle.Setup(second); err != nil {
		t.Fatalf("Setup(second) error = %v", err)
	}
	cancelFirst()
	if err := lifecycle.Cleanup(first); err != nil {
		t.Fatalf("Cleanup(first) error = %v", err)
	}
	if !lifecycle.Ready() {
		t.Fatal("stale session cancellation cleared the new assignment")
	}
}

func TestEndedManagedAssignmentRejectsLateClaim(t *testing.T) {
	t.Parallel()

	lifecycle := newAssignmentLifecycle()
	session := newFakeSession(context.Background(), &[]string{})
	claim := newFakeClaim("trigger-input", 0, nil)
	session.claims = map[string][]int32{"trigger-input": {0}}
	if err := lifecycle.Setup(session); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if err := lifecycle.Cleanup(session); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if err := lifecycle.ClaimInitialized(session, claim); !errors.Is(err, errAssignmentInactive) {
		t.Fatalf("ClaimInitialized() error = %v, want inactive assignment", err)
	}
	if lifecycle.TryBeginRecord(session, claim) {
		t.Fatal("TryBeginRecord() accepted a record from an ended managed assignment")
	}
}

func TestDrainWaitsForInflightAndRejectsNewRecords(t *testing.T) {
	t.Parallel()

	lifecycle := newAssignmentLifecycle()
	session := newFakeSession(context.Background(), &[]string{})
	claim := newFakeClaim("trigger-input", 0, nil)
	session.claims = map[string][]int32{"trigger-input": {0}}
	if err := lifecycle.Setup(session); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if err := lifecycle.ClaimInitialized(session, claim); err != nil {
		t.Fatalf("ClaimInitialized() error = %v", err)
	}
	if !lifecycle.TryBeginRecord(session, claim) {
		t.Fatal("TryBeginRecord() rejected an active assignment")
	}
	drained := lifecycle.BeginDrain()
	select {
	case <-drained:
		t.Fatal("drain completed while a record was in flight")
	default:
	}
	if lifecycle.TryBeginRecord(session, claim) {
		t.Fatal("TryBeginRecord() accepted a record after drain began")
	}
	lifecycle.EndRecord()
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("drain did not finish after the in-flight record ended")
	}
	if lifecycle.Ready() {
		t.Fatal("draining assignment remained ready")
	}
}

func TestLateSetupCannotRestoreReadyAfterDrain(t *testing.T) {
	t.Parallel()

	lifecycle := newAssignmentLifecycle()
	<-lifecycle.BeginDrain()
	session := newFakeSession(context.Background(), &[]string{})
	session.claims = map[string][]int32{}
	if err := lifecycle.Setup(session); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if lifecycle.Ready() {
		t.Fatal("late setup restored readiness after drain")
	}
}
