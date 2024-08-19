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
	"fmt"

	"github.com/Shopify/sarama"
	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/diffutil"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
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

// CreateInfo 创建一个新的Topic信息
func (s KafkaTopicInfoSvc) CreateInfo(bkDataId uint, topic string, partition int, batchSize *int64, flushInterval *string, consumeRate *int64) (*storage.KafkaTopicInfo, error) {
	db := mysql.GetDBSession().DB
	count, err := storage.NewKafkaTopicInfoQuerySet(db).BkDataIdEq(bkDataId).Count()
	if err != nil {
		return nil, err
	}
	if count != 0 {
		return nil, errors.Errorf("kafka topic for data_id [%v] already exists", bkDataId)
	}
	if topic == "" {
		topic = fmt.Sprintf("%s%v0", "0bkmonitor_", bkDataId)
	}
	if partition == 0 {
		partition = 1
	}
	info := storage.KafkaTopicInfo{
		BkDataId:      bkDataId,
		Topic:         topic,
		Partition:     partition,
		BatchSize:     batchSize,
		FlushInterval: flushInterval,
		ConsumeRate:   consumeRate,
	}
	if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "discover_bcs_clusters") {
		logger.Info(diffutil.BuildLogStr("discover_bcs_clusters", diffutil.OperatorTypeDBCreate, diffutil.NewSqlBody(info.TableName(), map[string]interface{}{
			storage.KafkaTopicInfoDBSchema.BkDataId.String():      info.BkDataId,
			storage.KafkaTopicInfoDBSchema.Topic.String():         info.Topic,
			storage.KafkaTopicInfoDBSchema.Partition.String():     info.Partition,
			storage.KafkaTopicInfoDBSchema.BatchSize.String():     info.BatchSize,
			storage.KafkaTopicInfoDBSchema.FlushInterval.String(): info.FlushInterval,
			storage.KafkaTopicInfoDBSchema.ConsumeRate.String():   info.ConsumeRate,
		}), ""))
	} else {
		err = info.Create(db)
		if err != nil {
			return nil, err
		}
	}
	logger.Infof("new kafka topic is set for data_id [%v] topic [%s] partition [%v]", info.BkDataId, info.Topic, info.Partition)
	return &info, nil
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
	if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "refresh_kafka_topic_info") {
		logger.Info(diffutil.BuildLogStr("refresh_kafka_topic_info", diffutil.OperatorTypeDBUpdate, diffutil.NewSqlBody(s.TableName(), map[string]interface{}{
			storage.KafkaTopicInfoDBSchema.Id.String():        s.Id,
			storage.KafkaTopicInfoDBSchema.Partition.String(): s.Partition,
		}), ""))
	} else {
		if err := s.Update(db, storage.KafkaTopicInfoDBSchema.Partition); err != nil {
			return errors.Wrapf(err, "update KafkaTopicInfo [%s] Partition to [%v] failed", s.Topic, partitionLen)
		}
	}
	logger.Infof("kafka topic info for partition of topic [%s] with partition [%v] has been refreshed", s.Topic, s.Partition)
	return nil
}
