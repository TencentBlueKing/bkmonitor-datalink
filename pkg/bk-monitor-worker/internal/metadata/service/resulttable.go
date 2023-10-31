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
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"github.com/pkg/errors"
	"strconv"
	"strings"
	"time"

	"github.com/jinzhu/gorm"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
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
	var storageList []Storage
	// es storage
	var esStorage storage.ESStorage
	if err := storage.NewESStorageQuerySet(mysql.GetDBSession().DB).TableIDEq(r.TableId).One(&esStorage); err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	} else {
		storageList = append(storageList, NewEsStorageSvc(&esStorage))
	}
	// influxdb storage
	var influxdbStorage storage.InfluxdbStorage
	if err := storage.NewInfluxdbStorageQuerySet(mysql.GetDBSession().DB).TableIDEq(r.TableId).One(&influxdbStorage); err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	} else {
		storageList = append(storageList, NewInfluxdbStorageSvc(&influxdbStorage))
	}
	// kafka storage
	var kafkaStorage storage.KafkaStorage
	if err := storage.NewKafkaStorageQuerySet(mysql.GetDBSession().DB).TableIDEq(r.TableId).One(&kafkaStorage); err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	} else {
		storageList = append(storageList, NewKafkaStorageSvc(&kafkaStorage))
	}
	// redis storage
	var redisStorage storage.RedisStorage
	if err := storage.NewRedisStorageQuerySet(mysql.GetDBSession().DB).TableIDEq(r.TableId).One(&redisStorage); err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	} else {
		storageList = append(storageList, NewRedisStorageSvc(&redisStorage))
	}
	// argus storage
	var argusStorage storage.ArgusStorage
	if err := storage.NewArgusStorageQuerySet(mysql.GetDBSession().DB).TableIDEq(r.TableId).One(&argusStorage); err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	} else {
		storageList = append(storageList, NewArgusStorageSvc(&argusStorage))
	}

	return storageList, nil
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
	// 判断label是否真实存在的配置
	count, err := resulttable.NewLabelQuerySet(mysql.GetDBSession().DB).LabelTypeEq(models.LabelTypeResultTable).LabelIdEq(label).Count()
	if err != nil {
		return err
	}
	if count == 0 {
		return errors.New(fmt.Sprintf("label [%s] is not exists as a rt label", label))
	}
	tableId = strings.ToLower(tableId)
	// 判断data_source是否存在
	var ds resulttable.DataSource
	if err := resulttable.NewDataSourceQuerySet(mysql.GetDBSession().DB).BkDataIdEq(bkDataId).One(&ds); err != nil {
		if gorm.IsRecordNotFoundError(err) {
			return errors.New(fmt.Sprintf("bk_data_id [%v] is not exists", bkDataId))
		}
		return err
	}
	count, err = resulttable.NewResultTableQuerySet(mysql.GetDBSession().DB).TableIdEq(tableId).Count()
	if err != nil {
		return err
	}
	if count != 0 {
		return errors.New(fmt.Sprintf("table_id [%s] is already exist", tableId))
	}
	var spaceTypeId, spaceId string
	if bkBizId == 0 {
		ds.IsPlatformDataId = true
		if err := ds.Update(mysql.GetDBSession().DB, resulttable.DataSourceDBSchema.IsPlatformDataId); err != nil {
			return err
		}
	} else {
		// 当业务不为 0 时，进行空间和数据源的关联
		if bkBizId > 0 {
			spaceTypeId = "bkcc"
			spaceId = strconv.Itoa(bkBizId)
		} else {
			var sp space.Space
			if err := space.NewSpaceQuerySet(mysql.GetDBSession().DB).IdEq(-bkBizId).One(&sp); err != nil {
				return err
			}
		}
		count, err := space.NewSpaceDataSourceQuerySet(mysql.GetDBSession().DB).BkDataIdEq(bkDataId).FromAuthorizationEq(false).Count()
		if err != nil {
			return err
		}
		// data id 已有所属记录，则不处理
		if count == 0 {
			sds := space.SpaceDataSource{
				SpaceTypeId:       spaceTypeId,
				SpaceId:           spaceId,
				BkDataId:          bkDataId,
				FromAuthorization: false,
			}
			if err := sds.Create(mysql.GetDBSession().DB); err != nil {
				return errors.Wrapf(err, "create spacedatasource for %v failed, %v", bkDataId, err)
			}
		}
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
		DataLabel:      "",
	}
	if err := rt.Create(mysql.GetDBSession().DB); err != nil {
		return err
	}
	rtSvc := NewResultTableSvc(&rt)
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
	if err := rtSvc.BulkCreateFields(fieldList, false, true); err != nil {
		return err
	}
	dsrt := resulttable.DataSourceResultTable{
		BkDataId:   bkDataId,
		TableId:    tableId,
		Creator:    operator,
		CreateTime: time.Now(),
	}
	// 创建data_id和该结果表的关系
	if err := dsrt.Create(mysql.GetDBSession().DB); err != nil {
		return err
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
		return errors.New(fmt.Sprintf("result_table [%s] schema type is set, no field can be added", r.TableId))
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
func (r ResultTableSvc) CreateStorage(defaultStorage string, isSyncDb bool, StorageConfig map[string]interface{}) error {
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
		return errors.New(fmt.Sprintf("storage [%s] now is not supported", defaultStorage))
	}
	if err := s.CreateTable(r.TableId, isSyncDb, StorageConfig); err != nil {
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
	var dsrt resulttable.DataSourceResultTable
	if err := resulttable.NewDataSourceResultTableQuerySet(mysql.GetDBSession().DB).TableIdEq(r.TableId).One(&dsrt); err != nil {
		return err
	}
	var ds resulttable.DataSource
	if err := resulttable.NewDataSourceQuerySet(mysql.GetDBSession().DB).BkDataIdEq(dsrt.BkDataId).One(&ds); err != nil {
		return err
	}
	if err := NewDataSourceSvc(&ds).RefreshConsulConfig(context.TODO()); err != nil {
		return err
	}
	logger.Infof("table_id [%s] refresh etl config success", r.TableId)
	return nil
}
