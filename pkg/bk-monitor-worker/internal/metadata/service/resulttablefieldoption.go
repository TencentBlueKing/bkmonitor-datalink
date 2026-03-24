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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
)

// ResultTableFieldOptionSvc result table field option service
type ResultTableFieldOptionSvc struct {
	*resulttable.ResultTableFieldOption
}

func NewResultTableFieldOptionSvc(obj *resulttable.ResultTableFieldOption) ResultTableFieldOptionSvc {
	return ResultTableFieldOptionSvc{
		ResultTableFieldOption: obj,
	}
}

// BathFieldOption 返回批量的result table field option
func (ResultTableFieldOptionSvc) BathFieldOption(tableIdList []string) (map[string]map[string]map[string]any, error) {
	var resultTableFieldOption []resulttable.ResultTableFieldOption

	if err := resulttable.NewResultTableFieldOptionQuerySet(mysql.GetDBSession().DB).
		TableIDIn(tableIdList...).All(&resultTableFieldOption); err != nil {
		return nil, err
	}
	optionData := make(map[string]map[string]map[string]any)
	for _, option := range resultTableFieldOption {
		value, err := option.InterfaceValue()
		if err != nil {
			return nil, err
		}
		if tableOption, ok := optionData[option.TableID]; ok {
			if opt, ok := tableOption[option.FieldName]; ok {
				opt[option.Name] = value
			} else {
				tableOption[option.FieldName] = map[string]any{option.Name: value}
			}
		} else {
			optionData[option.TableID] = map[string]map[string]any{option.FieldName: {option.Name: value}}
		}
	}
	return optionData, nil
}
