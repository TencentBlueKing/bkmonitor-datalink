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
	"context"
	"errors"
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"strings"
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
	clusterConsulConfig := NewClusterInfoSvc(clusterInfo).ConsulConfig()
	// influxdb的consul配置
	consulConfig := &StorageConsulConfig{
		ClusterInfoConsulConfig: clusterConsulConfig,
		StorageConfig: map[string]interface{}{
			"real_table_name":       k.RealTableName,
			"database":              k.Database,
			"retention_policy_name": k.RpName(),
		},
	}
	return consulConfig, nil
}

// CreateTable 创建存储
func (k InfluxdbStorageSvc) CreateTable(tableId string, isSyncDb bool, storageConfig map[string]interface{}) error {
	influxdbProxyStorageId, _ := storageConfig["influxdb_proxy_storage_id"].(*uint)
	proxyClusterName, _ := storageConfig["proxy_cluster_name"].(*string)
	storageClusterId, _ := storageConfig["storage_cluster_id"].(*uint)

	influxdbStorage, err := NewInfluxdbProxyStorageSvc(nil).GetInfluxdbStorage(influxdbProxyStorageId, proxyClusterName, storageClusterId)
	if err != nil {
		return err
	}
	influxdbProxyStorageId = &influxdbStorage.ID
	proxyClusterName = &influxdbStorage.InstanceClusterName
	storageClusterId = &influxdbStorage.ProxyClusterId
	// 校验后端是否存在
	count, err := storage.NewInfluxdbClusterInfoQuerySet(mysql.GetDBSession().DB).ClusterNameEq(*proxyClusterName).Count()
	if err != nil {
		return err
	}
	if count == 0 {
		return errors.New(fmt.Sprintf("proxy_cluster [%s] has no config", *proxyClusterName))
	}
	// 如果未有指定对应的结果表及数据库，则从table_id中分割获取
	split := strings.Split(tableId, ".")
	database := split[0]
	realTableName := split[1]
	// InfluxDB不需要实际创建结果表，只需要创建一条DB记录即可
	UseDefaultRp, ok := storageConfig["use_default_rp"].(bool)
	if !ok {
		UseDefaultRp = true
	}
	EnableRefreshRp, _ := storageConfig["enable_refresh_rp"].(bool)
	if !ok {
		EnableRefreshRp = true
	}
	SourceDurationTime, ok := storageConfig["source_duration_time"].(string)
	if !ok {
		SourceDurationTime = "30d"
	}
	DownSampleTable, _ := storageConfig["down_sample_table"].(string)
	DownSampleGap, _ := storageConfig["down_sample_gap"].(string)
	DownSampleDurationTime, _ := storageConfig["down_sample_duration_time"].(string)
	PartitionTag, _ := storageConfig["partition_tag"].(string)
	VmTableId, _ := storageConfig["vm_table_id"].(string)

	influxdb := storage.InfluxdbStorage{
		TableID:                tableId,
		StorageClusterID:       *storageClusterId,
		RealTableName:          realTableName,
		Database:               database,
		ProxyClusterName:       *proxyClusterName,
		InfluxdbProxyStorageId: *influxdbProxyStorageId,
		UseDefaultRp:           UseDefaultRp,
		EnableRefreshRp:        EnableRefreshRp,
		SourceDurationTime:     SourceDurationTime,
		DownSampleTable:        DownSampleTable,
		DownSampleGap:          DownSampleGap,
		DownSampleDurationTime: DownSampleDurationTime,
		PartitionTag:           PartitionTag,
		VmTableId:              VmTableId,
	}
	if err := influxdb.Create(mysql.GetDBSession().DB); err != nil {
		return err
	}
	logger.Infof("result_table [%s] now has create influxDB storage", influxdb.TableID)
	if isSyncDb {
		if err := NewInfluxdbStorageSvc(&influxdb).syncDb(); err != nil {
			return err
		}
	}

	// 刷新一次结果表的路由信息至consul中, 由于是创建结果表，必须强行刷新到consul配置中
	if err := influxdb.RefreshConsulClusterConfig(context.Background(), true, true); err != nil {
		return err
	}
	logger.Infof("result_table [%s] all database create is done", influxdb.TableID)
	return nil
}

func (k InfluxdbStorageSvc) syncDb() error {
	return k.CreateDatabase()
}
