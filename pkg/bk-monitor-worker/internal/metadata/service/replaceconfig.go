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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/bcs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
)

// ReplaceConfigSvc influxdb storage service
type ReplaceConfigSvc struct {
	*bcs.ReplaceConfig
}

func NewReplaceConfigSvc(obj *bcs.ReplaceConfig) ReplaceConfigSvc {
	return ReplaceConfigSvc{
		ReplaceConfig: obj,
	}
}

// GetReplaceConfig 构造ReplaceConfig配置
func (s ReplaceConfigSvc) GetReplaceConfig(items []bcs.ReplaceConfig) map[string]map[string]interface{} {
	var metricReplace map[string]interface{}
	var dimensionReplace map[string]interface{}
	for _, item := range items {
		if item.ReplaceType == models.ReplaceTypesMetric {
			metricReplace[item.SourceName] = item.TargetName
		} else {
			dimensionReplace[item.SourceName] = item.TargetName
		}
	}
	return map[string]map[string]interface{}{
		models.ReplaceTypesMetric:    metricReplace,
		models.ReplaceTypesDimension: dimensionReplace,
	}
}

// GetCommonReplaceConfig 构造CommonReplaceConfig配置
func (s ReplaceConfigSvc) GetCommonReplaceConfig() (map[string]map[string]interface{}, error) {
	var items []bcs.ReplaceConfig
	if err := bcs.NewReplaceConfigQuerySet(mysql.GetDBSession().DB).IsCommonEq(true).All(&items); err != nil {
		return nil, err
	}
	return s.GetReplaceConfig(items), nil
}

// GetClusterReplaceConfig 构造ClusterReplaceConfig配置
func (s ReplaceConfigSvc) GetClusterReplaceConfig(clusterId string) (map[string]map[string]interface{}, error) {
	var items []bcs.ReplaceConfig
	if err := bcs.NewReplaceConfigQuerySet(mysql.GetDBSession().DB).IsCommonEq(false).
		CustomLevelEq(models.ReplaceCustomLevelsCluster).ClusterIdEq(clusterId).All(&items); err != nil {
		return nil, err
	}
	return s.GetReplaceConfig(items), nil
}

// GetResourceReplaceConfig 构造ResourceReplaceConfig配置
func (s ReplaceConfigSvc) GetResourceReplaceConfig(clusterId, resourceName, resourceNamespace, resourceType string) (map[string]map[string]interface{}, error) {
	var items []bcs.ReplaceConfig
	if err := bcs.NewReplaceConfigQuerySet(mysql.GetDBSession().DB).IsCommonEq(false).ClusterIdEq(clusterId).CustomLevelEq(models.ReplaceCustomLevelsResource).
		ResourceNameEq(resourceName).ResourceNamespaceEq(resourceNamespace).ResourceTypeEq(resourceType).All(&items); err != nil {
		return nil, err
	}
	return s.GetReplaceConfig(items), nil
}
