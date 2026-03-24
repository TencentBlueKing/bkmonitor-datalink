// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package service

import (
	"github.com/Shopify/sarama"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// KafkaTopicInfoSvc kafka topic info service
type KafkaTopicInfoSvc struct {
	*storage.KafkaTopicInfo
}

func NewKafkaTopicInfoSvc(obj *storage.KafkaTopicInfo) KafkaTopicInfoSvc {
	return KafkaTopicInfoSvc{
		KafkaTopicInfo: obj,
	}
}

func (s KafkaTopicInfoSvc) RefreshTopicInfo(clusterInfo storage.ClusterInfo, kafkaClient sarama.Client) error {
	if s.KafkaTopicInfo == nil {
		return errors.New("KafkaTopicInfo can not be nil")
	}
	db := mysql.GetDBSession().DB
	if kafkaClient == nil {
		kafkaClient, err := NewClusterInfoSvc(&clusterInfo).GetKafkaClient()
		if err != nil {
			return errors.Wrapf(err, "get kafka client from cluster [%s] failed", clusterInfo.ClusterName)
		}
		defer kafkaClient.Close()
	}

	partitions, err := kafkaClient.Partitions(s.Topic)
	if err != nil {
		return errors.Wrapf(err, "query topic[%s] partitions failed", s.Topic)
	}
	partitionLen := len(partitions)
	if partitionLen == 0 {
		logger.Infof("query topic[%s] partitions len, bug got 0", s.Topic)
		return nil
	}
	// NOTE: 如果源大于kafka中数据，则忽略
	if s.Partition >= partitionLen {
		logger.Infof("kafka topic info partition [%v] is greater than topic partition len [%v]", s.Partition, partitionLen)
		return nil
	}

	s.Partition = partitionLen
	if err := s.Update(db, storage.KafkaTopicInfoDBSchema.Partition); err != nil {
		return errors.Wrapf(err, "update KafkaTopicInfo [%s] Partition to [%v] failed", s.Topic, partitionLen)
	}
	logger.Infof("kafka topic info for partition of topic [%s] with partition [%v] has been refreshed", s.Topic, s.Partition)
	return nil
}
