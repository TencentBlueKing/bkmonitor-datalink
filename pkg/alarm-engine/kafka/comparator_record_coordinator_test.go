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
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/comparator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/consumer"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/contract"
)

func TestComparatorRecordCoordinatorCommitsBrokerThenRunThenLocalOffset(t *testing.T) {
	t.Parallel()

	var (
		run   *comparator.Run
		epoch string
	)
	events := []string{}
	committer := comparatorOffsetCommitterFunc(func(_ context.Context, _ sarama.ConsumerGroupSession, _ consumer.Record) error {
		if _, err := run.NextOffset(epoch, comparator.StreamInput, 0); !errors.Is(err, comparator.ErrRecordInFlight) {
			t.Fatalf("NextOffset() during broker commit error = %v, want record in flight", err)
		}
		events = append(events, "broker-ack")
		return nil
	})
	coordinator, currentRun, currentEpoch, session := setupComparatorRecordCoordinator(t, 100, committer, &events)
	run, epoch = currentRun, currentEpoch
	session.onMark = func(_ string, _ int32, offset int64) {
		next, err := run.NextOffset(epoch, comparator.StreamInput, 0)
		if err != nil || next != offset {
			t.Fatalf("NextOffset() at MarkOffset = %d, error=%v, want %d", next, err, offset)
		}
	}
	payload, key := comparatorTriggerInputFixture(t, "normal")
	if _, err := coordinator.Process(context.Background(), consumer.Record{
		Topic: "trigger-input", Partition: 0, Offset: 10, Key: key, Value: payload,
	}); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if got := len(events); got != 2 || events[0] != "broker-ack" || events[1] != "mark" {
		t.Fatalf("events = %#v, want broker-ack then mark", events)
	}
}

func TestComparatorRecordCoordinatorCommitFailureStopsAssignment(t *testing.T) {
	t.Parallel()

	want := errors.New("commit unavailable")
	commitCalls := 0
	events := []string{}
	committer := comparatorOffsetCommitterFunc(func(_ context.Context, _ sarama.ConsumerGroupSession, _ consumer.Record) error {
		commitCalls++
		return want
	})
	coordinator, run, _, _ := setupComparatorRecordCoordinator(t, 100, committer, &events)
	payload, key := comparatorTriggerInputFixture(t, "normal")
	record := consumer.Record{Topic: "trigger-input", Partition: 0, Offset: 10, Key: key, Value: payload}
	if _, err := coordinator.Process(context.Background(), record); !errors.Is(err, want) {
		t.Fatalf("Process() error = %v, want commit failure", err)
	}
	if run.Valid() {
		t.Fatal("commit failure left the Run valid")
	}
	if _, err := coordinator.Process(context.Background(), record); err == nil {
		t.Fatal("Process() accepted another record after assignment failure")
	}
	if commitCalls != 1 || len(events) != 0 {
		t.Fatalf("commitCalls=%d events=%#v, want one commit and no local mark", commitCalls, events)
	}
}

func TestComparatorRecordCoordinatorPrepareFailureDoesNotCommit(t *testing.T) {
	t.Parallel()

	commitCalls := 0
	events := []string{}
	committer := comparatorOffsetCommitterFunc(func(_ context.Context, _ sarama.ConsumerGroupSession, _ consumer.Record) error {
		commitCalls++
		return nil
	})
	coordinator, run, _, _ := setupComparatorRecordCoordinator(t, 100, committer, &events)
	if _, err := coordinator.Process(context.Background(), consumer.Record{
		Topic: "trigger-input", Partition: 0, Offset: 10, Key: []byte("invalid"), Value: []byte(`{}`),
	}); err == nil {
		t.Fatal("Process() accepted an invalid TriggerInput")
	}
	if run.Valid() {
		t.Fatal("prepare failure left the Run valid")
	}
	if commitCalls != 0 || len(events) != 0 {
		t.Fatalf("commitCalls=%d events=%#v, want zero broker/local commits", commitCalls, events)
	}
}

func TestComparatorRecordCoordinatorCapacityFailureDoesNotRotateRun(t *testing.T) {
	t.Parallel()

	commitCalls := 0
	events := []string{}
	committer := comparatorOffsetCommitterFunc(func(_ context.Context, _ sarama.ConsumerGroupSession, _ consumer.Record) error {
		commitCalls++
		return nil
	})
	coordinator, run, _, _ := setupComparatorRecordCoordinator(t, 1, committer, &events)
	firstPayload, firstKey := comparatorTriggerInputFixture(t, "normal")
	if _, err := coordinator.Process(context.Background(), consumer.Record{
		Topic: "trigger-input", Partition: 0, Offset: 10, Key: firstKey, Value: firstPayload,
	}); err != nil {
		t.Fatalf("Process(first) error = %v", err)
	}
	secondPayload, secondKey := comparatorTriggerInputFixture(t, "anomalous")
	if _, err := coordinator.Process(context.Background(), consumer.Record{
		Topic: "trigger-input", Partition: 0, Offset: 11, Key: secondKey, Value: secondPayload,
	}); err == nil {
		t.Fatal("Process(second) accepted a new input beyond the Run capacity")
	}
	if run.Valid() {
		t.Fatal("capacity failure silently rotated or retained a valid Run")
	}
	if commitCalls != 1 || len(events) != 1 || events[0] != "mark" {
		t.Fatalf("commitCalls=%d events=%#v, want only the first record committed", commitCalls, events)
	}
}

func TestComparatorRecordCoordinatorSuccessfulAckWinsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	events := []string{}
	committer := comparatorOffsetCommitterFunc(func(_ context.Context, _ sarama.ConsumerGroupSession, _ consumer.Record) error {
		cancel()
		events = append(events, "broker-ack")
		return nil
	})
	coordinator, run, epoch, _ := setupComparatorRecordCoordinator(t, 100, committer, &events)
	payload, key := comparatorTriggerInputFixture(t, "normal")
	if _, err := coordinator.Process(ctx, consumer.Record{
		Topic: "trigger-input", Partition: 0, Offset: 10, Key: key, Value: payload,
	}); err != nil {
		t.Fatalf("Process() after successful broker ack = %v, want nil", err)
	}
	next, err := run.NextOffset(epoch, comparator.StreamInput, 0)
	if err != nil || next != 11 {
		t.Fatalf("NextOffset() = %d, error=%v, want 11", next, err)
	}
	if len(events) != 2 || events[0] != "broker-ack" || events[1] != "mark" {
		t.Fatalf("events = %#v, want broker-ack then mark", events)
	}
}

func TestComparatorRecordCoordinatorRebalanceWaitsForSuccessfulInflightCommit(t *testing.T) {
	t.Parallel()

	parentContext, cancel := context.WithCancel(context.Background())
	sessionContext := newObservedCancelContext(parentContext)
	events := []string{}
	var coordinator *comparatorRecordCoordinator
	committer := comparatorOffsetCommitterFunc(func(_ context.Context, _ sarama.ConsumerGroupSession, _ consumer.Record) error {
		cancel()
		for {
			coordinator.assignment.mu.Lock()
			active := coordinator.handle.generation.active
			coordinator.assignment.mu.Unlock()
			if !active {
				break
			}
			runtime.Gosched()
		}
		events = append(events, "broker-ack")
		return nil
	})
	var run *comparator.Run
	coordinator, run, _, _ = setupComparatorRecordCoordinatorWithContext(
		t, sessionContext, 100, committer, &events,
	)
	payload, key := comparatorTriggerInputFixture(t, "normal")
	if _, err := coordinator.Process(sessionContext, consumer.Record{
		Topic: "trigger-input", Partition: 0, Offset: 10, Key: key, Value: payload,
	}); err != nil {
		t.Fatalf("Process() after successful rebalance-raced broker ack = %v, want nil", err)
	}
	if run.Valid() {
		t.Fatal("completed in-flight record left the canceled assignment Run valid")
	}
	if len(events) != 2 || events[0] != "broker-ack" || events[1] != "mark" {
		t.Fatalf("events = %#v, want broker-ack then mark", events)
	}
}

func TestComparatorRecordCoordinatorPreCanceledContextStopsAssignment(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	commitCalls := 0
	events := []string{}
	committer := comparatorOffsetCommitterFunc(func(_ context.Context, _ sarama.ConsumerGroupSession, _ consumer.Record) error {
		commitCalls++
		return nil
	})
	coordinator, run, _, _ := setupComparatorRecordCoordinator(t, 100, committer, &events)
	payload, key := comparatorTriggerInputFixture(t, "normal")
	if _, err := coordinator.Process(ctx, consumer.Record{
		Topic: "trigger-input", Partition: 0, Offset: 10, Key: key, Value: payload,
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Process() error = %v, want context canceled", err)
	}
	if run.Valid() || commitCalls != 0 || len(events) != 0 {
		t.Fatalf("valid=%v commitCalls=%d events=%#v, want failed assignment without commit", run.Valid(), commitCalls, events)
	}
}

func TestComparatorRecordCoordinatorCanceledSessionStopsBackgroundCaller(t *testing.T) {
	t.Parallel()

	commitCalls := 0
	events := []string{}
	committer := comparatorOffsetCommitterFunc(func(_ context.Context, _ sarama.ConsumerGroupSession, _ consumer.Record) error {
		commitCalls++
		return nil
	})
	coordinator, run, _, session := setupComparatorRecordCoordinator(t, 100, committer, &events)
	payload, key := comparatorTriggerInputFixture(t, "normal")

	sessionContext, cancel := context.WithCancel(context.Background())
	cancel()
	session.fakeSession.ctx = sessionContext
	if _, err := coordinator.Process(context.Background(), consumer.Record{
		Topic: "trigger-input", Partition: 0, Offset: 10, Key: key, Value: payload,
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Process() error = %v, want canceled session", err)
	}
	if run.Valid() || commitCalls != 0 || len(events) != 0 {
		t.Fatalf("valid=%v commitCalls=%d events=%#v, want failed assignment without commit", run.Valid(), commitCalls, events)
	}
}

func TestComparatorRecordCoordinatorHasOneOwnerPerAssignment(t *testing.T) {
	t.Parallel()

	events := []string{}
	committer := comparatorOffsetCommitterFunc(func(_ context.Context, _ sarama.ConsumerGroupSession, _ consumer.Record) error {
		return nil
	})
	coordinator, _, _, _ := setupComparatorRecordCoordinator(t, 100, committer, &events)
	if _, err := newComparatorRecordCoordinator(
		coordinator.assignment,
		coordinator.handle,
		coordinator.session,
		committer,
	); err == nil {
		t.Fatal("newComparatorRecordCoordinator() allowed a second owner for one assignment")
	}
}

func TestComparatorRecordCoordinatorFirstFailureStopsQueuedClaim(t *testing.T) {
	t.Parallel()

	want := errors.New("commit unavailable")
	commitStarted := make(chan struct{})
	releaseCommit := make(chan struct{})
	commitCalls := 0
	events := []string{}
	committer := comparatorOffsetCommitterFunc(func(_ context.Context, _ sarama.ConsumerGroupSession, _ consumer.Record) error {
		commitCalls++
		close(commitStarted)
		<-releaseCommit
		return want
	})
	coordinator, run, _, _ := setupComparatorRecordCoordinator(t, 100, committer, &events)
	payload, key := comparatorTriggerInputFixture(t, "normal")
	record := consumer.Record{Topic: "trigger-input", Partition: 0, Offset: 10, Key: key, Value: payload}
	firstDone := make(chan error, 1)
	go func() {
		_, err := coordinator.Process(context.Background(), record)
		firstDone <- err
	}()
	<-commitStarted

	secondContext := newObservedErrContext(context.Background())
	secondDone := make(chan error, 1)
	go func() {
		_, err := coordinator.Process(secondContext, record)
		secondDone <- err
	}()
	<-secondContext.observed
	close(releaseCommit)
	if err := <-firstDone; !errors.Is(err, want) {
		t.Fatalf("Process(first) error = %v, want commit failure", err)
	}
	if err := <-secondDone; !errors.Is(err, want) {
		t.Fatalf("Process(second) error = %v, want first commit failure", err)
	}
	if run.Valid() || commitCalls != 1 || len(events) != 0 {
		t.Fatalf("valid=%v commitCalls=%d events=%#v, want one failed commit and no mark", run.Valid(), commitCalls, events)
	}
}

func TestComparatorRecordCoordinatorMapsAllThreeFrozenStreamRoles(t *testing.T) {
	t.Parallel()

	commitCalls := 0
	events := []string{}
	committer := comparatorOffsetCommitterFunc(func(_ context.Context, _ sarama.ConsumerGroupSession, _ consumer.Record) error {
		commitCalls++
		return nil
	})
	coordinator, run, epoch, _ := setupComparatorRecordCoordinator(t, 100, committer, &events)
	inputPayload, inputKey := comparatorTriggerInputFixture(t, "anomalous")
	input, err := contract.DecodeTriggerInput(inputPayload)
	if err != nil {
		t.Fatalf("DecodeTriggerInput() error = %v", err)
	}
	decisionPayload, decisionKey := comparatorTriggerDecisionFixture(t, input)

	for _, record := range []consumer.Record{
		{Topic: "py-decision", Partition: 0, Offset: 30, Key: decisionKey, Value: decisionPayload},
		{Topic: "trigger-input", Partition: 0, Offset: 10, Key: inputKey, Value: inputPayload},
		{Topic: "go-decision", Partition: 0, Offset: 20, Key: decisionKey, Value: decisionPayload},
	} {
		if _, err := coordinator.Process(context.Background(), record); err != nil {
			t.Fatalf("Process(%s) error = %v", record.Topic, err)
		}
	}

	source := input.DetectionOutcomes[0]
	epochStart := source.Record.SourceTime - 299
	assessment, ok, err := run.Assess(epoch, source.InputID, comparator.Gates{
		StableEpoch:          true,
		CoverageComplete:     true,
		EpochStartSourceTime: &epochStart,
	})
	if err != nil || !ok {
		t.Fatalf("Assess() ok=%v error=%v", ok, err)
	}
	if assessment.Join != comparator.JoinComplete || assessment.Verdict != comparator.VerdictMatch {
		t.Fatalf("assessment = %#v, want complete match", assessment)
	}
	if commitCalls != 3 || len(events) != 3 {
		t.Fatalf("commitCalls=%d events=%#v, want three committed records", commitCalls, events)
	}
}

type comparatorOffsetCommitterFunc func(context.Context, sarama.ConsumerGroupSession, consumer.Record) error

type observedErrContext struct {
	context.Context
	once     sync.Once
	observed chan struct{}
}

func newObservedErrContext(parent context.Context) *observedErrContext {
	return &observedErrContext{Context: parent, observed: make(chan struct{})}
}

func (c *observedErrContext) Err() error {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Err()
}

func (f comparatorOffsetCommitterFunc) CommitOffset(
	ctx context.Context,
	session sarama.ConsumerGroupSession,
	record consumer.Record,
) error {
	return f(ctx, session, record)
}

type comparatorMarkSession struct {
	*fakeSession
	onMark func(topic string, partition int32, offset int64)
}

func (s *comparatorMarkSession) MarkOffset(topic string, partition int32, offset int64, metadata string) {
	if s.onMark != nil {
		s.onMark(topic, partition, offset)
	}
	s.fakeSession.MarkOffset(topic, partition, offset, metadata)
}

func setupComparatorRecordCoordinator(
	t *testing.T,
	maxEntries int,
	committer OffsetCommitter,
	events *[]string,
) (*comparatorRecordCoordinator, *comparator.Run, string, *comparatorMarkSession) {
	t.Helper()
	return setupComparatorRecordCoordinatorWithContext(t, context.Background(), maxEntries, committer, events)
}

func setupComparatorRecordCoordinatorWithContext(
	t *testing.T,
	ctx context.Context,
	maxEntries int,
	committer OffsetCommitter,
	events *[]string,
) (*comparatorRecordCoordinator, *comparator.Run, string, *comparatorMarkSession) {
	t.Helper()
	metadata := newFakeComparatorMetadata(map[string][]int32{
		"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
	})
	assignment, err := newComparatorAssignmentCoordinator(
		metadata,
		map[comparator.StreamRole]string{
			comparator.StreamInput:  "trigger-input",
			comparator.StreamGo:     "go-decision",
			comparator.StreamPython: "py-decision",
		},
		maxEntries,
		10*time.Second,
	)
	if err != nil {
		t.Fatalf("newComparatorAssignmentCoordinator() error = %v", err)
	}
	session := &comparatorMarkSession{fakeSession: newFakeSession(ctx, events)}
	session.claims = map[string][]int32{
		"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
	}
	handle, err := assignment.Setup(session)
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	for _, claim := range []*fakeClaim{
		{topic: "trigger-input", partition: 0, initial: 10},
		{topic: "go-decision", partition: 0, initial: 20},
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
	coordinator, err := newComparatorRecordCoordinator(assignment, handle, session, committer)
	if err != nil {
		t.Fatalf("newComparatorRecordCoordinator() error = %v", err)
	}
	return coordinator, run, epoch, session
}

type comparatorGoldenFixture struct {
	Name       string          `json:"name"`
	StrategyIR json.RawMessage `json:"strategy_ir"`
	Outcome    json.RawMessage `json:"outcome"`
}

func comparatorTriggerInputFixture(t *testing.T, name string) ([]byte, []byte) {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "contract", "testdata", "python-v1", "detection_outcome_v1.json"))
	if err != nil {
		t.Fatalf("ReadFile(golden) error = %v", err)
	}
	var document struct {
		Fixtures []comparatorGoldenFixture `json:"fixtures"`
	}
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatalf("json.Unmarshal(golden) error = %v", err)
	}
	for _, fixture := range document.Fixtures {
		if fixture.Name != name {
			continue
		}
		inputPayload, err := json.Marshal(map[string]any{
			"schema":                 map[string]any{"name": "trigger-input", "major": 1, "minor": 0},
			"required_features":      []string{},
			"partition_hash_version": contract.PartitionHashVersionV1,
			"strategy_ir":            fixture.StrategyIR,
			"detection_outcomes":     []json.RawMessage{fixture.Outcome},
		})
		if err != nil {
			t.Fatalf("json.Marshal(input) error = %v", err)
		}
		input, err := contract.DecodeTriggerInput(inputPayload)
		if err != nil {
			t.Fatalf("DecodeTriggerInput() error = %v", err)
		}
		key, err := input.PartitionKey()
		if err != nil {
			t.Fatalf("PartitionKey() error = %v", err)
		}
		return inputPayload, key
	}
	t.Fatalf("fixture %q not found", name)
	return nil, nil
}

func comparatorTriggerDecisionFixture(t *testing.T, input *contract.TriggerInput) ([]byte, []byte) {
	t.Helper()
	if input == nil || len(input.DetectionOutcomes) != 1 {
		t.Fatal("decision fixture requires exactly one decoded outcome")
	}
	source := input.DetectionOutcomes[0]
	decisionID, err := contract.DeriveTriggerDecisionID(source.InputID)
	if err != nil {
		t.Fatalf("DeriveTriggerDecisionID() error = %v", err)
	}
	level := 3
	batch, err := input.BuildTriggerDecisionBatch([]contract.TriggerDecision{{
		DecisionID:        decisionID,
		InputID:           source.InputID,
		RecordID:          source.Record.RecordID,
		Outcome:           contract.DecisionOutcomeTrigger,
		ReasonCode:        contract.DecisionReasonTriggerConditionMet,
		Level:             &level,
		AnomalyTimestamps: []int64{source.Record.SourceTime},
	}})
	if err != nil {
		t.Fatalf("BuildTriggerDecisionBatch() error = %v", err)
	}
	payload, err := contract.EncodeTriggerDecisionBatch(batch)
	if err != nil {
		t.Fatalf("EncodeTriggerDecisionBatch() error = %v", err)
	}
	key, err := batch.PartitionKey()
	if err != nil {
		t.Fatalf("PartitionKey() error = %v", err)
	}
	return payload, key
}
