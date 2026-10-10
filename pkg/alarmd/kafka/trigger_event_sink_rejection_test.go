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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// What this process decides on its own about an output is not a dependency
// failure. A converter that will not write a decision, and a client that
// refuses a record before any broker sees it, both used to come back as
// triggerEventDependencyError, and a caller that retries dependencies
// retried them every round: six objects on one deployment sat at
// OUTPUT_ACK_UNKNOWN for 88 rounds with their Kafka up, because the client
// had been built for a protocol without record headers. Each refusal now
// names itself, carries the sentence that says why, and does not ask to be
// retried.

func outputRejectionOf(t *testing.T, err error) *OutputRejectedError {
	t.Helper()
	if err == nil {
		t.Fatal("WriteBatch() = nil, want a refusal")
	}
	var rejected *OutputRejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("WriteBatch() = %v (%T), want an OutputRejectedError", err, err)
	}
	var dependency interface{ RetryableOutputDependency() }
	if errors.As(err, &dependency) {
		t.Fatalf("WriteBatch() = %v marks RetryableOutputDependency: a refusal decided here would be retried as a broker failure", err)
	}
	if rejected.OutputRejectionReason() != rejected.Reason || rejected.Reason == "" {
		t.Fatalf("rejection reason = %q / %q, want one non-empty reason", rejected.OutputRejectionReason(), rejected.Reason)
	}
	// The two methods a reader outside this package goes through: the
	// reason and the bare sentence, with the identities left to the trace.
	var structured interface {
		OutputRejectionReason() string
		OutputRejectionDetail() string
	}
	if !errors.As(err, &structured) || structured.OutputRejectionDetail() != rejected.Detail || rejected.Detail == "" ||
		strings.Contains(structured.OutputRejectionDetail(), "(event ") {
		t.Fatalf("structured detail = %q, want the bare sentence %q without the identity suffix", structured.OutputRejectionDetail(), rejected.Detail)
	}
	return rejected
}

func TestEachConverterRefusalIsAConversionRejectionNotADependency(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		mutate func(event *contract.TriggerEventV1)
		detail string
	}{
		{name: "no frozen strategy revision", mutate: func(event *contract.TriggerEventV1) { event.StrategyRef = nil },
			detail: "without a frozen strategy revision"},
		{name: "no series identity", mutate: func(event *contract.TriggerEventV1) { event.DedupeMD5 = "" },
			detail: "without a series identity"},
		{name: "decision kind with no action", mutate: func(event *contract.TriggerEventV1) { event.EventKind = "UNHEARD_OF" },
			detail: "has no action"},
		{name: "primary level absent", mutate: func(event *contract.TriggerEventV1) { event.PrimaryLevelID = 99 },
			detail: "primary level 99 is absent"},
		{name: "business identity not a number", mutate: func(event *contract.TriggerEventV1) { event.BusinessID = "biz-2" },
			detail: `business identity "biz-2"`},
		{name: "two levels share one severity", mutate: func(event *contract.TriggerEventV1) {
			// The golden's abnormal level, twice: both map to an action and
			// to the same severity name, which the consumer refuses whole.
			for _, level := range event.LevelResults {
				if level.Result == contract.LevelResultAbnormal {
					event.LevelResults = append(event.LevelResults, level)
					break
				}
			}
		}, detail: "share the severity"},
	}
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sent := 0
			producer := &fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) {
				sent++
				return 0, 0, nil
			}}
			sink, err := newTriggerEventSink("alarmd-trigger-event", producer, &fakeCloser{})
			if err != nil {
				t.Fatal(err)
			}
			event := triggerEventGolden(t)
			event.WireFormat = contract.WireFormatStandardRawEvent
			test.mutate(&event)

			rejected := outputRejectionOf(t, sink.WriteBatch(context.Background(), []contract.TriggerEventV1{event}))
			if rejected.Reason != contract.ReasonOutputConversionRejected {
				t.Fatalf("reason = %q, want %q", rejected.Reason, contract.ReasonOutputConversionRejected)
			}
			if !strings.Contains(rejected.Detail, test.detail) {
				t.Fatalf("detail = %q, want the converter's own sentence containing %q", rejected.Detail, test.detail)
			}
			if rejected.EventID != event.EventID || rejected.StrategyID != event.PlanRef.StrategyID || rejected.BusinessID != event.BusinessID ||
				rejected.Format != contract.WireFormatStandardRawEvent {
				t.Fatalf("rejection names %+v, want the refused event's identities and format", rejected)
			}
			if sent != 0 {
				t.Fatalf("producer sent %d messages after a refused conversion, want none", sent)
			}
		})
	}
}

// The client refusing a record before the network is this deployment's
// wiring, and it says so: the exact shape that stopped a deployment was a
// producer built for 0.10.2.0 asked to send a record with a header.
func TestAClientRefusalBeforeTheNetworkIsAClientRejectionNotADependency(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		fail   func(*sarama.ProducerMessage) error
		detail string
	}{
		{name: "headers on a protocol without them", detail: "requires Kafka at least v0.11",
			fail: func(message *sarama.ProducerMessage) error {
				return &sarama.ProducerError{Msg: message, Err: sarama.ConfigurationError("Producing headers requires Kafka at least v0.11")}
			}},
		{name: "record over the client's own cap", detail: sarama.ErrMessageSizeTooLarge.Error(),
			fail: func(message *sarama.ProducerMessage) error {
				return &sarama.ProducerError{Msg: message, Err: sarama.ErrMessageSizeTooLarge}
			}},
		{name: "a batch refused whole", detail: "requires Kafka at least v0.11",
			fail: func(message *sarama.ProducerMessage) error {
				return sarama.ProducerErrors{
					&sarama.ProducerError{Msg: message, Err: sarama.ConfigurationError("Producing headers requires Kafka at least v0.11")},
					&sarama.ProducerError{Msg: message, Err: sarama.ConfigurationError("Producing headers requires Kafka at least v0.11")},
				}
			}},
	}
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			producer := &fakeSyncProducer{send: func(message *sarama.ProducerMessage) (int32, int64, error) {
				return -1, -1, test.fail(message)
			}}
			sink, err := newTriggerEventSink("alarmd-trigger-event", producer, &fakeCloser{})
			if err != nil {
				t.Fatal(err)
			}
			event := triggerEventGolden(t)
			event.WireFormat = contract.WireFormatStandardRawEvent

			rejected := outputRejectionOf(t, sink.WriteBatch(context.Background(), []contract.TriggerEventV1{event}))
			if rejected.Reason != contract.ReasonOutputClientRejected {
				t.Fatalf("reason = %q, want %q", rejected.Reason, contract.ReasonOutputClientRejected)
			}
			if !strings.Contains(rejected.Detail, test.detail) {
				t.Fatalf("detail = %q, want the client's own sentence containing %q", rejected.Detail, test.detail)
			}
			if rejected.EventID != event.EventID || rejected.Format != contract.WireFormatStandardRawEvent {
				t.Fatalf("rejection names %+v, want the refused event and its format", rejected)
			}
		})
	}
}

// A broker failure among the refusals keeps the batch a dependency failure:
// some of it may have landed, and only a replay settles which.
func TestABrokerFailureBesideAClientRefusalStaysADependencyFailure(t *testing.T) {
	t.Parallel()

	producer := &fakeSyncProducer{send: func(message *sarama.ProducerMessage) (int32, int64, error) {
		return -1, -1, sarama.ProducerErrors{
			&sarama.ProducerError{Msg: message, Err: sarama.ConfigurationError("Producing headers requires Kafka at least v0.11")},
			&sarama.ProducerError{Msg: message, Err: sarama.ErrRequestTimedOut},
		}
	}}
	sink, err := newTriggerEventSink("alarmd-trigger-event", producer, &fakeCloser{})
	if err != nil {
		t.Fatal(err)
	}
	event := triggerEventGolden(t)
	event.WireFormat = contract.WireFormatStandardRawEvent
	err = sink.WriteBatch(context.Background(), []contract.TriggerEventV1{event})
	var dependency interface{ RetryableOutputDependency() }
	if err == nil || !errors.As(err, &dependency) {
		t.Fatalf("WriteBatch() = %v, want a retryable dependency failure", err)
	}
	var rejected *OutputRejectedError
	if errors.As(err, &rejected) {
		t.Fatalf("WriteBatch() = %v was read as a client refusal although a broker failed in the same batch", err)
	}
}
