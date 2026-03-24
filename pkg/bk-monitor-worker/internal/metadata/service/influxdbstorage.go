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
)

// InfluxdbStorageSvc influxdb storage service
type InfluxdbStorageSvc struct {
	*storage.InfluxdbStorage
}

func NewInfluxdbStorageSvc(obj *storage.InfluxdbStorage) InfluxdbStorageSvc {
	return InfluxdbStorageSvc{
		InfluxdbStorage: obj,
	}
}

// ConsulConfig 获取influxdb storage的consul配置信息
func (k InfluxdbStorageSvc) ConsulConfig() (*StorageConsulConfig, error) {
	// 集群信息
	clusterInfo, err := k.StorageCluster()
	if err != nil {
		return nil, err
	}
	clusterConsulConfig, err := NewClusterInfoSvc(clusterInfo).ConsulConfig()
	if err != nil {
		return nil, err
	}
	// 获取 influxdb 集群名称
	defaultInstanceClusterName := ""
	if k.InfluxdbProxyStorageId != 0 || &k.InfluxdbProxyStorageId != nil {
		if influxdbStorage, err := NewInfluxdbProxyStorageSvc(nil).GetInfluxdbStorage(&k.InfluxdbProxyStorageId, nil, nil); err == nil {
			defaultInstanceClusterName = influxdbStorage.InstanceClusterName
		}
	}
	clusterConsulConfig.ClusterConfig.InstanceClusterName = defaultInstanceClusterName
	// influxdb的consul配置
	consulConfig := &StorageConsulConfig{
		ClusterInfoConsulConfig: clusterConsulConfig,
		StorageConfig: map[string]any{
			"real_table_name":       k.RealTableName,
			"database":              k.Database,
			"retention_policy_name": k.RpName(),
		},
	}
	return consulConfig, nil
}
