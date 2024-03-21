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

	"github.com/agiledragon/gomonkey/v2"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/optionx"
)

func TestSpacePusher_getMeasurementType(t *testing.T) {
	type args struct {
		schemaType            string
		isSplitMeasurement    bool
		isDisableMetricCutter bool
		etlConfig             string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{name: "fixed", args: args{schemaType: models.ResultTableSchemaTypeFixed, isSplitMeasurement: false, isDisableMetricCutter: false, etlConfig: ""}, want: models.MeasurementTypeBkTraditional},
		{name: "free-split", args: args{schemaType: models.ResultTableSchemaTypeFree, isSplitMeasurement: true, isDisableMetricCutter: false, etlConfig: ""}, want: models.MeasurementTypeBkSplit},
		{name: "free-nosplit-nots", args: args{schemaType: models.ResultTableSchemaTypeFree, isSplitMeasurement: false, isDisableMetricCutter: false, etlConfig: models.ETLConfigTypeBkStandard}, want: models.MeasurementTypeBkExporter},
		{name: "free-nosplit-ts-nocut", args: args{schemaType: models.ResultTableSchemaTypeFree, isSplitMeasurement: false, isDisableMetricCutter: false, etlConfig: models.ETLConfigTypeBkStandardV2TimeSeries}, want: models.MeasurementTypeBkExporter},
		{name: "free-nosplit-ts-cut", args: args{schemaType: models.ResultTableSchemaTypeFree, isSplitMeasurement: false, isDisableMetricCutter: true, etlConfig: models.ETLConfigTypeBkStandardV2TimeSeries}, want: models.MeasurementTypeBkStandardV2TimeSeries},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &SpacePusher{}
			assert.Equalf(t, tt.want, s.getMeasurementType(tt.args.schemaType, tt.args.isSplitMeasurement, tt.args.isDisableMetricCutter, tt.args.etlConfig), "getMeasurementType(%v, %v, %v, %v)", tt.args.schemaType, tt.args.isSplitMeasurement, tt.args.isDisableMetricCutter, tt.args.etlConfig)
		})
	}
}

func TestSpacePusher_refineTableIds(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	itableName := "i_table_test.dbname"
	iTable := storage.InfluxdbStorage{TableID: itableName, RealTableName: "i_table_test", Database: "dbname"}
	db.Delete(&iTable)
	err := iTable.Create(db)
	assert.NoError(t, err)

	itableName1 := "i_table_test1.dbname1"
	iTable1 := storage.InfluxdbStorage{TableID: itableName1, RealTableName: "i_table_test1", Database: "dbname1"}
	db.Delete(&iTable1)
	err = iTable1.Create(db)
	assert.NoError(t, err)

	vmTableName := "vm_table_name"
	vmTable := storage.AccessVMRecord{ResultTableId: vmTableName}
	db.Delete(&vmTable)
	err = vmTable.Create(db)
	assert.NoError(t, err)

	notExistTable := "not_exist_rt"

	ids, err := NewSpacePusher().refineTableIds([]string{itableName, itableName1, notExistTable, vmTableName})
	assert.ElementsMatch(t, []string{itableName, itableName1, vmTableName}, ids)
}

func TestSpacePusher_GetSpaceTableIdDataId(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	_, redisPatch := mocker.RedisMocker()
	defer redisPatch.Reset()
	var platformDataId uint = 18003
	platformRt := "rt_18003"
	dsRtMap := map[string]uint{
		"rt_18000": 18000,
		"rt_18001": 18001,
		"rt_18002": 18002,
	}
	for rti, dataId := range dsRtMap {
		db.Delete(&resulttable.DataSourceResultTable{}, "bk_data_id = ? and table_id = ?", dataId, rti)
		dsrt := resulttable.DataSourceResultTable{
			BkDataId:   dataId,
			TableId:    rti,
			CreateTime: time.Now(),
		}
		err := dsrt.Create(db)
		assert.NoError(t, err)
		spds := space.SpaceDataSource{
			SpaceTypeId:       "bkcc_t",
			SpaceId:           "2",
			BkDataId:          dataId,
			FromAuthorization: false,
		}
		db.Delete(&spds, "bk_data_id = ?", spds.BkDataId)
		err = spds.Create(db)
		assert.NoError(t, err)
	}
	// 添加
	db.Delete(&resulttable.DataSourceResultTable{}, "bk_data_id = ? and table_id = ?", platformDataId, platformRt)
	dsrt := resulttable.DataSourceResultTable{
		BkDataId:   platformDataId,
		TableId:    platformRt,
		CreateTime: time.Now(),
	}
	err := dsrt.Create(db)
	assert.NoError(t, err)
	db.Delete(&resulttable.DataSource{}, "bk_data_id = ?", platformDataId)
	ds := resulttable.DataSource{
		BkDataId: platformDataId,
		IsPlatformDataId: true,
	}
	err = ds.Create(db)
	assert.NoError(t, err)

	pusher := NewSpacePusher()
	// 指定rtList
	dataMap, err := pusher.GetSpaceTableIdDataId("", "", []string{"rt_18000", "rt_18002"}, nil, nil)
	assert.NoError(t, err)
	assert.Equal(t, map[string]uint{"rt_18000": 18000, "rt_18002": 18002}, dataMap)

	// 执行类型，不指定结果表
	dataMap, err = pusher.GetSpaceTableIdDataId("bkcc_t", "2", nil, nil, nil)
	assert.NoError(t, err)
	assert.Equal(t, map[string]uint{"rt_18000": 18000, "rt_18001": 18001, "rt_18002": 18002}, dataMap)

	// 测试排除
	dataMap, err = pusher.GetSpaceTableIdDataId("bkcc_t", "2", nil, []uint{18000, 18002}, nil)
	assert.NoError(t, err)
	assert.Equal(t, map[string]uint{"rt_18001": 18001}, dataMap)

	// 不包含全局数据源
	opt := optionx.NewOptions(map[string]interface{}{"includePlatformDataId": false})
	dataMap, err = pusher.GetSpaceTableIdDataId("bkcc_t", "2", nil, nil, opt)
	fmt.Println(dataMap)
}

func TestSpacePusher_getTableInfoForInfluxdbAndVm(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	_, redisPatch := mocker.RedisMocker()
	defer redisPatch.Reset()
	db := mysql.GetDBSession().DB
	s := storage.InfluxdbProxyStorage{
		ProxyClusterId:      2,
		InstanceClusterName: "default",
		ServiceName:         "svc_name",
		IsDefault:           true,
	}
	db.Delete(&s, "proxy_cluster_id = ?", s.ProxyClusterId)
	err := s.Create(db)
	assert.NoError(t, err)

	itableName := "i_table_test.dbname"
	iTable := storage.InfluxdbStorage{
		TableID:                itableName,
		InfluxdbProxyStorageId: s.ID,
		RealTableName:          "i_table_test",
		Database:               "dbname",
		PartitionTag:           "t1,t2",
	}
	db.Delete(&iTable)
	err = iTable.Create(db)
	assert.NoError(t, err)

	cluster := storage.ClusterInfo{
		ClusterName: "vm_cluster_abc",
		ClusterType: models.StorageTypeVM,
	}
	db.Delete(&cluster, "cluster_name = ?", cluster.ClusterName)
	err = cluster.Create(db)
	assert.NoError(t, err)
	vmTableName := "vm_table_name"
	vmTable := storage.AccessVMRecord{
		ResultTableId:   vmTableName,
		VmResultTableId: "vm_result_table_id",
		VmClusterId:     cluster.ClusterID,
	}
	db.Delete(&vmTable)
	err = vmTable.Create(db)
	assert.NoError(t, err)

	data, err := NewSpacePusher().getTableInfoForInfluxdbAndVm([]string{itableName, vmTableName})
	assert.NoError(t, err)
	assert.Equal(t, 2, len(data))
	vmData, err := jsonx.MarshalString(data[vmTableName])
	assert.NoError(t, err)
	assert.JSONEq(t, `{"cluster_name":"","db":"","measurement":"","storage_name":"vm_cluster_abc","tags_key":[],"vm_rt":"vm_result_table_id"}`, vmData)
	itableData, err := jsonx.MarshalString(data[itableName])
	assert.NoError(t, err)
	assert.JSONEq(t, `{"cluster_name":"default","db":"dbname","measurement":"i_table_test","storage_id":2,"storage_name":"","tags_key":["t1","t2"],"vm_rt":""}`, itableData)
}

func TestSpaceRedisSvc_PushAndPublishSpaceRouter(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	redisClient := &mocker.RedisClientMocker{
		SetMap: map[string]mapset.Set[string]{},
	}
	patch := gomonkey.ApplyFunc(redis.GetInstance, func() *redis.Instance {
		return &redis.Instance{
			Client: redisClient,
		}
	})
	defer patch.Reset()
	// no panic
	err := NewSpaceRedisSvc(1).PushAndPublishSpaceRouter("", "", nil)
	assert.NoError(t, err)
}


func TestSpaceRedisSvc_composeAllTypeTableIds(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	// 初始化前置 db 数据
	spaceType, spaceId := "bkcc", "1"
	obj := space.Space{Id: 1, SpaceTypeId: spaceType, SpaceId: spaceId, SpaceName: "testTable"}
	db.Delete(obj)
	err := obj.Create(db)
	assert.NoError(t, err)

	data, err := NewSpacePusher().composeAllTypeTableIds(spaceType, spaceId)
	assert.NoError(t, err)
	assert.Equal(t, len(data), 2)
	// 比对数据
	for _, val := range data {
		filter := val["filters"]
		mapFilter := filter.([]map[string]interface{})
		assert.Equal(t, len(mapFilter), 1)
	}
}


func TestSpaceRedisSvc_composeBcsSpaceBizTableIds(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	spaceType, spaceId, resourceType, resourceId := "bkci", "bcs_project", "bkcc", "1"
	obj := space.SpaceResource{SpaceTypeId: spaceType, SpaceId: spaceId, ResourceType: resourceType, ResourceId: &resourceId}
	db.Delete(obj)
	err := obj.Create(db)
	assert.NoError(t, err)

	// 初始化结果表
	tableIdOne, tableIdTwo, tableIdThree := "system.mem1", "dbm_system.mem1", "script_p4_connect_monitor.__default__"
	objone := resulttable.ResultTable{TableId: tableIdOne, TableNameZh: tableIdOne}
	objtwo := resulttable.ResultTable{TableId: tableIdTwo, TableNameZh: tableIdTwo}
	objthree := resulttable.ResultTable{TableId: tableIdThree, TableNameZh: tableIdThree}
	for _, obj := range []resulttable.ResultTable{objone, objtwo, objthree} {
		db.Delete(obj)
		err := obj.Create(db)
		assert.NoError(t, err)
	}
	
	data, err := NewSpacePusher().composeBcsSpaceBizTableIds(spaceType, spaceId)
	assert.NoError(t, err)
	assert.NotContains(t, data, tableIdTwo)
	for _, tid := range []string{tableIdOne, tableIdThree}{
		assert.Contains(t, data, tid)
		val := data[tid]["filters"]
		d := val.([]map[string]interface{})
		bk_biz_id := d[0]["bk_biz_id"].(string)
		assert.Equal(t, resourceId, bk_biz_id)
	}
}