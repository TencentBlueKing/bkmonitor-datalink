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
	"fmt"
	"math"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/comparator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/consumer"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/contract"
)

func TestComparatorBarrierCapturesMissingPartitionsOnceAfterRunClock(t *testing.T) {
	t.Parallel()

	metadata := newObservedBarrierMetadata(map[string][]int32{
		"trigger-input": {0}, "go-decision": {0, 1}, "py-decision": {0},
	})
	coordinator, run, epoch := setupComparatorBarrierCoordinator(t, metadata, time.Nanosecond)
	adapter, err := newComparatorBarrierAdapter(coordinator)
	if err != nil {
		t.Fatalf("newComparatorBarrierAdapter() error = %v", err)
	}
	adapter.beginCapture = func(epoch string) (time.Time, error) {
		metadata.recordEvent("capture-start")
		return run.BeginBarrierCapture(epoch)
	}

	inputPayload, inputKey := comparatorTriggerInputFixture(t, "anomalous")
	input, err := comparatorTriggerInputFromPayload(inputPayload)
	if err != nil {
		t.Fatalf("DecodeTriggerInput() error = %v", err)
	}
	decisionPayload, decisionKey := comparatorTriggerDecisionFixture(t, input)
	for _, record := range []consumer.Record{
		{Topic: "trigger-input", Partition: 0, Offset: 10, Key: inputKey, Value: inputPayload},
		{Topic: "py-decision", Partition: 0, Offset: 30, Key: decisionKey, Value: decisionPayload},
	} {
		if _, err := coordinator.Process(context.Background(), record); err != nil {
			t.Fatalf("Process(%s) error = %v", record.Topic, err)
		}
	}
	metadata.resetObservations()

	frozen, err := adapter.CaptureOverdue(context.Background())
	if err != nil || frozen != 1 {
		t.Fatalf("CaptureOverdue() frozen=%d error=%v, want 1", frozen, err)
	}
	if got := metadata.eventsSnapshot(); fmt.Sprint(got) != fmt.Sprint([]string{
		"capture-start", "hwm:go-decision/0", "hwm:go-decision/1",
	}) {
		t.Fatalf("capture events = %#v", got)
	}
	snapshot, ok, err := run.Coverage(epoch, input.DetectionOutcomes[0].InputID)
	if err != nil || !ok || snapshot.Phase != comparator.CoverageOverdue || !snapshot.BarrierFrozen {
		t.Fatalf("Coverage() = %#v, ok=%v error=%v", snapshot, ok, err)
	}
	if frozen, err := adapter.CaptureOverdue(context.Background()); err != nil || frozen != 0 {
		t.Fatalf("CaptureOverdue(repeat) frozen=%d error=%v, want no-op", frozen, err)
	}
	if got := metadata.eventsSnapshot(); len(got) != 3 {
		t.Fatalf("repeat capture read more HWM values: %#v", got)
	}
}

func TestComparatorBarrierSharesOneHWMVectorAcrossOverdueInputs(t *testing.T) {
	t.Parallel()

	metadata := newObservedBarrierMetadata(map[string][]int32{
		"trigger-input": {0}, "go-decision": {0, 1}, "py-decision": {0},
	})
	coordinator, run, epoch := setupComparatorBarrierCoordinator(t, metadata, time.Nanosecond)
	adapter, err := newComparatorBarrierAdapter(coordinator)
	if err != nil {
		t.Fatalf("newComparatorBarrierAdapter() error = %v", err)
	}
	adapter.beginCapture = func(epoch string) (time.Time, error) {
		metadata.recordEvent("capture-start")
		return run.BeginBarrierCapture(epoch)
	}
	inputIDs := make([]string, 0, 2)
	for index, name := range []string{"normal", "anomalous"} {
		payload, key := comparatorTriggerInputFixture(t, name)
		input, err := comparatorTriggerInputFromPayload(payload)
		if err != nil {
			t.Fatalf("DecodeTriggerInput(%s) error = %v", name, err)
		}
		inputIDs = append(inputIDs, input.DetectionOutcomes[0].InputID)
		if _, err := coordinator.Process(context.Background(), consumer.Record{
			Topic: "trigger-input", Partition: 0, Offset: int64(10 + index), Key: key, Value: payload,
		}); err != nil {
			t.Fatalf("Process(%s) error = %v", name, err)
		}
	}
	metadata.resetObservations()

	frozen, err := adapter.CaptureOverdue(context.Background())
	if err != nil || frozen != 2 {
		t.Fatalf("CaptureOverdue() frozen=%d error=%v, want 2", frozen, err)
	}
	if got := metadata.eventsSnapshot(); fmt.Sprint(got) != fmt.Sprint([]string{
		"capture-start", "hwm:go-decision/0", "hwm:go-decision/1", "hwm:py-decision/0",
	}) {
		t.Fatalf("capture events = %#v", got)
	}
	for _, inputID := range inputIDs {
		snapshot, ok, err := run.Coverage(epoch, inputID)
		if err != nil || !ok || !snapshot.BarrierFrozen {
			t.Fatalf("Coverage(%s) = %#v, ok=%v error=%v", inputID, snapshot, ok, err)
		}
	}
}

func TestComparatorBarrierHWMFailureStopsAssignmentBeforeAnyRetry(t *testing.T) {
	t.Parallel()

	metadata := newObservedBarrierMetadata(map[string][]int32{
		"trigger-input": {0}, "go-decision": {0, 1}, "py-decision": {0},
	})
	coordinator, run, _ := setupComparatorBarrierCoordinator(t, metadata, time.Nanosecond)
	adapter, err := newComparatorBarrierAdapter(coordinator)
	if err != nil {
		t.Fatalf("newComparatorBarrierAdapter() error = %v", err)
	}
	adapter.beginCapture = func(epoch string) (time.Time, error) {
		metadata.recordEvent("capture-start")
		return run.BeginBarrierCapture(epoch)
	}
	payload, key := comparatorTriggerInputFixture(t, "normal")
	if _, err := coordinator.Process(context.Background(), consumer.Record{
		Topic: "trigger-input", Partition: 0, Offset: 10, Key: key, Value: payload,
	}); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	failedCoordinate := metadataOffset{"py-decision", 0, sarama.OffsetNewest}
	metadata.fakeComparatorMetadata.mu.Lock()
	metadata.fakeComparatorMetadata.offsetErrors[failedCoordinate] = fmt.Errorf("metadata unavailable")
	metadata.fakeComparatorMetadata.mu.Unlock()
	metadata.resetObservations()

	if _, err := adapter.CaptureOverdue(context.Background()); err == nil {
		t.Fatal("CaptureOverdue() accepted a partial HWM vector")
	}
	if run.Valid() {
		t.Fatal("HWM failure left the Run valid")
	}
	beforeRetry := len(metadata.eventsSnapshot())
	if _, err := adapter.CaptureOverdue(context.Background()); err == nil {
		t.Fatal("CaptureOverdue(retry) accepted a failed assignment")
	}
	if got := len(metadata.eventsSnapshot()); got != beforeRetry {
		t.Fatalf("failed assignment performed more HWM reads: before=%d after=%d", beforeRetry, got)
	}
}

func TestComparatorBarrierSerializesHWMFreezeBeforeNextRecord(t *testing.T) {
	t.Parallel()

	metadata := newObservedBarrierMetadata(map[string][]int32{
		"trigger-input": {0}, "go-decision": {0, 1}, "py-decision": {0},
	})
	coordinator, run, epoch := setupComparatorBarrierCoordinator(t, metadata, time.Nanosecond)
	adapter, err := newComparatorBarrierAdapter(coordinator)
	if err != nil {
		t.Fatalf("newComparatorBarrierAdapter() error = %v", err)
	}
	inputPayload, inputKey := comparatorTriggerInputFixture(t, "anomalous")
	input, err := comparatorTriggerInputFromPayload(inputPayload)
	if err != nil {
		t.Fatalf("DecodeTriggerInput() error = %v", err)
	}
	if _, err := coordinator.Process(context.Background(), consumer.Record{
		Topic: "trigger-input", Partition: 0, Offset: 10, Key: inputKey, Value: inputPayload,
	}); err != nil {
		t.Fatalf("Process(input) error = %v", err)
	}
	decisionPayload, decisionKey := comparatorTriggerDecisionFixture(t, input)
	blockedCoordinate := metadataOffset{"go-decision", 0, sarama.OffsetNewest}
	metadata.mu.Lock()
	metadata.blockAt = &blockedCoordinate
	metadata.blockStarted = make(chan struct{})
	metadata.blockRelease = make(chan struct{})
	metadata.mu.Unlock()
	metadata.resetObservations()

	captureDone := make(chan error, 1)
	go func() {
		_, err := adapter.CaptureOverdue(context.Background())
		captureDone <- err
	}()
	<-metadata.blockStarted
	commitStarted := make(chan struct{})
	coordinator.offsets = comparatorOffsetCommitterFunc(func(context.Context, sarama.ConsumerGroupSession, consumer.Record) error {
		close(commitStarted)
		return nil
	})
	processContext := newObservedErrContext(context.Background())
	processDone := make(chan error, 1)
	go func() {
		_, err := coordinator.Process(processContext, consumer.Record{
			Topic: "go-decision", Partition: 0, Offset: 100, Key: decisionKey, Value: decisionPayload,
		})
		processDone <- err
	}()
	<-processContext.observed
	select {
	case <-commitStarted:
		t.Fatal("record reached broker commit before the HWM barrier froze")
	default:
	}
	close(metadata.blockRelease)
	if err := <-captureDone; err != nil {
		t.Fatalf("CaptureOverdue() error = %v", err)
	}
	if err := <-processDone; err != nil {
		t.Fatalf("Process(decision) error = %v", err)
	}
	<-commitStarted
	snapshot, ok, err := run.Coverage(epoch, input.DetectionOutcomes[0].InputID)
	if err != nil || !ok || len(snapshot.LateRoles) != 1 || snapshot.LateRoles[0] != comparator.StreamGo {
		t.Fatalf("Coverage() = %#v, ok=%v error=%v, want late Go decision", snapshot, ok, err)
	}
}

func TestComparatorBarrierSessionCancellationDuringHWMStopsBeforeFreeze(t *testing.T) {
	t.Parallel()

	sessionContext, cancel := context.WithCancel(context.Background())
	metadata := newObservedBarrierMetadata(map[string][]int32{
		"trigger-input": {0}, "go-decision": {0, 1}, "py-decision": {0},
	})
	coordinator, run, epoch := setupComparatorBarrierCoordinatorWithContext(
		t, sessionContext, metadata, time.Nanosecond,
	)
	adapter, err := newComparatorBarrierAdapter(coordinator)
	if err != nil {
		t.Fatalf("newComparatorBarrierAdapter() error = %v", err)
	}
	adapter.beginCapture = func(epoch string) (time.Time, error) {
		metadata.recordEvent("capture-start")
		return run.BeginBarrierCapture(epoch)
	}
	payload, key := comparatorTriggerInputFixture(t, "normal")
	input, err := comparatorTriggerInputFromPayload(payload)
	if err != nil {
		t.Fatalf("DecodeTriggerInput() error = %v", err)
	}
	if _, err := coordinator.Process(context.Background(), consumer.Record{
		Topic: "trigger-input", Partition: 0, Offset: 10, Key: key, Value: payload,
	}); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	blockedCoordinate := metadataOffset{"go-decision", 0, sarama.OffsetNewest}
	metadata.mu.Lock()
	metadata.blockAt = &blockedCoordinate
	metadata.blockStarted = make(chan struct{})
	metadata.blockRelease = make(chan struct{})
	metadata.mu.Unlock()
	metadata.resetObservations()

	captureDone := make(chan error, 1)
	go func() {
		_, err := adapter.CaptureOverdue(context.Background())
		captureDone <- err
	}()
	<-metadata.blockStarted
	cancel()
	deadline := time.Now().Add(time.Second)
	for {
		coordinator.assignment.mu.Lock()
		active := coordinator.handle.generation.active
		coordinator.assignment.mu.Unlock()
		if !active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("session watcher did not end the generation")
		}
		runtime.Gosched()
	}
	if !run.Valid() {
		t.Fatal("session cancellation invalidated the Run before the HWM lease was released")
	}
	snapshot, ok, err := run.Coverage(epoch, input.DetectionOutcomes[0].InputID)
	if err != nil || !ok || snapshot.BarrierFrozen {
		t.Fatalf("Coverage() = %#v, ok=%v error=%v, want valid unfrozen candidate", snapshot, ok, err)
	}
	close(metadata.blockRelease)
	if err := <-captureDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("CaptureOverdue() error = %v, want session cancellation", err)
	}
	if run.Valid() {
		t.Fatal("session cancellation left the Run valid")
	}
	if got := metadata.eventsSnapshot(); fmt.Sprint(got) != fmt.Sprint([]string{
		"capture-start", "hwm:go-decision/0",
	}) {
		t.Fatalf("capture continued after session cancellation: %#v", got)
	}
}

func TestComparatorBarrierCleanupDuringLastHWMStopsBeforeFreeze(t *testing.T) {
	t.Parallel()

	metadata := newObservedBarrierMetadata(map[string][]int32{
		"trigger-input": {0}, "go-decision": {0, 1}, "py-decision": {0},
	})
	coordinator, run, epoch := setupComparatorBarrierCoordinator(t, metadata, time.Nanosecond)
	adapter, err := newComparatorBarrierAdapter(coordinator)
	if err != nil {
		t.Fatalf("newComparatorBarrierAdapter() error = %v", err)
	}
	payload, key := comparatorTriggerInputFixture(t, "normal")
	input, err := comparatorTriggerInputFromPayload(payload)
	if err != nil {
		t.Fatalf("DecodeTriggerInput() error = %v", err)
	}
	if _, err := coordinator.Process(context.Background(), consumer.Record{
		Topic: "trigger-input", Partition: 0, Offset: 10, Key: key, Value: payload,
	}); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	blockedCoordinate := metadataOffset{"py-decision", 0, sarama.OffsetNewest}
	metadata.mu.Lock()
	metadata.blockAt = &blockedCoordinate
	metadata.blockStarted = make(chan struct{})
	metadata.blockRelease = make(chan struct{})
	metadata.mu.Unlock()
	metadata.resetObservations()

	captureDone := make(chan error, 1)
	go func() {
		_, err := adapter.CaptureOverdue(context.Background())
		captureDone <- err
	}()
	<-metadata.blockStarted
	if err := coordinator.assignment.Cleanup(coordinator.handle, coordinator.session); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if !run.Valid() {
		t.Fatal("Cleanup invalidated the Run before the HWM lease was released")
	}
	snapshot, ok, err := run.Coverage(epoch, input.DetectionOutcomes[0].InputID)
	if err != nil || !ok || snapshot.BarrierFrozen {
		t.Fatalf("Coverage() = %#v, ok=%v error=%v, want valid unfrozen candidate", snapshot, ok, err)
	}
	close(metadata.blockRelease)
	if err := <-captureDone; !errors.Is(err, errComparatorAssignmentEnded) {
		t.Fatalf("CaptureOverdue() error = %v, want ended assignment", err)
	}
	if run.Valid() {
		t.Fatal("Cleanup left the Run valid after the HWM operation released its lease")
	}
}

func TestComparatorBarrierPreCanceledCallerReadsNoHWM(t *testing.T) {
	t.Parallel()

	metadata := newObservedBarrierMetadata(map[string][]int32{
		"trigger-input": {0}, "go-decision": {0, 1}, "py-decision": {0},
	})
	coordinator, run, _ := setupComparatorBarrierCoordinator(t, metadata, time.Nanosecond)
	adapter, err := newComparatorBarrierAdapter(coordinator)
	if err != nil {
		t.Fatalf("newComparatorBarrierAdapter() error = %v", err)
	}
	payload, key := comparatorTriggerInputFixture(t, "normal")
	if _, err := coordinator.Process(context.Background(), consumer.Record{
		Topic: "trigger-input", Partition: 0, Offset: 10, Key: key, Value: payload,
	}); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	metadata.resetObservations()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := adapter.CaptureOverdue(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("CaptureOverdue() error = %v, want caller cancellation", err)
	}
	if got := metadata.eventsSnapshot(); len(got) != 0 {
		t.Fatalf("pre-canceled capture read HWM: %#v", got)
	}
	if run.Valid() {
		t.Fatal("pre-canceled capture left the Run valid")
	}
}

func TestComparatorBarrierRejectsUnsafeHighWater(t *testing.T) {
	t.Parallel()

	for name, highWater := range map[string]int64{
		"negative":              -1,
		"maximum":               math.MaxInt64,
		"behind committed next": 19,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			metadata := newObservedBarrierMetadata(map[string][]int32{
				"trigger-input": {0}, "go-decision": {0, 1}, "py-decision": {0},
			})
			coordinator, run, _ := setupComparatorBarrierCoordinator(t, metadata, time.Nanosecond)
			adapter, err := newComparatorBarrierAdapter(coordinator)
			if err != nil {
				t.Fatalf("newComparatorBarrierAdapter() error = %v", err)
			}
			payload, key := comparatorTriggerInputFixture(t, "normal")
			if _, err := coordinator.Process(context.Background(), consumer.Record{
				Topic: "trigger-input", Partition: 0, Offset: 10, Key: key, Value: payload,
			}); err != nil {
				t.Fatalf("Process() error = %v", err)
			}
			coordinate := metadataOffset{"go-decision", 0, sarama.OffsetNewest}
			metadata.fakeComparatorMetadata.mu.Lock()
			metadata.fakeComparatorMetadata.offsets[coordinate] = highWater
			metadata.fakeComparatorMetadata.mu.Unlock()
			if _, err := adapter.CaptureOverdue(context.Background()); err == nil {
				t.Fatalf("CaptureOverdue() accepted high water %d", highWater)
			}
			if run.Valid() {
				t.Fatalf("unsafe high water %d left the Run valid", highWater)
			}
		})
	}
}

type observedBarrierMetadata struct {
	*fakeComparatorMetadata
	mu           sync.Mutex
	events       []string
	blockAt      *metadataOffset
	blockStarted chan struct{}
	blockRelease chan struct{}
	blockOnce    sync.Once
}

func newObservedBarrierMetadata(partitions map[string][]int32) *observedBarrierMetadata {
	return &observedBarrierMetadata{fakeComparatorMetadata: newFakeComparatorMetadata(partitions)}
}

func (m *observedBarrierMetadata) GetOffset(topic string, partition int32, timestamp int64) (int64, error) {
	if timestamp == sarama.OffsetNewest {
		m.recordEvent(fmt.Sprintf("hwm:%s/%d", topic, partition))
	}
	coordinate := metadataOffset{topic: topic, partition: partition, time: timestamp}
	m.mu.Lock()
	blocked := m.blockAt != nil && *m.blockAt == coordinate
	started, release := m.blockStarted, m.blockRelease
	m.mu.Unlock()
	if blocked {
		m.blockOnce.Do(func() { close(started) })
		<-release
	}
	return m.fakeComparatorMetadata.GetOffset(topic, partition, timestamp)
}

func (m *observedBarrierMetadata) recordEvent(event string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
}

func (m *observedBarrierMetadata) resetObservations() {
	m.mu.Lock()
	m.events = nil
	m.mu.Unlock()
	m.fakeComparatorMetadata.mu.Lock()
	m.fakeComparatorMetadata.offsetCalls = nil
	m.fakeComparatorMetadata.mu.Unlock()
}

func (m *observedBarrierMetadata) eventsSnapshot() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.events...)
}

func setupComparatorBarrierCoordinator(
	t *testing.T,
	metadata comparatorMetadata,
	coverageTimeout time.Duration,
) (*comparatorRecordCoordinator, *comparator.Run, string) {
	t.Helper()
	return setupComparatorBarrierCoordinatorWithContext(t, context.Background(), metadata, coverageTimeout)
}

func setupComparatorBarrierCoordinatorWithContext(
	t *testing.T,
	ctx context.Context,
	metadata comparatorMetadata,
	coverageTimeout time.Duration,
) (*comparatorRecordCoordinator, *comparator.Run, string) {
	t.Helper()
	assignment, err := newComparatorAssignmentCoordinator(
		metadata,
		map[comparator.StreamRole]string{
			comparator.StreamInput:  "trigger-input",
			comparator.StreamGo:     "go-decision",
			comparator.StreamPython: "py-decision",
		},
		100,
		coverageTimeout,
	)
	if err != nil {
		t.Fatalf("newComparatorAssignmentCoordinator() error = %v", err)
	}
	events := []string{}
	session := &comparatorMarkSession{fakeSession: newFakeSession(ctx, &events)}
	session.claims = map[string][]int32{
		"trigger-input": {0}, "go-decision": {0, 1}, "py-decision": {0},
	}
	handle, err := assignment.Setup(session)
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	for _, claim := range []*fakeClaim{
		{topic: "trigger-input", partition: 0, initial: 10},
		{topic: "go-decision", partition: 0, initial: 20},
		{topic: "go-decision", partition: 1, initial: 40},
		{topic: "py-decision", partition: 0, initial: 30},
	} {
		if err := assignment.RegisterClaim(handle, session, claim); err != nil {
			t.Fatalf("RegisterClaim() error = %v", err)
		}
	}
	run, epoch, err := assignment.WaitReady(context.Background(), handle, session)
	if err != nil {
		t.Fatalf("WaitReady() error = %v", err)
	}
	committer := comparatorOffsetCommitterFunc(func(context.Context, sarama.ConsumerGroupSession, consumer.Record) error {
		return nil
	})
	coordinator, err := newComparatorRecordCoordinator(assignment, handle, session, committer)
	if err != nil {
		t.Fatalf("newComparatorRecordCoordinator() error = %v", err)
	}
	return coordinator, run, epoch
}

func comparatorTriggerInputFromPayload(payload []byte) (*contract.TriggerInput, error) {
	return contract.DecodeTriggerInput(payload)
}
