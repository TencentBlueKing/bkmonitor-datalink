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
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jinzhu/gorm"
	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/hashconsul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/stringx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/timex"
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

func (e *InfluxdbStorage) BeforeCreate(tx *gorm.DB) error {
	if e.ProxyClusterName == "" {
		e.ProxyClusterName = "default"
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
	models.PushToRedis(ctx, models.InfluxdbProxyStorageRouterKey, i.TableID, val, isPublish)
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
	err = hashconsul.Put(consulClient, i.ConsulConfigPath(), val)
	if err != nil {
		logger.Errorf("put consul path [%s] value [%s] err, %v", i.ConsulConfigPath(), val, err)
		return err
	}
	err = i.PushRedisData(ctx, isPublish)
	if err != nil {
		return err
	}
	if isVersionRefresh {
		if err := models.RefreshRouterVersion(ctx, fmt.Sprintf("%s/metadata/influxdb_info/version/", cfg.StorageConsulPathPrefix)); err != nil {
			return err
		}
	}
	return nil
}

// EnsureOuterDependence 更新结果表外部的依赖信息
func (i InfluxdbStorage) EnsureOuterDependence() error {
	// 确认数据库已经创建
	err := i.CreateDatabase()
	if err != nil {
		return err
	}
	// 确保存在可用的清理策略
	err = i.EnsureRp()
	if err != nil {
		return err
	}
	return nil
}

// CreateDatabase 创建一个配置记录对应的数据库内容
func (i InfluxdbStorage) CreateDatabase() error {
	storageCluster, err := i.StorageCluster()
	if err != nil {
		return err
	}
	if stringx.StringInSlice(storageCluster.ClusterType, IgnoredStorageClusterTypes) {
		logger.Infof("cluster: %v is victoria_metrics type, not supported create database api", storageCluster.ClusterID)
		return nil
	}
	proxyStorage, err := i.InfluxdbProxyStorage()
	if err != nil {
		return err
	}

	// 1. 数据库的创建
	client := &http.Client{
		Timeout: 5 * time.Second,
	}
	target, err := url.Parse(fmt.Sprintf("http://%s:%v/create_database", storageCluster.DomainName, storageCluster.Port))
	if err != nil {
		return err
	}
	params := url.Values{}
	params.Set("q", fmt.Sprintf(`CREATE DATABASE "%s"`, i.Database))
	params.Set("db", i.Database)
	params.Set("cluster", proxyStorage.InstanceClusterName)
	target.RawQuery = params.Encode()
	req, err := http.NewRequest(http.MethodPost, target.String(), nil)
	req.Header.Set("Content-type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		logger.Errorf(
			"failed to create database [%s] for status [%v]",
			i.Database, resp.StatusCode,
		)
		return errors.New("create database failed")
	}
	logger.Infof("database [%s] is create on host [%s:%v]", i.Database, storageCluster.DomainName, storageCluster.Port)
	return nil
}

// EnsureRp 确保数据库存在该存储的独立RP策略
func (i InfluxdbStorage) EnsureRp() error {
	if i.UseDefaultRp {
		logger.Infof("table [%s] use default rp, nothing will refresh for it", i.TableID)
		return nil
	}
	if !i.EnableRefreshRp {
		logger.Infof("table [%s] disabled refresh rp, nothing will refresh for it", i.TableID)
		return nil
	}
	// 需要在相关的所有机器上，遍历判断RP是否正确配置了
	proxyStorage, err := i.InfluxdbProxyStorage()
	if err != nil {
		return err
	}
	dbSession := mysql.GetDBSession()
	var clusterInfoList []InfluxdbClusterInfo
	err = NewInfluxdbClusterInfoQuerySet(dbSession.DB).ClusterNameEq(proxyStorage.InstanceClusterName).All(&clusterInfoList)
	if err != nil {
		return err
	}
	for _, clusterInfo := range clusterInfoList {
		err := func() error {
			// 获取当次集群机器的信息
			var hostInfo InfluxdbHostInfo
			err := NewInfluxdbHostInfoQuerySet(dbSession.DB).HostNameEq(clusterInfo.HostName).One(&hostInfo)
			if err != nil {
				return err
			}
			influxdbClient, err := influxdb.GetClient(
				fmt.Sprintf("http://%s:%v", hostInfo.DomainName, hostInfo.Port), hostInfo.Username, hostInfo.Password, 5,
			)
			if err != nil {
				return err
			}
			defer influxdbClient.Close()

			results, err := influxdb.QueryDB(influxdbClient, "SHOW RETENTION POLICIES", i.Database, nil)
			if err != nil {
				return err
			}
			var needCreate = true
			result := results[0]
			if result.Err != "" {
				return errors.New(result.Err)
			}
			rpInfoList := influxdb.ParseResult(result)
			for _, rpInfo := range rpInfoList {
				duration := rpInfo["duration"].(string)
				name := rpInfo["name"].(string)
				if name != i.RpName() {
					continue
				}
				// 已经存在配置则不需要再创建
				needCreate = false
				// 判断duration是否一致
				queryDuration, err := timex.ParseDuration(duration)
				if err != nil {
					return err
				}
				dbDuration, err := timex.ParseDuration(i.SourceDurationTime)
				if err != nil {
					return err
				}
				// duration与配置的一致，直接跳过处理
				if queryDuration == dbDuration {
					logger.Infof(
						"table [%s] rp [%s | %s] check fine on host [%s]",
						i.TableID, i.RpName(), i.SourceDurationTime, hostInfo.DomainName,
					)
					break
				}
				// 此处发现rp配置不一致，需要修复
				// 修复前根据新的duration判断shard的长度，并修改为合适的shard
				shardGroupDuration, err := JudgeShardByDuration("inf")
				if err != nil {
					logger.Errorf(
						"table->[%s] rp->[%s | %s] is updated on host->[%s] failed: [%v]",
						i.TableID, i.RpName(), i.SourceDurationTime, hostInfo.DomainName, err,
					)
					break
				}
				_, err = influxdb.QueryDB(
					influxdbClient,
					fmt.Sprintf(`ALTER RETENTION POLICY "%s" ON %s DURATION %s SHARD DURATION %s`, i.RpName(), i.Database, i.SourceDurationTime, shardGroupDuration),
					i.Database,
					nil,
				)
				if err != nil {
					logger.Errorf(
						"table [%s] rp [%s | %s | %s] updated on host [%s] failed: [%v]",
						i.TableID, i.RpName(), i.SourceDurationTime, shardGroupDuration, hostInfo.DomainName, err,
					)
				} else {
					logger.Infof(
						"table [%s] rp [%s | %s | %s] is updated on host [%s]",
						i.TableID, i.RpName(), i.SourceDurationTime, shardGroupDuration, hostInfo.DomainName,
					)
				}
				break
			}
			if !needCreate {
				return nil
			}
			// 如果没有找到, 那么需要创建一个RP
			shardGroupDuration, err := JudgeShardByDuration("inf")
			if err != nil {
				return err
			}
			_, err = influxdb.QueryDB(
				influxdbClient,
				fmt.Sprintf(`CREATE RETENTION POLICY "%s" ON %s DURATION %s REPLICATION %v SHARD DURATION %s`, i.RpName(), i.Database, i.SourceDurationTime, 1, shardGroupDuration),
				i.Database,
				nil,
			)
			if err != nil {
				logger.Errorf(
					"table [%s] rp [%s | %s | %s] is create on host [%s] failed: [%s]",
					i.TableID, i.RpName(), i.SourceDurationTime, shardGroupDuration, hostInfo.DomainName, err,
				)
				return err
			}
			logger.Infof(
				"table [%s] rp [%s | %s | %s] is create on host [%s]",
				i.TableID, i.RpName(), i.SourceDurationTime, shardGroupDuration, hostInfo.DomainName,
			)
			return nil
		}()
		if err != nil {
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

// RefreshInfluxDBStorageOuterDependence 更新TS结果表外部的依赖信息
func RefreshInfluxDBStorageOuterDependence(ctx context.Context, objs *[]InfluxdbStorage, goroutineLimit int) {
	wg := &sync.WaitGroup{}
	ch := make(chan bool, goroutineLimit)
	wg.Add(len(*objs))
	for _, influxdbStorage := range *objs {
		ch <- true
		go func(s InfluxdbStorage, wg *sync.WaitGroup, ch chan bool) {
			defer func() {
				<-ch
				wg.Done()
			}()
			err := s.EnsureOuterDependence()
			if err != nil {
				logger.Errorf("result_table: [%s] try to sync database failed, %v", s.TableID, err)
			} else {
				logger.Infof("result_table: [%s] sync database success", s.TableID)
			}
		}(influxdbStorage, wg, ch)
	}
	wg.Wait()

}
