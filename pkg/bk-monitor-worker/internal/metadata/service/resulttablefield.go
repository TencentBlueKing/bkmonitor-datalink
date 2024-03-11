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
	"strings"
	"time"

	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/diffutil"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// FieldDefBkAgentId GSE agent ID
var FieldDefBkAgentId = map[string]interface{}{
	"field_name":        "bk_agent_id",
	"field_type":        models.ResultTableFieldTypeString,
	"unit":              "",
	"is_config_by_user": true,
	"default_value":     "",
	"operator":          "system",
	"tag":               models.ResultTableFieldTagDimension,
	"description":       "Agent ID",
	"alias_name":        "",
}

// FieldDefBkHostId CMDB host ID
var FieldDefBkHostId = map[string]interface{}{
	"field_name":        "bk_host_id",
	"field_type":        models.ResultTableFieldTypeString,
	"unit":              "",
	"is_config_by_user": true,
	"default_value":     "",
	"operator":          "system",
	"tag":               models.ResultTableFieldTagDimension,
	"description":       "采集主机ID",
	"alias_name":        "",
}

// CMDB host ID
var FieldDefBkTargetHostId = map[string]interface{}{
	"field_name":        "bk_target_host_id",
	"field_type":        models.ResultTableFieldTypeString,
	"unit":              "",
	"is_config_by_user": true,
	"default_value":     "",
	"operator":          "system",
	"tag":               models.ResultTableFieldTagDimension,
	"description":       "目标主机ID",
	"alias_name":        "",
}

// ResultTableFieldSvc result table field service
type ResultTableFieldSvc struct {
	*resulttable.ResultTableField
}

func NewResultTableFieldSvc(obj *resulttable.ResultTableField) ResultTableFieldSvc {
	return ResultTableFieldSvc{
		ResultTableField: obj,
	}
}

func (ResultTableFieldSvc) BatchGetFields(tableIdList []string, isConsulConfig bool) (map[string][]interface{}, error) {
	tableFieldOptionData, err := NewResultTableFieldOptionSvc(nil).BathFieldOption(tableIdList)
	if err != nil {
		return nil, err
	}
	var rtFieldList []resulttable.ResultTableField
	if err := resulttable.NewResultTableFieldQuerySet(mysql.GetDBSession().DB).
		TableIDIn(tableIdList...).All(&rtFieldList); err != nil {
		return nil, err
	}
	var data = make(map[string][]interface{})
	for _, field := range rtFieldList {
		var option interface{}
		if _, ok := tableFieldOptionData[field.TableID]; ok {
			if _, ok := tableFieldOptionData[field.TableID][field.FieldName]; ok {
				option = tableFieldOptionData[field.TableID][field.FieldName]
			} else {
				option = make(map[string]interface{})
			}
		} else {
			option = make(map[string]interface{})
		}
		item := map[string]interface{}{
			"field_name":        field.FieldName,
			"type":              field.FieldType,
			"tag":               field.Tag,
			"default_value":     field.DefaultValue,
			"is_config_by_user": field.IsConfigByUser,
			"description":       field.Description,
			"unit":              field.Unit,
			"alias_name":        field.AliasName,
			"option":            option,
			"is_disabled":       field.IsDisabled,
		}
		if isConsulConfig && field.AliasName != "" {
			item["field_name"] = field.AliasName
			item["alias_name"] = field.FieldName
		}
		if items, ok := data[field.TableID]; ok {
			data[field.TableID] = append(items, item)
		} else {
			data[field.TableID] = []interface{}{item}
		}
	}
	return data, nil
}

// BulkCreateDefaultFields 批量创建默认字段
func (s ResultTableFieldSvc) BulkCreateDefaultFields(tableId string, timeOption map[string]interface{}, isTimeFieldOnly bool) error {
	// 组装要创建的默认字段数据
	// 上报时间
	timeFieldData := map[string]interface{}{
		"field_name":        "time",
		"field_type":        models.ResultTableFieldTypeTimestamp,
		"unit":              "",
		"is_config_by_user": true,
		"default_value":     "",
		"operator":          "system",
		"description":       "数据上报时间",
		"tag":               models.ResultTableFieldTagTimestamp,
		"alias_name":        "",
		"option":            timeOption,
	}
	//  当限制仅包含时间字段时，创建时间字段，然后返回
	if isTimeFieldOnly {
		if err := s.BulkCreateFields(
			tableId, []map[string]interface{}{timeFieldData}); err != nil {
			return err
		}
		return nil
	}
	// 业务 ID
	bkBizIdFieldData := map[string]interface{}{
		"field_name":        "bk_biz_id",
		"field_type":        models.ResultTableFieldTypeInt,
		"unit":              "",
		"is_config_by_user": true,
		"default_value":     "-1",
		"operator":          "system",
		"tag":               models.ResultTableFieldTagDimension,
		"description":       "业务ID",
		"alias_name":        "",
	}
	//  开发商 ID
	bkSupplierIdFieldData := map[string]interface{}{
		"field_name":        "bk_supplier_id",
		"field_type":        models.ResultTableFieldTypeInt,
		"unit":              "",
		"is_config_by_user": true,
		"default_value":     "-1",
		"operator":          "system",
		"tag":               models.ResultTableFieldTagDimension,
		"description":       "开发商ID",
		"alias_name":        "",
	}
	// 云区域 ID
	bkCloudIdFieldData := map[string]interface{}{
		"field_name":        "bk_cloud_id",
		"field_type":        models.ResultTableFieldTypeInt,
		"unit":              "",
		"is_config_by_user": true,
		"default_value":     "-1",
		"operator":          "system",
		"tag":               models.ResultTableFieldTagDimension,
		"description":       "采集器云区域ID",
		"alias_name":        "",
	}
	// IP 地址
	ipFieldData := map[string]interface{}{
		"field_name":        "ip",
		"field_type":        models.ResultTableFieldTypeString,
		"unit":              "",
		"is_config_by_user": true,
		"default_value":     "",
		"operator":          "system",
		"tag":               models.ResultTableFieldTagDimension,
		"description":       "采集器IP",
		"alias_name":        "",
	}
	// CMDB 层级记录信息
	bkCmdbLevelFieldData := map[string]interface{}{

		"field_name":        "bk_cmdb_level",
		"field_type":        models.ResultTableFieldTypeString,
		"unit":              "",
		"is_config_by_user": true,
		"default_value":     "",
		"operator":          "system",
		"tag":               models.ResultTableFieldTagDimension,
		"description":       "CMDB层级信息",
		"alias_name":        "",
	}

	if err := s.BulkCreateFields(
		tableId, []map[string]interface{}{timeFieldData, bkBizIdFieldData, bkSupplierIdFieldData,
			bkCloudIdFieldData, ipFieldData, bkCmdbLevelFieldData, FieldDefBkAgentId, FieldDefBkHostId, FieldDefBkTargetHostId}); err != nil {
		return err
	}
	// 当前cmdb_level默认都不需要写入influxdb, 防止维度增长问题
	if err := NewResultTableFieldOptionSvc(nil).CreateOption(tableId, "bk_cmdb_level", models.RTFOInfluxdbDisabled, true, "system", nil); err != nil {
		return err
	}
	logger.Infof("all default field is created for table->[%s]", tableId)
	return nil
}

func (s ResultTableFieldSvc) BulkCreateFields(tableId string, fieldList []map[string]interface{}) error {
	fields, fieldNameList, optionData, err := s.composeData(tableId, fieldList)
	if len(fieldList) == 0 {
		logger.Warnf("create fields for table [%s] skip, got no filed", tableId)
		return nil
	}
	if err != nil {
		return err
	}
	db := mysql.GetDBSession().DB
	var rtfList []resulttable.ResultTableField
	if err := resulttable.NewResultTableFieldQuerySet(db).TableIDEq(tableId).FieldNameIn(fieldNameList...).All(&rtfList); err != nil {
		return err
	}
	if len(rtfList) != 0 {
		var names []string
		for _, rtf := range rtfList {
			names = append(names, rtf.FieldName)
		}
		return errors.Errorf("field [%s] is exists under table [%s]", strings.Join(names, ","), tableId)
	}
	tx := db.Begin()
	for _, field := range fields {
		description, _ := field["description"].(string)
		unit, _ := field["unit"].(string)
		aliasName, _ := field["alias_name"].(string)
		tag, _ := field["tag"].(string)
		isConfigByUser, _ := field["is_config_by_user"].(bool)
		defaultValue, _ := field["default_value"].(*string)
		rtf := resulttable.ResultTableField{
			TableID:        field["table_id"].(string),
			FieldName:      field["field_name"].(string),
			FieldType:      field["field_type"].(string),
			Description:    description,
			Unit:           unit,
			Tag:            tag,
			IsConfigByUser: isConfigByUser,
			DefaultValue:   defaultValue,
			Creator:        field["creator"].(string),
			CreateTime:     time.Now(),
			LastModifyUser: field["creator"].(string),
			LastModifyTime: time.Now(),
			AliasName:      aliasName,
			IsDisabled:     false,
		}
		if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "discover_bcs_clusters") {
			logger.Info(diffutil.BuildLogStr("discover_bcs_clusters", diffutil.OperatorTypeDBCreate, diffutil.NewSqlBody(rtf.TableName(), map[string]interface{}{
				resulttable.ResultTableFieldDBSchema.TableID.String():        rtf.TableID,
				resulttable.ResultTableFieldDBSchema.FieldName.String():      rtf.FieldName,
				resulttable.ResultTableFieldDBSchema.FieldType.String():      rtf.FieldType,
				resulttable.ResultTableFieldDBSchema.Description.String():    rtf.Description,
				resulttable.ResultTableFieldDBSchema.Unit.String():           rtf.Unit,
				resulttable.ResultTableFieldDBSchema.Tag.String():            rtf.Tag,
				resulttable.ResultTableFieldDBSchema.IsConfigByUser.String(): rtf.IsConfigByUser,
				resulttable.ResultTableFieldDBSchema.DefaultValue.String():   rtf.DefaultValue,
				resulttable.ResultTableFieldDBSchema.AliasName.String():      rtf.AliasName,
				resulttable.ResultTableFieldDBSchema.IsDisabled.String():     rtf.IsDisabled,
			}), ""))
		} else {
			if err := rtf.Create(tx); err != nil {
				tx.Rollback()
				return err
			}
		}
	}
	logger.Infof("new field [%s] is create for table->[%s]", strings.Join(fieldNameList, ","), tableId)
	for _, option := range optionData {
		if err := NewResultTableFieldOptionSvc(nil).CreateOption(tableId, option["field_name"].(string), option["name"].(string), option["value"], option["creator"].(string), tx); err != nil {
			tx.Rollback()
			return err
		}
	}
	if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "discover_bcs_clusters") {
		tx.Rollback()
	} else {
		tx.Commit()
	}

	return nil
}

func (s ResultTableFieldSvc) composeData(tableId string, fieldList []map[string]interface{}) ([]map[string]interface{}, []string, []map[string]interface{}, error) {
	var fields []map[string]interface{}
	var fieldNameList []string
	var optionData []map[string]interface{}
	for _, field := range fieldList {
		var newField map[string]interface{}
		jsonStr, err := jsonx.MarshalString(field)
		if err != nil {
			return nil, nil, nil, err
		}
		err = jsonx.UnmarshalString(jsonStr, &newField)
		if err != nil {
			return nil, nil, nil, err
		}
		delete(newField, "is_reserved_check")
		newField["table_id"] = tableId
		operator := newField["operator"].(string)
		delete(newField, "operator")
		newField["creator"] = operator
		fieldNameList = append(fieldNameList, newField["field_name"].(string))
		optionInterface := newField["option"]
		fields = append(fields, newField)
		if optionInterface == nil {
			continue
		}
		option := optionInterface.(map[string]interface{})
		for k, v := range option {
			optionData = append(optionData, map[string]interface{}{
				"table_id":   newField["table_id"],
				"field_name": newField["field_name"],
				"name":       k,
				"value":      v,
				"creator":    newField["creator"],
			})
		}

	}
	return fields, fieldNameList, optionData, nil
}
