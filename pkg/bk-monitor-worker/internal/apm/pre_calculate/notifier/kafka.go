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

	"github.com/IBM/sarama"
	"github.com/valyala/fastjson"
	"github.com/xdg-go/scram"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/window"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
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
		"KafkaNotifier started. host: %s topic: %s groupId: %s",
		k.config.KafkaHost, k.config.KafkaTopic, k.config.KafkaGroupId,
	)
	for {
		select {
		case <-k.ctx.Done():
			if err := k.consumerGroup.Close(); err != nil {
				logger.Errorf("Failed to close ConsumerGroup, error: %s", err)
			}
			logger.Infof("ConsumerGroup stopped.")
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
loop:
	for {
		select {
		case msg := <-claim.Messages():
			metrics.AddApmNotifierReceiveMessageCount(c.dataId, c.topic)
			if session != nil {
				session.MarkMessage(msg, "")
				c.sendSpans(msg.Value)
			}
		case <-session.Context().Done():
			logger.Infof("kafka consume handler session done. topic: %s groupId: %s", c.topic, c.groupId)
			break loop
		case <-c.ctx.Done():
			logger.Infof("kafka consume handler context done. topic: %s groupId: %s", c.topic, c.groupId)
			break loop
		}
	}
	return nil
}

func (c consumeHandler) sendSpans(message []byte) {
	start := time.Now()
	var res []window.StandardSpan
	v, _ := fastjson.ParseBytes(message)
	items := v.GetArray("items")

	for _, item := range items {
		res = append(res, *window.ToStandardSpan(item))
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
	return &kafkaNotifier{
		ctx:           args.ctx,
		config:        args.kafkaConfig,
		consumerGroup: group,
		handler: consumeHandler{
			ctx:     args.ctx,
			dataId:  dataId,
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

var (
	SHA512 scram.HashGeneratorFcn = sha512.New
)

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
	return
}
