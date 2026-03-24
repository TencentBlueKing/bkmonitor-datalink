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

func TestEsStorageSvc_ConsulConfig(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	version := "7.2"
	schema := "http"
	clusterInfo := storage.ClusterInfo{
		ClusterID:           3,
		ClusterName:         "es7_cluster",
		ClusterType:         models.StorageTypeES,
		DomainName:          "127.0.0.1",
		Port:                9200,
		ExtranetDomainName:  "",
		ExtranetPort:        0,
		Description:         "default cluster for ES7",
		IsDefaultCluster:    true,
		Username:            "elastic",
		Password:            "8E6lprO6OPiT",
		Version:             &version,
		CustomOption:        `{"bk_biz_id": 2, "hot_warm_config": {"is_enabled": false, "hot_attr_name": "", "hot_attr_value": "", "warm_attr_name": "", "warm_attr_value": ""}, "source_type": "other", "visible_config": {"visible_type": "all_biz", "visible_bk_biz": [], "bk_biz_labels": {}}, "setup_config": {"retention_days_max": 3, "retention_days_default": 1, "number_of_replicas_max": 1, "number_of_replicas_default": 0, "es_shards_default": 1, "es_shards_max": 3}, "admin": ["system"], "description": "", "enable_archive": false, "enable_assessment": true}`,
		Schema:              &schema,
		IsSslVerify:         false,
		SslVerificationMode: "none",
		RegisteredSystem:    "_default",
		GseStreamToId:       -1,
		CreateTime:          time.Now(),
		LastModifyTime:      time.Now(),
		Creator:             "system",
	}

	db := mysql.GetDBSession().DB

	db.AutoMigrate(&storage.ClusterInfo{})

	db.Delete(&clusterInfo, "cluster_id = ?", 3)
	err := clusterInfo.Create(db)
	if err != nil {
		panic(err)
	}

	assert.NoError(t, err)
	ess := &storage.ESStorage{
		TableID:           "es_table_id",
		SliceSize:         1,
		SliceGap:          2,
		Retention:         3,
		WarmPhaseDays:     4,
		WarmPhaseSettings: `{"c":"c"}`,
		TimeZone:          0,
		IndexSettings:     `{"a":"a"}`,
		MappingSettings:   `{"b":"b"}`,
		StorageClusterID:  3,
		DateFormat:        "%Y%m%d",
	}
	svc := NewEsStorageSvc(ess)
	config, err := svc.ConsulConfig()
	assert.NoError(t, err)

	if err != nil {
		panic(err)
	}

	// 判断结构体中 InstanceClusterName 为空
	assert.Equal(t, "", config.ClusterInfoConsulConfig.ClusterConfig.InstanceClusterName)
	// 判断 instance_cluster_name 不存在
	clusterConfig, err := jsonx.MarshalString(config.ClusterInfoConsulConfig.ClusterConfig)
	assert.NoError(t, err)
	var clusterConfigMap map[string]any
	err = jsonx.Unmarshal([]byte(clusterConfig), &clusterConfigMap)
	assert.NoError(t, err)
	_, ok := clusterConfigMap["instance_cluster_name"]
	assert.False(t, ok)

	storageConfigStr, err := jsonx.MarshalString(config.StorageConfig)
	assert.NoError(t, err)
	assert.JSONEq(t, storageConfigStr, `{"base_index":"es_table_id","date_format":"%Y%m%d","index_datetime_format":"write_20060102","index_datetime_timezone":0,"index_settings":{"a":"a"},"mapping_settings":{"b":"b"},"retention":3,"slice_gap":2,"slice_size":1,"warm_phase_days":4,"warm_phase_settings":{"c":"c"}}`)
}
