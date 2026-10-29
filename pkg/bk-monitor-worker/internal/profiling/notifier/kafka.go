// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package notifier

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Shopify/sarama"
	"go.uber.org/zap"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/core"
	utilsKafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/kafka"
	monitorLogger "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// KafkaNotifier consumes one DataID's Active Profiling Events.
type KafkaNotifier struct {
	processor *core.Processor
	dataID    uint64
	config    kafkaConfig
}

type kafkaConfig struct {
	brokers        []string
	topic          string
	groupID        string
	flushInterval  time.Duration
	eventQueueSize int
	eventBatchSize int
	saramaConfig   *sarama.Config
}

const (
	eventProcessingRetryDelay = time.Second
	eventQueueDrainTimeout    = 30 * time.Second
)

// NewKafkaNotifier creates a Kafka consumer from the DataID metadata.
func NewKafkaNotifier(
	processor *core.Processor,
	dataID uint64,
	info core.KafkaInfo,
	flushInterval time.Duration,
	eventQueueSize int,
	eventBatchSize int,
) (*KafkaNotifier, error) {
	if processor == nil || dataID == 0 || info.Host == "" || info.Topic == "" {
		return nil, fmt.Errorf("profiling processor, DataID, Kafka brokers and topic are required")
	}
	if flushInterval <= 0 {
		return nil, fmt.Errorf("DataID %d requires a positive Kafka event flush interval", dataID)
	}
	if eventQueueSize <= 0 {
		return nil, fmt.Errorf("DataID %d requires a positive Kafka event queue size", dataID)
	}
	if eventBatchSize <= 0 {
		return nil, fmt.Errorf("DataID %d requires a positive Kafka event batch size", dataID)
	}
	if (info.Username == "") != (info.Password == "") {
		return nil, fmt.Errorf("DataID %d requires both Kafka username and password", dataID)
	}

	brokers := strings.Split(info.Host, ",")
	for i := range brokers {
		brokers[i] = strings.TrimSpace(brokers[i])
		if brokers[i] == "" {
			return nil, fmt.Errorf("DataID %d config contains an empty Kafka broker", dataID)
		}
	}

	saramaConfig := sarama.NewConfig()
	saramaConfig.Version = sarama.V0_10_2_0
	saramaConfig.Consumer.Offsets.Initial = sarama.OffsetOldest
	if info.Username != "" {
		saramaConfig.Net.SASL.Enable = true
		saramaConfig.Net.SASL.User = info.Username
		saramaConfig.Net.SASL.Password = info.Password
		saramaConfig.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA512
		saramaConfig.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
			return &utilsKafka.XDGSCRAMClient{HashGeneratorFcn: utilsKafka.SHA512}
		}
	}

	return &KafkaNotifier{
		processor: processor,
		dataID:    dataID,
		config: kafkaConfig{
			brokers:        brokers,
			topic:          info.Topic,
			groupID:        fmt.Sprintf("active-profiling-materializer-%d", dataID),
			flushInterval:  flushInterval,
			eventQueueSize: eventQueueSize,
			eventBatchSize: eventBatchSize,
			saramaConfig:   saramaConfig,
		},
	}, nil
}

// Run consumes until ctx is cancelled or the consumer group reports an error.
// The caller owns restarting the DataID consumer after an error.
func (n *KafkaNotifier) Run(ctx context.Context) error {
	config := *n.config.saramaConfig
	config.Consumer.Return.Errors = true
	if err := config.Validate(); err != nil {
		return fmt.Errorf("invalid profiling Kafka config: %w", err)
	}
	group, err := sarama.NewConsumerGroup(n.config.brokers, n.config.groupID, &config)
	if err != nil {
		return fmt.Errorf("create profiling Kafka consumer group: %w", err)
	}
	logger.Infof(
		"Kafka consumer group connected data_id=%d group_id=%s topic=%s brokers=%v",
		n.dataID, n.config.groupID, n.config.topic, n.config.brokers,
	)
	defer func() {
		if err := group.Close(); err != nil {
			logger.Warnf("close Kafka consumer group failed group_id=%s err=%s", n.config.groupID, err)
		}
		logger.Infof("Kafka consumer group stopped data_id=%d group_id=%s", n.dataID, n.config.groupID)
	}()

	consumeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	messages := make(chan []byte, n.config.eventQueueSize)
	workerCtx, cancelWorker := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelWorker()
	workerDone := make(chan struct{})
	go n.processMessages(workerCtx, messages, workerDone)
	defer func() {
		close(messages)
		timer := time.NewTimer(eventQueueDrainTimeout)
		defer timer.Stop()
		select {
		case <-workerDone:
		case <-timer.C:
			logger.Errorf("Active Profiling message queue drain timed out data_id=%d pending=%d",
				n.dataID, len(messages))
			cancelWorker()
			<-workerDone
		}
	}()

	groupErrors := make(chan error, 1)
	go n.watchErrors(consumeCtx, group, groupErrors, cancel)
	handler := &consumerGroupHandler{messages: messages}
	for {
		err := group.Consume(consumeCtx, []string{n.config.topic}, handler)
		if groupErr := takeError(groupErrors); groupErr != nil {
			return fmt.Errorf("consume profiling topic %q: %w", n.config.topic, groupErr)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			return fmt.Errorf("consume profiling topic %q: %w", n.config.topic, err)
		}
		if err := consumeCtx.Err(); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
	}
}

func (n *KafkaNotifier) processMessages(ctx context.Context, messages <-chan []byte, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(n.config.flushInterval)
	defer ticker.Stop()
	var pending [][]byte
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		for {
			err := n.processor.HandleMessages(ctx, pending)
			if err == nil || errors.Is(err, core.ErrInvalidEvent) {
				if errors.Is(err, core.ErrInvalidEvent) {
					logger.Errorf("skip invalid Event(s) in queued Kafka batch data_id=%d messages=%d: %s",
						n.dataID, len(pending), err)
				} else {
					logger.Debugf("queued Kafka batch persisted data_id=%d messages=%d", n.dataID, len(pending))
				}
				pending = nil
				return nil
			}
			logger.Errorf("persist queued Kafka batch failed; retrying in %s data_id=%d messages=%d err=%s",
				eventProcessingRetryDelay, n.dataID, len(pending), err)
			if err := waitContext(ctx, eventProcessingRetryDelay); err != nil {
				return err
			}
		}
	}
	stop := func(err error) {
		if err != nil {
			logger.Errorf("queued Kafka batch stopped before persistence data_id=%d messages=%d err=%s",
				n.dataID, len(pending), err)
		}
	}

	for {
		select {
		case message, ok := <-messages:
			if !ok {
				stop(flush())
				return
			}
			pending = append(pending, message)
			if len(pending) >= n.config.eventBatchSize {
				if err := flush(); err != nil {
					stop(err)
					return
				}
			}
		case <-ticker.C:
			if err := flush(); err != nil {
				stop(err)
				return
			}
		case <-ctx.Done():
			stop(flush())
			return
		}
	}
}

func (n *KafkaNotifier) watchErrors(
	ctx context.Context,
	group sarama.ConsumerGroup,
	groupErrors chan<- error,
	cancel context.CancelFunc,
) {
	for {
		select {
		case err, ok := <-group.Errors():
			if !ok {
				return
			}
			if err != nil {
				logger.Errorf("Kafka consumer group data_id=%d failed: %s", n.dataID, err)
				select {
				case groupErrors <- err:
				default:
				}
				cancel()
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

type consumerGroupHandler struct {
	messages chan<- []byte
}

func (*consumerGroupHandler) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (*consumerGroupHandler) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (h *consumerGroupHandler) ConsumeClaim(
	session sarama.ConsumerGroupSession,
	claim sarama.ConsumerGroupClaim,
) error {
	for {
		select {
		case message, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			select {
			case h.messages <- append([]byte(nil), message.Value...):
				session.MarkMessage(message, "")
			case <-session.Context().Done():
				return nil
			}
		case <-session.Context().Done():
			return nil
		}
	}
}

func takeError(ch <-chan error) error {
	select {
	case err := <-ch:
		return err
	default:
		return nil
	}
}

func waitContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

var logger = monitorLogger.With(zap.String("location", "profiling.notifier"))
