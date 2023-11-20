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

	"github.com/jinzhu/gorm"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
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
