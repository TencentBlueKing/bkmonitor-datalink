// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processor

import (
	"github.com/Shopify/sarama"
	"go.uber.org/zap"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/logging"
)

type KafkaBackend struct {
	DataSource    *define.KafkaDataSource
	syncProducer  sarama.SyncProducer
	asyncProducer sarama.AsyncProducer
	logger        *zap.SugaredLogger
}

func (k *KafkaBackend) Send(payload define.Payload) error {
	events, err := payload.CleanEachEvent()
	if err != nil {
		return err
	}
	for _, event := range events {
		message := k.NewMessage(event)
		if payload.IgnoreResult {
			// 异步写入，忽略写入结果
			k.asyncProducer.Input() <- message
		} else {
			_, _, err = k.syncProducer.SendMessage(message)
		}
		k.logger.Debugf("DataSource(%d) data was sent to kafka: %s", k.DataSource.DataID, event)
	}
	return err
}

// NewMessage 组装消息对象
func (k *KafkaBackend) NewMessage(message []byte) *sarama.ProducerMessage {
	return &sarama.ProducerMessage{
		Topic: k.DataSource.MQConfig.StorageConfig.Topic,
		Value: sarama.ByteEncoder(message),
	}
}

func NewKafkaBackend(d *define.DataSource) (IBackend, error) {
	logger := logging.GetLogger()

	kafkaConfig, err := define.NewKafkaDataSource(d)
	if err != nil {
		return nil, err
	}
	kafkaBackend := &KafkaBackend{
		DataSource: kafkaConfig,
		logger:     logging.GetLogger(),
	}
	addrs := []string{kafkaBackend.DataSource.GetAddress()}

	producerConfig, err := kafkaBackend.newProducerConfig()
	logger.Debugf("datasource->(%+v), addrs(%+v)", kafkaConfig, addrs)
	if err != nil {
		return nil, err
	}
	err = kafkaBackend.initSyncProducer(addrs, producerConfig)
	if err != nil {
		return nil, err
	}
	err = kafkaBackend.initAsyncProducer(addrs, producerConfig)
	if err != nil {
		return nil, err
	}
	return kafkaBackend, nil
}

func (k *KafkaBackend) newProducerConfig() (sarama.Config, error) {
	producerConfig := sarama.NewConfig()

	producerConfig.ClientID = define.ProcessID

	if k.DataSource.MQConfig.AuthInfo.Username != "" || k.DataSource.MQConfig.AuthInfo.Password != "" {
		producerConfig.Net.SASL.User = k.DataSource.MQConfig.AuthInfo.Username
		producerConfig.Net.SASL.Password = k.DataSource.MQConfig.AuthInfo.Password
		producerConfig.Net.SASL.Enable = true
	}

	err := producerConfig.Validate()
	if err != nil {
		return sarama.Config{}, err
	}
	return *producerConfig, nil
}

// initSyncProducer 创建同步生产者
func (k *KafkaBackend) initSyncProducer(addrs []string, c sarama.Config) error {
	c.Producer.Return.Successes = true

	producer, err := sarama.NewSyncProducer(addrs, &c)
	if err != nil {
		return err
	}
	k.syncProducer = producer
	return nil
}

// initAsyncProducer 创建异步生产者
func (k *KafkaBackend) initAsyncProducer(addrs []string, c sarama.Config) error {
	c.Producer.Return.Errors = false

	producer, err := sarama.NewAsyncProducer(addrs, &c)
	if err != nil {
		return err
	}

	//// 将异步写入产生的异常打到日志中
	//go func() {
	//	logger := logging.GetLogger()
	//
	//	for {
	//		select {
	//		case err = <-producer.Errors():
	//			if err != nil {
	//				logger.Errorf("KafkaBackend error: %v", err)
	//			}
	//		}
	//	}
	//}()
	k.asyncProducer = producer
	return nil
}

func (k *KafkaBackend) Close() {
	if k.asyncProducer != nil {
		k.asyncProducer.Close()
	}
	if k.syncProducer != nil {
		k.syncProducer.Close()
	}
}

func init() {
	RegisterBackend("kafka", NewKafkaBackend)
}
