// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/Shopify/sarama"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// Producer : 为了测试
type Producer = sarama.AsyncProducer

var NewKafkaProducerConfig = func(conf define.Configuration) (*sarama.Config, error) {
	// 部分参数设置依据,参见kafka性能报告
	c, err := NewKafkaConfig(conf)
	if err != nil {
		return nil, err
	}

	c.Producer.Partitioner = sarama.NewRandomPartitioner

	switch conf.GetInt(ConfKafkaProducerRequiredAcks) {
	case 1:
		c.Producer.RequiredAcks = sarama.WaitForLocal
	case -1:
		c.Producer.RequiredAcks = sarama.WaitForAll
	case 0:
		c.Producer.RequiredAcks = sarama.NoResponse
	default:
		c.Producer.RequiredAcks = sarama.WaitForLocal
	}

	c.Producer.Retry.Max = conf.GetInt(ConfKafkaProducerRetryMax)
	c.Producer.Retry.Backoff = conf.GetDuration(ConfKafkaProducerRetryBackoff)
	return c, c.Validate()
}

// Backend :
type Backend struct {
	*define.BaseBackend
	*define.ProcessorMonitor
	frontendDeltaObserver prometheus.Observer
	StartMonitor          prometheus.Counter
	skipStats             prometheus.Counter
	ctx                   context.Context
	cancelFunc            context.CancelFunc
	pushOnce              sync.Once
	payloadChan           chan define.Payload
	wg                    sync.WaitGroup
	producer              Producer
	dropEmptyMetrics      bool

	Topic     string
	Key       string
	Partition int32
	ETLConfig string
}

func (b *Backend) SetETLRecordFields(f *define.ETLRecordFields) {}

// NewProducer :创建producer　被单独提了出来，同样是为了测试
var NewProducer = func(cluster []string, conf *sarama.Config) (Producer, error) {
	return sarama.NewAsyncProducer(cluster, conf)
}

// NewKafkaBackend:
func NewKafkaBackend(ctx context.Context, name string) (*Backend, error) {
	shipper := config.ShipperConfigFromContext(ctx)
	kafkaConfig := shipper.AsKafkaCluster()

	topic := kafkaConfig.GetTopic()
	// 该参数实际为partition数量,目前分区情况为随机
	partition := kafkaConfig.GetPartition()

	cluster := fmt.Sprintf("%s:%d", kafkaConfig.GetDomain(), kafkaConfig.GetPort())
	logging.Debugf("prepare to push to cluster: %v, topic: %v", cluster, topic)

	ctx, cancelFun := context.WithCancel(ctx)
	pipeConfig := config.PipelineConfigFromContext(ctx)
	return &Backend{
		BaseBackend:      define.NewBaseBackend(name),
		ProcessorMonitor: NewKafkaBackendProcessorMonitor(pipeConfig),
		frontendDeltaObserver: define.MonitorFrontendRecvDeltaDuration.With(prometheus.Labels{
			"id":      strconv.Itoa(pipeConfig.DataID),
			"cluster": define.ConfClusterID,
		}),
		StartMonitor: NewKafkaBackendStartMonitor(pipeConfig),
		skipStats:    NewKafkaBackendSkippedMonitor(pipeConfig),
		cancelFunc:   cancelFun,
		ctx:          ctx,
		payloadChan:  make(chan define.Payload),
		producer:     nil,
		Topic:        topic,
		Partition:    int32(partition),
	}, nil
}

func (b *Backend) init() error {
	var (
		conf = config.FromContext(b.ctx)
		err  error
	)

	pipelineConfig := config.PipelineConfigFromContext(b.ctx)
	if pipelineConfig != nil {
		opts := utils.NewMapHelper(pipelineConfig.Option)
		b.dropEmptyMetrics, _ = opts.GetBool(config.PipelineConfigDropEmptyMetrics)
	}

	shipper := config.ShipperConfigFromContext(b.ctx)
	kafkaConfig := shipper.AsKafkaCluster()
	cluster := fmt.Sprintf("%s:%d", kafkaConfig.GetDomain(), kafkaConfig.GetPort())

	producerConfig, err := NewKafkaProducerConfig(conf)
	if err != nil {
		logging.Errorf("create producer config err: %v", err)
		return err
	}

	auth := config.NewAuthInfo(shipper)
	username, err := auth.GetUserName()
	if err != nil {
		logging.Warnf("%v may not establish connection %v: username", b.Name, define.ErrGetAuth)
	}
	password, err := auth.GetPassword()
	if err != nil {
		logging.Warnf("%v may not establish connection %v: password", b.Name, define.ErrGetAuth)
	}
	if username != "" || password != "" && err == nil {
		producerConfig.Net.SASL.User = username
		producerConfig.Net.SASL.Password = password
		producerConfig.Net.SASL.Enable = true

		// 目前仅支持 sha512/sha256
		info := utils.NewMapHelper(kafkaConfig.AuthInfo)
		if mechanisms, ok := info.GetString(optSaslMechanisms); ok {
			switch mechanisms {
			case "SCRAM-SHA-512":
				producerConfig.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA512
				producerConfig.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
					return &XDGSCRAMClient{HashGeneratorFcn: SHA512}
				}
			case "SCRAM-SHA-256":
				producerConfig.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA256
				producerConfig.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
					return &XDGSCRAMClient{HashGeneratorFcn: SHA256}
				}
			}
		}
	}

	b.producer, err = NewProducer([]string{cluster}, producerConfig)
	if err != nil {
		logging.Errorf("create backend client, cluster=%v, topic=%v, err %s", cluster, b.Topic, err)
		return err
	}
	return nil
}

// Push :
func (b *Backend) Push(d define.Payload, killChan chan<- error) {
	b.pushOnce.Do(func() {
		if err := b.init(); err != nil {
			b.StartMonitor.Inc()
			killChan <- err
			return
		}

		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			for {
				select {
				case err := <-b.producer.Errors():
					if err != nil {
						logging.Errorf("%v write kafka failed, err: %v", b, err)
						b.CounterFails.Inc()
					}
				case <-b.ctx.Done():
					return
				}
			}
		}()

		for i := 0; i < define.Concurrency(); i++ {
			b.wg.Add(1)
			go func() {
				defer b.wg.Done()
				defer utils.RecoverError(func(e error) {
					logging.Errorf("push kafka backend error %v", e)
				})

			loop:
				for {
					select {
					case p, ok := <-b.payloadChan:
						if !ok {
							logging.Infof("Fetch payload failed, no data get from pipeline, pipeline quit") // 关闭
							break loop
						}
						b.SendMsg(p)
					case <-b.ctx.Done():
						break loop
					}
				}
			}()
		}
	})
	if b.producer == nil {
		return
	}
	b.payloadChan <- d
}

// SendMsg :
func (b *Backend) SendMsg(payload define.Payload) {
	var (
		message []byte
		err     error
	)

	var etlRecord define.ETLRecord
	// 只过滤能够被转换为 ETLRecord 的数据，且metric不为空
	// 此时需要判断是否所有metric值都是nil
	// 注意：此处还有部分事件数据是没有metric字段，此时metric长度为0;所以需要增加长度大于0的判断，避免误伤
	if err := payload.To(&etlRecord); err == nil && len(etlRecord.Metrics) > 0 {
		drop := true

		for _, v := range etlRecord.Metrics {
			drop = drop && v == nil
		}

		// cases: 1) 所有的 metrics 值都为 nil
		if drop {
			b.skipStats.Inc()
			logging.Warnf("skip useless record: %+v", etlRecord)
			return
		}
	}

	// 丢弃空 metrics
	if b.dropEmptyMetrics {
		for k, v := range etlRecord.Metrics {
			if v == nil {
				delete(etlRecord.Metrics, k)
			}
		}
		if len(etlRecord.Metrics) <= 0 {
			b.skipStats.Inc()
			logging.Warnf("skip empty record: %+v", etlRecord)
			return
		}
	}

	// 时间非空
	if etlRecord.Time != nil {
		t := payload.GetTime()
		at := utils.ParseTimeStamp(*etlRecord.Time)
		// 接收时刻 - 数据本身时间
		b.frontendDeltaObserver.Observe(t.Sub(at).Seconds())
	}

	err = payload.To(&message)
	if err != nil {
		logging.Warnf("%v load %#v error %v", b, payload, err)
		b.CounterFails.Inc()
		return
	}

	ok := b.write(&sarama.ProducerMessage{
		Topic:     b.Topic,
		Key:       sarama.StringEncoder(b.Key),
		Value:     sarama.ByteEncoder(message),
		Partition: b.Partition,
	})
	if !ok {
		b.CounterFails.Inc()
		return
	}
	b.CounterSuccesses.Inc()
}

// write :
func (b *Backend) write(msg *sarama.ProducerMessage) bool {
	select {
	case b.producer.Input() <- msg:
		return true
	case <-b.ctx.Done():
		logging.Errorf("%v write kafka cancel", b)
		return false
	}
}

// Close :
func (b *Backend) Close() error {
	var err error
	b.cancelFunc()
	close(b.payloadChan)
	if b.producer != nil {
		err = b.producer.Close()
	}
	b.wg.Wait()
	return err
}

func init() {
	define.RegisterBackend("kafka", func(ctx context.Context, name string) (backend define.Backend, e error) {
		if config.FromContext(ctx) == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "config is empty")
		}
		if config.ShipperConfigFromContext(ctx) == nil { // shipper config 包括　MQconfig config
			return nil, errors.Wrapf(define.ErrOperationForbidden, "shipper config is empty")
		}
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}

		return NewKafkaBackend(ctx, pipeConfig.FormatName(name))
	})
}
