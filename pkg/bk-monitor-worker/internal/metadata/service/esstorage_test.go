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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestEsStorageSvc_ConsulConfig(t *testing.T) {
	config.FilePath = "../../../bmw.yaml"
	mocker.PatchDBSession()

	clusterInfo := storage.ClusterInfo{
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
	mysql.GetDBSession().DB.Delete(&clusterInfo, "cluster_id = ?", 99)
	err := clusterInfo.Create(mysql.GetDBSession().DB)
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
		StorageClusterID:  99,
		DateFormat:        "%Y%m%d",
	}
	svc := NewEsStorageSvc(ess)
	config, err := svc.ConsulConfig()
	assert.NoError(t, err)
	storageConfigStr, err := jsonx.MarshalString(config.StorageConfig)
	assert.NoError(t, err)
	assert.JSONEq(t, storageConfigStr, `{"base_index":"es_table_id","date_format":"%Y%m%d","index_datetime_format":"write_20060102","index_datetime_timezone":0,"index_settings":{"a":"a"},"mapping_settings":{"b":"b"},"retention":3,"slice_gap":2,"slice_size":1,"warm_phase_days":4,"warm_phase_settings":{"c":"c"}}`)

}
