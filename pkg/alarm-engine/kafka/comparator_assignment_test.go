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
	"sync"
	"testing"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/comparator"
)

func TestComparatorAssignmentRequiresOneMemberToOwnEveryPartition(t *testing.T) {
	t.Parallel()

	metadata := newFakeComparatorMetadata(map[string][]int32{
		"trigger-input": {0, 1},
		"go-decision":   {0},
		"py-decision":   {0},
	})
	assignment := mustComparatorAssignment(t, metadata)
	for name, claims := range map[string]map[string][]int32{
		"partial input": {
			"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
		},
		"extra topic": {
			"trigger-input": {0, 1}, "go-decision": {0}, "py-decision": {0}, "unexpected": {0},
		},
	} {
		t.Run(name, func(t *testing.T) {
			events := []string{}
			session := newFakeSession(context.Background(), &events)
			session.claims = claims
			if _, err := assignment.Setup(session); err == nil {
				t.Fatal("Setup() accepted an incomplete or unexpected topology")
			}
		})
	}
}

func TestComparatorAssignmentPreCanceledSetupDoesNotReadMetadata(t *testing.T) {
	t.Parallel()

	metadata := newFakeComparatorMetadata(map[string][]int32{
		"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
	})
	assignment := mustComparatorAssignment(t, metadata)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	events := []string{}
	session := newFakeSession(ctx, &events)
	session.claims = map[string][]int32{
		"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
	}
	if _, err := assignment.Setup(session); !errors.Is(err, context.Canceled) {
		t.Fatalf("Setup() error = %v, want context canceled", err)
	}
	metadata.mu.Lock()
	defer metadata.mu.Unlock()
	if len(metadata.refreshes) != 0 {
		t.Fatalf("Setup() refreshed metadata after cancellation: %#v", metadata.refreshes)
	}
}

func TestComparatorAssignmentCancellationDuringMetadataDoesNotInstallGeneration(t *testing.T) {
	t.Parallel()

	metadata := newFakeComparatorMetadata(map[string][]int32{
		"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
	})
	metadata.refreshStarted = make(chan struct{})
	metadata.refreshRelease = make(chan struct{})
	assignment := mustComparatorAssignment(t, metadata)
	ctx, cancel := context.WithCancel(context.Background())
	events := []string{}
	session := newFakeSession(ctx, &events)
	session.claims = map[string][]int32{
		"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
	}
	type setupResult struct {
		handle *comparatorAssignmentHandle
		err    error
	}
	result := make(chan setupResult, 1)
	go func() {
		handle, err := assignment.Setup(session)
		result <- setupResult{handle: handle, err: err}
	}()
	<-metadata.refreshStarted
	cancel()
	close(metadata.refreshRelease)
	setup := <-result
	if setup.handle != nil || !errors.Is(setup.err, context.Canceled) {
		t.Fatalf("Setup() handle=%p error=%v, want nil/context canceled", setup.handle, setup.err)
	}

	nextSession := newFakeSession(context.Background(), &events)
	nextSession.claims = session.claims
	handle, err := assignment.Setup(nextSession)
	if err != nil || handle == nil {
		t.Fatalf("Setup(next) handle=%p error=%v", handle, err)
	}
	if err := assignment.Cleanup(handle, nextSession); err != nil {
		t.Fatalf("Cleanup(next) error = %v", err)
	}
}

func TestComparatorAssignmentRejectsUntrustworthyMetadata(t *testing.T) {
	t.Parallel()

	want := errors.New("metadata unavailable")
	for name, metadata := range map[string]*fakeComparatorMetadata{
		"empty partitions": newFakeComparatorMetadata(map[string][]int32{
			"trigger-input": {}, "go-decision": {0}, "py-decision": {0},
		}),
		"duplicate partitions": newFakeComparatorMetadata(map[string][]int32{
			"trigger-input": {0, 0}, "go-decision": {0}, "py-decision": {0},
		}),
		"refresh failure": func() *fakeComparatorMetadata {
			metadata := newFakeComparatorMetadata(map[string][]int32{
				"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
			})
			metadata.refreshErr = want
			return metadata
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assignment := mustComparatorAssignment(t, metadata)
			events := []string{}
			session := newFakeSession(context.Background(), &events)
			session.claims = map[string][]int32{
				"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
			}
			if _, err := assignment.Setup(session); err == nil {
				t.Fatal("Setup() accepted untrustworthy metadata")
			}
		})
	}
}

func TestComparatorAssignmentCreatesOneRunAfterEveryClaimRegisters(t *testing.T) {
	t.Parallel()

	metadata := newFakeComparatorMetadata(map[string][]int32{
		"trigger-input": {0, 1},
		"go-decision":   {0},
		"py-decision":   {0},
	})
	metadata.offsets[metadataOffset{"trigger-input", 1, sarama.OffsetOldest}] = 11
	assignment := mustComparatorAssignment(t, metadata)
	events := []string{}
	session := newFakeSession(context.Background(), &events)
	session.claims = map[string][]int32{
		"trigger-input": {0, 1}, "go-decision": {0}, "py-decision": {0},
	}
	handle, err := assignment.Setup(session)
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}

	claims := []*fakeClaim{
		{topic: "trigger-input", partition: 0, initial: 10, highWater: 100},
		{topic: "trigger-input", partition: 1, initial: sarama.OffsetOldest, highWater: 100},
		{topic: "go-decision", partition: 0, initial: 20, highWater: 100},
		{topic: "py-decision", partition: 0, initial: 30, highWater: 100},
	}
	for _, claim := range claims[:len(claims)-1] {
		if err := assignment.RegisterClaim(handle, session, claim); err != nil {
			t.Fatalf("RegisterClaim() error = %v", err)
		}
	}
	waitContext, cancelWait := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelWait()
	if _, _, err := assignment.WaitReady(waitContext, handle, session); err == nil {
		t.Fatal("WaitReady() returned before all claims registered")
	}
	if err := assignment.RegisterClaim(handle, session, claims[len(claims)-1]); err != nil {
		t.Fatalf("RegisterClaim(last) error = %v", err)
	}
	run, epoch, err := assignment.WaitReady(context.Background(), handle, session)
	if err != nil || run == nil || epoch == "" {
		t.Fatalf("WaitReady() run=%p epoch=%q error=%v", run, epoch, err)
	}
	for _, expected := range []struct {
		role      comparator.StreamRole
		partition int32
		offset    int64
	}{
		{comparator.StreamInput, 0, 10},
		{comparator.StreamInput, 1, 11},
		{comparator.StreamGo, 0, 20},
		{comparator.StreamPython, 0, 30},
	} {
		offset, err := run.NextOffset(epoch, expected.role, expected.partition)
		if err != nil || offset != expected.offset {
			t.Fatalf("NextOffset(%d,%d) = %d, error=%v, want %d", expected.role, expected.partition, offset, err, expected.offset)
		}
	}
	metadata.assertOffsetCalls(t, []metadataOffset{
		{"trigger-input", 0, sarama.OffsetNewest},
		{"trigger-input", 1, sarama.OffsetOldest},
		{"trigger-input", 1, sarama.OffsetNewest},
		{"go-decision", 0, sarama.OffsetNewest},
		{"py-decision", 0, sarama.OffsetNewest},
	})
}

func TestComparatorAssignmentUsesBrokerNewestInsteadOfClaimStartupHighWater(t *testing.T) {
	t.Parallel()

	metadata := newFakeComparatorMetadata(map[string][]int32{
		"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
	})
	metadata.offsets[metadataOffset{"trigger-input", 0, sarama.OffsetNewest}] = 100
	assignment := mustComparatorAssignment(t, metadata)
	events := []string{}
	session := newFakeSession(context.Background(), &events)
	session.claims = map[string][]int32{
		"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
	}
	handle, err := assignment.Setup(session)
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if err := assignment.RegisterClaim(handle, session, &fakeClaim{
		topic: "trigger-input", partition: 0, initial: 10, highWater: 0,
	}); err != nil {
		t.Fatalf("RegisterClaim() rejected a valid offset before the first fetch: %v", err)
	}
}

func TestComparatorAssignmentRejectsUnsafeInitialOffsets(t *testing.T) {
	t.Parallel()

	want := errors.New("offset unavailable")
	for _, test := range []struct {
		name        string
		initial     int64
		resolved    int64
		resolverErr error
		newest      int64
		newestErr   error
	}{
		{name: "newest", initial: sarama.OffsetNewest, newest: 100},
		{name: "unknown value", initial: -3, newest: 100},
		{name: "oldest resolver failure", initial: sarama.OffsetOldest, resolverErr: want, newest: 100},
		{name: "newest resolver failure", initial: 10, newestErr: want},
		{name: "negative resolution", initial: sarama.OffsetOldest, resolved: -1, newest: 100},
		{name: "overflow resolution", initial: sarama.OffsetOldest, resolved: math.MaxInt64, newest: 100},
		{name: "resolution beyond high water", initial: sarama.OffsetOldest, resolved: 101, newest: 100},
		{name: "negative high water", initial: 10, newest: -1},
		{name: "overflow high water", initial: 10, newest: math.MaxInt64},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			metadata := newFakeComparatorMetadata(map[string][]int32{
				"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
			})
			coordinate := metadataOffset{"trigger-input", 0, sarama.OffsetOldest}
			metadata.offsets[coordinate] = test.resolved
			metadata.offsetErrors[coordinate] = test.resolverErr
			newestCoordinate := metadataOffset{"trigger-input", 0, sarama.OffsetNewest}
			metadata.offsets[newestCoordinate] = test.newest
			metadata.offsetErrors[newestCoordinate] = test.newestErr
			assignment := mustComparatorAssignment(t, metadata)
			events := []string{}
			session := newFakeSession(context.Background(), &events)
			session.claims = map[string][]int32{
				"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
			}
			handle, err := assignment.Setup(session)
			if err != nil {
				t.Fatalf("Setup() error = %v", err)
			}
			if err := assignment.RegisterClaim(handle, session, &fakeClaim{
				topic: "trigger-input", partition: 0, initial: test.initial,
			}); err == nil {
				t.Fatal("RegisterClaim() accepted an unsafe initial offset")
			}
			if _, _, err := assignment.WaitReady(context.Background(), handle, session); err == nil {
				t.Fatal("WaitReady() accepted a failed assignment")
			}
		})
	}
}

func TestComparatorAssignmentDuplicateClaimInvalidatesGeneration(t *testing.T) {
	t.Parallel()

	metadata := newFakeComparatorMetadata(map[string][]int32{
		"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
	})
	assignment := mustComparatorAssignment(t, metadata)
	events := []string{}
	session := newFakeSession(context.Background(), &events)
	session.claims = map[string][]int32{
		"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
	}
	handle, err := assignment.Setup(session)
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	claim := &fakeClaim{topic: "trigger-input", partition: 0, initial: 10, highWater: 100}
	if err := assignment.RegisterClaim(handle, session, claim); err != nil {
		t.Fatalf("RegisterClaim(first) error = %v", err)
	}
	if err := assignment.RegisterClaim(handle, session, claim); err == nil {
		t.Fatal("RegisterClaim() accepted a duplicate claim")
	}
	if _, _, err := assignment.WaitReady(context.Background(), handle, session); err == nil {
		t.Fatal("WaitReady() accepted a generation with duplicate claims")
	}
}

func TestComparatorAssignmentConcurrentClaimsShareOneRun(t *testing.T) {
	t.Parallel()

	metadata := newFakeComparatorMetadata(map[string][]int32{
		"trigger-input": {0, 1}, "go-decision": {0}, "py-decision": {0},
	})
	assignment := mustComparatorAssignment(t, metadata)
	events := []string{}
	session := newFakeSession(context.Background(), &events)
	session.claims = map[string][]int32{
		"trigger-input": {0, 1}, "go-decision": {0}, "py-decision": {0},
	}
	handle, err := assignment.Setup(session)
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	claims := []*fakeClaim{
		{topic: "trigger-input", partition: 0, initial: 10, highWater: 0},
		{topic: "trigger-input", partition: 1, initial: 11, highWater: 0},
		{topic: "go-decision", partition: 0, initial: 20, highWater: 0},
		{topic: "py-decision", partition: 0, initial: 30, highWater: 0},
	}
	registerErrors := make(chan error, len(claims))
	var registerGroup sync.WaitGroup
	for _, claim := range claims {
		registerGroup.Add(1)
		go func(current *fakeClaim) {
			defer registerGroup.Done()
			registerErrors <- assignment.RegisterClaim(handle, session, current)
		}(claim)
	}
	registerGroup.Wait()
	close(registerErrors)
	for err := range registerErrors {
		if err != nil {
			t.Fatalf("RegisterClaim() error = %v", err)
		}
	}

	type waitResult struct {
		run   *comparator.Run
		epoch string
		err   error
	}
	results := make(chan waitResult, 4)
	for range 4 {
		go func() {
			run, epoch, err := assignment.WaitReady(context.Background(), handle, session)
			results <- waitResult{run: run, epoch: epoch, err: err}
		}()
	}
	var expectedRun *comparator.Run
	var expectedEpoch string
	for range 4 {
		result := <-results
		if result.err != nil {
			t.Fatalf("WaitReady() error = %v", result.err)
		}
		if expectedRun == nil {
			expectedRun, expectedEpoch = result.run, result.epoch
			continue
		}
		if result.run != expectedRun || result.epoch != expectedEpoch {
			t.Fatal("concurrent waiters observed different Run or epoch values")
		}
	}
}

func TestComparatorAssignmentFailureWakesEveryWaiter(t *testing.T) {
	t.Parallel()

	metadata := newFakeComparatorMetadata(map[string][]int32{
		"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
	})
	assignment := mustComparatorAssignment(t, metadata)
	events := []string{}
	session := newFakeSession(context.Background(), &events)
	session.claims = map[string][]int32{
		"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
	}
	handle, err := assignment.Setup(session)
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	claim := &fakeClaim{topic: "trigger-input", partition: 0, initial: 10, highWater: 0}
	if err := assignment.RegisterClaim(handle, session, claim); err != nil {
		t.Fatalf("RegisterClaim(first) error = %v", err)
	}
	waitErrors := make(chan error, 4)
	for range 4 {
		waitContext := newObservedWaitContext(context.Background())
		go func() {
			_, _, err := assignment.WaitReady(waitContext, handle, session)
			waitErrors <- err
		}()
		<-waitContext.entered
	}
	duplicateErr := assignment.RegisterClaim(handle, session, claim)
	if duplicateErr == nil {
		t.Fatal("RegisterClaim() accepted a duplicate claim")
	}
	for range 4 {
		if err := <-waitErrors; err == nil || err.Error() != duplicateErr.Error() {
			t.Fatalf("WaitReady() error = %v, want %v", err, duplicateErr)
		}
	}
}

func TestComparatorAssignmentStaleWatcherCannotInvalidateNewGeneration(t *testing.T) {
	t.Parallel()

	metadata := newFakeComparatorMetadata(map[string][]int32{
		"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
	})
	assignment := mustComparatorAssignment(t, metadata)
	parentContext, cancelOld := context.WithCancel(context.Background())
	oldContext := newObservedCancelContext(parentContext)
	events := []string{}
	oldSession := newFakeSession(oldContext, &events)
	oldSession.claims = map[string][]int32{
		"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
	}
	oldHandle, err := assignment.Setup(oldSession)
	if err != nil {
		t.Fatalf("Setup(old) error = %v", err)
	}
	if err := assignment.Cleanup(oldHandle, oldSession); err != nil {
		t.Fatalf("Cleanup(old) error = %v", err)
	}
	_, _, currentRun, _ := setupComparatorRun(t, assignment, 2, "member-2")
	cancelOld()
	select {
	case <-oldContext.observed:
	case <-time.After(time.Second):
		t.Fatal("stale session watcher did not observe cancellation")
	}
	if !currentRun.Valid() {
		t.Fatal("a stale session watcher invalidated the current Run")
	}
}

func TestComparatorAssignmentHandleRejectsSameIdentityABA(t *testing.T) {
	t.Parallel()

	metadata := newFakeComparatorMetadata(map[string][]int32{
		"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
	})
	assignment := mustComparatorAssignment(t, metadata)
	firstSession, firstHandle, firstRun, firstEpoch := setupComparatorRun(t, assignment, 1, "member-1")
	if err := assignment.Cleanup(firstHandle, firstSession); err != nil {
		t.Fatalf("Cleanup(first) error = %v", err)
	}
	if firstRun.Valid() {
		t.Fatal("Cleanup() left the old Run valid")
	}

	secondSession, secondHandle, secondRun, secondEpoch := setupComparatorRun(t, assignment, 1, "member-1")
	if secondRun == firstRun || secondEpoch == firstEpoch {
		t.Fatal("a new assignment reused the old Run or epoch")
	}
	if _, _, err := assignment.WaitReady(context.Background(), firstHandle, firstSession); err == nil {
		t.Fatal("WaitReady() accepted a stale handle with a reused session identity")
	}
	if err := assignment.Cleanup(firstHandle, firstSession); err != nil {
		t.Fatalf("Cleanup(stale) error = %v", err)
	}
	if !secondRun.Valid() {
		t.Fatal("stale Cleanup invalidated the current Run")
	}
	if err := assignment.RegisterClaim(firstHandle, firstSession, &fakeClaim{
		topic: "trigger-input", partition: 0, initial: 10,
	}); err == nil {
		t.Fatal("RegisterClaim() accepted a stale assignment")
	}
	if !secondRun.Valid() {
		t.Fatal("a stale claim invalidated the current Run")
	}
	if err := assignment.Cleanup(secondHandle, secondSession); err != nil {
		t.Fatalf("Cleanup(second) error = %v", err)
	}
}

func mustComparatorAssignment(t *testing.T, metadata *fakeComparatorMetadata) *comparatorAssignmentCoordinator {
	t.Helper()
	assignment, err := newComparatorAssignmentCoordinator(
		metadata,
		map[comparator.StreamRole]string{
			comparator.StreamInput:  "trigger-input",
			comparator.StreamGo:     "go-decision",
			comparator.StreamPython: "py-decision",
		},
		100,
		10*time.Second,
	)
	if err != nil {
		t.Fatalf("newComparatorAssignment() error = %v", err)
	}
	return assignment
}

func setupComparatorRun(
	t *testing.T,
	assignment *comparatorAssignmentCoordinator,
	generation int32,
	member string,
) (*fakeSession, *comparatorAssignmentHandle, *comparator.Run, string) {
	t.Helper()
	events := []string{}
	session := newFakeSession(context.Background(), &events)
	session.generation = generation
	session.member = member
	session.claims = map[string][]int32{
		"trigger-input": {0}, "go-decision": {0}, "py-decision": {0},
	}
	handle, err := assignment.Setup(session)
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	claims := []*fakeClaim{
		{topic: "trigger-input", partition: 0, initial: 10, highWater: 100},
		{topic: "go-decision", partition: 0, initial: 20, highWater: 100},
		{topic: "py-decision", partition: 0, initial: 30, highWater: 100},
	}
	for _, claim := range claims {
		if err := assignment.RegisterClaim(handle, session, claim); err != nil {
			t.Fatalf("RegisterClaim() error = %v", err)
		}
	}
	run, epoch, err := assignment.WaitReady(context.Background(), handle, session)
	if err != nil {
		t.Fatalf("WaitReady() error = %v", err)
	}
	return session, handle, run, epoch
}

type metadataOffset struct {
	topic     string
	partition int32
	time      int64
}

type fakeComparatorMetadata struct {
	mu             sync.Mutex
	refreshOnce    sync.Once
	partitions     map[string][]int32
	offsets        map[metadataOffset]int64
	offsetErrors   map[metadataOffset]error
	offsetCalls    []metadataOffset
	refreshes      [][]string
	refreshErr     error
	refreshStarted chan struct{}
	refreshRelease chan struct{}
}

type observedCancelContext struct {
	context.Context
	once     sync.Once
	observed chan struct{}
}

type observedWaitContext struct {
	context.Context
	once    sync.Once
	entered chan struct{}
}

func newObservedCancelContext(parent context.Context) *observedCancelContext {
	return &observedCancelContext{Context: parent, observed: make(chan struct{})}
}

func newObservedWaitContext(parent context.Context) *observedWaitContext {
	return &observedWaitContext{Context: parent, entered: make(chan struct{})}
}

func (c *observedCancelContext) Err() error {
	err := c.Context.Err()
	if err != nil {
		c.once.Do(func() { close(c.observed) })
	}
	return err
}

func (c *observedWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

func newFakeComparatorMetadata(partitions map[string][]int32) *fakeComparatorMetadata {
	metadata := &fakeComparatorMetadata{
		partitions: partitions, offsets: make(map[metadataOffset]int64), offsetErrors: make(map[metadataOffset]error),
	}
	for topic, topicPartitions := range partitions {
		for _, partition := range topicPartitions {
			metadata.offsets[metadataOffset{topic, partition, sarama.OffsetNewest}] = 100
		}
	}
	return metadata
}

func (m *fakeComparatorMetadata) RefreshMetadata(topics ...string) error {
	m.mu.Lock()
	m.refreshes = append(m.refreshes, append([]string(nil), topics...))
	err := m.refreshErr
	started := m.refreshStarted
	release := m.refreshRelease
	m.mu.Unlock()
	if started != nil {
		m.refreshOnce.Do(func() { close(started) })
	}
	if release != nil {
		<-release
	}
	return err
}

func (m *fakeComparatorMetadata) Partitions(topic string) ([]int32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	partitions, ok := m.partitions[topic]
	if !ok {
		return nil, fmt.Errorf("unknown topic %q", topic)
	}
	return append([]int32(nil), partitions...), nil
}

func (m *fakeComparatorMetadata) GetOffset(topic string, partition int32, timestamp int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	coordinate := metadataOffset{topic, partition, timestamp}
	m.offsetCalls = append(m.offsetCalls, coordinate)
	if err := m.offsetErrors[coordinate]; err != nil {
		return 0, err
	}
	offset, ok := m.offsets[coordinate]
	if !ok {
		return 0, fmt.Errorf("offset not configured for %#v", coordinate)
	}
	return offset, nil
}

func (m *fakeComparatorMetadata) assertOffsetCalls(t *testing.T, expected []metadataOffset) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if fmt.Sprint(m.offsetCalls) != fmt.Sprint(expected) {
		t.Fatalf("GetOffset() calls = %#v, want %#v", m.offsetCalls, expected)
	}
}
