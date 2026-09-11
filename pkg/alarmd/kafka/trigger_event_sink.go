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
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/legacyoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
)

// StandardEventConverter writes a decision as the standard raw event.
type StandardEventConverter interface {
	Convert(*contract.TriggerEventV1) (linkdoutput.Event, error)
}

// TriggerEventSink is the critical Kafka output for Trigger events.
// A successful WriteBatch means every event received a synchronous broker ACK.
type TriggerEventSink struct {
	core              *DecisionSink
	legacyConverter   LegacyEventConverter
	standardConverter StandardEventConverter
	legacyTopic       string
	maxLegacyBytes    int
}

// ConfigureLegacyOutput is called once during assembly, before any writes.
func (sink *TriggerEventSink) ConfigureLegacyOutput(converter LegacyEventConverter, topic string, maxBytes int) error {
	if converter == nil || !strings.HasPrefix(topic, "alarmd_") || maxBytes <= 0 {
		return errors.New("invalid legacy output configuration")
	}
	sink.legacyConverter, sink.legacyTopic, sink.maxLegacyBytes = converter, topic, maxBytes
	return nil
}

// triggerEventDependencyError marks a conversion dependency or broker write whose ACK
// failed or is unknown. Encoding and local lifecycle errors remain ordinary.
type triggerEventDependencyError struct {
	err error
}

func (err *triggerEventDependencyError) Error() string {
	if err == nil || err.err == nil {
		return "kafka trigger event sink: output dependency failure"
	}
	return err.err.Error()
}

func (err *triggerEventDependencyError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.err
}

func (err *triggerEventDependencyError) RetryableOutputDependency() {}

func OpenTriggerEventSink(coordinates DecisionSinkConfig) (*TriggerEventSink, error) {
	config, err := NewDecisionProducerOnlyConfig(coordinates)
	if err != nil {
		return nil, err
	}
	client, err := sarama.NewClient(coordinates.Brokers, config)
	if err != nil {
		return nil, fmt.Errorf("kafka trigger event sink: open client: %w", err)
	}
	producer, err := newSyncProducerForOutput(client, coordinates.OutputTopic)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("kafka trigger event sink: open producer: %w", err), client.Close())
	}
	sink, err := newTriggerEventSink(coordinates.OutputTopic, producer, client)
	if err != nil {
		return nil, errors.Join(err, producer.Close(), client.Close())
	}
	return sink, nil
}

func newTriggerEventSink(
	outputTopic string,
	producer syncMessageProducer,
	client closeableClient,
) (*TriggerEventSink, error) {
	core, err := newDecisionSink(outputTopic, producer, client)
	if err != nil {
		return nil, err
	}
	standard, err := linkdoutput.NewConverter(time.Now)
	if err != nil {
		return nil, err
	}
	return &TriggerEventSink{
		core: core, legacyConverter: &legacyoutput.Converter{}, standardConverter: standard,
		legacyTopic: "alarmd_0bkmonitor_backend_event", maxLegacyBytes: 524288,
	}, nil
}

func (sink *TriggerEventSink) WriteBatch(ctx context.Context, events []contract.TriggerEventV1) error {
	if sink == nil || sink.core == nil {
		return ErrDecisionSinkClosed
	}
	if ctx == nil {
		return errors.New("kafka trigger event sink: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	messages := make([]*sarama.ProducerMessage, len(events))
	groups := make(map[string][]int)
	for index := range events {
		payload, err := contract.EncodeTriggerEventV1(&events[index])
		if err != nil {
			return fmt.Errorf("kafka trigger event sink: encode event %d: %w", index, err)
		}
		messages[index] = &sarama.ProducerMessage{
			Topic: sink.core.outputTopic,
			Key:   nil,
			Value: sarama.ByteEncoder(payload),
		}
		if events[index].DedupeMD5 != "" {
			// Keep a series on the same hash partition using the protocol's
			// lowercase hex text, not the decoded 16-byte digest.
			messages[index].Key = sarama.StringEncoder(events[index].DedupeMD5)
		}
		if events[index].WireFormat == contract.WireFormatStandardRawEvent {
			converted, convertErr := sink.standardConverter.Convert(&events[index])
			if convertErr != nil {
				return &triggerEventDependencyError{err: convertErr}
			}
			messages[index].Value = sarama.ByteEncoder(converted.Payload)
			messages[index].Key = sarama.StringEncoder(converted.AlertID)
			continue
		}
		// The Plan's frozen format selects the protocol, and for a Plan built
		// before the choice existed that is still the frozen revision: a
		// compatibility context is attached only where the compatibility
		// protocol is what the Plan publishes. Missing Python snapshot
		// dependencies must never change that choice to native output.
		if events[index].LegacyOutput != nil || events[index].StrategyRef == nil {
			if events[index].LegacyOutput == nil || events[index].LegacyOutput.Configuration == nil {
				return errors.New("legacy event has no frozen compatibility context")
			}
			// The Python protocol carries anomaly points and nothing else: its
			// producer builds every message from the anomaly list and stamps
			// ABNORMAL, and recovery is decided downstream from the absence of
			// anomalies. alarmd's own Recovery is a steady-state result, so
			// every healthy series produces one every cycle - converting those
			// put a hundred times Python's volume on the topic. A Recovery
			// event has no representation in this protocol, so it produces no
			// message here and no snapshot; the native protocol still carries
			// it, because the choice of protocol is the revision's, not the
			// event kind's.
			if events[index].EventKind != contract.TriggerEventAbnormal {
				messages[index] = nil
				continue
			}
			key := events[index].TenantID + "\x00" + events[index].BusinessID
			groups[key] = append(groups[key], index)
		}
	}
	for _, indices := range groups {
		batch := make([]contract.TriggerEventV1, len(indices))
		for i, index := range indices {
			batch[i] = events[index]
		}
		converted, err := sink.legacyConverter.ConvertBatch(ctx, batch)
		if err != nil {
			return &triggerEventDependencyError{err: fmt.Errorf("legacy conversion failed: %w", err)}
		}
		if len(converted) != len(batch) {
			return &triggerEventDependencyError{err: errors.New("legacy conversion result count mismatch")}
		}
		for i, item := range converted {
			if item.EventID != batch[i].EventID || len(item.Payload) == 0 || len(item.Payload) > sink.maxLegacyBytes || !json.Valid(item.Payload) || len(item.DedupeMD5) != 32 || strings.ToLower(item.DedupeMD5) != item.DedupeMD5 {
				return &triggerEventDependencyError{err: errors.New("legacy conversion returned invalid event identity/payload")}
			}
			if _, err := hex.DecodeString(item.DedupeMD5); err != nil {
				return &triggerEventDependencyError{err: err}
			}
			messages[indices[i]] = &sarama.ProducerMessage{Topic: sink.legacyTopic, Key: sarama.StringEncoder(item.DedupeMD5), Value: sarama.ByteEncoder(item.Payload)}
		}
	}
	published := messages[:0]
	for _, message := range messages {
		if message != nil {
			published = append(published, message)
		}
	}
	messages = published
	if len(messages) == 0 {
		return nil
	}
	if err := sink.core.writeMessages(ctx, messages); err != nil {
		publishErr := fmt.Errorf("kafka trigger event sink: publish batch: %w", err)
		if errors.Is(err, ErrDecisionSinkClosed) || ctx.Err() != nil {
			return publishErr
		}
		return &triggerEventDependencyError{err: publishErr}
	}
	return nil
}

func (sink *TriggerEventSink) Shutdown(ctx context.Context) error {
	if sink == nil || sink.core == nil {
		return nil
	}
	return sink.core.Shutdown(ctx)
}

func (sink *TriggerEventSink) Close() error {
	if sink == nil || sink.core == nil {
		return nil
	}
	return sink.core.Close()
}
