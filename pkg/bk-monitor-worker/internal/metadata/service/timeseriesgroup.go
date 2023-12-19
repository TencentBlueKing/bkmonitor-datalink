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
	"time"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var TSDefaultStorageConfig = map[string]interface{}{"use_default_rp": true}

var TSStorageFieldList = []map[string]interface{}{
	{
		"field_name":        "target",
		"field_type":        "string",
		"tag":               models.ResultTableFieldTagDimension,
		"option":            map[string]interface{}{},
		"is_config_by_user": true,
	},
}

// TimeSeriesGroupSvc time series group service
type TimeSeriesGroupSvc struct {
	*customreport.TimeSeriesGroup
}

func NewTimeSeriesGroupSvc(obj *customreport.TimeSeriesGroup) TimeSeriesGroupSvc {
	return TimeSeriesGroupSvc{
		TimeSeriesGroup: obj,
	}
}

func (s TimeSeriesGroupSvc) MetricConsulPath() string {
	return fmt.Sprintf("%s/metadata/influxdb_metrics/%v/time_series_metric", cfg.StorageConsulPathPrefix, s.BkDataID)
}

func (s TimeSeriesGroupSvc) CreateCustomGroup(bkDataId uint, bkBizId int, customGroupName, label, operator string, isSplitMeasurement bool, defaultStorageConfig map[string]interface{}, additionalOptions map[string][]string) (*customreport.TimeSeriesGroup, error) {
	err := s.PreCheck(label, bkDataId, customGroupName, bkBizId)
	if err != nil {
		return nil, err
	}
	tableId := s.MakeTableId(bkBizId, bkDataId)
	tsGroup := customreport.TimeSeriesGroup{
		CustomGroupBase: customreport.CustomGroupBase{
			BkDataID:           bkDataId,
			BkBizID:            bkBizId,
			TableID:            tableId,
			MaxRate:            -1,
			Label:              label,
			IsEnable:           true,
			IsDelete:           false,
			Creator:            operator,
			CreateTime:         time.Now(),
			LastModifyUser:     operator,
			LastModifyTime:     time.Now(),
			IsSplitMeasurement: false,
		},
		TimeSeriesGroupName: customGroupName,
	}
	db := mysql.GetDBSession().DB
	if err := tsGroup.Create(db); err != nil {
		return nil, err
	}
	tsGroupSvc := NewTimeSeriesGroupSvc(&tsGroup)
	logger.Infof("TimeSeriesGroup [%v] now is created from data_id [%v] by operator [%s]", tsGroupSvc.TimeSeriesGroupID, bkDataId, operator)
	// 创建一个关联的存储关系
	for k, v := range TSDefaultStorageConfig {
		defaultStorageConfig[k] = v
	}
	option := map[string]interface{}{"is_split_measurement": isSplitMeasurement}
	for k, v := range additionalOptions {
		option[k] = v
	}
	// 清除历史 DataSourceResultTable 数据
	if err := db.Delete(&resulttable.DataSourceResultTable{}, "bk_data_id = ?", bkDataId).Error; err != nil {
		return nil, err
	}
	rtSvc := NewResultTableSvc(nil)
	err = rtSvc.CreateResultTable(
		tsGroup.BkDataID,
		tsGroup.BkBizID,
		tableId,
		tsGroup.TimeSeriesGroupName,
		true,
		models.ResultTableSchemaTypeFree,
		operator,
		models.StorageTypeInfluxdb,
		defaultStorageConfig,
		TSStorageFieldList,
		true,
		map[string]interface{}{},
		label,
		option,
	)
	if err != nil {
		return nil, err
	}
	// 需要为datasource增加option，否则transfer无法得知需要拆解的字段内容
	dsOptions := []map[string]string{
		{"name": "metrics_report_path", "value": tsGroupSvc.MetricConsulPath()},
		{"name": "disable_metric_cutter", "value": "true"},
		{"name": "flat_batch_key", "value": "data"},
	}
	tx := db.Begin()
	for _, dsOption := range dsOptions {
		if err := NewDataSourceOptionSvc(nil).CreateOption(bkDataId, dsOption["name"], dsOption["value"], "system", tx); err != nil {
			tx.Rollback()
			return nil, err
		}
	}
	tx.Commit()
	if err != nil {
		return nil, err
	}
	// 刷新配置到节点管理，通过节点管理下发配置到采集器
	// todo 做异步调用 RefreshCustomReportConfig(bkBizId)

	return &tsGroup, nil
}

// PreCheck 参数检查
func (TimeSeriesGroupSvc) PreCheck(label string, bkDataId uint, customGroupName string, bkBizId int) error {
	db := mysql.GetDBSession().DB
	// 确认label是否存在
	count, err := resulttable.NewLabelQuerySet(db).LabelTypeEq(models.LabelTypeResultTable).LabelIdEq(label).Count()
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("label [%s] is not exists as a rt label", label)
	}
	// 判断同一个data_id是否已经被其他事件绑定了
	count, err = customreport.NewTimeSeriesGroupQuerySet(db).BkDataIDEq(bkDataId).Count()
	if err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("bk_data_id [%v] is already used by other custom group, use it first?", bkDataId)
	}
	// 判断同一个业务下是否有重名的custom_group_name
	count, err = customreport.NewTimeSeriesGroupQuerySet(db).BkBizIDEq(bkBizId).IsDeleteEq(false).TimeSeriesGroupNameEq(customGroupName).Count()
	if err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("biz_id [%v] already has TimeSeriesGroup [%s], should change TimeSeriesGroupName and try again", bkBizId, customGroupName)
	}
	return nil
}

func (s TimeSeriesGroupSvc) MakeTableId(bkBizId int, bkDataId uint) string {
	if bkBizId != 0 {
		return fmt.Sprintf("%v_bkmonitor_time_series_%v.%v", bkBizId, bkDataId, models.TSGroupDefaultMeasurement)
	}
	return fmt.Sprintf("bkmonitor_time_series_%v.%v", bkDataId, models.TSGroupDefaultMeasurement)
}
