// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"fmt"
	"strings"

	"github.com/jinzhu/gorm"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
)

//go:generate goqueryset -in influxdbstorage.go -out qs_influxdbstorage_gen.go

// InfluxdbStorage influxdb storage model
// gen:qs
type InfluxdbStorage struct {
	BkTenantId                string `gorm:"column:bk_tenant_id;size:256" json:"bk_tenant_id"`
	TableID                   string `json:"table_id" gorm:"primary_key;size:128"`
	StorageClusterID          uint   `gorm:"storage_cluster_id" json:"storage_cluster_id"`
	RealTableName             string `gorm:"size:128" json:"real_table_id"`
	Database                  string `gorm:"size:128" json:"database"`
	SourceDurationTime        string `gorm:"size:32" json:"source_duration_time"`
	DownSampleTable           string `gorm:"size:128" json:"down_sample_table"`
	DownSampleGap             string `gorm:"size:32" json:"down_sample_gap"`
	DownSampleDurationTime    string `gorm:"size:32" json:"down_sample_duration_time"`
	ProxyClusterName          string `gorm:"size:128" json:"proxy_cluster_name"`
	UseDefaultRp              bool   `gorm:"column:use_default_rp" json:"use_default_rp"`
	EnableRefreshRp           bool   `gorm:"column:enable_refresh_rp" json:"enable_refresh_rp"`
	PartitionTag              string `gorm:"size:128" json:"partition_tag"`
	VmTableId                 string `gorm:"vm_table_id;size:128" json:"vm_table_id"`
	InfluxdbProxyStorageId    uint   `gorm:"influxdb_proxy_storage_id" json:"influxdb_proxy_storage_id"`
	influxdbProxyStorageCache *InfluxdbProxyStorage
	storageClusterCache       *ClusterInfo
}

// TableName 用于设置表的别名
func (InfluxdbStorage) TableName() string {
	return "metadata_influxdbstorage"
}

func (i *InfluxdbStorage) BeforeCreate(tx *gorm.DB) error {
	if i.ProxyClusterName == "" {
		i.ProxyClusterName = "default"
	}
	return nil
}

// RpName 该结果表的rp名字
func (i InfluxdbStorage) RpName() string {
	if i.UseDefaultRp {
		return ""
	}
	return fmt.Sprintf("bkmonitor_rp_%s", i.TableID)
}

// InfluxdbProxyStorage 获取该结果表的proxyStorage对象
func (i InfluxdbStorage) InfluxdbProxyStorage() (*InfluxdbProxyStorage, error) {
	if i.influxdbProxyStorageCache != nil && i.influxdbProxyStorageCache.ID == i.InfluxdbProxyStorageId {
		return i.influxdbProxyStorageCache, nil
	}
	dbSession := mysql.GetDBSession()
	var influxdbProxyStorage InfluxdbProxyStorage
	err := NewInfluxdbProxyStorageQuerySet(dbSession.DB).IDEq(i.InfluxdbProxyStorageId).One(&influxdbProxyStorage)
	if err != nil {
		return nil, errors.Wrapf(err, "query InfluxdbProxyStorage with id [%v] failed", i.InfluxdbProxyStorageId)
	}
	i.influxdbProxyStorageCache = &influxdbProxyStorage
	return &influxdbProxyStorage, nil
}

// StorageCluster 获取该结果表的clusterInfo对象
func (i InfluxdbStorage) StorageCluster() (*ClusterInfo, error) {
	if i.storageClusterCache != nil {
		return i.storageClusterCache, nil
	}
	proxyStorage, err := i.InfluxdbProxyStorage()
	if err != nil {
		return nil, err
	}
	dbSession := mysql.GetDBSession()
	var clusterInfo ClusterInfo
	err = NewClusterInfoQuerySet(dbSession.DB).ClusterIDEq(proxyStorage.ProxyClusterId).One(&clusterInfo)
	if err != nil {
		return nil, err
	}
	i.storageClusterCache = &clusterInfo
	return &clusterInfo, nil
}

// ConsulClusterConfig 获取集群配置信息
func (i InfluxdbStorage) ConsulClusterConfig() (map[string]any, error) {
	proxyStorage, err := i.InfluxdbProxyStorage()
	if err != nil {
		return nil, err
	}
	config := map[string]any{
		"cluster": proxyStorage.InstanceClusterName,
	}
	if i.PartitionTag != "" {
		config["partition_tag"] = strings.Split(i.PartitionTag, ",")
	}
	return config, nil
}
