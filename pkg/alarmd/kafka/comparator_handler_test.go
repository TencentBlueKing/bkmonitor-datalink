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
	"sync"
	"testing"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/comparator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/consumer"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestComparatorHandlerJoinsThreeClaimsIntoAudit(t *testing.T) {
	t.Parallel()

	metadata := newFakeComparatorMetadata(map[string][]int32{
		"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
	})
	assignment, err := newComparatorAssignmentCoordinator(
		metadata,
		map[comparator.StreamRole]string{
			comparator.StreamInput: "trigger-input", comparator.StreamGo: "go-decision", comparator.StreamPython: "py-decision",
		},
		100,
		10*time.Second,
	)
	if err != nil {
		t.Fatalf("newComparatorAssignmentCoordinator() error = %v", err)
	}
	audits := &collectingComparisonAuditSink{}
	events := []string{}
	handler, err := newComparatorHandler(assignment, fakeSyncOffsetCommitter{events: &events}, audits, time.Hour, nil)
	if err != nil {
		t.Fatalf("newComparatorHandler() error = %v", err)
	}
	sessionContext, cancelSession := context.WithCancel(context.Background())
	defer cancelSession()
	session := newFakeSession(sessionContext, &events)
	session.claims = map[string][]int32{
		"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
	}
	if err := handler.Setup(session); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	inputPayload, inputKey := comparatorTriggerInputFixture(t, "normal")
	input, err := contract.DecodeTriggerInput(inputPayload)
	if err != nil {
		t.Fatalf("DecodeTriggerInput() error = %v", err)
	}
	decisionPayload, decisionKey := comparatorTriggerDecisionFixture(t, input)
	claims := []*fakeClaim{
		newFakeClaim("py-decision", 0, []*sarama.ConsumerMessage{{Topic: "py-decision", Partition: 0, Offset: 0, Key: decisionKey, Value: decisionPayload}}),
		newFakeClaim("trigger-input", 0, []*sarama.ConsumerMessage{{Topic: "trigger-input", Partition: 0, Offset: 0, Key: inputKey, Value: inputPayload}}),
		newFakeClaim("go-decision", 0, []*sarama.ConsumerMessage{{Topic: "go-decision", Partition: 0, Offset: 0, Key: decisionKey, Value: decisionPayload}}),
	}
	var wait sync.WaitGroup
	errorsByClaim := make(chan error, len(claims))
	for _, claim := range claims {
		claim := claim
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsByClaim <- handler.ConsumeClaim(session, claim)
		}()
	}
	wait.Wait()
	close(errorsByClaim)
	for err := range errorsByClaim {
		if err != nil {
			t.Fatalf("ConsumeClaim() error = %v", err)
		}
	}
	if !handler.Ready() {
		t.Fatal("handler never became ready after all claims registered")
	}
	foundMatch := false
	for _, batch := range audits.Batches() {
		for _, audit := range batch.Audits {
			if audit.InputID == input.DetectionOutcomes[0].InputID && audit.JoinStatus == contract.ComparisonJoinComplete &&
				audit.Verdict == contract.ComparisonVerdictMatch {
				foundMatch = true
			}
		}
	}
	if !foundMatch {
		t.Fatalf("audit batches = %#v, want a complete match", audits.Batches())
	}
	cancelSession()
	if err := handler.Cleanup(session); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
}

func TestComparatorHandlerRejectsAssignmentAfterReadyRunEnds(t *testing.T) {
	t.Parallel()

	metadata := newFakeComparatorMetadata(map[string][]int32{
		"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
	})
	assignment, err := newComparatorAssignmentCoordinator(
		metadata,
		map[comparator.StreamRole]string{
			comparator.StreamInput: "trigger-input", comparator.StreamGo: "go-decision", comparator.StreamPython: "py-decision",
		},
		100,
		10*time.Second,
	)
	if err != nil {
		t.Fatalf("newComparatorAssignmentCoordinator() error = %v", err)
	}
	var fatalErr error
	handler, err := newComparatorHandler(
		assignment,
		fakeSyncOffsetCommitter{},
		&collectingComparisonAuditSink{},
		time.Hour,
		func(err error) { fatalErr = err },
	)
	if err != nil {
		t.Fatalf("newComparatorHandler() error = %v", err)
	}
	first := newFakeSession(context.Background(), &[]string{})
	first.claims = map[string][]int32{
		"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
	}
	if err := handler.Setup(first); err != nil {
		t.Fatalf("Setup(first) error = %v", err)
	}
	claims := []*fakeClaim{
		newFakeClaim("trigger-input", 0, nil),
		newFakeClaim("go-decision", 0, nil),
		newFakeClaim("py-decision", 0, nil),
	}
	var wait sync.WaitGroup
	claimErrors := make(chan error, len(claims))
	for _, claim := range claims {
		claim := claim
		wait.Add(1)
		go func() {
			defer wait.Done()
			claimErrors <- handler.ConsumeClaim(first, claim)
		}()
	}
	wait.Wait()
	close(claimErrors)
	for err := range claimErrors {
		if err != nil {
			t.Fatalf("ConsumeClaim(first) error = %v", err)
		}
	}
	if !handler.Ready() {
		t.Fatal("handler never became ready for the first assignment")
	}
	if err := handler.Cleanup(first); err != nil {
		t.Fatalf("Cleanup(first) error = %v", err)
	}

	second := newFakeSession(context.Background(), &[]string{})
	second.generation = 2
	second.claims = first.claims
	if err := handler.Setup(second); err == nil {
		t.Fatal("Setup(second) accepted a new assignment after the finite run became ready")
	}
	if fatalErr == nil {
		t.Fatal("Setup(second) did not fail the finite run")
	}
	if assignment.nextGeneration != 1 {
		t.Fatalf("assignment generations = %d, want exactly one finite-run epoch", assignment.nextGeneration)
	}
	if handler.Ready() {
		t.Fatal("handler remained ready after the rebalance was rejected")
	}
}

func TestComparatorHandlerDrainWaitsForActiveBarrier(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		block func(*testing.T, *observedBarrierMetadata, *comparatorRecordCoordinator) (<-chan struct{}, func())
	}{
		{
			name: "high-water read",
			block: func(t *testing.T, metadata *observedBarrierMetadata, _ *comparatorRecordCoordinator) (<-chan struct{}, func()) {
				t.Helper()
				coordinate := metadataOffset{"go-decision", 0, sarama.OffsetNewest}
				metadata.mu.Lock()
				metadata.blockAt = &coordinate
				metadata.blockStarted = make(chan struct{})
				metadata.blockRelease = make(chan struct{})
				started, release := metadata.blockStarted, metadata.blockRelease
				metadata.mu.Unlock()
				return started, func() { close(release) }
			},
		},
		{
			name: "audit acknowledgement",
			block: func(t *testing.T, _ *observedBarrierMetadata, records *comparatorRecordCoordinator) (<-chan struct{}, func()) {
				t.Helper()
				started := make(chan struct{})
				release := make(chan struct{})
				records.audits = comparisonAuditSinkFunc(func(context.Context, *contract.ComparisonAuditBatch) error {
					close(started)
					<-release
					return nil
				})
				return started, func() { close(release) }
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			metadata := newObservedBarrierMetadata(map[string][]int32{
				"trigger-input": {0}, "go-decision": {0, 1}, "py-decision": {0},
			})
			records, _, _ := setupComparatorBarrierCoordinator(t, metadata, time.Nanosecond)
			payload, key := comparatorTriggerInputFixture(t, "normal")
			if _, err := records.Process(context.Background(), consumer.Record{
				Topic: "trigger-input", Partition: 0, Offset: 10, Key: key, Value: payload,
			}); err != nil {
				t.Fatalf("Process(input) error = %v", err)
			}
			started, release := test.block(t, metadata, records)
			barrier, err := newComparatorBarrierAdapter(records)
			if err != nil {
				t.Fatalf("newComparatorBarrierAdapter() error = %v", err)
			}
			handler := &comparatorHandler{
				assignment: records.assignment, offsets: records.offsets, audits: records.audits,
				barrierInterval: time.Millisecond, drained: make(chan struct{}),
			}
			state := &comparatorHandlerSession{
				handle: records.handle, records: records, barrier: barrier,
				stopBarrier: make(chan struct{}), barrierDone: make(chan struct{}),
			}
			handler.state = state
			go handler.runBarrier(records.session.Context(), state)
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("barrier did not reach the blocking operation")
			}

			drainStarted := make(chan (<-chan struct{}), 1)
			go func() { drainStarted <- handler.BeginDrain() }()
			var drained <-chan struct{}
			select {
			case drained = <-drainStarted:
			case <-time.After(time.Second):
				t.Fatal("BeginDrain() blocked on the active barrier")
			}
			select {
			case <-drained:
				t.Fatal("handler drained before the active barrier completed")
			default:
			}
			release()
			select {
			case <-drained:
			case <-time.After(time.Second):
				t.Fatal("handler did not drain after the barrier completed")
			}
		})
	}
}

func TestComparatorHandlerCleanupWaitsForActiveBarrier(t *testing.T) {
	t.Parallel()

	metadata := newObservedBarrierMetadata(map[string][]int32{
		"trigger-input": {0}, "go-decision": {0, 1}, "py-decision": {0},
	})
	records, _, _ := setupComparatorBarrierCoordinator(t, metadata, time.Nanosecond)
	payload, key := comparatorTriggerInputFixture(t, "normal")
	if _, err := records.Process(context.Background(), consumer.Record{
		Topic: "trigger-input", Partition: 0, Offset: 10, Key: key, Value: payload,
	}); err != nil {
		t.Fatalf("Process(input) error = %v", err)
	}
	coordinate := metadataOffset{"go-decision", 0, sarama.OffsetNewest}
	metadata.mu.Lock()
	metadata.blockAt = &coordinate
	metadata.blockStarted = make(chan struct{})
	metadata.blockRelease = make(chan struct{})
	started, release := metadata.blockStarted, metadata.blockRelease
	metadata.mu.Unlock()
	barrier, err := newComparatorBarrierAdapter(records)
	if err != nil {
		t.Fatalf("newComparatorBarrierAdapter() error = %v", err)
	}
	handler := &comparatorHandler{
		assignment: records.assignment, offsets: records.offsets, audits: records.audits,
		barrierInterval: time.Millisecond, drained: make(chan struct{}),
	}
	state := &comparatorHandlerSession{
		handle: records.handle, records: records, barrier: barrier,
		stopBarrier: make(chan struct{}), barrierDone: make(chan struct{}), barrierStarted: true,
	}
	handler.state = state
	go handler.runBarrier(records.session.Context(), state)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("barrier did not reach the blocking HWM read")
	}

	cleanupDone := make(chan error, 1)
	go func() { cleanupDone <- handler.Cleanup(records.session) }()
	deadline := time.After(time.Second)
	for {
		records.assignment.mu.Lock()
		active := state.handle.generation.active
		records.assignment.mu.Unlock()
		if !active {
			break
		}
		select {
		case err := <-cleanupDone:
			t.Fatalf("Cleanup() returned before the assignment became inactive: %v", err)
		case <-deadline:
			t.Fatal("Cleanup() did not deactivate the assignment")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	select {
	case err := <-cleanupDone:
		t.Fatalf("Cleanup() returned before the active barrier completed: %v", err)
	default:
	}
	close(release)
	select {
	case err := <-cleanupDone:
		if err != nil {
			t.Fatalf("Cleanup() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Cleanup() did not return after the active barrier completed")
	}
}

type collectingComparisonAuditSink struct {
	mu      sync.Mutex
	batches []*contract.ComparisonAuditBatch
}

func (s *collectingComparisonAuditSink) WriteBatch(_ context.Context, batch *contract.ComparisonAuditBatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches = append(s.batches, batch)
	return nil
}

func (s *collectingComparisonAuditSink) Batches() []*contract.ComparisonAuditBatch {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*contract.ComparisonAuditBatch(nil), s.batches...)
}
