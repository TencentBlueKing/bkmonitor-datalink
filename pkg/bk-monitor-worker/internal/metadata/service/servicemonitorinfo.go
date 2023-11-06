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
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/bcs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// ServiceMonitorInfoSvc service monitor info service
type ServiceMonitorInfoSvc struct {
	*bcs.ServiceMonitorInfo
}

func NewServiceMonitorInfoSvc(obj *bcs.ServiceMonitorInfo) ServiceMonitorInfoSvc {
	return ServiceMonitorInfoSvc{
		ServiceMonitorInfo: obj,
	}
}

// RefreshResource 刷新集群资源信息，追加未发现的资源,删除已不存在的资源
func (ServiceMonitorInfoSvc) RefreshResource(clusterSvc *BcsClusterInfoSvc, bkDataId uint) error {
	// 获取所有命名空间下的本资源信息
	resp, err := clusterSvc.ListK8sResource(models.BcsMonitorResourceGroupName, models.BcsMonitorResourceVersion, models.BcsServiceMonitorResourcePlural)
	if err != nil {
		return err
	}
	logger.Infof("cluster [%s] got resource [%s] total [%v]", clusterSvc.ClusterID, models.BcsServiceMonitorResourcePlural, len(resp.Items))
	// k8s中的namespace_name
	var resourceNameList []string
	for _, res := range resp.Items {
		namespace := res.GetNamespace()
		name := res.GetName()
		resourceNameList = append(resourceNameList, fmt.Sprintf("%s_%s", namespace, name))
	}

	var existMonitorInfo []bcs.ServiceMonitorInfo
	if err := bcs.NewServiceMonitorInfoQuerySet(mysql.GetDBSession().DB).ClusterIDEq(clusterSvc.ClusterID).All(&existMonitorInfo); err != nil {
		return err
	}
	// db中存在的namespace_name
	var existMonitorName []string
	// db中namespace_name与id的映射
	var existNameIdMap = make(map[string]uint)
	for _, info := range existMonitorInfo {
		existMonitorName = append(existMonitorName, fmt.Sprintf("%s_%s", info.Namespace, info.Name))
		existNameIdMap[fmt.Sprintf("%s_%s", info.Namespace, info.Name)] = info.Id
	}
	// 遍历所有的资源信息，未注册的进行注册
	for _, identifyName := range resourceNameList {
		var exist = false
		for _, existName := range existMonitorName {
			if identifyName == existName {
				// 已经存在，继续下一个
				exist = true
				logger.Infof("cluster [%s] resource [%s] [%s] is already exists, nothing will do.", clusterSvc.ClusterID, models.BcsServiceMonitorResourcePlural, identifyName)
				break
			}
		}
		if exist {
			continue
		}
		// 不存在则创建记录
		splits := strings.Split(identifyName, "_")
		namespace := splits[0]
		name := splits[1]
		serviceMonitor := bcs.ServiceMonitorInfo{
			BCSResource: bcs.BCSResource{
				ClusterID:          clusterSvc.ClusterID,
				Namespace:          namespace,
				Name:               name,
				BkDataId:           bkDataId,
				IsCustomResource:   true,
				IsCommonDataId:     true,
				RecordCreateTime:   time.Now(),
				ResourceCreateTime: time.Now(),
			},
		}
		if err := serviceMonitor.Create(mysql.GetDBSession().DB); err != nil {
			return err
		}
		// 新增的记录起来
		existNameIdMap[identifyName] = serviceMonitor.Id
		logger.Infof("cluster [%s] now create resource [%s] name [%s] under namespace [%s] with data_id [%v] success", clusterSvc.ClusterID, models.BcsServiceMonitorResourcePlural, name, namespace, bkDataId)
	}
	var needDeleteIdList []uint
	var needDeleteNameList []string
	for name, recordId := range existNameIdMap {
		var needDelete = true
		for _, resourceName := range resourceNameList {
			// k8s中存在及db中存在则继续下一个
			if name == resourceName {
				needDelete = false
				break
			}
		}
		if needDelete {
			// db中存在而k8s中不存在，需要删除已经不存在的resource映射
			needDeleteIdList = append(needDeleteIdList, recordId)
			needDeleteNameList = append(needDeleteNameList, name)
		}
	}
	// 删除已经不存在的resource映射
	if len(needDeleteIdList) != 0 {
		if err := mysql.GetDBSession().DB.Delete(&bcs.ServiceMonitorInfo{}, "id in (?)", needDeleteIdList).Error; err != nil {
			return err
		}
		logger.Infof("cluster [%s] delete monitor info [%s] records [%s] success", clusterSvc.ClusterID, models.BcsServiceMonitorResourcePlural, strings.Join(needDeleteNameList, ","))
	}
	logger.Infof("cluster [%s] all resource [%s] update success.", clusterSvc.ClusterID, models.BcsServiceMonitorResourcePlural)
	return nil
}

// RefreshCustomResource 刷新自定义资源信息，追加部署的资源，更新未同步的资源
func (s ServiceMonitorInfoSvc) RefreshCustomResource(clusterSvc *BcsClusterInfoSvc) error {
	// 获取所有命名空间下的本资源信息
	resp, err := clusterSvc.ListK8sResource(models.BcsResourceGroupName, models.BcsResourceVersion, models.BcsResourceDataIdResourcePlural)
	if err != nil {
		return err
	}
	logger.Infof("cluster [%s] got resource [%s] total->[%s]", clusterSvc.ClusterID, models.BcsResourceDataIdResourcePlural, len(resp.Items))
	resourceMap := make(map[string]unstructured.Unstructured)
	for _, res := range resp.Items {
		resourceMap[res.GetName()] = res
	}

	var monitorList []bcs.ServiceMonitorInfo
	if err := bcs.NewServiceMonitorInfoQuerySet(mysql.GetDBSession().DB).ClusterIDEq(clusterSvc.ClusterID).All(&monitorList); err != nil {
		return err
	}
	for _, monitor := range monitorList {
		// 判断是否需要刷新独立的dataid resource
		need, err := ShouldRefreshOwnDataId(&monitor.BCSResource)
		if err != nil {
			return err
		}
		if !need {
			continue
		}
		// 检查k8s集群里是否已经存在对应resource
		configName, err := s.GetConfigName()
		if err != nil {
			return err
		}
		config, err := GetMonitorInfoConfig(configName, clusterSvc.ClusterID, s.Name, s.Namespace, models.BcsServiceMonitorResourcePlural, models.BcsServiceMonitorResourceUsage, s.BkDataId)
		if err != nil {
			return err
		}
		if _, ok := resourceMap[configName]; !ok {
			// 如果k8s_resource不存在，则增加
			if err := clusterSvc.ensureDataIdResource(configName, config); err != nil {
				return err
			}
			logger.Infof("cluster [%s] add new resource [%#v]", clusterSvc.ClusterID, config.UnstructuredContent())
		} else {
			// 否则检查信息是否一致，不一致则更新
			res := resourceMap[configName]
			if !clusterSvc.isSameResourceConfig(config.UnstructuredContent(), res.UnstructuredContent()) {
				if err := clusterSvc.ensureDataIdResource(configName, config); err != nil {
					return err
				}
				logger.Infof("cluster [%s] update resource [%#v]", clusterSvc.ClusterID, config.UnstructuredContent())
			}
		}
	}
	return nil
}

// GetConfigName 构造config name
func (s ServiceMonitorInfoSvc) GetConfigName() (string, error) {
	var prefix string
	if s.IsCommonDataId {
		prefix = "common"
	} else {
		prefix = "custom"
	}
	var end string
	if s.IsCustomResource {
		end = "custom"
	} else {
		end = "system"
	}

	bkEnvLabel, err := s.GetBkEnvLabel()
	if err != nil {
		return "", err
	}
	if bkEnvLabel != "" {
		return fmt.Sprintf("%s-%s-%s-%s-%s", bkEnvLabel, prefix, models.BcsServiceMonitorResourceUsage, s.Name, end), nil
	}
	return fmt.Sprintf("%s-%s-%s-%s", prefix, models.BcsServiceMonitorResourceUsage, s.Name, end), nil
}

func (s ServiceMonitorInfoSvc) GetBkEnvLabel() (string, error) {
	var cluster bcs.BCSClusterInfo
	if err := bcs.NewBCSClusterInfoQuerySet(mysql.GetDBSession().DB).ClusterIDGt(s.ClusterID).One(&cluster); err != nil {
		return "", err
	}
	if cluster.BkEnv != nil && *cluster.BkEnv != "" {
		return *cluster.BkEnv, nil
	}
	return viper.GetString(BcsClusterBkEnvLabelPath), nil
}

// GetMonitorInfoConfig 构造bcs monitor info的config
func GetMonitorInfoConfig(configName, clusterId, resourceName, resourceNamespace, resourceType, usage string, bkDataId uint) (*unstructured.Unstructured, error) {
	rcSvc := NewReplaceConfigSvc(nil)
	// 获取全局replace配置
	replaceConfig, err := rcSvc.GetCommonReplaceConfig()
	if err != nil {
		return nil, err
	}
	// 获取集群层级replace配置
	clusterReplaceConfig, err := rcSvc.GetClusterReplaceConfig(clusterId)
	if err != nil {
		return nil, err
	}
	// 获取resource层级replace配置
	customReplaceConfig, err := rcSvc.GetResourceReplaceConfig(clusterId, resourceName, resourceNamespace, resourceType)
	if err != nil {
		return nil, err
	}
	// 将replace配置逐层覆盖
	for k, v := range clusterReplaceConfig[models.ReplaceTypesMetric] {
		replaceConfig[models.ReplaceTypesMetric][k] = v
	}
	for k, v := range clusterReplaceConfig[models.ReplaceTypesDimension] {
		replaceConfig[models.ReplaceTypesDimension][k] = v
	}
	for k, v := range customReplaceConfig[models.ReplaceTypesMetric] {
		replaceConfig[models.ReplaceTypesMetric][k] = v
	}
	for k, v := range customReplaceConfig[models.ReplaceTypesDimension] {
		replaceConfig[models.ReplaceTypesDimension][k] = v
	}
	var cluster bcs.BCSClusterInfo
	if err := bcs.NewBCSClusterInfoQuerySet(mysql.GetDBSession().DB).ClusterIDEq(clusterId).One(&cluster); err != nil {
		return nil, err
	}
	clusterSvc := NewBcsClusterInfoSvc(&cluster)
	if err != nil {
		return nil, err
	}
	labels := map[string]interface{}{
		"usage": usage, "isCommon": "false", "isSystem": "false",
	}
	result := map[string]interface{}{
		"apiVersion": fmt.Sprintf("%s/%s", models.BcsResourceGroupName, models.BcsResourceVersion),
		"kind":       models.BcsResourceDataIdResourceKind,
		"metadata": map[string]interface{}{
			"name":   clusterSvc.composeDataidResourceName(configName),
			"labels": clusterSvc.composeDataidResourceLabel(labels)},
		"spec": map[string]interface{}{
			"dataID": int64(bkDataId),
			"labels": map[string]string{
				"bcs_cluster_id": clusterId,
				"bk_biz_id":      strconv.Itoa(clusterSvc.BkBizId),
			},
			"metricReplace":    replaceConfig[models.ReplaceTypesMetric],
			"dimensionReplace": replaceConfig[models.ReplaceTypesDimension],
			// 追加一个指定的资源对应描述
			"monitorResource": map[string]interface{}{"namespace": resourceNamespace, "kind": resourceType, "name": resourceName},
		},
	}

	return &unstructured.Unstructured{Object: result}, nil
}

// ShouldRefreshOwnDataId 判断该resource是否应该向k8s刷新只属于自己的dataid resource
func ShouldRefreshOwnDataId(monitor *bcs.BCSResource) (bool, error) {
	// 该resource使用的非公共dataid，则需要刷新
	if !monitor.IsCommonDataId {
		return true, nil
	}
	// 该resource存在配置，则需要刷新
	count, err := bcs.NewReplaceConfigQuerySet(mysql.GetDBSession().DB).IsCommonEq(true).CustomLevelEq(models.ReplaceCustomLevelsResource).
		ClusterIdEq(monitor.ClusterID).ResourceNameEq(monitor.Name).ResourceNamespaceEq(monitor.Namespace).
		ResourceTypeEq(models.BcsResourceDataIdResourcePlural).Count()
	if err != nil {
		return false, err
	}
	if count != 0 {
		return true, nil
	}
	return false, nil
}
