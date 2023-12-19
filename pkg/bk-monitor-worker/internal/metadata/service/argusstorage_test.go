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

	"github.com/jinzhu/gorm"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestArgusStorage_ConsulConfig(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	defer db.Close()
	cluster := storage.ClusterInfo{
		ClusterID:   100,
		ClusterName: "argus_storage_100",
		ClusterType: models.StorageTypeArgus,
	}
	db.Delete(&cluster, "cluster_id = ?", cluster.ClusterID)
	err := cluster.Create(db)
	assert.NoError(t, err)
	as := storage.ArgusStorage{
		TableID:          "argus_storage_test",
		StorageClusterID: 123,
		TenantId:         "1",
	}
	svc := NewArgusStorageSvc(&as)
	consulConfig, err := svc.ConsulConfig()
	assert.Error(t, gorm.ErrRecordNotFound, err)
	as.StorageClusterID = cluster.ClusterID
	consulConfig, err = svc.ConsulConfig()
	assert.NoError(t, err)
	assert.Equal(t, as.TenantId, consulConfig.StorageConfig["tenant_id"])
	assert.Equal(t, models.StorageTypeArgus, consulConfig.ClusterType)
	assert.Equal(t, as.StorageClusterID, consulConfig.ClusterConfig.ClusterId)
}
