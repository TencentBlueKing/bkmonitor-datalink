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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestEsSnapshotRestoreSvc_DeleteRestoreIndices(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	essr := storage.EsSnapshotRestore{
		RestoreID:     12122,
		TableID:       "test_rt_for_expired_restore",
		StartTime:     time.Now().Add(-10 * time.Hour),
		EndTime:       time.Now().Add(-5 * time.Hour),
		ExpiredTime:   time.Now().Add(-time.Hour),
		ExpiredDelete: false,
		Indices:       "index_r1,index_r2,index_r3",
		IsDeleted:     false,
	}
	db.Delete(&essr, "table_id = ?", essr.TableID)
	err := essr.Create(db)
	assert.NoError(t, err)
	cluster := storage.ClusterInfo{
		ClusterID:        99,
		ClusterType:      models.StorageTypeES,
		Version:          "7.10.1",
		Schema:           "https",
		DomainName:       "example.com",
		Port:             9200,
		Username:         "elastic",
		Password:         "123456",
		CreateTime:       time.Now(),
		LastModifyTime:   time.Now(),
		RegisteredSystem: "_default",
		Creator:          "system",
		GseStreamToId:    -1,
	}
	db.Delete(&cluster, "cluster_id = ?", cluster.ClusterID)
	err = cluster.Create(db)
	assert.NoError(t, err)
	ess := storage.ESStorage{
		TableID:          essr.TableID,
		StorageClusterID: cluster.ClusterID,
	}
	db.Delete(&ess, "table_id = ?", ess.TableID)
	err = ess.Create(db)

	//gomonkey.ApplyFunc(ClusterInfoSvc.GetESClient, func(svc ClusterInfoSvc, ctx context.Context) (*elasticsearch.Elasticsearch, error) {
	//	return mockerClient, nil
	//})
	assert.NoError(t, err)
	svc := NewEsSnapshotRestoreSvc(&essr)
	err = svc.DeleteRestoreIndices(context.Background())
	assert.NoError(t, err)
}
