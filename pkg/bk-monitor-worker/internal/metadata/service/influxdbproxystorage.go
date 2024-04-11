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

// InfluxdbProxyStorageSvc influxdb proxy storage service
type InfluxdbProxyStorageSvc struct {
	*storage.InfluxdbProxyStorage
}

func NewInfluxdbProxyStorageSvc(obj *storage.InfluxdbProxyStorage) InfluxdbProxyStorageSvc {
	return InfluxdbProxyStorageSvc{
		InfluxdbProxyStorage: obj,
	}
}

// GetInfluxdbStorage 获取 proxy 集群和存储集群名称
func (k InfluxdbProxyStorageSvc) GetInfluxdbStorage(influxdbProxyStorageId *uint, proxyClusterName *string, storageClusterId *uint) (*storage.InfluxdbProxyStorage, error) {
	db := mysql.GetDBSession().DB
	var proxy storage.InfluxdbProxyStorage
	// 如果 influxdb_proxy_storage_id 存在，则查询到对应 proxy_cluster_name 和 storage_cluster_id
	if influxdbProxyStorageId != nil {
		if err := storage.NewInfluxdbProxyStorageQuerySet(db).IDEq(*influxdbProxyStorageId).One(&proxy); err != nil {
			return nil, err
		}
		return &proxy, nil
	}
	// 如果 proxy_cluster_name 和 storage_cluster_id 都存在，则需要校验是否存在，如果不存在，则记录并使用默认值
	if proxyClusterName != nil && storageClusterId != nil {
		if err := storage.NewInfluxdbProxyStorageQuerySet(db).ProxyClusterIdEq(*storageClusterId).InstanceClusterNameEq(*proxyClusterName).One(&proxy); err != nil {
			return nil, err
		}
		return &proxy, nil
	}
	// 如果 proxy_cluster_name 或者 storage_cluster_id 只有一个存在时，则使用默认查询到的第一个记录
	if storageClusterId != nil {
		if err := storage.NewInfluxdbProxyStorageQuerySet(db).ProxyClusterIdEq(*storageClusterId).One(&proxy); err != nil {
			return nil, err
		}
		return &proxy, nil
	} else if proxyClusterName != nil {
		if err := storage.NewInfluxdbProxyStorageQuerySet(db).InstanceClusterNameEq(*proxyClusterName).One(&proxy); err != nil {
			return nil, err
		}
		return &proxy, nil
	} else {
		if err := storage.NewInfluxdbProxyStorageQuerySet(db).IsDefaultEq(true).One(&proxy); err != nil {
			return nil, err
		}
		return &proxy, nil
	}
}
