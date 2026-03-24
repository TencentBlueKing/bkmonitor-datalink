// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package notifier

import (
	"context"
	"crypto/sha512"
	"time"

	"github.com/Shopify/sarama"
	"github.com/xdg-go/scram"
	"k8s.io/client-go/util/flowcontrol"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/window"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/runtimex"
)

type kafkaConfig struct {
	KafkaTopic    string
	KafkaGroupId  string
	KafkaHost     string
	KafkaUsername string
	KafkaPassword string
}

// KafkaHost host of kafka
func KafkaHost(h string) Option {
	return func(args *Options) {
		args.KafkaHost = h
	}
}

// KafkaUsername username of kafka
func KafkaUsername(u string) Option {
	return func(args *Options) {
		args.KafkaUsername = u
	}
}

// KafkaPassword password of kafka
func KafkaPassword(p string) Option {
	return func(args *Options) {
		args.KafkaPassword = p
	}
}

// KafkaTopic listen topic of kafka
func KafkaTopic(t string) Option {
	return func(options *Options) {
		options.KafkaTopic = t
	}
}

// KafkaGroupId consumerGroupId of kafka
func KafkaGroupId(g string) Option {
	return func(options *Options) {
		options.KafkaGroupId = g
	}
}

type kafkaNotifier struct {
	ctx context.Context

	config        kafkaConfig
	consumerGroup sarama.ConsumerGroup
	handler       consumeHandler
}

// Spans return a chan that can receive messages
func (k *kafkaNotifier) Spans() <-chan []window.StandardSpan {
	return k.handler.spans
}

// Start kafka start listen message for queue
func (k *kafkaNotifier) Start(errorReceiveChan chan<- error) {
	defer runtimex.HandleCrashToChan(errorReceiveChan)
	logger.Infof(
		"KafkaNotifier started. host: %s topic: %s groupId: %s qps: %d",
		k.config.KafkaHost, k.config.KafkaTopic, k.config.KafkaGroupId, k.handler.qps,
	)
	for {
		select {
		case <-k.ctx.Done():
			if err := k.consumerGroup.Close(); err != nil {
				logger.Errorf("Failed to close ConsumerGroup, error: %s", err)
			}
			logger.Infof("ConsumerGroup stopped.")
			close(k.handler.spans)
			return
		default:
			if err := k.consumerGroup.Consume(k.ctx, []string{k.config.KafkaTopic}, k.handler); err != nil {
				logger.Errorf("ConsumerGroup fails to consume. error: %s", err)
				time.Sleep(1 * time.Second)
			}
		}
	}
}

type consumeHandler struct {
	ctx     context.Context
	qps     int
	limiter *tokenBucketRateLimiter
	dataId  string
	spans   chan []window.StandardSpan
	groupId string
	topic   string
}

// Setup is run at the beginning of a new session, before ConsumeClaim.
func (c consumeHandler) Setup(session sarama.ConsumerGroupSession) error {
	return nil
}

// Cleanup is run at the end of a session, once all ConsumeClaim goroutines have exited
// but before the offsets are committed for the very last time.
func (c consumeHandler) Cleanup(_ sarama.ConsumerGroupSession) error {
	return nil
}

// ConsumeClaim must start a consumer loop of ConsumerGroupClaim's Messages().
// Once the Messages() channel is closed, the Handler must finish its processing
// loop and exit.
func (c consumeHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for {
		select {
		case msg, ok := <-claim.Messages():
			if !ok {
				logger.Warnf("kafka claim session chan closed, return")
				return nil
			}

			if !c.limiter.TryAccept() {
				logger.Errorf("[RateLimiter] Topic: %s reject the message, max qps: %d", c.topic, c.qps)
				metrics.AddApmPreCalcNotifierRejectMessageCount(c.dataId, c.topic)
				continue
			}

			metrics.AddApmNotifierReceiveMessageCount(c.dataId, c.topic)
			if session != nil {
				c.sendSpans(msg.Value)
				session.MarkMessage(msg, "")
			}
		case <-session.Context().Done():
			logger.Infof("kafka consume handler session done. topic: %s groupId: %s", c.topic, c.groupId)
			return nil
		case <-c.ctx.Done():
			logger.Infof("kafka consume handler context done. topic: %s groupId: %s", c.topic, c.groupId)
			return nil
		}
	}
}

func (c consumeHandler) sendSpans(message []byte) {
	start := time.Now()
	var res []window.StandardSpan

	var msg window.OriginMessage
	if err := jsonx.Unmarshal(message, &msg); err != nil {
		logger.Errorf("kafka received a abnormal message! dataId: %s error: %s message: %s", c.dataId, err, message)
		return
	}

	for _, item := range msg.Items {
		s := window.ToStandardSpan(item)
		res = append(res, s)
		metrics.RecordQueueSpanDelta(c.dataId, s.StartTime)
	}
	metrics.RecordNotifierParseSpanDuration(c.dataId, c.topic, start)
	c.spans <- res
}

func newKafkaNotifier(dataId string, setters ...Option) (Notifier, error) {
	args := &Options{}

	for _, setter := range setters {
		setter(args)
	}
	config := args.kafkaConfig
	logger.Infof(
		"dataId: %s listen %s topic as groupId: %s, establish a kafka[%s(%s:%s)] connection",
		dataId,
		config.KafkaTopic,
		config.KafkaGroupId,
		config.KafkaHost,
		config.KafkaUsername,
		config.KafkaPassword,
	)
	authConfig := getConnectionSASLConfig(config.KafkaUsername, config.KafkaPassword)
	group, err := sarama.NewConsumerGroup([]string{config.KafkaHost}, config.KafkaGroupId, authConfig)
	if err != nil {
		logger.Errorf(
			"Failed to create a consumer group, topic: %s may not be consumed correctly. error: %s",
			config.KafkaTopic, err,
		)
		return nil, err
	}

	var limiter tokenBucketRateLimiter
	if args.qps == 0 {
		limiter = tokenBucketRateLimiter{unlimited: true}
	} else if args.qps < 0 {
		limiter = tokenBucketRateLimiter{rejected: true}
	} else {
		limiter = tokenBucketRateLimiter{limiter: flowcontrol.NewTokenBucketRateLimiter(float32(args.qps), args.qps*2)}
	}

	return &kafkaNotifier{
		ctx:           args.ctx,
		config:        args.kafkaConfig,
		consumerGroup: group,
		handler: consumeHandler{
			ctx:     args.ctx,
			dataId:  dataId,
			qps:     args.qps,
			limiter: &limiter,
			spans:   make(chan []window.StandardSpan, args.chanBufferSize),
			groupId: config.KafkaGroupId,
			topic:   config.KafkaTopic,
		},
	}, nil
}

// getConnectionSASLConfig Establish a connection by SHA512
func getConnectionSASLConfig(username, password string) *sarama.Config {
	config := sarama.NewConfig()
	config.Producer.Return.Successes = true
	config.Version = sarama.V0_10_2_1
	config.Consumer.Offsets.Initial = sarama.OffsetNewest

	if username != "" && password != "" {
		config.Net.SASL.Enable = true
		config.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA512
		config.Net.SASL.User = username
		config.Net.SASL.Password = password
		config.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
			return &SCRAMSHA512Client{HashGeneratorFcn: SHA512}
		}
	}

	return config
}

var SHA512 scram.HashGeneratorFcn = sha512.New

// SCRAMSHA512Client SHA 512 implements
type SCRAMSHA512Client struct {
	*scram.Client
	*scram.ClientConversation
	scram.HashGeneratorFcn
}

// Begin SHA512
func (x *SCRAMSHA512Client) Begin(userName, password, authzID string) (err error) {
	x.Client, err = x.HashGeneratorFcn.NewClient(userName, password, authzID)
	if err != nil {
		return err
	}
	x.ClientConversation = x.Client.NewConversation()
	return nil
}

// Step SHA512
func (x *SCRAMSHA512Client) Step(challenge string) (response string, err error) {
	response, err = x.ClientConversation.Step(challenge)
	return response, err
}
