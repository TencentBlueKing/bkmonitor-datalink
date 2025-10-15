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
	"github.com/jinzhu/gorm"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
)

// ResultTableSvc result table service
type ResultTableSvc struct {
	*resulttable.ResultTable
}

func NewResultTableSvc(obj *resulttable.ResultTable) ResultTableSvc {
	return ResultTableSvc{
		ResultTable: obj,
	}
}

// RealStorageList 获取结果表的所有实际存储对象
func (r ResultTableSvc) RealStorageList() ([]Storage, error) {
	db := mysql.GetDBSession().DB
	var storageList []Storage
	// es storage
	var esStorage storage.ESStorage
	if err := storage.NewESStorageQuerySet(db).TableIDEq(r.TableId).One(&esStorage); err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	} else {
		storageList = append(storageList, NewEsStorageSvc(&esStorage))
	}
	// influxdb storage
	var influxdbStorage storage.InfluxdbStorage
	if err := storage.NewInfluxdbStorageQuerySet(db).TableIDEq(r.TableId).One(&influxdbStorage); err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	} else {
		storageList = append(storageList, NewInfluxdbStorageSvc(&influxdbStorage))
	}
	// kafka storage
	var kafkaStorage storage.KafkaStorage
	if err := storage.NewKafkaStorageQuerySet(db).TableIDEq(r.TableId).One(&kafkaStorage); err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	} else {
		storageList = append(storageList, NewKafkaStorageSvc(&kafkaStorage))
	}

	return storageList, nil
}

// GetTableIdCutter 批量获取结果表是否禁用切分模块
func (r ResultTableSvc) GetTableIdCutter(bkTenantId string, tableIdList []string) (map[string]bool, error) {
	db := mysql.GetDBSession().DB
	var dsrtList []resulttable.DataSourceResultTable
	for _, chunkTableIds := range slicex.ChunkSlice(tableIdList, 0) {
		var tempList []resulttable.DataSourceResultTable
		if err := resulttable.NewDataSourceResultTableQuerySet(db).Select(resulttable.DataSourceResultTableDBSchema.TableId, resulttable.DataSourceResultTableDBSchema.BkDataId).BkTenantIdEq(bkTenantId).TableIdIn(chunkTableIds...).All(&tempList); err != nil {
			return nil, err
		}
		dsrtList = append(dsrtList, tempList...)
	}

	tableIdDataIdMap := make(map[string]uint)
	var dataIdList []uint
	for _, dsrt := range dsrtList {
		tableIdDataIdMap[dsrt.TableId] = dsrt.BkDataId
		dataIdList = append(dataIdList, dsrt.BkDataId)
	}
	dataIdList = slicex.RemoveDuplicate(&dataIdList)
	var dsoList []resulttable.DataSourceOption
	for _, chunkDataIds := range slicex.ChunkSlice(dataIdList, 0) {
		var tempList []resulttable.DataSourceOption
		if err := resulttable.NewDataSourceOptionQuerySet(db).Select(resulttable.DataSourceOptionDBSchema.BkDataId, resulttable.DataSourceOptionDBSchema.Value).BkTenantIdEq(bkTenantId).BkDataIdIn(chunkDataIds...).NameEq(models.OptionDisableMetricCutter).All(&tempList); err != nil {
			return nil, err
		}
		dsoList = append(dsoList, tempList...)
	}
	dataIdOptionMap := make(map[uint]bool)
	for _, dso := range dsoList {
		var value bool
		if err := jsonx.UnmarshalString(dso.Value, &value); err != nil {
			dataIdOptionMap[dso.BkDataId] = false
			continue
		}
		dataIdOptionMap[dso.BkDataId] = value
	}

	// 组装数据
	tableIdCutter := make(map[string]bool)
	for _, tableId := range tableIdList {
		bkdataId, ok := tableIdDataIdMap[tableId]
		if !ok {
			// 默认为 False
			tableIdCutter[tableId] = false
			continue
		}
		tableIdCutter[tableId] = dataIdOptionMap[bkdataId]
	}

	return tableIdCutter, nil
}
