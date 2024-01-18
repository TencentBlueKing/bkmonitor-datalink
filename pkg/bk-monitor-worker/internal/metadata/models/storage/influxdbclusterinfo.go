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
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/hashconsul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

//go:generate goqueryset -in influxdbclusterinfo.go -out qs_influxdbclusterinfo_gen.go

// InfluxdbClusterInfo influxdb cluster info model
// gen:qs
type InfluxdbClusterInfo struct {
	HostName     string `gorm:"size:128" json:"host_name"`
	ClusterName  string `gorm:"size:128" json:"cluster_name"`
	HostReadable bool   `gorm:"column:host_readable" json:"host_readable"`
}

// TableName 用于设置表的别名
func (InfluxdbClusterInfo) TableName() string {
	return "metadata_influxdbclusterinfo"
}

// ConsulPath 获取cluster_info的consul根路径
func (InfluxdbClusterInfo) ConsulPath() string {
	return fmt.Sprintf(models.InfluxdbClusterInfoConsulPathTemplate, config.StorageConsulPathPrefix, config.BypassSuffixPath)
}

// RefreshInfluxdbClusterInfoConsulClusterConfig 更新influxDB集群信息到Consul中
func RefreshInfluxdbClusterInfoConsulClusterConfig(ctx context.Context, objs *[]InfluxdbClusterInfo, goroutineLimit int) {
	refreshMap := make(map[string][]InfluxdbClusterInfo)
	// 按照clusterName分组处理
	for _, clusterInfo := range *objs {
		clusterList, ok := refreshMap[clusterInfo.ClusterName]
		if ok {
			clusterList = append(clusterList, clusterInfo)
		} else {
			clusterList = []InfluxdbClusterInfo{clusterInfo}
		}
		refreshMap[clusterInfo.ClusterName] = clusterList
	}

	wg := &sync.WaitGroup{}
	ch := make(chan bool, goroutineLimit)
	wg.Add(len(refreshMap))
	for clusterName, clusterInfoList := range refreshMap {
		ch <- true
		go func(clusterName string, clusterInfoList *[]InfluxdbClusterInfo, wg *sync.WaitGroup, ch chan bool) {
			defer func() {
				<-ch
				wg.Done()
			}()

			err := func() error {
				consulConfigPath := fmt.Sprintf("%s/%s", InfluxdbClusterInfo{}.ConsulPath(), clusterName)
				var hostNameList = make([]string, 0)
				var unreadableHostList = make([]string, 0)
				for _, clusterInfo := range *clusterInfoList {
					hostNameList = append(hostNameList, clusterInfo.HostName)
					if !clusterInfo.HostReadable {
						unreadableHostList = append(unreadableHostList, clusterInfo.HostName)
					}
				}
				var valMap = map[string][]string{
					"host_list":            hostNameList,
					"unreadable_host_list": unreadableHostList,
				}
				val, err := jsonx.MarshalString(valMap)
				if err != nil {
					return err
				}
				consulClient, err := consul.GetInstance()
				if err != nil {
					return err
				}
				err = hashconsul.Put(consulClient, consulConfigPath, val)
				if err != nil {
					logger.Errorf("consul path [%s] refresh with value [%s] failed, %v", consulConfigPath, val, err)
					return err
				}
				logger.Infof("consul path [%s] is refresh with value [%s] success", consulConfigPath, val)
				models.PushToRedis(ctx, models.InfluxdbClusterInfoKey, clusterName, val, true)
				return nil
			}()

			if err != nil {
				logger.Errorf("cluster: [%v] try to refresh consul config failed, %v", clusterName, err)
			} else {
				logger.Infof("cluster: [%v] refresh consul config success", clusterName)
			}
		}(clusterName, &clusterInfoList, wg, ch)
	}
	wg.Wait()
}
