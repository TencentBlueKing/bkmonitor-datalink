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

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// TriggerEventSink is the critical Kafka output for phase-one Trigger events.
// A successful WriteBatch means every event received a synchronous broker ACK.
type TriggerEventSink struct {
	core *DecisionSink
}

// triggerEventDependencyError marks only an attempted broker write whose ACK
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
	config, err := NewDecisionProducerConfig(coordinates)
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
	return &TriggerEventSink{core: core}, nil
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
