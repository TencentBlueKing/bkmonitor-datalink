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
	"strings"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/timex"
)

// EsStorageSvc es storage service
type EsStorageSvc struct {
	*storage.ESStorage
}

func NewEsStorageSvc(obj *storage.ESStorage) EsStorageSvc {
	return EsStorageSvc{
		ESStorage: obj,
	}
}

// StorageCluster 返回集群对象
func (e EsStorageSvc) StorageCluster() (*storage.ClusterInfo, error) {
	var clusterInfo storage.ClusterInfo
	if err := storage.NewClusterInfoQuerySet(mysql.GetDBSession().DB).ClusterIDEq(e.StorageClusterID).One(&clusterInfo); err != nil {
		return nil, err
	}
	return &clusterInfo, nil
}

// ConsulConfig 获取es storage的consul配置信息
func (e EsStorageSvc) ConsulConfig() (*StorageConsulConfig, error) {
	// 集群信息
	clusterInfo, err := e.StorageCluster()
	if err != nil {
		return nil, err
	}
	clusterConsulConfig, err := NewClusterInfoSvc(clusterInfo).ConsulConfig()
	if err != nil {
		return nil, err
	}
	// es的consul配置
	var indexSettingsMap map[string]any
	var mappingSettingMap map[string]any
	var WarmPhaseSettingsMap map[string]any
	err = jsonx.UnmarshalString(e.IndexSettings, &indexSettingsMap)
	if err != nil {
		return nil, errors.Wrapf(err, "unmarshal IndexSettings failed")
	}
	err = jsonx.UnmarshalString(e.MappingSettings, &mappingSettingMap)
	if err != nil {
		return nil, errors.Wrapf(err, "unmarshal MappingSettings failed")
	}
	err = jsonx.UnmarshalString(e.WarmPhaseSettings, &WarmPhaseSettingsMap)
	if err != nil {
		return nil, errors.Wrapf(err, "unmarshal WarmPhaseSettings failed")
	}
	consulConfig := &StorageConsulConfig{
		ClusterInfoConsulConfig: clusterConsulConfig,
		StorageConfig: map[string]any{
			"index_datetime_format":   fmt.Sprintf("write_%s", timex.ParsePyDateFormat(e.DateFormat)),
			"index_datetime_timezone": e.TimeZone,
			"date_format":             e.DateFormat,
			"slice_size":              e.SliceSize,
			"slice_gap":               e.SliceGap,
			"retention":               e.Retention,
			"warm_phase_days":         e.WarmPhaseDays,
			"warm_phase_settings":     WarmPhaseSettingsMap,
			"base_index":              strings.ReplaceAll(e.TableID, ".", "_"),
			"index_settings":          indexSettingsMap,
			"mapping_settings":        mappingSettingMap,
		},
	}

	return consulConfig, nil
}
