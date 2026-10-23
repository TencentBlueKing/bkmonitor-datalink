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
	"io"
	"sort"
	"testing"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/legacyoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func twoSeriesBatch(t *testing.T) []contract.TriggerEventV1 {
	t.Helper()
	return []contract.TriggerEventV1{standardSeriesEvent(t, "series-a", "a"), standardSeriesEvent(t, "series-b", "b")}
}

// The answers read as "the broker refused, nothing was written" are a closed
// list of Kafka protocol error codes this client does not retry; every
// other answer - the ones that say the write may have been taken, and every
// one the client retries - is "whether it landed is unknown", as is an
// error that is no answer at all. A batch is a refusal only when every
// failed record was refused.
func TestOnlyAClosedListOfBrokerAnswersReadsAsRefused(t *testing.T) {
	refused := []sarama.KError{sarama.ErrInvalidTopic, sarama.ErrMessageSetSizeTooLarge, sarama.ErrInvalidRequiredAcks,
		sarama.ErrTopicAuthorizationFailed, sarama.ErrClusterAuthorizationFailed, sarama.ErrInvalidTimestamp,
		sarama.ErrUnsupportedVersion, sarama.ErrUnsupportedForMessageFormat}
	listed := make([]int, 0, len(brokerNotWritten))
	for answer := range brokerNotWritten {
		listed = append(listed, int(answer))
	}
	sort.Ints(listed)
	want := make([]int, 0, len(refused))
	for _, answer := range refused {
		want = append(want, int(answer))
	}
	sort.Ints(want)
	if fmt.Sprint(listed) != fmt.Sprint(want) {
		t.Fatalf("brokerNotWritten = %v, want exactly %v", listed, want)
	}
	write := func(cause error, refuse map[int]bool) string {
		sink := newTriggerEventSinkForTest(t, &partlyRefusingProducer{refuse: refuse, cause: cause}, &fakeCloser{})
		err := sink.WriteBatch(context.Background(), twoSeriesBatch(t))
		if err == nil {
			t.Fatalf("%v: the write did not fail", cause)
		}
		return observability.OutputFailureKindOf(err)
	}
	for _, answer := range refused {
		if got := write(answer, map[int]bool{0: true, 1: true}); got != observability.OutputFailureBrokerRefused {
			t.Errorf("%v on every record read %s, want broker_refused", answer, got)
		}
	}
	for _, cause := range []error{sarama.ErrRequestTimedOut, sarama.ErrNotEnoughReplicasAfterAppend, sarama.ErrNetworkException,
		sarama.ErrNotLeaderForPartition, sarama.ErrLeaderNotAvailable, sarama.ErrNotEnoughReplicas,
		sarama.ErrUnknownTopicOrPartition, sarama.ErrInvalidMessage, sarama.ErrOutOfBrokers, io.EOF} {
		if got := write(cause, map[int]bool{0: true, 1: true}); got != observability.OutputFailureAckUnknown {
			t.Errorf("%v read %s, want ack_unknown", cause, got)
		}
	}
	// One record refused, the other without an answer: what landed is unknown.
	mixed := &mixedAnswerProducer{answers: []error{sarama.ErrTopicAuthorizationFailed, sarama.ErrRequestTimedOut}}
	sink := newTriggerEventSinkForTest(t, mixed, &fakeCloser{})
	if got := observability.OutputFailureKindOf(sink.WriteBatch(context.Background(), twoSeriesBatch(t))); got != observability.OutputFailureAckUnknown {
		t.Fatalf("a refusal beside a timeout read %s, want ack_unknown", got)
	}
}

// mixedAnswerProducer fails each message of a batch with its own answer.
type mixedAnswerProducer struct{ answers []error }

func (producer *mixedAnswerProducer) SendMessage(*sarama.ProducerMessage) (int32, int64, error) {
	return 0, 0, errors.New("single sends are not used")
}

func (producer *mixedAnswerProducer) SendMessages(messages []*sarama.ProducerMessage) error {
	var failures sarama.ProducerErrors
	for position, message := range messages {
		failures = append(failures, &sarama.ProducerError{Msg: message, Err: producer.answers[position%len(producer.answers)]})
	}
	return failures
}

func (*mixedAnswerProducer) Close() error { return nil }

// cancellingProducer ends the Slot's context while the batch is being sent
// and fails it, as a send cut short does.
type cancellingProducer struct{ cancel context.CancelFunc }

func (producer *cancellingProducer) SendMessage(*sarama.ProducerMessage) (int32, int64, error) {
	return 0, 0, errors.New("single sends are not used")
}

func (producer *cancellingProducer) SendMessages([]*sarama.ProducerMessage) error {
	producer.cancel()
	return sarama.ErrOutOfBrokers
}

func (*cancellingProducer) Close() error { return nil }

// Every error the sink returns for a failed write carries a kind from the
// closed set. The named exceptions are the caller's own: its context ending
// before anything was sent, or during a compatible conversion whose error is
// not the snapshot store's, and the lease too short to start a batch, which
// has a completion code of its own.
func TestEverySinkErrorCarriesAKind(t *testing.T) {
	kindOf := observability.OutputFailureKindOf
	var nilSink *TriggerEventSink
	cases := map[string]struct {
		err  error
		want string
	}{
		"nil sink":       {nilSink.WriteBatch(context.Background(), twoSeriesBatch(t)), observability.OutputFailureSinkNotOpen},
		"nil close sink": {nilSink.WriteCloseBatch(context.Background(), nil), observability.OutputFailureSinkNotOpen},
	}
	closed := newTriggerEventSinkForTest(t, &partlyRefusingProducer{}, &fakeCloser{})
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	cases["closed sink"] = struct {
		err  error
		want string
	}{closed.WriteBatch(context.Background(), twoSeriesBatch(t)), observability.OutputFailureSinkNotOpen}
	noAnswer := newTriggerEventSinkForTest(t, &partlyRefusingProducer{refuse: map[int]bool{0: true, 1: true}, cause: sarama.ErrOutOfBrokers}, &fakeCloser{})
	cases["no answer"] = struct {
		err  error
		want string
	}{noAnswer.WriteBatch(context.Background(), twoSeriesBatch(t)), observability.OutputFailureAckUnknown}
	whole := newTriggerEventSinkForTest(t, &partlyRefusingProducer{refuse: map[int]bool{0: true, 1: true}, cause: sarama.ErrMessageSizeTooLarge}, &fakeCloser{})
	cases["refused whole by the producer"] = struct {
		err  error
		want string
	}{whole.WriteBatch(context.Background(), twoSeriesBatch(t)), observability.OutputFailureClientRejected}
	part := newTriggerEventSinkForTest(t, &partlyRefusingProducer{refuse: map[int]bool{1: true}, cause: sarama.ErrMessageSizeTooLarge}, &fakeCloser{})
	cases["refused in part by the producer"] = struct {
		err  error
		want string
	}{part.WriteBatch(context.Background(), twoSeriesBatch(t)), observability.OutputFailureClientRejected}
	noHeaders := newTriggerEventSinkForTest(t, &partlyRefusingProducer{}, &fakeCloser{})
	noHeaders.protocol = &ProtocolNegotiation{HeadersSupported: false}
	cases["refused before sending"] = struct {
		err  error
		want string
	}{noHeaders.WriteBatch(context.Background(), twoSeriesBatch(t)), observability.OutputFailureClientRejected}
	ctx, cancel := context.WithCancel(context.Background())
	cutShort := newTriggerEventSinkForTest(t, &cancellingProducer{cancel: cancel}, &fakeCloser{})
	cases["context ended during the send"] = struct {
		err  error
		want string
	}{cutShort.WriteBatch(ctx, twoSeriesBatch(t)), observability.OutputFailureAckUnknown}
	cases["snapshot store under a conversion failure"] = struct {
		err  error
		want string
	}{&triggerEventDependencyError{err: fmt.Errorf("legacy conversion failed: %w", &legacyoutput.SnapshotStoreError{Err: io.EOF})},
		observability.OutputFailureSnapshotStore}
	for name, test := range cases {
		if test.err == nil {
			t.Errorf("%s: the write did not fail", name)
			continue
		}
		if got := kindOf(test.err); got != test.want {
			t.Errorf("%s: kind %s, want %s (%v)", name, got, test.want, test.err)
		}
	}
	// The caller's own: no kind.
	ended, end := context.WithCancel(context.Background())
	end()
	if got := kindOf(newTriggerEventSinkForTest(t, &partlyRefusingProducer{}, &fakeCloser{}).WriteBatch(ended, twoSeriesBatch(t))); got != observability.OutputFailureUnknown {
		t.Errorf("a context that ended before the send: kind %s, want unknown", got)
	}
	if got := kindOf(&OutputDeferredError{Remaining: time.Second, Needed: time.Minute}); got != observability.OutputFailureUnknown {
		t.Errorf("a lease deferral: kind %s, want unknown under its own code", got)
	}
}

// A batch of one goes through the client's single send, which answers with
// the broker's error itself rather than a list: read the same way.
func TestASingleSendIsReadByTheBrokersAnswer(t *testing.T) {
	for cause, want := range map[error]string{
		sarama.ErrTopicAuthorizationFailed: observability.OutputFailureBrokerRefused,
		sarama.ErrRequestTimedOut:          observability.OutputFailureAckUnknown,
		io.EOF:                             observability.OutputFailureAckUnknown,
	} {
		producer := &fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) { return 0, 0, cause }}
		sink := newTriggerEventSinkForTest(t, producer, &fakeCloser{})
		err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{standardSeriesEvent(t, "series-a", "a")})
		if got := observability.OutputFailureKindOf(err); got != want {
			t.Errorf("single send answered %v: kind %s, want %s (%v)", cause, got, want, err)
		}
	}
}
