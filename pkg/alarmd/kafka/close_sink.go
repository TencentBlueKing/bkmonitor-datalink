package kafka

import (
	"context"
	"errors"
	"fmt"

	"github.com/Shopify/sarama"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// WriteCloseBatch uses the same producer, topic, partition key, tenant header
// and lease admission as native detection output. No Python event is emitted.
func (sink *TriggerEventSink) WriteCloseBatch(ctx context.Context, requests []linkdoutput.CloseRequest) error {
	if sink == nil || sink.core == nil {
		return ErrDecisionSinkClosed
	}
	if ctx == nil {
		return errors.New("close sink requires context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := admitAgainstLease(ctx, sink.now); err != nil {
		return err
	}
	if !sink.headersSupported() {
		return errors.New("close sink requires Kafka record headers")
	}
	messages := make([]*sarama.ProducerMessage, 0, len(requests))
	for _, request := range requests {
		event, err := linkdoutput.ConvertClose(request)
		if err != nil {
			return err
		}
		messages = append(messages, &sarama.ProducerMessage{Topic: sink.core.outputTopic,
			Key: sarama.StringEncoder(event.AlertID), Value: sarama.ByteEncoder(event.Payload),
			Headers: []sarama.RecordHeader{{Key: []byte(tenantHeader), Value: []byte(event.TenantID)}}})
	}
	if len(messages) == 0 {
		return nil
	}
	observability.ReportOutputWrite(ctx, len(messages), 0, nil)
	if err := sink.core.writeMessages(ctx, messages); err != nil {
		return &triggerEventDependencyError{err: fmt.Errorf("publish close batch: %w", err)}
	}
	return nil
}
