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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
)

// FieldDefBkAgentId GSE agent ID
var FieldDefBkAgentId = map[string]any{
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
var FieldDefBkHostId = map[string]any{
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
var FieldDefBkTargetHostId = map[string]any{
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

func (ResultTableFieldSvc) BatchGetFields(tableIdList []string, isConsulConfig bool) (map[string][]any, error) {
	tableFieldOptionData, err := NewResultTableFieldOptionSvc(nil).BathFieldOption(tableIdList)
	if err != nil {
		return nil, err
	}
	var rtFieldList []resulttable.ResultTableField
	if err := resulttable.NewResultTableFieldQuerySet(mysql.GetDBSession().DB).
		TableIDIn(tableIdList...).All(&rtFieldList); err != nil {
		return nil, err
	}
	data := make(map[string][]any)
	for _, field := range rtFieldList {
		var option any
		if _, ok := tableFieldOptionData[field.TableID]; ok {
			if _, ok := tableFieldOptionData[field.TableID][field.FieldName]; ok {
				option = tableFieldOptionData[field.TableID][field.FieldName]
			} else {
				option = make(map[string]any)
			}
		} else {
			option = make(map[string]any)
		}
		item := map[string]any{
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
			data[field.TableID] = []any{item}
		}
	}
	return data, nil
}
