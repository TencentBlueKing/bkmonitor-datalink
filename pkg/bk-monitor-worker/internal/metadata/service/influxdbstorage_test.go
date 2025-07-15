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

func TestInfluxdbStorageSvc_ConsulConfig(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")

	clusterInfo := storage.ClusterInfo{
		ClusterID:        2,
		ClusterName:      "cluster_name2",
		ClusterType:      models.StorageTypeInfluxdb,
		CreateTime:       time.Now(),
		LastModifyTime:   time.Now(),
		RegisteredSystem: "_default",
		Creator:          "system",
		GseStreamToId:    -1,
	}
	db := mysql.GetDBSession().DB
	db.Delete(&storage.ClusterInfo{})
	err := clusterInfo.Create(db)
	assert.NoError(t, err)

	instanceClusterName := "name"
	p := storage.InfluxdbProxyStorage{
		ProxyClusterId:      2,
		InstanceClusterName: instanceClusterName,
		ServiceName:         "svc_name",
		IsDefault:           true,
	}
	db.Delete(&p, "instance_cluster_name = ?", p.InstanceClusterName)
	err = p.Create(db)
	assert.NoError(t, err)
	is := &storage.InfluxdbStorage{
		TableID:                "influxdb_table_id",
		StorageClusterID:       2,
		RealTableName:          "real_table_nm",
		Database:               "db",
		SourceDurationTime:     "1",
		DownSampleTable:        "dstb",
		DownSampleGap:          "dsg",
		DownSampleDurationTime: "dsdt",
		ProxyClusterName:       "default",
		UseDefaultRp:           false,
		PartitionTag:           "",
		VmTableId:              "",
		InfluxdbProxyStorageId: p.ID,
	}

	svc := NewInfluxdbStorageSvc(is)
	config, err := svc.ConsulConfig()
	assert.NoError(t, err)

	// 判断 instance_cluster_name 存在
	assert.Equal(t, instanceClusterName, config.ClusterInfoConsulConfig.ClusterConfig.InstanceClusterName)

	storageConfigStr, err := jsonx.MarshalString(config.StorageConfig)
	assert.NoError(t, err)
	assert.JSONEq(t, storageConfigStr, `{"database":"db","real_table_name":"real_table_nm","retention_policy_name":"bkmonitor_rp_influxdb_table_id"}`)

	is.UseDefaultRp = true
	config, err = svc.ConsulConfig()
	assert.NoError(t, err)
	storageConfigStr, err = jsonx.MarshalString(config.StorageConfig)
	assert.NoError(t, err)
	assert.JSONEq(t, storageConfigStr, `{"database":"db","real_table_name":"real_table_nm","retention_policy_name":""}`)
}
