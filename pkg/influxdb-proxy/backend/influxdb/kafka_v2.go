// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"
	"fmt"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

// emptyKafkaStorage 空的kafka对象，用于启动屏蔽kafka操作的安全模式
type emptyKafkaStorage struct{}

func newEmptyKafka() StorageBackup {
	return new(emptyKafkaStorage)
}

func (e *emptyKafkaStorage) Push(data string) error {
	return nil
}

func (e *emptyKafkaStorage) Close() {
}

func (e *emptyKafkaStorage) Pull(ctx context.Context, handler DataHandler) error {
	return nil
}

func (e *emptyKafkaStorage) HasData() (bool, error) {
	return false, nil
}

func (e *emptyKafkaStorage) GetOffsetSize() (int64, error) {
	return 0, nil
}

func (e *emptyKafkaStorage) Topic() string {
	return ""
}

type kafkaBackup struct {
	ctx    context.Context
	cancel context.CancelFunc

	client   sarama.Client
	producer sarama.SyncProducer
	config   *sarama.Config

	topicName string
	groupName string

	pullHandler DataHandler
}

// Broker :
type Broker interface {
	CreateTopics(request *sarama.CreateTopicsRequest) (*sarama.CreateTopicsResponse, error)
	Open(conf *sarama.Config) error
	Close() error
	Connected() (bool, error)
}

// stubbing :
var (
	NewKafkaConsumerGroup      = sarama.NewConsumerGroupFromClient
	NewSyncProducer            = sarama.NewSyncProducer
	NewClient                  = sarama.NewClient
	NewOffsetManagerFromClient = sarama.NewOffsetManagerFromClient
	NewKafkaConfig             = func() (*sarama.Config, error) {
		config := sarama.NewConfig()
		config.Producer.RequiredAcks = sarama.WaitForAll // Wait for all in-sync replicas to ack the message
		config.Producer.Retry.Max = 10                   // Retry up to 10 times to produce the message
		config.Producer.Return.Successes = true
		config.Producer.Return.Errors = true
		config.Consumer.Offsets.Initial = sarama.OffsetOldest
		offsetString := common.Config.GetString(common.ConfigKeyKafkaRetention)
		// 如果offset_retention配置为0，则不设置该参数
		if offsetString != "0" {
			offsetRetention, err := time.ParseDuration(offsetString)
			// default retention: 7 days
			if err != nil {
				offsetRetention = 336 * time.Hour
			}
			// min retention: 1 day
			if offsetRetention < 24*time.Hour {
				offsetRetention = 24 * time.Hour
			}
			config.Consumer.Offsets.Retention = offsetRetention
		}

		// check if auth is needed
		if common.Config.GetBool(common.ConfigKeyKafkaIsAuth) {
			config.Net.SASL.Enable = true
			config.Net.SASL.User = common.Config.GetString(common.ConfigKeyKafkaUsername)
			config.Net.SASL.Password = common.Config.GetString(common.ConfigKeyKafkaPassword)
			config.Net.SASL.Mechanism = sarama.SASLMechanism(common.Config.GetString(common.ConfigKeyKafkaMechanism))
			config.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient { return &XDGSCRAMClient{HashGeneratorFcn: SHA512} }
		}

		version := common.Config.GetString(common.ConfigKeyKafkaVersion)
		r, err := sarama.ParseKafkaVersion(version)
		if err != nil {
			return nil, err
		}
		config.Version = r
		return config, config.Validate()
	}
	NewClusterAdmin = sarama.NewClusterAdmin
)

// NewKafkaBackup :
var NewKafkaBackup = func(ctx context.Context, topicName string) (StorageBackup, error) {
	return newKafkaBackup(ctx, topicName)
}

func newKafkaBackup(ctx context.Context, topicName string) (StorageBackup, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": "newKafkaBackup",
		"topic":  topicName,
	})
	// we get the info here but not to save it
	// for the config file will reload, we will always get the last config info
	info := MakeKafkaConfig()

	// Make a new Topic string
	topic := fmt.Sprintf("%s_%s", info.TopicPrefix, topicName)

	backup := &kafkaBackup{
		topicName: topic,

		groupName: fmt.Sprintf("bkmonitor_influxdb_proxy_%s", topicName),
	}

	backup.ctx, backup.cancel = context.WithCancel(ctx)

	// init kafka config
	config, err := NewKafkaConfig()
	if err != nil {
		flowLog.Warningf("create kafka config error: %v", err)
		return nil, err
	}

	retention := config.Consumer.Offsets.Retention
	flowLog.Debugf("kafka offset retention:%s", retention)

	backup.config = config

	// init a kafka client
	brokerAddress := GetBrokerAddress()
	backup.client, err = NewClient([]string{brokerAddress}, config)
	if err != nil {
		flowLog.Errorf("failed to create kafka client for->[%#v]", err)
		return nil, err
	}

	// make the create topic request ready
	topicInfo := &sarama.TopicDetail{
		NumPartitions:     int32(1),
		ReplicationFactor: int16(1),
	}

	admin, err := NewClusterAdmin([]string{brokerAddress}, config)
	if err != nil {
		flowLog.Errorf("failed to get kafka admin for->[%#v]", err)
		return nil, err
	}

	defer func() { _ = admin.Close() }()
	err = admin.CreateTopic(topicName, topicInfo, false)
	if kError, ok := err.(sarama.KError); ok && kError != sarama.ErrTopicAlreadyExists && kError != sarama.ErrNoError {
		flowLog.Errorf("failed to create topic->[%s] for->[%v] or [%#v]", backup.topicName, err, err)
		return nil, err
	}
	flowLog.Debugf("success to create or make sure topic->[%s] exists", backup.topicName)

	// init a sync producer
	backup.producer, err = NewSyncProducer([]string{brokerAddress}, config)
	if err != nil {
		flowLog.Errorf("failed for create kafka producer for->[%s]", err)
		return nil, err
	}
	// 启动一个线程，关闭backend时关闭backup
	go func() {
		<-backup.ctx.Done()
		backup.client.Close()
		backup.producer.Close()
	}()

	// keep all info
	return backup, nil
}

func (k *kafkaBackup) Topic() string {
	return k.topicName
}

// Push: will push all the data you want into the kafka
// topic name will load from the config file every time it fire
func (k *kafkaBackup) Push(data string) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
		"topic":  k.topicName,
	})
	// We are not setting a message key, which means that all messages will
	// be distributed randomly over the different partitions.
	partition, offset, err := k.producer.SendMessage(&sarama.ProducerMessage{
		Topic:     k.topicName,
		Value:     sarama.StringEncoder(data),
		Partition: 0,
	})
	if err != nil {
		flowLog.Errorf("Failed to store data:, %s", err)
		return err
	}
	// The tuple (topic, partition, offset) can be used as a unique identifier
	// for a message in a Kafka cluster.
	flowLog.Debugf("data is stored with unique topic %s identifier important/%d/%d", k.topicName, partition, offset)
	return nil
}

// Pull: will try to pull all the data from kafka, every data pull from the kafka will fire handler
// pull will be exit in:
// 1. context has been cancel.
// 2. data is consume to the offset when pull is fired.
func (k *kafkaBackup) Pull(ctx context.Context, handler DataHandler) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
		"topic":  k.topicName,
	})

	pullCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// make a consumerGroup
	consumerGroup, err := NewKafkaConsumerGroup(k.groupName, k.client)
	if err != nil {
		flowLog.Errorf("failed to create kafka consumer for topic->[%s] for->[%#v]", k.topicName, err)
		return err
	}
	defer func() {
		err = consumerGroup.Close()
		if err != nil {
			flowLog.Errorf("failed to close kafka consumer for topic->[%s] for->[%#v]", k.topicName, err)
		}
	}()

	k.pullHandler = handler
	// Reset the handler
	defer func() { k.pullHandler = nil }()

	flowLog.Warnf("topic->[%s] now is ready to Consume backup data ", k.topicName)
	// here will be block util
	// 1. the consumer session is finish.
	// 2. the consumer get the message meet the offset we want.
	var loopErr error
	var hasData bool
	// 没有数据需要消费则不再重新调用consume，当次拉取结束
	for hasData, loopErr = k.HasData(); loopErr == nil && hasData; hasData, loopErr = k.HasData() {
		flowLog.Infof("found data in topic:%s", k.topicName)
		// 每次都检查一下是否上层已经关闭了
		select {
		case <-pullCtx.Done():
			flowLog.Warnf("topic->[%s] received ctx done,exit now", k.topicName)
			return nil
		default:
		}
		flowLog.Infof("consumer topic:%s start to consume data", k.topicName)
		if err = consumerGroup.Consume(pullCtx, []string{k.topicName}, k); err != nil {
			flowLog.Errorf("consume topic->[%s] failed for->[%#v], pull finish", k.topicName, err)
			loopErr = err
			break
		}
	}
	flowLog.Infof("consume loop break,consumer topic:%s", k.topicName)

	if loopErr != nil {
		flowLog.Errorf("consume topic->[%s] break by error:%s", k.topicName, loopErr)
		return loopErr
	}

	flowLog.Debugf("topic->[%s] consume done", k.topicName)
	return nil
}

func (k *kafkaBackup) Close() {
	k.cancel()
}

// GetOffsetSize 获取当前kafka的未被读取的备份量
func (k *kafkaBackup) GetOffsetSize() (int64, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
		"topic":  k.topicName,
	})
	var lastOffset int64
	var oldestOffset int64
	var currentOffset int64
	var err error

	partitions, err := k.client.Partitions(k.topicName)
	if err != nil {
		flowLog.Errorf("get topic [%s] partitions failed:%v", k.topicName, err)
		return 0, err
	}

	var totalOffset int64
	// 遍历全部partition,将所有偏移量累加
	for _, partitionID := range partitions {
		if lastOffset, err = k.client.GetOffset(k.topicName, partitionID, sarama.OffsetNewest); err != nil {
			flowLog.Errorf("failed to get last Offset for topic->[%s] for->[%#v]", k.topicName, err)
			return 0, err
		}
		flowLog.Debugf("topic->[%s] partition->[%d] record:lastoffset:%d", k.topicName, partitionID, lastOffset)
		// 判断最老的offset是否已经都大于了这个目标的offset
		if oldestOffset, err = k.client.GetOffset(k.topicName, partitionID, sarama.OffsetOldest); err != nil {
			flowLog.Errorf("failed to get oldest Offset for topic->[%s] for->[%#v]", k.topicName, err)
			return 0, err
		}
		flowLog.Debugf("topic->[%s] partition->[%d] record:oldestoffset:%d", k.topicName, partitionID, oldestOffset)
		if lastOffset == oldestOffset {
			flowLog.Debugf("topic->[%s] partition->[%d] has the same lastoffset as oldestoffset:%d", k.topicName, partitionID, oldestOffset)
			continue
		}
		// 传入当前检查中的partitionID
		if currentOffset, err = k.currentOffset(partitionID); err != nil {
			flowLog.Errorf("topic->[%s] failed to get current offset for->[%#v]", k.topicName, err)
			return 0, err
		}
		flowLog.Debugf("topic->[%s] partition->[%d] record:current offset:%d", k.topicName, partitionID, currentOffset)
		offset := lastOffset - currentOffset
		totalOffset = totalOffset + offset
	}
	flowLog.Debugf("topic->[%s] total offset:%d", k.topicName, totalOffset)

	return totalOffset, nil
}

// HasData: return if there is any data storage in the kafka and should trans into influxDB
func (k *kafkaBackup) HasData() (bool, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
		"topic":  k.topicName,
	})
	totalOffset, err := k.GetOffsetSize()
	if err != nil {
		flowLog.Errorf("topic->[%s] get offset size failed,error:%s", k.topicName, err)
		return false, err
	}
	if totalOffset > 0 {
		flowLog.Debugf("topic->[%s] has %d offset to consume", k.topicName, totalOffset)
		return true, nil
	}

	// 如果拿到了预期以外的值，则报错
	if totalOffset < 0 {
		flowLog.Errorf("topic->[%s] has %d offset to consume,which lower than zero", k.topicName, totalOffset)
		return false, backend.ErrLowerZeroOffset
	}
	flowLog.Debugf("topic->[%s] has no offset to consume", k.topicName)
	// totalOffset==0说明没有检查到任何partition需要消费数据，此时返回false
	return false, nil
}

// getCurrentOffset: return the topic first partition current offset
func (k *kafkaBackup) currentOffset(partitionID int32) (offset int64, err error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
		"topic":  k.topicName,
	})
	offset = -1
	var manager sarama.OffsetManager
	var pom sarama.PartitionOffsetManager

	if manager, err = NewOffsetManagerFromClient(k.groupName, k.client); err != nil {
		flowLog.Warnf("failed to build offset manage from client for->[%v], "+
			"we will assert that the backend still has data.", err)
		return offset, err
	}
	defer func() { _ = manager.Close() }()

	if pom, err = manager.ManagePartition(k.topicName, partitionID); err != nil {
		flowLog.Warnf("failed to build partition offset manage from client for->[%v], "+
			"we will assert that the backend still has data.", err)
		return offset, err
	}
	defer func() { _ = pom.Close() }()

	offset, _ = pom.NextOffset()
	flowLog.Debugf("Get topic->[%s] on partition->[%d] next offset->[%d]", k.topicName, partitionID, offset)
	return offset, err
}

// ALL methods below is create for kafka Consumer use
// Setup : Do nothing
func (k *kafkaBackup) Setup(sess sarama.ConsumerGroupSession) error {
	return nil
}

// Cleanup :
func (k *kafkaBackup) Cleanup(sess sarama.ConsumerGroupSession) error {
	return nil
}

// ConsumeClaim :
// kafka data format : dbname.content, so db name can not include . character
func (k *kafkaBackup) ConsumeClaim(sess sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":    moduleName,
		"topic":     k.topicName,
		"member_id": sess.MemberID(),
	})
	flowLog.Warnf("topic->[%s | %s | %d] now is inside the Consumer start offset->[%d] last offset->[%d] function wait for messages.", k.topicName, claim.Topic(), claim.Partition(), claim.InitialOffset(), claim.HighWaterMarkOffset())
	flowLog.Errorf("session info->[%#v]", sess.Claims())

	messages := claim.Messages()
	// 增加一个定时器
	t := time.NewTicker(5 * time.Second)

	for {
		select {
		case msg, ok := <-messages:

			// if message channel is closed?
			if !ok {
				flowLog.Errorf("message channel closed,claim exit,topic name:%s", k.topicName)
				return nil
			}
			flowLog.Debugf("%v topic->[%q] partition->[%d] offset->[%d] message length->[%v]", k, msg.Topic, msg.Partition, msg.Offset, len(msg.Value))
			content := string(msg.Value)

			// fire the handler, check if any error, it will push back to kafka
			// WARNING: ONCE THE DATA IS HANDLED BY THE HANDLER, IT WILL BE MARKED, AND REMOVE FROM KAFKA
			// ANY REPROCESSING MUST HANDLE IN THE HANDLER!
			k.pullHandler(content)
			flowLog.Debugf("content->[%s] pull from kafka topic->[%s] offset->[%d]", content, k.topicName, msg.Offset)

			// Marked data is consumed
			sess.MarkMessage(msg, "")
			flowLog.Debugf("topic->[%s] offset->[%d] push data_content->[%s] to handler and marked.", msg.Topic, msg.Offset, content)

		case <-t.C:
			// 判断如果已经没有数据在队列中了，那么其实这个goroutines的消费可以退出了
			flag, err := k.HasData()
			if err != nil {
				flowLog.Errorf("check has data failed,error:%s", err)
				return err
			}
			if !flag {
				flowLog.Infof("ticker alarm and found no data in kafka, consumer topic:%s will exit now.", k.topicName)
				return nil
			}
		}
	}
}
