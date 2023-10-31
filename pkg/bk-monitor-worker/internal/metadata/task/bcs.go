// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package task

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jinzhu/gorm"
	"github.com/pkg/errors"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/bcs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	t "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// DiscoverBcsClusters 周期刷新bcs集群列表，将未注册进metadata的集群注册进来
func DiscoverBcsClusters(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("DiscoverBcsClusters Runtime panic caught: %v", err)
		}
	}()

	// 获取集群信息
	bcsClusterList, err := service.NewBcsClusterInfoSvc(nil).FetchK8sClusterList()
	if err != nil {
		return err
	}

	var clusterIdList []string
	wg := &sync.WaitGroup{}
	ch := make(chan bool, GetGoroutineLimit("discover_bcs_clusters"))
	wg.Add(len(bcsClusterList))
	for _, cluster := range bcsClusterList {
		clusterIdList = append(clusterIdList, cluster.ClusterId)
		ch <- true
		go func(cluster service.BcsClusterInfo, wg *sync.WaitGroup, ch chan bool) {
			defer func() {
				<-ch
				wg.Done()
			}()
			var bcsClusterInfo bcs.BCSClusterInfo
			if err := bcs.NewBCSClusterInfoQuerySet(mysql.GetDBSession().DB).
				ClusterIDEq(cluster.ClusterId).One(&bcsClusterInfo); err != nil {
				if !gorm.IsRecordNotFoundError(err) {
					logger.Errorf("query bcs cluster info record from db failed, %v", err)
					return
				}
			}
			if err != nil {
				// err为nil表示数据库中存在该集群，检查更新
				err := updateBcsCluster(cluster, &bcsClusterInfo)
				if err != nil {
					logger.Errorf("update bcs cluster %v failed, %v", cluster.BcsClusterId, err)
				}
				return
			} else {
				// 注册不存在的集群
				err := createBcsCluster(cluster)
				if err != nil {
					logger.Errorf("update bcs cluster %v failed, %v", cluster.BcsClusterId, err)
				}
			}
			return
		}(cluster, wg, ch)
	}
	wg.Wait()
	// 接口未返回的集群标记为删除状态
	if len(clusterIdList) != 0 {
		if err := bcs.NewBCSClusterInfoUpdater(mysql.GetDBSession().DB.Model(&bcs.BCSClusterInfo{}).Where("cluster_id not in (?)", clusterIdList)).
			SetStatus(models.BcsClusterStatusDeleted).SetLastModifyTime(time.Now()).Update(); err != nil {
			return err
		}
	}
	return nil

}

func createBcsCluster(cluster service.BcsClusterInfo) error {
	// 注册集群
	newCluster, err := service.NewBcsClusterInfoSvc(nil).RegisterCluster(cluster.BkBizId, cluster.ClusterId, cluster.ProjectId, "system")
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("register cluster %s failed, %s", cluster.ClusterId, err))
	}
	newBcsClusterInfoSvc := service.NewBcsClusterInfoSvc(newCluster)
	// 初始化资源resource信息
	err = newBcsClusterInfoSvc.InitResource()
	if err != nil {
		return err
	}
	logger.Infof("cluster_id [%s], project_id [%s], bk_biz_id [%v] registered", newCluster.ClusterID, newCluster.ProjectId, newCluster.BkBizId)
	// 更新云区域ID
	err = newBcsClusterInfoSvc.UpdateBcsClusterCloudIdConfig()
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("update bcs cluster cloud id failed, %s", err))
	}
	logger.Infof("cluster_id [%s], project_id [%s], bk_biz_id [%v] init resource finished", newCluster.ClusterID, newCluster.ProjectId, newCluster.BkBizId)
	return nil
}

func updateBcsCluster(cluster service.BcsClusterInfo, bcsClusterInfo *bcs.BCSClusterInfo) error {
	// 状态发生变化需要更新
	var runningStatus = "RUNNING"
	var updateFields []bcs.BCSClusterInfoDBSchemaField
	// 仅记录到 running 和 deleted 状态的集群数据，其中，非 running 的都设置为 deleted
	if cluster.Status == runningStatus {
		if bcsClusterInfo.Status != models.BcsClusterStatusRunning {
			bcsClusterInfo.Status = models.BcsClusterStatusRunning
			updateFields = append(updateFields, bcs.BCSClusterInfoDBSchema.Status)
		}
	} else if bcsClusterInfo.Status == models.BcsClusterStatusRunning {
		// 非 running 的都设置为 deleted
		bcsClusterInfo.Status = models.BcsClusterStatusDeleted
		updateFields = append(updateFields, bcs.BCSClusterInfoDBSchema.Status)
	}
	// 如果 BCS Token 变了需要刷新
	apiKeyContent := viper.GetString(api.BkApiBcsApiGatewayTokenPath)
	if bcsClusterInfo.ApiKeyContent != apiKeyContent {
		bcsClusterInfo.ApiKeyContent = apiKeyContent
		updateFields = append(updateFields, bcs.BCSClusterInfoDBSchema.ApiKeyContent)
	}
	// 进行更新操作
	if len(updateFields) != 0 {
		bcsClusterInfo.LastModifyTime = time.Now()
		bcsClusterInfo.LastModifyUser = "system"
		updateFields = append(updateFields, bcs.BCSClusterInfoDBSchema.LastModifyTime, bcs.BCSClusterInfoDBSchema.LastModifyUser)
		if err := bcsClusterInfo.Update(mysql.GetDBSession().DB, updateFields...); err != nil {
			return err
		}
	}
	if bcsClusterInfo.BkCloudId == nil {
		// 更新云区域ID
		if err := service.NewBcsClusterInfoSvc(bcsClusterInfo).UpdateBcsClusterCloudIdConfig(); err != nil {
			return errors.Wrap(err, fmt.Sprintf("update bk_cloud_id for cluster [%v] error, %s", bcsClusterInfo.ClusterID, err))
		}
	}
	logger.Infof("cluster_id [%s], project_id [%s] already exists, skip create", cluster.ClusterId, cluster.ProjectId)
	return nil
}

// RefreshBcsMonitorInfo 刷新monitor info信息
func RefreshBcsMonitorInfo(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("DiscoverBcsClusters Runtime panic caught: %v", err)
		}
	}()

	var bcsClusterInfoList []bcs.BCSClusterInfo
	if err := bcs.NewBCSClusterInfoQuerySet(mysql.GetDBSession().DB).StatusEq(models.BcsClusterStatusRunning).All(&bcsClusterInfoList); err != nil {
		return err
	}

	wg := &sync.WaitGroup{}
	ch := make(chan bool, GetGoroutineLimit("refresh_bcs_monitor_info"))
	wg.Add(len(bcsClusterInfoList))
	for _, cluster := range bcsClusterInfoList {
		ch <- true
		go func(cluster *bcs.BCSClusterInfo, wg *sync.WaitGroup, ch chan bool) {
			defer func() {
				<-ch
				wg.Done()
			}()
			clusterSvc := service.NewBcsClusterInfoSvc(cluster)
			// 刷新集群内置公共dataid resource
			if err := clusterSvc.RefreshCommonResource(); err != nil {
				logger.Errorf("refresh bcs common resource in cluster: %s failed, %v", cluster.ClusterID, err)
				return
			}
			logger.Infof("refresh bcs common resource in cluster: %s done", cluster.ClusterID)

			// 查找新的monitor info并记录到数据库，删除已不存在的
			// service monitor custom metric dataid
			if err := service.NewServiceMonitorInfoSvc(nil).RefreshResource(&clusterSvc, cluster.CustomMetricDataID); err != nil {
				logger.Errorf("refresh bcs service monitor resource in cluster: %s failed, %v", cluster.ClusterID, err)
				return
			}
			logger.Infof("refresh bcs service monitor resource in cluster: %s done", cluster.ClusterID)

			// pod monitor custom metric dataid
			if err := service.NewPodMonitorInfoSvc(nil).RefreshResource(&clusterSvc, cluster.CustomMetricDataID); err != nil {
				logger.Errorf("refresh bcs pod monitor resource in cluster: %s failed, %v", cluster.ClusterID, err)
				return
			}
			logger.Infof("refresh bcs pod monitor resource in cluster: %s done", cluster.ClusterID)

			// 刷新配置了自定义dataid的dataid resource
			// service monitor custom resource
			if err := service.NewServiceMonitorInfoSvc(nil).RefreshCustomResource(&clusterSvc); err != nil {
				logger.Errorf("refresh bcs service monitor custom resource in cluster: %s failed, %v", cluster.ClusterID, err)
				return
			}
			logger.Infof("refresh bcs service monitor custom resource in cluster: %s done", cluster.ClusterID)

			// pod monitor custom resource
			if err := service.NewPodMonitorInfoSvc(nil).RefreshCustomResource(&clusterSvc); err != nil {
				logger.Errorf("refresh bcs pod monitor custom resource in cluster: %s failed, %v", cluster.ClusterID, err)
				return
			}
			logger.Infof("refresh bcs pod monitor custom resource in cluster: %s done", cluster.ClusterID)

		}(&cluster, wg, ch)
	}
	wg.Wait()
	return nil

}
