// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

// compareHostList 对比两个列表，获取新增和删除列表
func (httpService *Service) compareHostList(oldList, newList []string) ([]string, []string) {
	addList := make([]string, 0)
	delList := make([]string, 0)
	// 获取删除的
	for _, oldBackend := range oldList {
		exist := false
		for _, newBackend := range newList {
			if newBackend == oldBackend {
				exist = true
				break
			}
		}
		if !exist {
			delList = append(delList, oldBackend)
		}

	}

	// 获取新增的
	for _, newBackend := range newList {
		exist := false
		for _, oldBackend := range oldList {
			if newBackend == oldBackend {
				exist = true
				break
			}
		}
		if !exist {
			addList = append(addList, newBackend)
		}

	}
	return addList, delList
}

// rebalanceByCluster 对当前cluster的已存在路由进行一次重新分配
func (httpService *Service) rebalanceByCluster(clusterName string, clusterInfo *consul.ClusterInfo) error {
	// 获取集群的全部主机
	backends := clusterInfo.HostList
	tags, err := consul.GetTagsInfo(clusterName)
	if err != nil {
		return err
	}
	// 遍历tags，逐个重新计算
	for tagKey, tagInfo := range tags {
		logger := logging.NewEntry(map[string]interface{}{
			"module":  moduleName,
			"cluster": clusterName,
			"tags":    tagKey,
		})
		logger.Info("handle tag start")
		// 如果该tag处于其他状态，则不允许进行处理，保持原状
		if tagInfo.Status != StatusReady {
			logger.Info("skip")
			continue
		}
		newTagInfo := new(consul.TagInfo)
		// 否则开始分析新旧数据
		oldBackends := tagInfo.HostList
		newBackends := common.GenerateBackendRoute(tagKey, backends, 2)
		addList, delList := httpService.compareHostList(oldBackends, newBackends)
		// 如果没变化则不用更新
		if len(addList) == 0 && len(delList) == 0 {
			logger.Info("no change,skip")
			continue
		}
		newTagInfo.HostList = oldBackends
		newTagInfo.UnreadableHost = addList
		newTagInfo.DeleteHostList = delList
		newTagInfo.Status = StatusChanged
		err := consul.ModifyTagInfo(clusterName, tagKey, tagInfo, newTagInfo)
		if err != nil {
			logger.Errorf("modify failed,error:%s", err)
			return err
		}
		// 通知proxy各cluster更新自身的内容
		err = consul.NotifyTagChanged(clusterName)
		if err != nil {
			logger.Errorf("notify failed,error:%s", err)
			return err
		}
		logger.Info("handle tag done")
	}
	return nil
}

// Rebalance 对全部tag数据重新均衡
func (httpService *Service) Rebalance() error {
	logger := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	logger.Info("rebalance start")
	// 这里要获取tag全局锁，rebalance的时候要保证不互相干扰
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// 获取临时session
	sessionID, err := consul.NewSession(ctx)
	success, err := consul.GetTagLock(sessionID)
	if err != nil {
		return err
	}
	// 占锁失败，直接退出
	if !success {
		return nil
	}
	defer consul.ReleaseTagLock(sessionID)
	clusterData, err := consul.GetAllClustersData()
	if err != nil {
		return err
	}
	for clusterName, clusterInfo := range clusterData {
		logger.Infof("rebalance cluster:%s", clusterName)
		err := httpService.rebalanceByCluster(clusterName, clusterInfo)
		if err != nil {
			logger.Infof("rebalance cluster:%s error:%s", clusterName, err)
			return err
		}
	}
	logger.Info("rebalance done")

	return nil
}
