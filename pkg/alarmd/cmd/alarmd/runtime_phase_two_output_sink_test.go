// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// configuringPhaseTwoEventSink records the converters it was configured
// with, so a test can see that a sink opened late received the ones
// configured while it was closed.
type configuringPhaseTwoEventSink struct {
	recordingPhaseTwoEventSink
	standardConfigured bool
	legacyTopic        string
}

func (s *configuringPhaseTwoEventSink) ConfigureStandardOutput(enginekafka.StandardEventConverter) error {
	s.standardConfigured = true
	return nil
}

func (s *configuringPhaseTwoEventSink) ConfigureLegacyOutput(_ enginekafka.LegacyEventConverter, topic string, _ int) error {
	s.legacyTopic = topic
	return nil
}

// WriteBatch records without validating: what is under test is whether the
// write reached the inner sink, not the event's shape.
func (s *configuringPhaseTwoEventSink) WriteBatch(_ context.Context, events []contract.TriggerEventV1) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, events...)
	return nil
}

// failingThenOpeningOpener fails the first failures attempts and then opens
// the sink it was given.
type failingThenOpeningOpener struct {
	mu       sync.Mutex
	failures int
	calls    int
	sink     productionPhaseTwoEventSink
}

func (opener *failingThenOpeningOpener) Open() (productionPhaseTwoEventSink, error) {
	opener.mu.Lock()
	defer opener.mu.Unlock()
	opener.calls++
	if opener.calls <= opener.failures {
		return nil, errors.New("kafka trigger event sink: open producer: client has run out of available brokers (last dial kafka://user:secret@broker:9092)")
	}
	return opener.sink, nil
}

// The sink that did not open is retried on a doubling interval capped at
// thirty seconds, and the process is told after every attempt; the sink
// opened late receives the converters configured while it was closed, and
// a write before then is a retryable output dependency, not a lost event.
func TestLazyOutputSinkRetriesWithDoublingIntervalsCappedAndConfiguresLate(t *testing.T) {
	inner := &configuringPhaseTwoEventSink{}
	opener := &failingThenOpeningOpener{failures: 8, sink: inner}
	clock := time.Unix(1_700_000_000, 0)
	sink, err := newLazyOutputSink(opener, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	var waits []time.Duration
	var waitsMu sync.Mutex
	released := make(chan struct{})
	sink.sleep = func(ctx context.Context, wait time.Duration) bool {
		waitsMu.Lock()
		waits = append(waits, wait)
		waitsMu.Unlock()
		select {
		case <-ctx.Done():
			return false
		case <-released:
			return true
		}
	}
	var states []outputSinkState
	var statesMu sync.Mutex
	sink.SetOnChange(func(state outputSinkState) {
		statesMu.Lock()
		states = append(states, state)
		statesMu.Unlock()
	})
	if err := sink.ConfigureStandardOutput(nil); err != nil {
		t.Fatal(err)
	}
	if err := sink.ConfigureLegacyOutput(nil, "compat-topic", 1024); err != nil {
		t.Fatal(err)
	}

	sink.Start()
	// The first attempt ran inline and failed: not ready, one attempt, the
	// failure kept without the credential, and a write refused as retryable.
	first := sink.State()
	if first.Ready || first.Attempts != 1 || first.LastFailureAt != clock || !strings.Contains(first.LastFailure, "run out of available brokers") {
		t.Fatalf("state after the first attempt = %+v, want not ready after one failed attempt", first)
	}
	// The text goes through the same redaction the log lines do.
	if strings.Contains(first.LastFailure, "secret@") {
		t.Fatalf("failure text kept a credential: %q", first.LastFailure)
	}
	err = sink.WriteBatch(context.Background(), []contract.TriggerEventV1{{}})
	var retryable interface{ RetryableOutputDependency() }
	if err == nil || !errors.As(err, &retryable) {
		t.Fatalf("write before the sink opened = %v, want a retryable output dependency error", err)
	}
	if len(inner.events) != 0 {
		t.Fatalf("a write before the sink opened reached the inner sink: %+v", inner.events)
	}

	close(released)
	sink.retrying.Wait()
	final := sink.State()
	if !final.Ready || final.Attempts != 9 || final.Since != clock || final.LastFailure != "" || !final.LastFailureAt.IsZero() {
		t.Fatalf("state after opening = %+v, want ready on the ninth attempt with the failure cleared", final)
	}
	wantWaits := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second,
		30 * time.Second, 30 * time.Second, 30 * time.Second}
	waitsMu.Lock()
	gotWaits := append([]time.Duration(nil), waits...)
	waitsMu.Unlock()
	if !reflect.DeepEqual(gotWaits, wantWaits) {
		t.Fatalf("retry intervals = %v, want doubling from 1s and capped at 30s: %v", gotWaits, wantWaits)
	}
	if !inner.standardConfigured || inner.legacyTopic != "compat-topic" {
		t.Fatalf("the sink opened late was not given the converters configured while closed: standard=%v legacy=%q", inner.standardConfigured, inner.legacyTopic)
	}
	statesMu.Lock()
	reported := append([]outputSinkState(nil), states...)
	statesMu.Unlock()
	if len(reported) != 9 || reported[0].Ready || !reported[8].Ready || reported[8].Attempts != 9 {
		t.Fatalf("reported states = %d (first %+v, last %+v), want one per attempt ending ready", len(reported), reported[0], reported[len(reported)-1])
	}
	if err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{{EventID: "e1"}}); err != nil {
		t.Fatalf("write after opening = %v", err)
	}
	if len(inner.events) != 1 {
		t.Fatalf("write after opening did not reach the inner sink: %+v", inner.events)
	}
	if err := sink.Close(); err != nil || !inner.closed {
		t.Fatalf("Close() = %v, inner closed = %v", err, inner.closed)
	}
}

// Closing a sink that is still retrying ends the retry; a sink that opens
// after the close is closed again rather than kept.
func TestLazyOutputSinkCloseEndsTheRetry(t *testing.T) {
	inner := &configuringPhaseTwoEventSink{}
	opener := &failingThenOpeningOpener{failures: 1 << 20, sink: inner}
	sink, err := newLazyOutputSink(opener, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	sink.sleep = func(ctx context.Context, _ time.Duration) bool {
		select {
		case <-ctx.Done():
			return false
		case <-released:
			return true
		}
	}
	sink.Start()
	done := make(chan error, 1)
	go func() { done <- sink.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close() = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close() did not end the retry loop")
	}
	if state := sink.State(); state.Ready || state.Attempts != 1 {
		t.Fatalf("state after close = %+v, want the single attempt made before the close", state)
	}
}

// negotiatingOpener fails its first attempt the way an open does when a broker
// does not answer ApiVersions -- carrying the answers it did get -- and opens
// the sink it was given on the next.
type negotiatingOpener struct {
	mu    sync.Mutex
	calls int
	sink  productionPhaseTwoEventSink
}

func (opener *negotiatingOpener) Open() (productionPhaseTwoEventSink, error) {
	opener.mu.Lock()
	defer opener.mu.Unlock()
	opener.calls++
	if opener.calls == 1 {
		return nil, &enginekafka.ProtocolNegotiationError{
			Negotiation: enginekafka.ProtocolNegotiation{
				Configured: "0.10.2.0", Wanted: "0.11.0.0", Negotiated: "0.10.2.0", ProduceVersion: 2, WantedProduceVersion: 3,
				Brokers: []enginekafka.BrokerProtocol{
					{Address: "b1:9092", ID: -1, Answered: true, ProduceMinVersion: 0, ProduceMaxVersion: 2},
					{Address: "b2:9092", ID: -1, ProduceMinVersion: -1, ProduceMaxVersion: -1, Error: "EOF"},
				},
			},
			Err: errors.New("kafka: protocol negotiation: broker b2:9092 did not answer ApiVersions: EOF"),
		}
	}
	return opener.sink, nil
}

// The sink's state carries the brokers' answer: an attempt that failed
// because a broker did not answer keeps the partial answers, naming the
// broker, and the sink opened afterwards replaces them with the agreement it
// speaks under. Before the first attempt there is nothing, and that is a
// different state from either.
func TestLazyOutputSinkStateCarriesTheBrokersAnswer(t *testing.T) {
	inner := &configuringPhaseTwoEventSink{}
	inner.protocol = &enginekafka.ProtocolNegotiation{
		Configured: "0.10.2.0", Wanted: "0.11.0.0", Negotiated: "0.11.0.0", ProduceVersion: 3, WantedProduceVersion: 3, HeadersSupported: true,
		Brokers: []enginekafka.BrokerProtocol{
			{Address: "b1:9092", ID: -1, Answered: true, ProduceMinVersion: 0, ProduceMaxVersion: 7},
			{Address: "b2:9092", ID: -1, Answered: true, ProduceMinVersion: 0, ProduceMaxVersion: 7},
		},
	}
	opener := &negotiatingOpener{sink: inner}
	sink, err := newLazyOutputSink(opener, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if sink.ProtocolNegotiation() != nil {
		t.Fatal("a sink that has not tried claims an agreement")
	}
	if sink.tryOpen() {
		t.Fatal("the first attempt opened")
	}
	partial := sink.State()
	if partial.Ready || partial.Protocol == nil || len(partial.Protocol.Brokers) != 2 ||
		partial.Protocol.Brokers[1].Answered || partial.Protocol.Brokers[1].Error != "EOF" || partial.Protocol.Negotiated != "0.10.2.0" {
		t.Fatalf("state after a negotiation failure = %+v, want the partial answers naming b2", partial)
	}
	if !strings.Contains(partial.LastFailure, "b2:9092 did not answer") {
		t.Fatalf("last failure = %q", partial.LastFailure)
	}
	if !sink.tryOpen() {
		t.Fatal("the second attempt did not open")
	}
	opened := sink.State()
	if !opened.Ready || opened.Protocol == nil || opened.Protocol.Negotiated != "0.11.0.0" || !opened.Protocol.HeadersSupported ||
		len(opened.Protocol.Brokers) != 2 || !opened.Protocol.Brokers[1].Answered {
		t.Fatalf("state after opening = %+v, want the open sink's agreement", opened)
	}
	// The state's copy is its own: the sink's later answer does not reach
	// back into a state a reader already holds.
	inner.protocol.Brokers[0].ProduceMaxVersion = 1
	if opened.Protocol.Brokers[0].ProduceMaxVersion == 1 {
		t.Fatal("the state shares the sink's broker list")
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
}

// A replica whose output sink is not open stays up, reports not ready under
// the dependency's name with output_sink_ready false, and registers as
// starting so the rendezvous hands it no Query Groups; when the sink opens
// it reports ready and the next registration is ready. The two places that
// wrote output_sink_ready as a constant true are gone: this is the one test
// that reads it false.
func TestPhaseTwoWorkerBundleWaitsForTheOutputSinkInsteadOfExiting(t *testing.T) {
	queryGroup := execution.QueryGroupIdentity("query-group-1")
	health := newPhaseTwoApplicationHealth()
	runner := newFakePhaseTwoQueryGroup()
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{queryGroup}}
	owner := &fakePhaseTwoOwnership{follower: true, assigned: []execution.QueryGroupIdentity{queryGroup}, runner: runner}
	bundle, _ := newBundleWithRecorder(t, health, control, owner)

	inner := &configuringPhaseTwoEventSink{}
	opener := &failingThenOpeningOpener{failures: 1, sink: inner}
	sink, err := newLazyOutputSink(opener, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	sink.sleep = func(ctx context.Context, _ time.Duration) bool {
		select {
		case <-ctx.Done():
			return false
		case <-released:
			return true
		}
	}
	sink.SetOnChange(bundle.outputSinkChanged)
	sink.Start()
	defer func() { _ = sink.Close() }()

	if err := bundle.Start(context.Background()); err != nil {
		t.Fatalf("Start(output sink closed) error = %v, want the replica to wait rather than exit", err)
	}
	defer func() { _ = bundle.Shutdown(context.Background()) }()
	snapshot := health.HealthSnapshot()
	if snapshot.State != observability.HealthNotReady || snapshot.Ready || snapshot.OutputSinkReady ||
		!reflect.DeepEqual(snapshot.Reasons, []observability.ReasonCode{phaseTwoOutputSinkReason}) {
		t.Fatalf("health with the sink closed = %+v, want not_ready under KAFKA_UNAVAILABLE with output_sink_ready false", snapshot)
	}
	if got := lastRegistration(owner); got != ownership.WorkerStarting {
		t.Fatalf("registration with the sink closed = %s, want STARTING: a replica that cannot publish must not be placed onto", got)
	}
	// A renewal while the sink is closed still registers starting.
	if err := bundle.register(context.Background(), ownership.WorkerReady); err != nil {
		t.Fatal(err)
	}
	if got := lastRegistration(owner); got != ownership.WorkerStarting {
		t.Fatalf("renewal with the sink closed registered %s, want STARTING", got)
	}

	close(released)
	sink.retrying.Wait()
	if state := sink.State(); !state.Ready || state.Attempts != 2 {
		t.Fatalf("sink after the retry = %+v, want open on the second attempt", state)
	}
	snapshot = health.HealthSnapshot()
	if snapshot.State != observability.HealthReady || !snapshot.Ready || !snapshot.OutputSinkReady {
		t.Fatalf("health after the sink opened = %+v, want ready with output_sink_ready true", snapshot)
	}
	// The next renewal registers ready: the renewal loop does this within
	// one interval; here it is called directly.
	if err := bundle.register(context.Background(), ownership.WorkerReady); err != nil {
		t.Fatal(err)
	}
	if got := lastRegistration(owner); got != ownership.WorkerReady {
		t.Fatalf("renewal after the sink opened registered %s, want READY", got)
	}
}

// The configuration half still refuses to start: a coordinate the client
// configuration cannot be built from is an error at prepare time, before
// any network, and is not retried.
func TestPrepareTriggerEventSinkRefusesBadCoordinatesWithoutDialing(t *testing.T) {
	coordinates := validGoAccessRuntimeConfig().Kafka.TriggerEventCoordinates()
	coordinates.BrokerVersion = "not-a-version"
	if _, err := enginekafka.PrepareTriggerEventSink(coordinates); err == nil || !strings.Contains(err.Error(), "broker_version") {
		t.Fatalf("PrepareTriggerEventSink(bad version) error = %v, want a broker_version refusal", err)
	}
	external := defaultPhaseTwoProductionExternalDependencies()
	if _, err := external.prepareEvents(coordinates); err == nil {
		t.Fatal("the production hook prepared a sink from coordinates the client configuration refuses")
	}
	// Good coordinates prepare without touching the network; only Open does.
	good := validGoAccessRuntimeConfig().Kafka.TriggerEventCoordinates()
	if _, err := external.prepareEvents(good); err != nil {
		t.Fatalf("prepareEvents(good) error = %v", err)
	}
}
