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

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/bcs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestVmUtils_getDataTypeCluster(t *testing.T) {
	defer mocker.PatchDBSession().Reset()
	cluster := bcs.BCSClusterInfo{
		ClusterID:          "",
		K8sMetricDataID:    299991,
		CustomMetricDataID: 299992,
	}
	mysql.GetDBSession().DB.Delete(&cluster, "K8sMetricDataID = ? or CustomMetricDataID = ?", cluster.K8sMetricDataID, cluster.CustomMetricDataID)
	err := cluster.Create(mysql.GetDBSession().DB)
	assert.NoError(t, err)
	dataMap, err := NewVmUtils().getDataTypeCluster(299991)
	assert.NoError(t, err)
	assert.Equal(t, models.VmDataTypeUserCustom, dataMap["data_type"])
	assert.Equal(t, "", dataMap["bcs_cluster_id"])

	cluster.ClusterID = "test_cluster_id"
	err = cluster.Update(mysql.GetDBSession().DB, bcs.BCSClusterInfoDBSchema.ClusterID)
	assert.NoError(t, err)
	dataMap, err = NewVmUtils().getDataTypeCluster(299991)
	assert.Equal(t, models.VmDataTypeBcsClusterK8s, dataMap["data_type"])
	assert.Equal(t, "test_cluster_id", dataMap["bcs_cluster_id"])

	dataMap, err = NewVmUtils().getDataTypeCluster(299992)
	assert.Equal(t, models.VmDataTypeBcsClusterCustom, dataMap["data_type"])
	assert.Equal(t, "test_cluster_id", dataMap["bcs_cluster_id"])
}

func TestVmUtils_getVmCluster(t *testing.T) {
	defer mocker.PatchDBSession().Reset()
	cluster := storage.ClusterInfo{
		ClusterName:      "testVmCluster",
		ClusterType:      models.StorageTypeVM,
		IsDefaultCluster: true,
	}
	mysql.GetDBSession().DB.Delete(&cluster, "cluster_type = ? and is_default_cluster = ? or cluster_name = ?", cluster.ClusterType, cluster.IsDefaultCluster, cluster.ClusterName)
	err := cluster.Create(mysql.GetDBSession().DB)
	assert.NoError(t, err)
	c, err := NewVmUtils().getVmCluster("", "", 0)
	assert.NoError(t, err)
	assert.Equal(t, cluster.ClusterID, c.ClusterID)

	c2, err := NewVmUtils().getVmCluster("", "", cluster.ClusterID)
	assert.NoError(t, err)
	assert.Equal(t, cluster.ClusterID, c2.ClusterID)

	svi := space.SpaceVmInfo{
		SpaceType:   "bkcc",
		SpaceID:     "123",
		VMClusterID: cluster.ClusterID,
	}
	mysql.GetDBSession().DB.Delete(&svi, "space_type = ? and space_id = ?", "bkcc", "123")
	err = svi.Create(mysql.GetDBSession().DB)
	assert.NoError(t, err)
	c3, err := NewVmUtils().getVmCluster("bkcc", "123", 0)
	assert.NoError(t, err)
	assert.Equal(t, cluster.ClusterID, c3.ClusterID)
}

func TestVmUtils_getBkbaseDataNameAndTopic(t *testing.T) {
	type args struct {
		tableId string
	}
	tests := []struct {
		name  string
		args  args
		want  string
		want1 string
	}{
		{name: "a-b.c--d.__default__", args: args{tableId: "a-b.c--d.__default__"}, want: "vm_a_b_c_d", want1: fmt.Sprintf("%s%v", "vm_a_b_c_d", cfg.GlobalDefaultBkdataBizId)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := VmUtils{}
			got, got1 := s.getBkbaseDataNameAndTopic(tt.args.tableId)
			assert.Equalf(t, tt.want, got, "getBkbaseDataNameAndTopic(%v)", tt.args.tableId)
			assert.Equalf(t, tt.want1, got1, "getBkbaseDataNameAndTopic(%v)", tt.args.tableId)
		})
	}
}

func TestVmUtils_getTimestampLen(t *testing.T) {
	defer mocker.PatchDBSession().Reset()
	ds := resulttable.DataSource{
		BkDataId: 198877,
	}
	mysql.GetDBSession().DB.Delete(&ds, "bk_data_id = ?", ds.BkDataId)
	err := ds.Create(mysql.GetDBSession().DB)
	assert.NoError(t, err)
	// default
	timestampLen, err := NewVmUtils().getTimestampLen(0, "")
	assert.NoError(t, err)
	assert.Equal(t, models.TimeStampLenMillisecondLen, timestampLen)
	// NSTimestampDataId
	timestampLen, err = NewVmUtils().getTimestampLen(1100007, "")
	assert.NoError(t, err)
	assert.Equal(t, models.TimeStampLenNanosecondLen, timestampLen)
	// SecondEtl
	timestampLen, err = NewVmUtils().getTimestampLen(ds.BkDataId, models.ETLConfigTypeBkExporter)
	assert.NoError(t, err)
	assert.Equal(t, models.TimeStampLenSecondLen, timestampLen)
	// otherEtl
	timestampLen, err = NewVmUtils().getTimestampLen(ds.BkDataId, "other etl")
	assert.NoError(t, err)
	assert.Equal(t, models.TimeStampLenMillisecondLen, timestampLen)
}

func TestVmUtils_AccessBkdata(t *testing.T) {
	defer mocker.PatchDBSession().Reset()
	cluster := storage.ClusterInfo{
		ClusterName:      "testVmCluster",
		ClusterType:      models.StorageTypeVM,
		IsDefaultCluster: true,
	}
	mysql.GetDBSession().DB.Delete(&cluster, "cluster_type = ? and is_default_cluster = ?", cluster.ClusterType, cluster.IsDefaultCluster)
	err := cluster.Create(mysql.GetDBSession().DB)
	assert.NoError(t, err)

	ds := resulttable.DataSource{
		DataName: "testDataName",
		BkDataId: 198888,
	}
	mysql.GetDBSession().DB.Delete(&ds)
	err = ds.Create(mysql.GetDBSession().DB)
	assert.NoError(t, err)

	funcPatch := gomonkey.ApplyFunc(VmUtils.AccessVmByKafka, func(s VmUtils, tableId, rawDataName, vmClusterName string, timestampLen int) (map[string]interface{}, error) {
		return map[string]interface{}{
			"clean_rt_id":         fmt.Sprintf("%v_%s", cfg.GlobalDefaultBkdataBizId, "raw_data_name"),
			"bk_data_id":          ds.BkDataId,
			"cluster_id":          cluster.ClusterID,
			"kafka_storage_exist": true,
		}, nil
	})
	defer funcPatch.Reset()

	sp := space.Space{
		SpaceTypeId: "bkcc",
		SpaceId:     "2233",
		IsBcsValid:  false,
	}
	mysql.GetDBSession().DB.Delete(&sp, "space_type_id = ? and space_id = ?", sp.SpaceTypeId, sp.SpaceId)
	err = sp.Create(mysql.GetDBSession().DB)
	assert.NoError(t, err)
	mysql.GetDBSession().DB.Delete(&space.SpaceVmInfo{}, "space_type = ? and space_id = ?", sp.SpaceTypeId, sp.SpaceId)
	bkBizId := 2233
	tableId := "test_table_id"
	mysql.GetDBSession().DB.Delete(&storage.AccessVMRecord{}, "result_table_id = ?", tableId)
	err = NewVmUtils().AccessBkdata(bkBizId, tableId, ds.BkDataId)
	assert.NoError(t, err)
	var record storage.AccessVMRecord
	err = storage.NewAccessVMRecordQuerySet(mysql.GetDBSession().DB).ResultTableIdEq(tableId).BkBaseDataIdEq(ds.BkDataId).One(&record)
	assert.NoError(t, err)
}
