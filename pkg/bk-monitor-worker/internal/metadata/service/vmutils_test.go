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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/optionx"
)

func TestVmUtils_getDataTypeCluster(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	cluster := bcs.BCSClusterInfo{
		ClusterID:          "",
		K8sMetricDataID:    299991,
		CustomMetricDataID: 299992,
	}
	db := mysql.GetDBSession().DB
	db.Delete(&cluster, "K8sMetricDataID = ? or CustomMetricDataID = ?", cluster.K8sMetricDataID, cluster.CustomMetricDataID)
	err := cluster.Create(db)
	assert.NoError(t, err)
	dataMap, err := NewVmUtils().getDataTypeCluster(299991)
	assert.NoError(t, err)
	assert.Equal(t, models.VmDataTypeUserCustom, dataMap["data_type"])
	assert.Equal(t, "", dataMap["bcs_cluster_id"])

	cluster.ClusterID = "test_cluster_id"
	err = cluster.Update(db, bcs.BCSClusterInfoDBSchema.ClusterID)
	assert.NoError(t, err)
	dataMap, err = NewVmUtils().getDataTypeCluster(299991)
	assert.Equal(t, models.VmDataTypeBcsClusterK8s, dataMap["data_type"])
	assert.Equal(t, "test_cluster_id", dataMap["bcs_cluster_id"])

	dataMap, err = NewVmUtils().getDataTypeCluster(299992)
	assert.Equal(t, models.VmDataTypeBcsClusterCustom, dataMap["data_type"])
	assert.Equal(t, "test_cluster_id", dataMap["bcs_cluster_id"])
}

func TestVmUtils_getVmCluster(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	cluster := storage.ClusterInfo{
		ClusterName:      "testVmCluster",
		ClusterType:      models.StorageTypeVM,
		IsDefaultCluster: true,
	}
	db := mysql.GetDBSession().DB
	db.Delete(&cluster, "cluster_type = ? and is_default_cluster = ? or cluster_name = ?", cluster.ClusterType, cluster.IsDefaultCluster, cluster.ClusterName)
	err := cluster.Create(db)
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
	db.Delete(&svi, "space_type = ? and space_id = ?", "bkcc", "123")
	err = svi.Create(db)
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
		{name: "a-b.c--d.__default__", args: args{tableId: "a-b.c--d.__default__"}, want: "vm_a_b_c_d", want1: fmt.Sprintf("%s%v", "vm_a_b_c_d", cfg.BkdataDefaultBizId)},
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
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	ds := resulttable.DataSource{
		BkDataId: 198877,
		DataName: "data_name_198877",
	}
	db := mysql.GetDBSession().DB
	db.Delete(&ds, "bk_data_id = ?", ds.BkDataId)
	err := ds.Create(db)
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
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	cluster := storage.ClusterInfo{
		ClusterName:      "testVmCluster",
		ClusterType:      models.StorageTypeVM,
		IsDefaultCluster: true,
	}
	db.Delete(&cluster, "cluster_type = ? and is_default_cluster = ?", cluster.ClusterType, cluster.IsDefaultCluster)
	err := cluster.Create(db)
	assert.NoError(t, err)

	ds := resulttable.DataSource{
		DataName: "testDataName",
		BkDataId: 198888,
	}
	db.Delete(&ds)
	err = ds.Create(db)
	assert.NoError(t, err)

	funcPatch := gomonkey.ApplyFunc(VmUtils.AccessVmByKafka, func(s VmUtils, tableId, rawDataName, vmClusterName string, timestampLen int) (map[string]interface{}, error) {
		return map[string]interface{}{
			"clean_rt_id":         fmt.Sprintf("%v_%s", cfg.BkdataDefaultBizId, "raw_data_name"),
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
	db.Delete(&sp, "space_type_id = ? and space_id = ?", sp.SpaceTypeId, sp.SpaceId)
	err = sp.Create(db)
	assert.NoError(t, err)
	db.Delete(&space.SpaceVmInfo{}, "space_type = ? and space_id = ?", sp.SpaceTypeId, sp.SpaceId)
	bkBizId := 2233
	tableId := "test_table_id"
	db.Delete(&storage.AccessVMRecord{}, "result_table_id = ?", tableId)
	err = NewVmUtils().AccessBkdata(bkBizId, tableId, ds.BkDataId)
	assert.NoError(t, err)
	var record storage.AccessVMRecord
	err = storage.NewAccessVMRecordQuerySet(db).ResultTableIdEq(tableId).BkBaseDataIdEq(ds.BkDataId).One(&record)
	assert.NoError(t, err)
}

func TestBkDataStorageWithDataID_Value(t *testing.T) {
	rawDataId := 180000
	resultTableName := "rt_name_for_test"
	clusterName := "cluster_name_for_test"
	bkDataStorage := NewBkDataStorageWithDataID(rawDataId, resultTableName, clusterName, "")
	value, err := bkDataStorage.Value()
	assert.NoError(t, err)
	assert.Equal(t, rawDataId, value["raw_data_id"])
	assert.Equal(t, "clean", value["data_type"])
	assert.Equal(t, resultTableName, value["result_table_name"])
	assert.Equal(t, resultTableName, value["result_table_name_alias"])
	assert.Equal(t, "vm", value["storage_type"])
	assert.Equal(t, models.VmRetentionTime, value["expires"])
	fields, ok := value["fields"].([]map[string]interface{})
	assert.True(t, ok)
	var fieldNames []string
	for _, f := range fields {
		nameI := f["field_name"]
		name, _ := nameI.(string)
		fieldNames = append(fieldNames, name)
	}
	assert.ElementsMatch(t, []string{"time", "value", "metric", "dimensions"}, fieldNames)
	assert.Equal(t, clusterName, value["storage_cluster"])
	assert.Equal(t, map[string]interface{}{"schemaless": true}, value["config"])
}

func TestBkDataClean_Value(t *testing.T) {
	rawDataName := "data_name_for_test"
	resultTableName := "rt_name_for_test"
	bkBizId := 2
	bkDataClean := NewBkDataClean(rawDataName, resultTableName, bkBizId, 0)
	value, err := bkDataClean.Value()
	assert.NoError(t, err)
	jsonConfigI, ok := value["json_config"]
	assert.True(t, ok)
	jsonConfig, ok := jsonConfigI.(map[string]interface{})
	assert.True(t, ok)
	confI, ok := jsonConfig["conf"]
	assert.True(t, ok)
	conf, ok := confI.(map[string]interface{})
	assert.True(t, ok)
	c := optionx.NewOptions(conf)
	timeFormat, ok := c.Get("time_format")
	assert.True(t, ok)
	assert.Equal(t, models.TimeStampLenValeMap[models.TimeStampLenMillisecondLen], timeFormat)
	timestampLen, ok := c.Get("timestamp_len")
	assert.True(t, ok)
	assert.Equal(t, float64(models.TimeStampLenMillisecondLen), timestampLen)
	assert.Equal(t, rawDataName, value["result_table_name"])
	assert.Equal(t, resultTableName, value["result_table_name_alias"])
	assert.Equal(t, fmt.Sprintf("%v_%s", bkBizId, rawDataName), value["processing_id"])
	fmt.Println(value)
}
