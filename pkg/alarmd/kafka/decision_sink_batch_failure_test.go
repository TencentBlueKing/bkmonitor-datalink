// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package kafka

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Shopify/sarama"
)

// refusingBatchProducer answers a batch the way the client does when every
// message is refused before it reaches a broker: a list of per-message errors
// each carrying the cause, whose own text says only how many failed.
type refusingBatchProducer struct {
	cause error
}

func (producer *refusingBatchProducer) SendMessage(*sarama.ProducerMessage) (int32, int64, error) {
	return 0, 0, producer.cause
}

func (producer *refusingBatchProducer) SendMessages(messages []*sarama.ProducerMessage) error {
	var failures sarama.ProducerErrors
	for _, message := range messages {
		failures = append(failures, &sarama.ProducerError{Msg: message, Err: producer.cause})
	}
	return failures
}

func (*refusingBatchProducer) Close() error { return nil }

// A batch the client refused is reported with the cause of the first refusal
// in the text, and the count beside it -- not the count alone. On a live
// deployment the cause was a configuration the client checks before sending
// ("Producing headers requires Kafka at least v0.11"), and the row, the log
// and the window all showed "Failed to deliver 6 messages." for an afternoon.
// The list stays in the chain so callers that classify by type still can.
func TestABatchTheClientRefusedNamesTheCauseNotJustTheCount(t *testing.T) {
	cause := sarama.ConfigurationError("Producing headers requires Kafka at least v0.11")
	sink := newDecisionSinkForTest(t, &refusingBatchProducer{cause: cause}, &fakeCloser{})
	messages := []*sarama.ProducerMessage{
		{Topic: "alarmd_event", Value: sarama.StringEncoder("a")},
		{Topic: "alarmd_event", Value: sarama.StringEncoder("b")},
	}
	err := sink.writeMessages(context.Background(), messages)
	if err == nil {
		t.Fatal("a refused batch wrote without error")
	}
	text := err.Error()
	if !strings.Contains(text, "Producing headers requires Kafka at least v0.11") {
		t.Fatalf("error text = %q, want the cause the client gave", text)
	}
	if !strings.Contains(text, "2 of 2 messages failed") {
		t.Fatalf("error text = %q, want the count beside the cause", text)
	}
	var failures sarama.ProducerErrors
	if !errors.As(err, &failures) || len(failures) != 2 {
		t.Fatalf("the per-message list left the chain: %v", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("the cause left the chain: %v", err)
	}
	// A failure that is not the client's list is passed through as it was.
	plain := errors.New("kafka: client has run out of available brokers to talk to")
	if got := describeBatchFailure(plain, 2); got != plain {
		t.Fatalf("describeBatchFailure(plain) = %v, want it untouched", got)
	}
}
