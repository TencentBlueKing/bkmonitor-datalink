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
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/jinzhu/gorm"
	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/hashconsul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

//go:generate goqueryset -in influxdbstorage.go -out qs_influxdbstorage_gen.go

// InfluxdbStorage influxdb storage model
// gen:qs
type InfluxdbStorage struct {
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

// ConsulPath 获取router的consul根路径
func (InfluxdbStorage) ConsulPath() string {
	return fmt.Sprintf(models.InfluxdbStorageConsulPathTemplate, cfg.StorageConsulPathPrefix, cfg.BypassSuffixPath)
}

// ConsulConfigPath 获取具体结果表router的consul配置路径
func (i InfluxdbStorage) ConsulConfigPath() string {
	return fmt.Sprintf("%s/%s/%s", i.ConsulPath(), i.Database, i.RealTableName)
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
func (i InfluxdbStorage) ConsulClusterConfig() (map[string]interface{}, error) {
	proxyStorage, err := i.InfluxdbProxyStorage()
	if err != nil {
		return nil, err
	}
	config := map[string]interface{}{
		"cluster": proxyStorage.InstanceClusterName,
	}
	if i.PartitionTag != "" {
		config["partition_tag"] = strings.Split(i.PartitionTag, ",")
	}
	return config, nil
}

// PushRedisData 路由存储关系同步写入到 redis 里面
func (i InfluxdbStorage) PushRedisData(ctx context.Context, isPublish bool) error {
	logger.Infof("PushRedisData: push storage relation to redis, table_id->[%s],is_pubish->[%v]", i.TableID, isPublish)
	// 通过 AccessVMRecord 获取结果表 ID
	var vmTableId string
	var vmRecord AccessVMRecord
	dbSession := mysql.GetDBSession()
	err := NewAccessVMRecordQuerySet(dbSession.DB).ResultTableIdEq(i.TableID).One(&vmRecord)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warnf("table_id: %s not access vm", i.TableID)
		} else {
			return err
		}
	}
	vmTableId = vmRecord.VmResultTableId
	var partitionTags = make([]string, 0)
	if i.PartitionTag != "" {
		partitionTags = strings.Split(i.PartitionTag, ",")
	}

	proxyStorage, err := i.InfluxdbProxyStorage()
	if err != nil {
		return err
	}
	valMap := map[string]interface{}{
		"storageID":   strconv.Itoa(int(proxyStorage.ProxyClusterId)),
		"clusterName": proxyStorage.InstanceClusterName,
		"tagsKey":     partitionTags,
		"db":          i.Database,
		"vm_rt":       vmTableId,
		"measurement": i.RealTableName,
		"retention_policies": map[string]interface{}{
			"autogen": map[string]interface{}{
				"is_default": true,
				"resolution": 0,
			},
		},
	}
	val, err := jsonx.MarshalString(valMap)
	if err != nil {
		return err
	}
	models.PushToRedis(ctx, models.InfluxdbProxyStorageRouterKey, i.TableID, val)
	return nil
}

// RefreshConsulClusterConfig 更新influxDB结果表信息到consul中
func (i InfluxdbStorage) RefreshConsulClusterConfig(ctx context.Context, isPublish bool, isVersionRefresh bool) error {
	consulClient, err := consul.GetInstance()
	if err != nil {
		return err
	}
	config, err := i.ConsulClusterConfig()
	if err != nil {
		return err
	}
	val, err := jsonx.MarshalString(config)
	if err != nil {
		return err
	}
	err = hashconsul.PutCas(consulClient, i.ConsulConfigPath(), val, 0, nil)
	if err != nil {
		logger.Errorf("put consul path [%s] value [%s] err, %v", i.ConsulConfigPath(), val, err)
		return err
	}
	err = i.PushRedisData(ctx, isPublish)
	if err != nil {
		return err
	}
	if isVersionRefresh {
		if err := models.RefreshRouterVersion(ctx, fmt.Sprintf(models.InfluxdbInfoVersionConsulPathTemplate, cfg.StorageConsulPathPrefix, cfg.BypassSuffixPath)); err != nil {
			return err
		}
	}
	return nil
}

// RefreshInfluxdbStorageConsulClusterConfig 更新influxDB结果表信息到consul中
func RefreshInfluxdbStorageConsulClusterConfig(ctx context.Context, objs *[]InfluxdbStorage, goroutineLimit int) {
	wg := &sync.WaitGroup{}
	ch := make(chan bool, goroutineLimit)
	wg.Add(len(*objs) - 1)
	for i, influxdbStorage := range *objs {
		if i == len(*objs)-1 {
			// 最后一个收尾单独处理进行publish
			continue
		}
		ch <- true
		go func(s InfluxdbStorage, wg *sync.WaitGroup, ch chan bool) {
			defer func() {
				<-ch
				wg.Done()
			}()
			err := s.RefreshConsulClusterConfig(ctx, false, false)
			if err != nil {
				logger.Errorf("result_table: [%s] try to refresh consul config failed, %v", s.TableID, err)
			} else {
				logger.Infof("result_table: [%s] refresh consul config success", s.TableID)
			}
		}(influxdbStorage, wg, ch)
	}
	wg.Wait()
	// 最后一个进行publish
	last := (*objs)[len(*objs)-1]
	err := last.RefreshConsulClusterConfig(ctx, true, false)
	if err != nil {
		logger.Errorf("result_table: [%s] try to refresh consul config failed, %v", last.TableID, err)
	} else {
		logger.Infof("result_table: [%s] refresh consul config success", last.TableID)
	}

}
