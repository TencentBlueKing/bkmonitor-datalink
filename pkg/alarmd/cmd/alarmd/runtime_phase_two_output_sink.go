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
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// outputSinkOpener is the network half of opening the output sink; the
// configuration half has already run when one exists, so its errors are the
// broker not answering, never the coordinates being wrong.
type outputSinkOpener interface {
	Open() (productionPhaseTwoEventSink, error)
}

type outputSinkOpenerFunc func() (productionPhaseTwoEventSink, error)

func (open outputSinkOpenerFunc) Open() (productionPhaseTwoEventSink, error) { return open() }

// Retry cadence for a sink that did not open: doubling from the first
// interval to the cap, then the cap. Not configuration: a broker outage is
// not something an operator knows the shape of better than the process, and
// thirty seconds is already within the readiness probe's own cadence.
const (
	outputSinkRetryFirst = time.Second
	outputSinkRetryCap   = 30 * time.Second
	// outputSinkFailureTextLimit bounds the failure text kept, as the Redis
	// client health does; long enough for a dial error and its reason.
	outputSinkFailureTextLimit = 256
)

// outputSinkState is what this replica knows about its output sink: whether
// it is open, since when, how many attempts it took or has taken so far, and
// what the last failure said. It is the readiness fact and the dependency
// fact, from one place.
type outputSinkState struct {
	Ready         bool
	Since         time.Time
	Attempts      int
	LastFailureAt time.Time
	LastFailure   string
	// Protocol is what the brokers answered when the sink last asked them:
	// the agreement an open sink speaks under, or the partial answers of an
	// attempt that failed because a broker did not answer. Nil until the
	// first attempt has asked -- and a reader tells the two apart, because
	// an entry that only said "ready" was what the page showed for an hour
	// of every write being refused.
	Protocol *enginekafka.ProtocolNegotiation
}

// outputSinkNotOpenError is the write refused because the sink is not open
// yet. It is a retryable output dependency, like a broker that did not ACK:
// the Slot does not advance and tries again. A replica that is not ready is
// not assigned Query Groups, so this is reached only by work already owned
// when the sink was lost, which is the case the retry exists for.
type outputSinkNotOpenError struct{}

func (*outputSinkNotOpenError) Error() string { return "output sink is not open" }

func (*outputSinkNotOpenError) RetryableOutputDependency() {}

// lazyOutputSink opens the output sink when it can and stands in for it
// until then.
//
// Before this the sink was opened at startup and a broker that did not
// answer ended the process. On a node with no route to the brokers that is
// a crash loop: the rollout stalls on it and the only diagnosis is the
// container log of a container that keeps restarting, while the health
// endpoint's output_sink_ready field was written true by two places that
// could not have known otherwise. Now the process stays up, says it is not
// ready and why, is given no Query Groups, and retries on its own; the
// first attempt still runs at startup, so a broker that answers is opened
// before the worker registers, exactly as before.
//
// Only reachability is lazy. The coordinates are validated before an opener
// exists, and a wrong configuration still refuses to start.
type lazyOutputSink struct {
	opener outputSinkOpener
	now    func() time.Time
	// sleep waits for the next attempt or for the stop; tests inject one
	// that records the intervals. It returns false when the wait was cut.
	sleep func(context.Context, time.Duration) bool
	// onChange is told after every change of state, so readiness and the
	// worker registration follow the sink without polling it.
	onChange func(outputSinkState)

	mu       sync.Mutex
	inner    productionPhaseTwoEventSink
	state    outputSinkState
	standard *lazyStandardOutput
	legacy   *lazyLegacyOutput
	cancel   context.CancelFunc
	retrying sync.WaitGroup
	closed   bool
}

// The converters configured while the sink was closed, kept as they were
// given so the sink opened later is configured exactly as an open one would
// have been.
type lazyStandardOutput struct {
	converter enginekafka.StandardEventConverter
}

type lazyLegacyOutput struct {
	converter enginekafka.LegacyEventConverter
	topic     string
	maxBytes  int
}

func newLazyOutputSink(opener outputSinkOpener, now func() time.Time) (*lazyOutputSink, error) {
	if opener == nil || now == nil {
		return nil, errors.New("output sink requires an opener and a clock")
	}
	return &lazyOutputSink{opener: opener, now: now, sleep: sleepUntil}, nil
}

func sleepUntil(ctx context.Context, wait time.Duration) bool {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Start makes the first attempt now and, if it fails, keeps trying in the
// background until it succeeds or the sink is closed. It never returns the
// broker's error: that error is state, read through State and reported by
// readiness, not a reason to stop.
func (sink *lazyOutputSink) Start() {
	if sink.tryOpen() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	sink.mu.Lock()
	if sink.closed {
		sink.mu.Unlock()
		cancel()
		return
	}
	sink.cancel = cancel
	sink.mu.Unlock()
	sink.retrying.Add(1)
	go sink.retry(ctx)
}

func (sink *lazyOutputSink) retry(ctx context.Context) {
	defer sink.retrying.Done()
	wait := outputSinkRetryFirst
	for {
		if !sink.sleep(ctx, wait) {
			return
		}
		if sink.tryOpen() {
			return
		}
		wait *= 2
		if wait > outputSinkRetryCap {
			wait = outputSinkRetryCap
		}
	}
}

// tryOpen makes one attempt and records its outcome. An open sink receives
// the converters configured while it was closed; a sink opened after the
// sink was closed is closed again rather than kept.
func (sink *lazyOutputSink) tryOpen() bool {
	opened, err := sink.opener.Open()
	at := sink.now()
	sink.mu.Lock()
	sink.state.Attempts++
	if err != nil {
		sink.state.LastFailureAt = at
		sink.state.LastFailure = boundedFailureText(err)
		// An attempt that asked the brokers and got no answer from one of
		// them keeps the answers it did get, so the entry names the broker.
		// Any other failure leaves the last agreement as it was.
		var negotiation *enginekafka.ProtocolNegotiationError
		if errors.As(err, &negotiation) {
			sink.state.Protocol = copyNegotiation(&negotiation.Negotiation)
		}
		state := sink.state
		sink.mu.Unlock()
		sink.notify(state)
		return false
	}
	if sink.closed {
		sink.mu.Unlock()
		_ = opened.Close()
		return true
	}
	if sink.standard != nil {
		if configureErr := opened.ConfigureStandardOutput(sink.standard.converter); configureErr != nil {
			err = errors.Join(err, configureErr)
		}
	}
	if sink.legacy != nil {
		if configureErr := opened.ConfigureLegacyOutput(sink.legacy.converter, sink.legacy.topic, sink.legacy.maxBytes); configureErr != nil {
			err = errors.Join(err, configureErr)
		}
	}
	if err != nil {
		// The converters were accepted by the same sink type at the same
		// versions the deployment validated; a refusal here is a defect,
		// and an open sink that could not be configured must not publish.
		sink.state.LastFailureAt = at
		sink.state.LastFailure = boundedFailureText(err)
		state := sink.state
		sink.mu.Unlock()
		_ = opened.Close()
		sink.notify(state)
		return false
	}
	sink.inner = opened
	sink.state.Ready, sink.state.Since = true, at
	sink.state.LastFailure, sink.state.LastFailureAt = "", time.Time{}
	// The agreement this open sink speaks under; a sink that asked nobody
	// gives nil, and the entry says so rather than carrying a stale one.
	sink.state.Protocol = copyNegotiation(opened.ProtocolNegotiation())
	state := sink.state
	sink.mu.Unlock()
	sink.notify(state)
	return true
}

func (sink *lazyOutputSink) notify(state outputSinkState) {
	sink.mu.Lock()
	onChange := sink.onChange
	sink.mu.Unlock()
	if onChange != nil {
		onChange(state)
	}
}

// SetOnChange installs the hook the attempts report to. It is installed
// before Start, so the first attempt is the first thing it hears.
func (sink *lazyOutputSink) SetOnChange(onChange func(outputSinkState)) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.onChange = onChange
}

func (sink *lazyOutputSink) State() outputSinkState {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return sink.state
}

// ProtocolNegotiation is the open sink's agreement, or the last attempt's
// partial answers; nil before anyone was asked.
func (sink *lazyOutputSink) ProtocolNegotiation() *enginekafka.ProtocolNegotiation {
	return sink.State().Protocol
}

// copyNegotiation is the state's own copy of an agreement, broker list
// included, so what a reader was handed does not change under it when the
// sink renegotiates.
func copyNegotiation(negotiation *enginekafka.ProtocolNegotiation) *enginekafka.ProtocolNegotiation {
	if negotiation == nil {
		return nil
	}
	copied := *negotiation
	copied.Brokers = append([]enginekafka.BrokerProtocol(nil), negotiation.Brokers...)
	return &copied
}

func boundedFailureText(err error) string {
	text := observability.SanitizeErrorText(err.Error())
	if len(text) > outputSinkFailureTextLimit {
		text = text[:outputSinkFailureTextLimit] + "..."
	}
	return text
}

func (sink *lazyOutputSink) WriteBatch(ctx context.Context, events []contract.TriggerEventV1) error {
	sink.mu.Lock()
	inner := sink.inner
	sink.mu.Unlock()
	if inner == nil {
		return &outputSinkNotOpenError{}
	}
	return inner.WriteBatch(ctx, events)
}

func (sink *lazyOutputSink) WriteCloseBatch(ctx context.Context, requests []linkdoutput.CloseRequest) error {
	sink.mu.Lock()
	inner := sink.inner
	sink.mu.Unlock()
	if inner == nil {
		return &outputSinkNotOpenError{}
	}
	writer, ok := inner.(interface {
		WriteCloseBatch(context.Context, []linkdoutput.CloseRequest) error
	})
	if !ok {
		return errors.New("output sink does not support native closure")
	}
	return writer.WriteCloseBatch(ctx, requests)
}

func (sink *lazyOutputSink) ConfigureStandardOutput(converter enginekafka.StandardEventConverter) error {
	sink.mu.Lock()
	sink.standard = &lazyStandardOutput{converter: converter}
	inner := sink.inner
	sink.mu.Unlock()
	if inner != nil {
		return inner.ConfigureStandardOutput(converter)
	}
	return nil
}

func (sink *lazyOutputSink) ConfigureLegacyOutput(converter enginekafka.LegacyEventConverter, topic string, maxBytes int) error {
	sink.mu.Lock()
	sink.legacy = &lazyLegacyOutput{converter: converter, topic: topic, maxBytes: maxBytes}
	inner := sink.inner
	sink.mu.Unlock()
	if inner != nil {
		return inner.ConfigureLegacyOutput(converter, topic, maxBytes)
	}
	return nil
}

// stop ends the retry loop and takes the inner sink, if any, out of service.
func (sink *lazyOutputSink) stop() productionPhaseTwoEventSink {
	sink.mu.Lock()
	sink.closed = true
	cancel := sink.cancel
	sink.cancel = nil
	sink.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	sink.retrying.Wait()
	sink.mu.Lock()
	inner := sink.inner
	sink.inner = nil
	sink.mu.Unlock()
	return inner
}

func (sink *lazyOutputSink) Shutdown(ctx context.Context) error {
	if inner := sink.stop(); inner != nil {
		return inner.Shutdown(ctx)
	}
	return nil
}

func (sink *lazyOutputSink) Close() error {
	if inner := sink.stop(); inner != nil {
		return inner.Close()
	}
	return nil
}
