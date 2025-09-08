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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
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
		StorageConfig: map[string]any{
			"topic":     a.Topic,
			"partition": a.Partition,
		},
	}
	return consulConfig, nil
}
