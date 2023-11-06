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
	"regexp"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/timex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
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
	clusterConsulConfig := NewClusterInfoSvc(clusterInfo).ConsulConfig()
	// es的consul配置
	var indexSettingsMap map[string]interface{}
	var mappingSettingMap map[string]interface{}
	err = jsonx.UnmarshalString(e.IndexSettings, &indexSettingsMap)
	if err != nil {
		return nil, err
	}
	err = jsonx.UnmarshalString(e.MappingSettings, &mappingSettingMap)
	if err != nil {
		return nil, err
	}
	consulConfig := &StorageConsulConfig{
		ClusterInfoConsulConfig: clusterConsulConfig,
		StorageConfig: map[string]interface{}{
			"index_datetime_format":   fmt.Sprintf("write_%s", timex.ParsePyDateFormat(e.DateFormat)),
			"index_datetime_timezone": e.TimeZone,
			"date_format":             e.DateFormat,
			"slice_size":              e.SliceSize,
			"slice_gap":               e.SliceGap,
			"retention":               e.Retention,
			"warm_phase_days":         e.WarmPhaseDays,
			"warm_phase_settings":     e.WarmPhaseSettings,
			"base_index":              strings.ReplaceAll(e.TableID, ".", "_"),
			"index_settings":          indexSettingsMap,
			"mapping_settings":        mappingSettingMap,
		},
	}

	return consulConfig, nil
}

// CreateTable 创建存储
func (e EsStorageSvc) CreateTable(tableId string, isSyncDb bool, storageConfig map[string]interface{}) error {
	// 判断是否需要使用默认集群信息
	clusterId, _ := storageConfig["cluster_id"].(*uint)
	if clusterId == nil {
		var clusterInfo storage.ClusterInfo
		if err := storage.NewClusterInfoQuerySet(mysql.GetDBSession().DB).ClusterTypeEq(models.StorageTypeES).IsDefaultClusterEq(true).One(&clusterInfo); err != nil {
			return err
		}
		clusterId = &clusterInfo.ClusterID
	} else {
		count, err := storage.NewClusterInfoQuerySet(mysql.GetDBSession().DB).ClusterIDEq(*clusterId).Count()
		if err != nil {
			return err
		}
		if count == 0 {
			return errors.New(fmt.Sprintf("cluster_id [%v] is not exists or is not redis cluster", clusterId))
		}
	}
	// 校验table_id， key是否存在冲突
	count, err := storage.NewESStorageQuerySet(mysql.GetDBSession().DB).TableIDEq(tableId).Count()
	if err != nil {
		return err
	}
	if count != 0 {
		return errors.New(fmt.Sprintf("result_table [%s] already has redis storage config", tableId))
	}
	// 测试date_format是否正确可用的 -- 格式化结果的数据只能包含数字，不能有其他结果
	dateformat, ok := storageConfig["date_format"].(string)
	if !ok {
		dateformat = "%Y%m%d%H"
	}
	dateformat = timex.ParsePyDateFormat(dateformat)
	nowStr := time.Now().Format(dateformat)
	if findString := regexp.MustCompile(`^\d+$`).FindString(nowStr); findString == "" {
		return errors.New(fmt.Sprintf("result_table [%s] date_format contains none digit info, it is bad", tableId))
	}
	// 	断言配置参数设置默认值
	sliceSize, ok := storageConfig["slice_size"].(uint)
	if !ok {
		sliceSize = 500
	}
	sliceGap, ok := storageConfig["slice_gap"].(int)
	if !ok {
		sliceGap = 120
	}
	retention, ok := storageConfig["retention"].(int)
	if !ok {
		retention = 30
	}
	warmPhaseDays, ok := storageConfig["warm_phase_days"].(int)
	if !ok {
		warmPhaseDays = 0
	}
	timeZone, ok := storageConfig["time_zone"].(int8)
	if !ok {
		timeZone = 0
	}
	enableCreateIndex, ok := storageConfig["enable_create_index"].(bool)
	if !ok {
		enableCreateIndex = true
	}
	indexSettingsMap, ok := storageConfig["index_settings"].(map[string]interface{})
	if !ok {
		indexSettingsMap = make(map[string]interface{})
	}
	mappingSettingsMap, _ := storageConfig["mapping_settings"].(map[string]interface{})
	if !ok {
		mappingSettingsMap = make(map[string]interface{})
	}
	warmPhaseSettings, _ := storageConfig["warm_phase_settings"].(map[string]interface{})
	if !ok {
		warmPhaseSettings = make(map[string]interface{})
	}

	if warmPhaseDays > 0 {
		if len(warmPhaseSettings) == 0 {
			return errors.New(fmt.Sprintf("result_table [%s] warm_phase_settings is empty, but min_days > 0.", tableId))
		}
		for _, key := range []string{"allocation_attr_name", "allocation_attr_value", "allocation_type"} {
			if _, ok := warmPhaseSettings[key]; !ok {
				return errors.New(fmt.Sprintf("warm_phase_settings.%s can not be empty", key))
			}

		}
	}

	if timeZone > 12 || timeZone < -12 {
		return errors.New(fmt.Sprintf("time_zone illegal"))
	}
	warmPhaseSettingsStr, err := jsonx.MarshalString(warmPhaseSettings)
	if err != nil {
		return err
	}
	indexSettingsMapStr, err := jsonx.MarshalString(indexSettingsMap)
	if err != nil {
		return err
	}
	mappingSettingsMapStr, err := jsonx.MarshalString(mappingSettingsMap)
	if err != nil {
		return err
	}
	ess := storage.ESStorage{
		TableID:           tableId,
		DateFormat:        dateformat,
		SliceSize:         sliceSize,
		SliceGap:          sliceGap,
		Retention:         retention,
		WarmPhaseDays:     warmPhaseDays,
		WarmPhaseSettings: warmPhaseSettingsStr,
		TimeZone:          timeZone,
		IndexSettings:     indexSettingsMapStr,
		MappingSettings:   mappingSettingsMapStr,
		StorageClusterID:  *clusterId,
	}
	if err := ess.Create(mysql.GetDBSession().DB); err != nil {
		return err
	}
	logger.Infof("result_table [%s] now has es_storage will try to create index", tableId)
	if enableCreateIndex {
		if err := ess.CreateEsIndex(context.Background(), isSyncDb); err != nil {
			return err
		}
	}
	return nil
}
