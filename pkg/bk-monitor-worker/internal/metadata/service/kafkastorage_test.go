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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
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
