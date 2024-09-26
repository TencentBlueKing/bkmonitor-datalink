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

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/diffutil"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/optionx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// KafkaStorageSvc kafka storage service
type KafkaStorageSvc struct {
	*storage.KafkaStorage
}

func NewKafkaStorageSvc(obj *storage.KafkaStorage) KafkaStorageSvc {
	return KafkaStorageSvc{
		KafkaStorage: obj,
	}
}

// StorageCluster 返回集群对象
func (a KafkaStorageSvc) StorageCluster() (*storage.ClusterInfo, error) {
	var clusterInfo storage.ClusterInfo
	if err := storage.NewClusterInfoQuerySet(mysql.GetDBSession().DB).ClusterIDEq(a.StorageClusterID).One(&clusterInfo); err != nil {
		return nil, err
	}
	return &clusterInfo, nil
}

// ConsulConfig 获取kafka storage的consul配置信息
func (a KafkaStorageSvc) ConsulConfig() (*StorageConsulConfig, error) {
	// 集群信息
	clusterInfo, err := a.StorageCluster()
	if err != nil {
		return nil, err
	}
	clusterConsulConfig, err := NewClusterInfoSvc(clusterInfo).ConsulConfig()
	if err != nil {
		return nil, err
	}
	// kafka的consul配置
	consulConfig := &StorageConsulConfig{
		ClusterInfoConsulConfig: clusterConsulConfig,
		StorageConfig: map[string]interface{}{
			"topic":     a.Topic,
			"partition": a.Partition,
		},
	}
	return consulConfig, nil
}

// CreateTable 创建存储
func (KafkaStorageSvc) CreateTable(tableId string, isSyncDb bool, storageConfig *optionx.Options) error {
	storageConfig.SetDefault("partition", uint(1))
	storageConfig.SetDefault("retention", int64(1800000))
	storageConfig.SetDefault("useDefaultFormat", true)
	db := mysql.GetDBSession().DB
	storageClusterId, ok := storageConfig.GetUint("storageClusterId")
	if !ok {
		if id := config.GlobalDefaultKafkaStorageClusterId; id != 0 {
			storageClusterId = id
		} else {
			var cluster storage.ClusterInfo
			if err := storage.NewClusterInfoQuerySet(db).ClusterTypeEq(models.StorageTypeKafka).IsDefaultClusterEq(true).One(&cluster); err != nil {
				return err
			}
			storageClusterId = cluster.ClusterID
		}
	} else {
		var cluster storage.ClusterInfo
		if err := storage.NewClusterInfoQuerySet(db).ClusterTypeEq(models.StorageTypeKafka).ClusterIDEq(storageClusterId).One(&cluster); err != nil {
			return errors.Wrapf(err, "query cluster_id [%v] failed", storageClusterId)
		}
	}
	// 校验table_id， key是否存在冲突
	count, err := storage.NewKafkaStorageQuerySet(db).TableIDEq(tableId).Count()
	if err != nil {
		return err
	}
	if count != 0 {
		return errors.Errorf("result_table [%s] already has redis storage config, nothing will add", tableId)
	}

	// 如果未有指定key，则改为table_id
	topic, ok := storageConfig.GetString("topic")
	if !ok {
		topic = tableId
	}
	// topic的构造为 ${prefix}_${key}
	if yes, _ := storageConfig.GetBool("useDefaultFormat"); yes {
		var KafkaTopicPrefixStorage = fmt.Sprintf("0%s_storage_", config.BkApiAppCode)
		topic = fmt.Sprintf("%s_%s", KafkaTopicPrefixStorage, topic)
	}
	partition, _ := storageConfig.GetUint("partition")
	retention, _ := storageConfig.GetInt64("retention")
	kafkaStorage := storage.KafkaStorage{
		TableID:          tableId,
		Topic:            topic,
		Partition:        partition,
		StorageClusterID: storageClusterId,
		Retention:        retention,
	}
	if config.BypassSuffixPath != "" && !slicex.IsExistItem(config.SkipBypassTasks, "discover_bcs_clusters") {
		logger.Info(diffutil.BuildLogStr("discover_bcs_clusters", diffutil.OperatorTypeDBCreate, diffutil.NewSqlBody(kafkaStorage.TableName(), map[string]interface{}{
			storage.KafkaStorageDBSchema.TableID.String():          kafkaStorage.TableID,
			storage.KafkaStorageDBSchema.Topic.String():            kafkaStorage.Topic,
			storage.KafkaStorageDBSchema.Partition.String():        kafkaStorage.Partition,
			storage.KafkaStorageDBSchema.StorageClusterID.String(): kafkaStorage.StorageClusterID,
			storage.KafkaStorageDBSchema.Retention.String():        kafkaStorage.Retention,
		}), ""))
	} else {
		if err := kafkaStorage.Create(db); err != nil {
			return err
		}
	}
	logger.Infof("table [%s] now has create kafka storage config", tableId)
	return nil
}
