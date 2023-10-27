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
	"time"

	"github.com/Shopify/sarama"
	"github.com/rcrowley/go-metrics"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// NewKafkaConfig
var NewKafkaConfig = func(conf define.Configuration) (*sarama.Config, error) {
	c := sarama.NewConfig()
	c.MetricRegistry = metrics.DefaultRegistry
	c.ClientID = define.ProcessID
	c.Consumer.Return.Errors = true
	c.Consumer.MaxProcessingTime = 20 * time.Second
	c.Consumer.Offsets.AutoCommit.Enable = false // 关闭自动提交特性 交由 DelayOffsetManager 管理
	return c, nil
}

const (
	ConfKafkaVersion               = "kafka.client.version"
	ConfKafkaMetricsSyncInterval   = "kafka.metrics_sync_interval"
	ConfKafkaConsumerGroupPrefix   = "kafka.consumer_group_prefix"
	ConfKafkaRebalanceTimeout      = "kafka.rebalance_timeout"
	ConfKafkaReconnectTimeout      = "kafka.reconnect_timeout"
	ConfKafkaConsumerOffsetInitial = "kafka.initial_offset"
	ConfKafkaClientType            = "kafka.client_type"
	ConfKafkaOffsetsCommitInterval = "kafka.consumer.offsets.commit_interval"
	ConfKafkaMaxProcessingTime     = "kafka.consumer.max_processing_time"
	ConfKafkaPartitioner           = "kafka.producer.partition_strategy"
	ConfKafkaProducerRequiredAcks  = "kafka.producer.required_acks"
	ConfKafkaProducerRetryMax      = "kafka.producer.retry_max"
	ConfKafkaProducerRetryBackoff  = "kafka.producer.retry_backoff"
	ConfKafkaFlowInterval          = "kafka.flow_interval"
)

func initConfiguration(c define.Configuration) {
	c.SetDefault(ConfKafkaVersion, "0.10.2.0")
	c.SetDefault(ConfKafkaConsumerGroupPrefix, "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/")
	c.SetDefault(ConfKafkaConsumerOffsetInitial, sarama.OffsetNewest)
	c.SetDefault(ConfKafkaClientType, "cluster")
	c.SetDefault(ConfKafkaMetricsSyncInterval, "10s")
	c.SetDefault(ConfKafkaRebalanceTimeout, "10s")
	c.SetDefault(ConfKafkaReconnectTimeout, "10s")
	c.SetDefault(ConfKafkaOffsetsCommitInterval, "3s") // 请勿随意调整此配置项
	c.SetDefault(ConfKafkaMaxProcessingTime, "20s")

	c.SetDefault(ConfKafkaPartitioner, "hash")
	c.SetDefault(ConfKafkaProducerRequiredAcks, 1)
	c.SetDefault(ConfKafkaProducerRetryMax, 3)
	c.SetDefault(ConfKafkaProducerRetryBackoff, 100*time.Millisecond)
	c.SetDefault(ConfKafkaFlowInterval, "60s")

	c.RegisterAlias("kafka.backend.channel_size", pipeline.ConfKeyPipelineChannelSize)
	c.RegisterAlias("kafka.backend.wait_delay", pipeline.ConfKeyPipelineFrontendWaitDelay)
	c.RegisterAlias("kafka.backend.buffer_size", pipeline.ConfKeyPayloadBufferSize)
	c.RegisterAlias("kafka.backend.flush_interval", pipeline.ConfKeyPayloadFlushInterval)
	c.RegisterAlias("kafka.backend.flush_reties", pipeline.ConfKeyPayloadFlushReties)
	c.RegisterAlias("kafka.backend.max_concurrency", pipeline.ConfKeyPayloadFlushConcurrency)
}

func init() {
	utils.CheckError(eventbus.Subscribe(eventbus.EvSysConfigPreParse, initConfiguration))
}
