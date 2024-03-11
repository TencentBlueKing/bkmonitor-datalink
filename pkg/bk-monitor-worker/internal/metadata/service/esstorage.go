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
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/diffutil"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/optionx"
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
	var WarmPhaseSettingsMap map[string]interface{}
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
		StorageConfig: map[string]interface{}{
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

// CreateTable 创建存储
func (EsStorageSvc) CreateTable(tableId string, isSyncDb bool, storageConfig *optionx.Options) error {
	db := mysql.GetDBSession().DB
	// 判断是否需要使用默认集群信息
	var clusterId uint
	if id, ok := storageConfig.GetUint("cluster_id"); !ok {
		var clusterInfo storage.ClusterInfo
		if err := storage.NewClusterInfoQuerySet(db).ClusterTypeEq(models.StorageTypeES).IsDefaultClusterEq(true).One(&clusterInfo); err != nil {
			return err
		}
		clusterId = clusterInfo.ClusterID
	} else {
		count, err := storage.NewClusterInfoQuerySet(db).ClusterIDEq(id).Count()
		if err != nil {
			return err
		}
		if count == 0 {
			return errors.Errorf("cluster_id [%v] is not exists or is not es cluster", clusterId)
		}
	}
	// 校验table_id， key是否存在冲突
	count, err := storage.NewESStorageQuerySet(db).TableIDEq(tableId).Count()
	if err != nil {
		return err
	}
	if count != 0 {
		return errors.Errorf("result_table [%s] already has redis storage config", tableId)
	}
	// 测试date_format是否正确可用的 -- 格式化结果的数据只能包含数字，不能有其他结果
	dateformat, ok := storageConfig.GetString("date_format")
	if !ok {
		dateformat = "%Y%m%d%H"
	}
	nowStr := time.Now().Format(timex.ParsePyDateFormat(dateformat))
	if findString := regexp.MustCompile(`^\d+$`).FindString(nowStr); findString == "" {
		return errors.Errorf("result_table [%s] date_format contains none digit info, it is bad", tableId)
	}
	// 	获取配置参数或使用默认值
	// 切分时间间隔
	sliceSize, ok := storageConfig.GetUint("slice_size")
	if !ok {
		sliceSize = 500
	}
	// 切分时间间隔
	sliceGap, ok := storageConfig.GetInt("slice_gap")
	if !ok {
		sliceGap = 120
	}
	// 保留时间
	retention, ok := storageConfig.GetInt("retention")
	if !ok {
		retention = 30
	}
	// 暖数据执行分配的等待天数
	warmPhaseDays, _ := storageConfig.GetInt("warm_phase_days")
	// 时区设置，默认零时区
	timeZone, _ := storageConfig.GetInt8("time_zone")
	enableCreateIndex, ok := storageConfig.GetBool("enable_create_index")
	if !ok {
		enableCreateIndex = true
	}
	// index创建配置
	indexSettingsMap, ok := storageConfig.GetStringMap("index_settings")
	if !ok {
		indexSettingsMap = make(map[string]interface{})
	}
	// index创建时的mapping配置
	mappingSettingsMap, _ := storageConfig.GetStringMap("mapping_settings")
	if !ok {
		mappingSettingsMap = make(map[string]interface{})
	}
	// 暖数据切换配置，当 warm_phase_days > 0 时，此项必填
	warmPhaseSettings, _ := storageConfig.GetStringMap("warm_phase_settings")
	if !ok {
		warmPhaseSettings = make(map[string]interface{})
	}

	if warmPhaseDays > 0 {
		if len(warmPhaseSettings) == 0 {
			return errors.Errorf("result_table [%s] warm_phase_settings is empty, but min_days > 0", tableId)
		}
		for _, key := range []string{"allocation_attr_name", "allocation_attr_value", "allocation_type"} {
			if _, ok := warmPhaseSettings[key]; !ok {
				return errors.Errorf("warm_phase_settings.%s can not be empty", key)
			}

		}
	}

	if timeZone > 12 || timeZone < -12 {
		return errors.Errorf("time_zone illegal")
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
		StorageClusterID:  clusterId,
	}
	if cfg.BypassSuffixPath != "" {
		logger.Info(diffutil.BuildLogStr("discover_bcs_clusters", diffutil.OperatorTypeDBCreate, diffutil.NewSqlBody(ess.TableName(), map[string]interface{}{
			storage.ESStorageDBSchema.TableID.String():           ess.TableID,
			storage.ESStorageDBSchema.DateFormat.String():        ess.DateFormat,
			storage.ESStorageDBSchema.SliceSize.String():         ess.SliceSize,
			storage.ESStorageDBSchema.SliceGap.String():          ess.SliceGap,
			storage.ESStorageDBSchema.Retention.String():         ess.Retention,
			storage.ESStorageDBSchema.WarmPhaseDays.String():     ess.WarmPhaseDays,
			storage.ESStorageDBSchema.WarmPhaseSettings.String(): ess.WarmPhaseSettings,
			storage.ESStorageDBSchema.TimeZone.String():          ess.TimeZone,
			storage.ESStorageDBSchema.IndexSettings.String():     ess.IndexSettings,
			storage.ESStorageDBSchema.MappingSettings.String():   ess.MappingSettings,
			storage.ESStorageDBSchema.StorageClusterID.String():  ess.StorageClusterID,
		}), ""))
		return nil
	} else {
		if err := ess.Create(db); err != nil {
			return err
		}
	}
	logger.Infof("result_table [%s] now has es_storage will try to create index", tableId)
	if enableCreateIndex {
		if err := ess.CreateEsIndex(context.Background(), isSyncDb); err != nil {
			return err
		}
	}
	return nil
}
