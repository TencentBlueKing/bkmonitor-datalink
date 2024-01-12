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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
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
func (ResultTableOptionSvc) BathResultTableOption(tableIdList []string) (map[string]map[string]interface{}, error) {
	var resultTableOption []resulttable.ResultTableOption
	if err := resulttable.NewResultTableOptionQuerySet(mysql.GetDBSession().DB).
		TableIDIn(tableIdList...).All(&resultTableOption); err != nil {
		return nil, err
	}
	optionData := make(map[string]map[string]interface{})
	for _, option := range resultTableOption {
		value, err := option.InterfaceValue()
		if err != nil {
			return nil, err
		}
		if rtOpts, ok := optionData[option.TableID]; ok {
			rtOpts[option.Name] = value
		} else {
			optionData[option.TableID] = map[string]interface{}{option.Name: value}
		}
	}
	return optionData, nil
}

// BulkCreateOptions 批量创建结果表级别的选项内容
func (ResultTableOptionSvc) BulkCreateOptions(tableId string, options map[string]interface{}, operator string) error {
	var rtoList []resulttable.ResultTableOption
	var optionNameList []string
	for optionName, optionValue := range options {
		valueStr, valueType, err := models.ParseOptionValue(optionValue)
		if err != nil {
			return err
		}
		rto := resulttable.ResultTableOption{
			OptionBase: models.OptionBase{
				ValueType:  valueType,
				Value:      valueStr,
				Creator:    operator,
				CreateTime: time.Now(),
			},
			TableID: tableId,
			Name:    optionName,
		}
		rtoList = append(rtoList, rto)
		optionNameList = append(optionNameList, optionName)
	}
	if len(optionNameList) == 0 {
		logger.Infof("table_id [%s] options is null", tableId)
		return nil
	}
	// 判断是否存在
	db := mysql.GetDBSession().DB
	var existOptions []resulttable.ResultTableOption
	if err := resulttable.NewResultTableOptionQuerySet(db).
		TableIDEq(tableId).NameIn(optionNameList...).All(&existOptions); err != nil {
		return err
	}
	if len(existOptions) != 0 {
		var existOptionsNames []string
		for _, o := range existOptions {
			existOptionsNames = append(existOptionsNames, o.Name)
		}
		return errors.Errorf("table_id [%s] already has option [%s]", tableId, strings.Join(existOptionsNames, ","))
	}
	tx := db.Begin()
	for _, option := range rtoList {
		if err := option.Create(tx); err != nil {
			tx.Rollback()
			return err
		}
	}
	tx.Commit()
	logger.Infof("table_id [%s] now has options [%#v]", tableId, options)
	return nil
}
