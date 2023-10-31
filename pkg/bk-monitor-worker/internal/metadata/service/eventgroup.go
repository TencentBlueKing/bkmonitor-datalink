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
	"errors"
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/nodeman"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var EventDefaultStorageConfig = map[string]interface{}{
	"retention":   30,
	"slice_gap":   60 * 24,
	"date_format": "%Y%m%d",
	"mapping_settings": map[string]interface{}{
		"dynamic_templates": []map[string]interface{}{
			{
				"discover_dimension": map[string]interface{}{
					"path_match": "dimensions.*",
					"mapping": map[string]interface{}{
						"type": "keyword",
					},
				},
			},
		},
	},
}

var EventStorageTimeOption = map[string]interface{}{
	"es_type":   "data_nanos",
	"es_format": "epoch_millis",
}

var EventStorageEventOption = map[string]interface{}{
	"es_type": "object",
	"es_format": map[string]interface{}{
		"content": map[string]interface{}{
			"type": "text",
		},
		"count": map[string]interface{}{
			"type": "integer",
		},
	},
}

var EventStorageTargetOption = map[string]string{
	"es_type": "keyword",
}

var EventStorageDimensionOption = map[string]interface{}{
	"es_type":    "object",
	"es_dynamic": true,
}

var EventStorageNameOption = map[string]string{
	"es_type": "keyword",
}

var EventStorageFieldList = []map[string]interface{}{
	{
		"field_name":        "event",
		"field_type":        models.ResultTableFieldTypeObject,
		"tag":               models.ResultTableFieldTagDimension,
		"option":            EventStorageEventOption,
		"is_config_by_user": true,
	},
	{
		"field_name":        "target",
		"field_type":        models.ResultTableFieldTypeString,
		"tag":               models.ResultTableFieldTagDimension,
		"option":            EventStorageTargetOption,
		"is_config_by_user": true,
	},
	{
		"field_name":        "dimensions",
		"field_type":        models.ResultTableFieldTypeObject,
		"tag":               models.ResultTableFieldTagDimension,
		"option":            EventStorageDimensionOption,
		"is_config_by_user": true,
	},
	{
		"field_name":        "event_name",
		"field_type":        models.ResultTableFieldTypeString,
		"tag":               models.ResultTableFieldTagDimension,
		"option":            EventStorageNameOption,
		"is_config_by_user": true,
	},
}

// EventGroupSvc event group service
type EventGroupSvc struct {
	*customreport.EventGroup
}

func NewEventGroupSvc(obj *customreport.EventGroup) EventGroupSvc {
	return EventGroupSvc{
		EventGroup: obj,
	}
}

func (s EventGroupSvc) CreateCustomGroup(bkDataId uint, bkBizId int, customGroupName, label, operator string, isSplitMeasurement bool, defaultStorageConfig map[string]interface{}, additionalOptions map[string][]string) (*customreport.EventGroup, error) {
	err := s.PreCheck(label, bkDataId, customGroupName, bkBizId)
	if err != nil {
		return nil, err
	}
	tableId := s.MakeTableId(bkBizId, bkDataId)
	eventGroup := customreport.EventGroup{
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
		EventGroupName: customGroupName,
	}
	if err := eventGroup.Create(mysql.GetDBSession().DB); err != nil {
		return nil, err
	}
	tsGroupSvc := NewEventGroupSvc(&eventGroup)
	logger.Infof("EventGroup [%v] now is created from data_id [%v] by operator [%s]", tsGroupSvc.EventGroupID, bkDataId, operator)

	// 创建一个关联的存储关系
	for k, v := range EventDefaultStorageConfig {
		defaultStorageConfig[k] = v
	}
	option := map[string]interface{}{"is_split_measurement": isSplitMeasurement}
	for k, v := range additionalOptions {
		option[k] = v
	}

	// 清除历史 DataSourceResultTable 数据
	if err := mysql.GetDBSession().DB.Delete(&resulttable.DataSourceResultTable{}, "bk_data_id = ?", bkDataId).Error; err != nil {
		return nil, err
	}
	rtSvc := NewResultTableSvc(nil)
	err = rtSvc.CreateResultTable(
		eventGroup.BkDataID,
		eventGroup.BkBizID,
		tableId,
		eventGroup.EventGroupName,
		true,
		models.ResultTableSchemaTypeFree,
		operator,
		models.StorageTypeES,
		defaultStorageConfig,
		EventStorageFieldList,
		true,
		EventStorageTimeOption,
		label,
		option,
	)
	if err != nil {
		return nil, err
	}
	// 需要为datasource增加option，否则transfer无法得知需要拆解的字段内容
	dsOptions := []map[string]string{
		{"name": "flat_batch_key", "value": "data"},
	}
	for _, dsOption := range dsOptions {
		if err := NewDataSourceOptionSvc(nil).CreateOption(bkDataId, dsOption["name"], dsOption["value"], "system"); err != nil {
			return nil, err
		}
	}
	if err != nil {
		return nil, err
	}
	// 刷新配置到节点管理，通过节点管理下发配置到采集器
	// todo 做异步调用 RefreshCustomReportConfig(bkBizId)
	return &eventGroup, nil
}

// PreCheck 参数检查
func (EventGroupSvc) PreCheck(label string, bkDataId uint, customGroupName string, bkBizId int) error {
	// 确认label是否存在
	count, err := resulttable.NewLabelQuerySet(mysql.GetDBSession().DB).LabelTypeEq(models.LabelTypeResultTable).LabelIdEq(label).Count()
	if err != nil {
		return err
	}
	if count == 0 {
		return errors.New(fmt.Sprintf("label [%s] is not exists as a rt label", label))
	}
	// 判断同一个data_id是否已经被其他事件绑定了
	count, err = customreport.NewTimeSeriesGroupQuerySet(mysql.GetDBSession().DB).BkDataIDEq(bkDataId).Count()
	if err != nil {
		return err
	}
	if count != 0 {
		return errors.New(fmt.Sprintf("bk_data_id [%v] is already used by other custom group, use it first?", bkDataId))
	}
	// 判断同一个业务下是否有重名的custom_group_name
	count, err = customreport.NewTimeSeriesGroupQuerySet(mysql.GetDBSession().DB).BkBizIDEq(bkBizId).IsDeleteEq(false).TimeSeriesGroupNameEq(customGroupName).Count()
	if err != nil {
		return err
	}
	if count != 0 {
		return errors.New(fmt.Sprintf("biz_id [%v] already has EventGroup [EventGroupName], should change %s and try again", bkDataId, customGroupName))
	}
	return nil
}

func (s EventGroupSvc) MakeTableId(bkBizId int, bkDataId uint) string {
	if bkBizId != 0 {
		return fmt.Sprintf("%v_bkmonitor_event_%v", bkBizId, bkDataId)
	}
	return fmt.Sprintf("_bkmonitor_event_%v", bkDataId)
}

func RefreshCustomReportConfig(bkBizId int) error {
	// 判定节点管理是否上传支持v2新配置模版的bk-collector版本0.16.1061
	defaultVersion := "0.0.0"
	nodemanApi, err := api.GetNodemanApi()
	if err != nil {
		return err
	}
	var resp nodeman.PluginInfoResp
	_, err = nodemanApi.PluginInfo().SetQueryParams(map[string]string{"name": "bk-collector"}).SetResult(&resp).Request()
	if err != nil {
		return err
	}
	var versionStrList []string
	for _, plugin := range resp.Data {
		if plugin.IsReady {
			if plugin.Version == "" {
				versionStrList = append(versionStrList, defaultVersion)
			} else {
				versionStrList = append(versionStrList, plugin.Version)
			}
		}
	}
	maxVersion := getMaxVersion(defaultVersion, versionStrList)

	if compareVersion(maxVersion, models.RecommendedBkCollectorVersion) > 0 {
		if err := NewCustomReportSubscriptionSvc(nil).RefreshCollectorCustomConf(&bkBizId, "bk-collector", "add"); err != nil {
			return err
		}
	} else {
		logger.Infof("bk-collector version [%s] lower than supported version %s, stop refresh bk-collector config", maxVersion, models.RecommendedBkCollectorVersion)
	}
	// bkmonitorproxy全量更新
	if err := NewCustomReportSubscriptionSvc(nil).RefreshCollectorCustomConf(nil, "bkmonitorproxy", "add"); err != nil {
		return err
	}
	return nil
}

func getMaxVersion(defaultVersion string, versionList []string) string {
	maxVersion := defaultVersion
	for _, version := range versionList {
		if compareVersion(maxVersion, version) < 0 {
			maxVersion = version
		}
	}
	return maxVersion
}

func compareVersion(version1 string, version2 string) int {
	var res int
	ver1Strs := getValidatedVersion(version1) //[1 2 3]
	ver2Strs := getValidatedVersion(version2) //[2 3 4]
	ver1Len := len(ver1Strs)
	ver2Len := len(ver2Strs)
	if ver1Len == 0 || ver2Len == 0 {
		return 0
	}
	for i := 0; i < 3; i++ {
		var ver1Int, ver2Int int
		var err error
		if i < ver1Len {
			// 字符串转换成整数strconv.Atoi
			ver1Int, err = strconv.Atoi(ver1Strs[i])
			if err != nil {
				return 0
			}
		}
		if i < ver2Len {
			ver2Int, err = strconv.Atoi(ver2Strs[i])
			if err != nil {
				return 0
			}
		}
		if ver1Int == ver2Int {
			res = 1
			continue
		}
		if ver1Int > ver2Int {
			res = 1
			break
		}
		if ver1Int < ver2Int {
			res = -1
			break
		}
	}
	return res
}

func getValidatedVersion(version string) []string {
	version = regexp.MustCompile(`[^\d.]`).ReplaceAllString(version, "")
	parts := strings.Split(version, ".")
	if len(parts) >= 3 {
		parts = parts[:3]
	}
	return parts
}
