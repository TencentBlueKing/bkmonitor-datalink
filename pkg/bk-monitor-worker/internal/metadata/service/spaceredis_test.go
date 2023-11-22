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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/bcs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestSpacePusher_getDataIdByCluster(t *testing.T) {
	defer mocker.PatchDBSession().Reset()
	cluster := &bcs.BCSClusterInfo{
		ClusterID:          "BCS-K8S-00000",
		BCSApiClusterId:    "BCS-K8S-00000",
		BkBizId:            2,
		BkCloudId:          new(int),
		ProjectId:          "xxxxx",
		Status:             models.BcsClusterStatusRunning,
		DomainName:         "www.xxx.com",
		Port:               80,
		ServerAddressPath:  "clusters",
		ApiKeyType:         "authorization",
		ApiKeyContent:      "xxxxxx",
		ApiKeyPrefix:       "Bearer",
		IsSkipSslVerify:    true,
		K8sMetricDataID:    1572864,
		CustomMetricDataID: 1572865,
		K8sEventDataID:     1572866,
		Creator:            "system",
		CreateTime:         time.Now(),
		LastModifyTime:     time.Now(),
		LastModifyUser:     "system",
	}
	mysql.GetDBSession().DB.Delete(cluster, "cluster_id=?", cluster.ClusterID)
	err := cluster.Create(mysql.GetDBSession().DB)
	assert.NoError(t, err)
	pusher := NewSpacePusher()
	clusterDataIdMap, err := pusher.getDataIdByCluster(cluster.ClusterID)
	assert.NoError(t, err)
	assert.ElementsMatch(t, clusterDataIdMap[cluster.ClusterID], []uint{cluster.K8sMetricDataID, cluster.CustomMetricDataID})
}

func TestSpacePusher_isNeedAddFilter(t *testing.T) {
	var defaultDataId uint = 1234567

	defer mocker.PatchDBSession().Reset()
	ds := resulttable.DataSource{
		BkDataId:         defaultDataId,
		Token:            "xxx",
		DataName:         "test_ds1",
		DataDescription:  "test_ds1",
		EtlConfig:        "",
		IsPlatformDataId: false,
		SpaceTypeId:      "bkcc",
		SpaceUid:         "bkcc__2",
	}
	mysql.GetDBSession().DB.Delete(ds)
	err := ds.Create(mysql.GetDBSession().DB)
	assert.NoError(t, err)

	pusher := NewSpacePusher()
	// bk_traditional_measurement
	should, err := pusher.isNeedAddFilter(models.MeasurementTypeBkTraditional, "bkcc", "2", defaultDataId)
	assert.NoError(t, err)
	assert.True(t, should)
	// bk_exporter same space_uid
	should, err = pusher.isNeedAddFilter(models.MeasurementTypeBkExporter, "bkcc", "2", defaultDataId)
	assert.NoError(t, err)
	assert.False(t, should)
	// bk_exporter diff space_uid
	should, err = pusher.isNeedAddFilter(models.MeasurementTypeBkExporter, "bkcc", "3", defaultDataId)
	assert.NoError(t, err)
	assert.True(t, should)
	// not found datasource
	should, err = pusher.isNeedAddFilter(models.MeasurementTypeBkExporter, "bkcc", "3", defaultDataId+100)
	assert.NoError(t, err)
	assert.True(t, should)

}

func TestSpacePusher_getData(t *testing.T) {
	defer mocker.PatchDBSession().Reset()
	sdr := space.SpaceDataSource{
		SpaceTypeId:       "bkcc",
		SpaceId:           "123",
		BkDataId:          1002,
		FromAuthorization: false,
	}
	mysql.GetDBSession().DB.Delete(sdr, "space_type_id = ? and space_id = ?", sdr.SpaceTypeId, sdr.SpaceId)
	err := sdr.Create(mysql.GetDBSession().DB)
	assert.NoError(t, err)
	pusher := NewSpacePusher()
	err = pusher.getData("bkcc", "123", "", nil)
	assert.NoError(t, err)
	assert.NotNil(t, pusher.tableDataIdMap)
	assert.NotNil(t, pusher.tableIdTableMap)
	assert.NotNil(t, pusher.measurementTypeMap)
	assert.NotNil(t, pusher.tableIdList)
	assert.NotNil(t, pusher.tableFieldMap)
	fieldValueMap, err := pusher.composeBizId("bkcc", "123", "bkcc", "123", nil, true)
	assert.NoError(t, err)
	assert.Equal(t, len(pusher.tableDataIdMap), len(fieldValueMap))
}
