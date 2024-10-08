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
	"strconv"
	"strings"
	"time"

	"github.com/jinzhu/gorm"
	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/diffutil"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/optionx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
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
	// redis storage
	var redisStorage storage.RedisStorage
	if err := storage.NewRedisStorageQuerySet(db).TableIDEq(r.TableId).One(&redisStorage); err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	} else {
		storageList = append(storageList, NewRedisStorageSvc(&redisStorage))
	}
	// argus storage
	var argusStorage storage.ArgusStorage
	if err := storage.NewArgusStorageQuerySet(db).TableIDEq(r.TableId).One(&argusStorage); err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	} else {
		storageList = append(storageList, NewArgusStorageSvc(&argusStorage))
	}

	return storageList, nil
}

// 判断label是否真实存在的配置，不存在则返回err
func (r ResultTableSvc) preCheckLabel(label string) error {
	count, err := resulttable.NewLabelQuerySet(mysql.GetDBSession().DB).LabelTypeEq(models.LabelTypeResultTable).LabelIdEq(label).Count()
	if err != nil {
		return err
	}
	if count == 0 {
		return errors.Errorf("label [%s] is not exists as a rt label", label)
	}
	return nil
}

// 获取dataSource，不存在则返回err
func (r ResultTableSvc) getDataSource(bkDataId uint) (*resulttable.DataSource, error) {
	var ds resulttable.DataSource
	if err := resulttable.NewDataSourceQuerySet(mysql.GetDBSession().DB).BkDataIdEq(bkDataId).One(&ds); err != nil {
		if gorm.IsRecordNotFoundError(err) {
			return nil, errors.Errorf("bk_data_id [%v] is not exists", bkDataId)
		}
		return nil, err
	}
	return &ds, nil
}

// 判断rt是否已经存在，存在则返回err
func (r ResultTableSvc) preCheckResultTable(tableId string) error {
	count, err := resulttable.NewResultTableQuerySet(mysql.GetDBSession().DB).TableIdEq(tableId).Count()
	if err != nil {
		return err
	}
	if count != 0 {
		return errors.Errorf("table_id [%s] is already exist", tableId)
	}
	return nil
}

// 根据业务id处理DS
func (r ResultTableSvc) dealDataSourceByBizId(bkBizId int, ds *resulttable.DataSource) error {
	db := mysql.GetDBSession().DB
	var spaceTypeId, spaceId string
	// 业务为0时更新数据源为平台级
	if bkBizId == 0 {
		ds.IsPlatformDataId = true
		if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "discover_bcs_clusters") {
			logger.Info(diffutil.BuildLogStr("discover_bcs_clusters", diffutil.OperatorTypeDBUpdate, diffutil.NewSqlBody(ds.TableName(), map[string]interface{}{
				resulttable.DataSourceDBSchema.IsPlatformDataId.String(): ds.IsPlatformDataId,
			}), ""))
		} else {
			if err := ds.Update(db, resulttable.DataSourceDBSchema.IsPlatformDataId); err != nil {
				return err
			}
		}
	} else {
		// 当业务不为 0 时，进行空间和数据源的关联
		if bkBizId > 0 {
			spaceTypeId = "bkcc"
			spaceId = strconv.Itoa(bkBizId)
		} else {
			var sp space.Space
			if err := space.NewSpaceQuerySet(db).IdEq(-bkBizId).One(&sp); err != nil {
				return err
			}
			spaceTypeId = sp.SpaceTypeId
			spaceId = sp.SpaceId
		}
		count, err := space.NewSpaceDataSourceQuerySet(db).BkDataIdEq(ds.BkDataId).FromAuthorizationEq(false).Count()
		if err != nil {
			return err
		}
		// data id 已有所属记录，则不处理
		if count == 0 {
			sds := space.SpaceDataSource{
				SpaceTypeId:       spaceTypeId,
				SpaceId:           spaceId,
				BkDataId:          ds.BkDataId,
				FromAuthorization: false,
			}
			if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "discover_bcs_clusters") {
				logger.Info(diffutil.BuildLogStr("discover_bcs_clusters", diffutil.OperatorTypeDBCreate, diffutil.NewSqlBody(sds.TableName(), map[string]interface{}{
					space.SpaceDataSourceDBSchema.SpaceTypeId.String():       sds.SpaceTypeId,
					space.SpaceDataSourceDBSchema.SpaceId.String():           sds.SpaceId,
					space.SpaceDataSourceDBSchema.BkDataId.String():          sds.BkDataId,
					space.SpaceDataSourceDBSchema.FromAuthorization.String(): sds.FromAuthorization,
				}), ""))
			} else {
				if err := sds.Create(db); err != nil {
					return errors.Wrapf(err, "create spacedatasource for %v failed", ds.BkDataId)
				}
			}
		}
	}
	return nil
}

func (r ResultTableSvc) CreateResultTable(
	bkDataId uint,
	bkBizId int,
	tableId string,
	tableNameZh string,
	isCustomTable bool,
	schemaType string,
	operator string,
	defaultStorage string,
	defaultStorageConfig map[string]interface{},
	fieldList []map[string]interface{},
	isTimeFieldOnly bool,
	timeOption map[string]interface{},
	label string,
	option map[string]interface{},
) error {
	tableId = strings.ToLower(tableId)
	// 判断label是否真实存在的配置
	if err := r.preCheckLabel(label); err != nil {
		return err
	}
	// 获取dataSource
	ds, err := r.getDataSource(bkDataId)
	if err != nil {
		return err
	}
	// 若rt已经存在，存在则返回err
	if err := r.preCheckResultTable(tableId); err != nil {
		return err
	}
	// 根据业务id处理DataSource
	if err := r.dealDataSourceByBizId(bkBizId, ds); err != nil {
		return err
	}

	// 创建逻辑结果表内容
	rt := resulttable.ResultTable{
		TableId:        tableId,
		TableNameZh:    tableNameZh,
		IsCustomTable:  isCustomTable,
		SchemaType:     schemaType,
		DefaultStorage: defaultStorage,
		Creator:        operator,
		CreateTime:     time.Now(),
		LastModifyUser: operator,
		LastModifyTime: time.Now(),
		BkBizId:        bkBizId,
		IsDeleted:      false,
		Label:          label,
		IsEnable:       true,
		DataLabel:      nil,
	}
	db := mysql.GetDBSession().DB
	if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "discover_bcs_clusters") {
		logger.Info(diffutil.BuildLogStr("discover_bcs_clusters", diffutil.OperatorTypeDBCreate, diffutil.NewSqlBody(rt.TableName(), map[string]interface{}{
			resulttable.ResultTableDBSchema.TableId.String():        rt.TableId,
			resulttable.ResultTableDBSchema.TableNameZh.String():    rt.TableNameZh,
			resulttable.ResultTableDBSchema.IsCustomTable.String():  rt.IsCustomTable,
			resulttable.ResultTableDBSchema.SchemaType.String():     rt.SchemaType,
			resulttable.ResultTableDBSchema.DefaultStorage.String(): rt.DefaultStorage,
			resulttable.ResultTableDBSchema.BkBizId.String():        rt.BkBizId,
			resulttable.ResultTableDBSchema.IsDeleted.String():      rt.IsDeleted,
			resulttable.ResultTableDBSchema.Label.String():          rt.Label,
			resulttable.ResultTableDBSchema.IsEnable.String():       rt.IsEnable,
		}), ""))
	} else {
		if err := rt.Create(db); err != nil {
			return err
		}
	}

	// 创建结果表的option内容如果option为非空
	if err := NewResultTableOptionSvc(nil).BulkCreateOptions(tableId, option, operator); err != nil {
		return err
	}

	// 创建新的字段信息，同时追加默认的字段
	if err := NewResultTableFieldSvc(nil).BulkCreateDefaultFields(tableId, timeOption, isTimeFieldOnly); err != nil {
		return err
	}

	// 批量创建 field 数据
	for _, field := range fieldList {
		var isConfigByUser bool
		isConfigByUserInterface, ok := field["is_config_by_user"]
		if ok {
			isConfigByUser = isConfigByUserInterface.(bool)
		} else {
			if operator == "system" {
				isConfigByUser = true
			}
		}
		field["operator"] = operator
		field["is_config_by_user"] = isConfigByUser
	}
	rtSvc := NewResultTableSvc(&rt)
	if err := rtSvc.BulkCreateFields(fieldList, false, true); err != nil {
		return err
	}

	// 创建data_id和该结果表的关系
	dsrt := resulttable.DataSourceResultTable{
		BkDataId:   bkDataId,
		TableId:    tableId,
		Creator:    operator,
		CreateTime: time.Now(),
	}
	if cfg.BypassSuffixPath != "" && !slicex.IsExistItem(cfg.SkipBypassTasks, "discover_bcs_clusters") {
		logger.Info(diffutil.BuildLogStr("discover_bcs_clusters", diffutil.OperatorTypeDBCreate, diffutil.NewSqlBody(dsrt.TableName(), map[string]interface{}{
			resulttable.DataSourceResultTableDBSchema.BkDataId.String(): dsrt.BkDataId,
			resulttable.DataSourceResultTableDBSchema.TableId.String():  dsrt.TableId,
		}), ""))
	} else {
		if err := dsrt.Create(db); err != nil {
			return err
		}
	}

	logger.Infof("result_table [%s] now has relate to bk_data [%v]", tableId, bkDataId)

	// 创建实际结果表
	if err := rtSvc.CreateStorage(rt.DefaultStorage, true, defaultStorageConfig); err != nil {
		return err
	}
	logger.Infof("result_table [%s] has create real storage on type [%s]", tableId, rt.DefaultStorage)

	// 更新数据写入 consul
	if err := rtSvc.RefreshEtlConfig(); err != nil {
		return err
	}
	return nil
}

// BulkCreateFields 批量创建新的字段
func (r ResultTableSvc) BulkCreateFields(fieldList []map[string]interface{}, isEtlRefresh bool, isForceAdd bool) error {
	if !isForceAdd && r.SchemaType == models.ResultTableSchemaTypeFixed {
		return errors.Errorf("result_table [%s] schema type is set, no field can be added", r.TableId)
	}
	if err := NewResultTableFieldSvc(nil).BulkCreateFields(r.TableId, fieldList); err != nil {
		return err
	}
	if isEtlRefresh {
		if err := r.RefreshEtlConfig(); err != nil {
			return err
		}
	}
	return nil
}

// CreateStorage 创建结果表的一个实际存储
func (r ResultTableSvc) CreateStorage(defaultStorage string, isSyncDb bool, storageConfig map[string]interface{}) error {
	var s Storage
	switch defaultStorage {
	case models.StorageTypeES:
		s = NewEsStorageSvc(nil)
	case models.StorageTypeInfluxdb:
		s = NewInfluxdbStorageSvc(nil)
	case models.StorageTypeRedis:
		s = NewRedisStorageSvc(nil)
	case models.StorageTypeKafka:
		s = NewKafkaStorageSvc(nil)
	case models.StorageTypeArgus:
		s = NewArgusStorageSvc(nil)
	default:
		return errors.Errorf("storage [%s] now is not supported", defaultStorage)
	}
	if err := s.CreateTable(r.TableId, isSyncDb, optionx.NewOptions(storageConfig)); err != nil {
		return err
	}
	logger.Infof("result_table [%s] has create real storage on type [%s]", r.TableId, defaultStorage)

	if isSyncDb {
		if err := r.RefreshEtlConfig(); err != nil {
			return err
		}
	}
	return nil
}

// RefreshEtlConfig 更新ETL配置，确保其符合当前数据库配置
func (r ResultTableSvc) RefreshEtlConfig() error {
	logger.Infof("RefreshEtlConfig:table_id [%s] refresh etl config start", r.TableId)
	db := mysql.GetDBSession().DB
	var dsrt resulttable.DataSourceResultTable
	if err := resulttable.NewDataSourceResultTableQuerySet(db).TableIdEq(r.TableId).One(&dsrt); err != nil {
		logger.Errorf("RefreshEtlConfig:table_id [%s] refresh etl config error: %s", r.TableId, err)
		return err
	}
	var ds resulttable.DataSource
	if err := resulttable.NewDataSourceQuerySet(db).BkDataIdEq(dsrt.BkDataId).One(&ds); err != nil {
		return err
	}
	if err := NewDataSourceSvc(&ds).RefreshConsulConfig(context.TODO()); err != nil {
		return err
	}
	logger.Infof("RefreshEtlConfig:table_id [%s] refresh etl config success", r.TableId)
	return nil
}

// IsDisableMetricCutter 获取结果表是否禁用切分模块
func (r ResultTableSvc) IsDisableMetricCutter(tableId string) (bool, error) {
	db := mysql.GetDBSession().DB
	var dsrt resulttable.DataSourceResultTable
	if err := resulttable.NewDataSourceResultTableQuerySet(db).TableIdEq(tableId).One(&dsrt); err != nil {
		if gorm.IsRecordNotFoundError(err) {
			return false, nil
		} else {
			return false, err
		}
	}
	var dso resulttable.DataSourceOption
	if err := resulttable.NewDataSourceOptionQuerySet(db).BkDataIdEq(dsrt.BkDataId).NameEq(models.OptionDisableMetricCutter).One(&dso); err != nil {
		if gorm.IsRecordNotFoundError(err) {
			return false, nil
		} else {
			return false, err
		}
	}
	var value bool
	if err := jsonx.UnmarshalString(dso.Value, &value); err != nil {
		return false, err
	}
	return value, nil
}

// GetTableIdCutter 批量获取结果表是否禁用切分模块
func (r ResultTableSvc) GetTableIdCutter(tableIdList []string) (map[string]bool, error) {
	db := mysql.GetDBSession().DB
	var dsrtList []resulttable.DataSourceResultTable
	for _, chunkTableIds := range slicex.ChunkSlice(tableIdList, 0) {
		var tempList []resulttable.DataSourceResultTable
		if err := resulttable.NewDataSourceResultTableQuerySet(db).Select(resulttable.DataSourceResultTableDBSchema.TableId, resulttable.DataSourceResultTableDBSchema.BkDataId).TableIdIn(chunkTableIds...).All(&tempList); err != nil {
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
		if err := resulttable.NewDataSourceOptionQuerySet(db).Select(resulttable.DataSourceOptionDBSchema.BkDataId, resulttable.DataSourceOptionDBSchema.Value).BkDataIdIn(chunkDataIds...).NameEq(models.OptionDisableMetricCutter).All(&tempList); err != nil {
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
