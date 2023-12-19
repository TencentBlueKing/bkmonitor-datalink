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

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/optionx"
)

func TestKafkaStorageSvc_ConsulConfig(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")

	clusterInfo := storage.ClusterInfo{
		ClusterID:        99,
		ClusterType:      models.StorageTypeKafka,
		CreateTime:       time.Now(),
		LastModifyTime:   time.Now(),
		RegisteredSystem: "_default",
		Creator:          "system",
		GseStreamToId:    -1,
	}
	db := mysql.GetDBSession().DB
	defer db.Close()
	db.Delete(&clusterInfo, "cluster_id = ?", 99)
	err := clusterInfo.Create(db)
	assert.NoError(t, err)
	ks := &storage.KafkaStorage{
		TableID:          "kafka_table_id",
		Topic:            "kafka_topic",
		Partition:        1,
		StorageClusterID: 99,
		Retention:        3,
	}

	svc := NewKafkaStorageSvc(ks)
	config, err := svc.ConsulConfig()
	assert.NoError(t, err)
	storageConfigStr, err := jsonx.MarshalString(config.StorageConfig)
	assert.NoError(t, err)
	assert.JSONEq(t, storageConfigStr, `{"partition":1,"topic":"kafka_topic"}`)

}

func TestKafkaStorageSvc_CreateTable(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")

	db := mysql.GetDBSession().DB
	tableId := "table_id_for_kafka_create_table"
	db.Delete(&storage.KafkaStorage{}, "table_id = ?", tableId)
	err := NewKafkaStorageSvc(nil).CreateTable(tableId, false, optionx.NewOptions(nil))
	assert.NoError(t, err)
	var record storage.KafkaStorage
	err = storage.NewKafkaStorageQuerySet(db).TableIDEq(tableId).One(&record)
	assert.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("0%s_storage__%s", config.BkApiAppCode, tableId), record.Topic)
	assert.Equal(t, int64(1800000), record.Retention)
	assert.Equal(t, uint(1), record.Partition)
}
