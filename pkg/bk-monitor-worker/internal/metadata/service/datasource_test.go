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
	"fmt"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/jinzhu/gorm"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

func TestDataSourceSvc_ToJson(t *testing.T) {
	config.InitConfig()
	patchDBSession := gomonkey.ApplyFunc(mysql.GetDBSession, func() *mysql.DBSession {
		db, err := gorm.Open("mysql", fmt.Sprintf(
			"%s:%s@tcp(%s:%s)/%s?&parseTime=True&loc=Local",
			config.TestStorageMysqlUser,
			config.TestStorageMysqlPassword,
			config.TestStorageMysqlHost,
			config.TestStorageMysqlPort,
			config.TestStorageMysqlDbName,
		))
		assert.Nil(t, err)
		return &mysql.DBSession{DB: db}
	})
	defer patchDBSession.Reset()

	ds := &resulttable.DataSource{
		BkDataId:          99999,
		Token:             "9e679720296f4ad7abf5ad95ac0acbdf",
		DataName:          "test_data_source",
		DataDescription:   "data source for test",
		MqClusterId:       1,
		MqConfigId:        21,
		EtlConfig:         "bk_standard_v2_event",
		IsCustomSource:    true,
		Creator:           "admin",
		CreateTime:        time.Time{},
		LastModifyUser:    "admin",
		LastModifyTime:    time.Time{},
		TypeLabel:         "event",
		SourceLabel:       "bk_monitor",
		IsEnable:          true,
		TransferClusterId: "default",
		SpaceTypeId:       "all",
		SpaceUid:          "",
	}
	kafkaTopic := &storage.KafkaTopicInfo{
		BkDataId:  ds.BkDataId,
		Topic:     "0bkmonitor_999990",
		Partition: 1,
	}
	// 初始化数据
	mysql.GetDBSession().DB.Where("bk_data_id=?", kafkaTopic.BkDataId).Delete(&kafkaTopic)
	kafkaTopic.Create(mysql.GetDBSession().DB)
	ds.Delete(mysql.GetDBSession().DB)
	err := ds.Create(mysql.GetDBSession().DB)
	assert.Nil(t, err)

	dsSvc := NewDataSourceSvc(ds)
	dsConfig, err := dsSvc.ToJson(true, true)
	assert.Nil(t, err)
	marshalString, err := jsonx.MarshalString(dsConfig)
	assert.Nil(t, err)
	assert.True(t, len(marshalString) != 0)
}
