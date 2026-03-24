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

// DataSourceOptionSvc data source option service
type DataSourceOptionSvc struct {
	*resulttable.DataSourceOption
}

func NewDataSourceOptionSvc(obj *resulttable.DataSourceOption) DataSourceOptionSvc {
	return DataSourceOptionSvc{
		DataSourceOption: obj,
	}
}

// GetOptions 获取datasource的配置项
func (DataSourceOptionSvc) GetOptions(bkDataId uint) (map[string]any, error) {
	var dataSourceOptionList []resulttable.DataSourceOption
	if err := resulttable.NewDataSourceOptionQuerySet(mysql.GetDBSession().DB).
		BkDataIdEq(bkDataId).All(&dataSourceOptionList); err != nil {
		return nil, err
	}
	optionData := make(map[string]any)
	for _, option := range dataSourceOptionList {
		value, err := option.InterfaceValue()
		if err != nil {
			return nil, err
		}
		optionData[option.Name] = value
	}
	return optionData, nil
}
