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

// ResultTableOptionSvc result table option service
type ResultTableOptionSvc struct {
	*resulttable.ResultTableOption
}

func NewResultTableOptionSvc(obj *resulttable.ResultTableOption) ResultTableOptionSvc {
	return ResultTableOptionSvc{
		ResultTableOption: obj,
	}
}

// BathResultTableOption 返回批量的result table option
func (ResultTableOptionSvc) BathResultTableOption(tableIdList []string) (map[string]map[string]any, error) {
	var resultTableOption []resulttable.ResultTableOption
	if err := resulttable.NewResultTableOptionQuerySet(mysql.GetDBSession().DB).
		TableIDIn(tableIdList...).All(&resultTableOption); err != nil {
		return nil, err
	}
	optionData := make(map[string]map[string]any)
	for _, option := range resultTableOption {
		value, err := option.InterfaceValue()
		if err != nil {
			return nil, err
		}
		if rtOpts, ok := optionData[option.TableID]; ok {
			rtOpts[option.Name] = value
		} else {
			optionData[option.TableID] = map[string]any{option.Name: value}
		}
	}
	return optionData, nil
}
