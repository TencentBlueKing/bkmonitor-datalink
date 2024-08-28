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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/bcs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/memcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/optionx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
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

func TestSpacePusher_composeBcsSpaceClusterTableIds(t *testing.T) {
	// 初始化测试数据库配置
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB

	// 创建一个真实的SpaceResource数据
	resourceId := "monitor"
	dimensionValues := `[{"cluster_id": "BCS-K8S-00000", "namespace": null, "cluster_type": "single"},
                          {"cluster_id": "BCS-K8S-00001", "namespace": ["bkm-test-4"], "cluster_type": "shared"},
                          {"cluster_id": "BCS-K8S-00002", "namespace": ["bkm-test-1", "bkm-test-2", "bkm-test-3"], "cluster_type": "shared"},
                          {"cluster_id": "BCS-K8S-00003", "namespace": [], "cluster_type": "shared"}]`
	spaceResource := space.SpaceResource{
		Id:              207,
		SpaceTypeId:     models.SpaceTypeBKCI,
		SpaceId:         "monitor",
		ResourceType:    "bcs",
		ResourceId:      &resourceId,
		DimensionValues: dimensionValues,
	}
	db.Delete(&spaceResource)
	err := db.Create(&spaceResource).Error
	assert.NoError(t, err)

	// 创建 BCSClusterInfo 数据
	clusterInfos := []bcs.BCSClusterInfo{
		{
			ClusterID:          "BCS-K8S-00000",
			K8sMetricDataID:    1001,
			CustomMetricDataID: 2001,
		},
		{
			ClusterID:          "BCS-K8S-00001",
			K8sMetricDataID:    1002,
			CustomMetricDataID: 2002,
		},
		{
			ClusterID:          "BCS-K8S-00002",
			K8sMetricDataID:    1003,
			CustomMetricDataID: 2003,
		},
		{
			ClusterID:          "BCS-K8S-00003",
			K8sMetricDataID:    1004,
			CustomMetricDataID: 2004,
		},
	}
	db.Delete(&bcs.BCSClusterInfo{})
	for _, ci := range clusterInfos {
		err = db.Create(&ci).Error
		assert.NoError(t, err)
	}

	// 创建 DataSourceResultTable 数据
	dataSourceResultTables := []resulttable.DataSourceResultTable{
		{
			BkDataId: 1001,
			TableId:  "table1",
		},
		{
			BkDataId: 2001,
			TableId:  "table2",
		},
		{
			BkDataId: 1002,
			TableId:  "table3",
		},
		{
			BkDataId: 2002,
			TableId:  "table4",
		},
		{
			BkDataId: 1003,
			TableId:  "table5",
		},
		{
			BkDataId: 2003,
			TableId:  "table6",
		},
		{
			BkDataId: 1004,
			TableId:  "table7",
		},
		{
			BkDataId: 2004,
			TableId:  "table8",
		},
	}
	db.Delete(&resulttable.DataSourceResultTable{})
	for _, dsrt := range dataSourceResultTables {
		err = db.Create(&dsrt).Error
		assert.NoError(t, err)
	}

	// 执行被测试的方法
	spacePusher := NewSpacePusher()
	result, err := spacePusher.composeBcsSpaceClusterTableIds("bkci", "monitor")
	assert.NoError(t, err)

	// 输出调试信息
	fmt.Printf("Result: %+v\n", result)

	expectedResults := map[string]map[string]interface{}{
		"table1": {
			"filters": []map[string]interface{}{
				{"bcs_cluster_id": "BCS-K8S-00000", "namespace": nil},
			},
		},
		"table2": {
			"filters": []map[string]interface{}{
				{"bcs_cluster_id": "BCS-K8S-00000", "namespace": nil},
			},
		},
		"table3": {
			"filters": []map[string]interface{}{
				{"bcs_cluster_id": "BCS-K8S-00001", "namespace": "bkm-test-4"},
			},
		},
		"table4": {
			"filters": []map[string]interface{}{
				{"bcs_cluster_id": "BCS-K8S-00001", "namespace": "bkm-test-4"},
			},
		},
		"table5": {
			"filters": []map[string]interface{}{
				{"bcs_cluster_id": "BCS-K8S-00002", "namespace": "bkm-test-1"},
				{"bcs_cluster_id": "BCS-K8S-00002", "namespace": "bkm-test-2"},
				{"bcs_cluster_id": "BCS-K8S-00002", "namespace": "bkm-test-3"},
			},
		},
		"table6": {
			"filters": []map[string]interface{}{
				{"bcs_cluster_id": "BCS-K8S-00002", "namespace": "bkm-test-1"},
				{"bcs_cluster_id": "BCS-K8S-00002", "namespace": "bkm-test-2"},
				{"bcs_cluster_id": "BCS-K8S-00002", "namespace": "bkm-test-3"},
			},
		},
	}

	assert.Equal(t, expectedResults, result)
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

func TestSpacePusher_refineEsTableIds(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	itableName := "i_table_test.dbname"
	iTable := storage.ESStorage{TableID: itableName, SourceType: models.EsSourceTypeLOG}
	db.Delete(&iTable)
	err := iTable.Create(db)
	assert.NoError(t, err)

	itableName1 := "i_table_test1.dbname1"
	iTable1 := storage.ESStorage{TableID: itableName1, SourceType: models.EsSourceTypeBKDATA}
	db.Delete(&iTable1)
	err = iTable1.Create(db)
	assert.NoError(t, err)

	notExistTable := "not_exist_rt"

	ids, err := NewSpacePusher().refineTableIds([]string{itableName, itableName1, notExistTable})
	assert.ElementsMatch(t, []string{itableName, itableName1}, ids)
}

func TestSpacePusher_GetBizIdBySpace(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB

	obj := space.Space{Id: 1, SpaceTypeId: "bkcc", SpaceId: "2"}
	obj2 := space.Space{Id: 5, SpaceTypeId: "bkci", SpaceId: "test"}
	obj3 := space.Space{Id: 6, SpaceTypeId: "bksaas", SpaceId: "test2"}

	db.Delete(obj)
	db.Delete(obj2)
	db.Delete(obj3)

	assert.NoError(t, obj.Create(db))
	assert.NoError(t, obj2.Create(db))
	assert.NoError(t, obj3.Create(db))

	tests := []struct {
		spaceType string
		spaceId   string
		want      int
	}{
		{spaceType: "bkcc", spaceId: "3", want: 0}, // 数据库无该记录
		{spaceType: "bkcc", spaceId: "2", want: 2},
		{spaceType: "bkci", spaceId: "test", want: -5},
		{spaceType: "bksaas", spaceId: "test2", want: -6},
	}

	s := &SpacePusher{}
	for _, tt := range tests {
		t.Run(tt.spaceType+tt.spaceId, func(t *testing.T) {
			bId, _ := s.GetBizIdBySpace(tt.spaceType, tt.spaceId)
			assert.Equal(t, tt.want, bId)
		})
	}
}

func TestSpacePusher_ComposeEsTableIds(t *testing.T) {
	t.Run("TestSpacePusher_GetBizIdBySpace", TestSpacePusher_GetBizIdBySpace)
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB

	obj := resulttable.ResultTable{TableId: "apache.net", BkBizId: 2, DefaultStorage: models.StorageTypeES, IsDeleted: false, IsEnable: true}
	obj2 := resulttable.ResultTable{TableId: "system.mem", BkBizId: -5, DefaultStorage: models.StorageTypeES, IsDeleted: false, IsEnable: true}
	obj3 := resulttable.ResultTable{TableId: "system.net", BkBizId: 2, DefaultStorage: models.StorageTypeES, IsDeleted: false, IsEnable: true}
	obj4 := resulttable.ResultTable{TableId: "system.io", BkBizId: -6, DefaultStorage: models.StorageTypeES, IsDeleted: false, IsEnable: true}

	db.Delete(obj)
	db.Delete(obj2)
	db.Delete(obj3)
	db.Delete(obj4)

	assert.NoError(t, obj.Create(db))
	assert.NoError(t, obj2.Create(db))
	assert.NoError(t, obj3.Create(db))
	assert.NoError(t, obj4.Create(db))

	tests := []struct {
		spaceType string
		spaceId   string
		want      map[string]map[string]interface{}
	}{
		{spaceType: "bkcc", spaceId: "3", want: nil}, // 数据库无该记录
		{spaceType: "bkcc", spaceId: "2", want: map[string]map[string]interface{}{"apache.net": {"filters": []interface{}{}}, // bizId=2
			"system.net": {"filters": []interface{}{}}}},
		{spaceType: "bkci", spaceId: "test", want: map[string]map[string]interface{}{"system.mem": {"filters": []interface{}{}}}},   // bizId=-5
		{spaceType: "bksaas", spaceId: "test2", want: map[string]map[string]interface{}{"system.io": {"filters": []interface{}{}}}}, // bizId=-6
	}

	s := &SpacePusher{}
	for _, tt := range tests {
		t.Run(tt.spaceType+tt.spaceId, func(t *testing.T) {
			datavalues, _ := s.ComposeEsTableIds(tt.spaceType, tt.spaceId)
			assert.Equal(t, tt.want, datavalues)
		})
	}
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
		BkDataId:         platformDataId,
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
	for _, tid := range []string{tableIdOne, tableIdThree} {
		assert.Contains(t, data, tid)
		val := data[tid]["filters"]
		d := val.([]map[string]interface{})
		bk_biz_id := d[0]["bk_biz_id"].(string)
		assert.Equal(t, resourceId, bk_biz_id)
	}
}

func TestSpaceRedisSvc_getCachedClusterDataIdList(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	obj := bcs.BCSClusterInfo{ClusterID: "BCS-K8S-00000", K8sMetricDataID: 100001, CustomMetricDataID: 100002}
	db.Delete(obj)
	assert.NoError(t, obj.Create(db))

	data, err := NewSpacePusher().getCachedClusterDataIdList()
	assert.NoError(t, err)
	assert.Equal(t, 2, len(data))
	assert.Equal(t, []uint{100001, 100002}, data)

	cache, err := memcache.GetMemCache()
	cache.Wait()
	assert.NoError(t, err)
	dataList, ok := cache.Get(CachedClusterDataIdKey)
	assert.True(t, ok)
	assert.Equal(t, []uint{100001, 100002}, dataList.([]uint))
}

func TestGetDataLabelByTableId(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	// 初始数据
	db := mysql.GetDBSession().DB
	// not data_label
	obj := resulttable.ResultTable{TableId: "not_data_label", DataLabel: nil}
	db.Delete(obj)
	assert.NoError(t, obj.Create(db))
	// with data_label
	dataLabel := "data_label_value"
	obj = resulttable.ResultTable{TableId: "data_label", DataLabel: &dataLabel}
	db.Delete(obj)
	assert.NoError(t, obj.Create(db))

	tests := []struct {
		name         string
		tableIdList  []string
		expectedList []string
	}{
		{"table_id is nil", []string{}, nil},
		{"table_id without data_label", []string{"not_data_label"}, nil},
		{"table_id with data_label", []string{"data_label"}, []string{dataLabel}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualList, _ := NewSpacePusher().getDataLabelByTableId(tt.tableIdList)
			assert.Equal(t, tt.expectedList, actualList)
		})
	}
}

func TestGetAllDataLabelTableId(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	// 初始数据
	db := mysql.GetDBSession().DB
	// not data_label
	obj := resulttable.ResultTable{TableId: "not_data_label", IsEnable: true, DataLabel: nil}
	db.Delete(obj)
	assert.NoError(t, obj.Create(db))
	// with data_label
	dataLabel := "data_label_value"
	obj = resulttable.ResultTable{TableId: "data_label", IsEnable: true, DataLabel: &dataLabel}
	db.Delete(obj)
	assert.NoError(t, obj.Create(db))

	dataLabel1 := "data_label_value1"
	obj = resulttable.ResultTable{TableId: "data_label1", IsEnable: true, DataLabel: &dataLabel1}
	db.Delete(obj)
	assert.NoError(t, obj.Create(db))

	data, err := NewSpacePusher().getAllDataLabelTableId()
	assert.NoError(t, err)

	dataLabelSet := mapset.NewSet[string]()
	for dataLabel, _ := range data {
		dataLabelSet.Add(dataLabel)
	}
	expectedSet := mapset.NewSet("data_label_value", "data_label_value1")

	assert.True(t, expectedSet.IsSubset(dataLabelSet))

	assert.Equal(t, []string{"data_label"}, data["data_label_value"])
}

func TestComposeBksaasSpaceClusterTableIds(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	// 初始数据
	db := mysql.GetDBSession().DB
	sr := "demo"
	srObj := space.SpaceResource{SpaceTypeId: "bksaas", SpaceId: "demo", ResourceType: "bksaas", ResourceId: &sr, DimensionValues: `[{"cluster_id": "BCS-K8S-00000", "namespace": ["bkapp-demo-stage", "bkapp-demo-prod"], "cluster_type":"shared"}]`}
	db.Delete(srObj)
	assert.NoError(t, srObj.Create(db))

	// 添加集群信息
	clusterObj := bcs.BCSClusterInfo{ClusterID: "BCS-K8S-00000", K8sMetricDataID: 100001, CustomMetricDataID: 100002}
	db.Delete(clusterObj)
	assert.NoError(t, clusterObj.Create(db))

	// 添加结果表
	rtObj := resulttable.ResultTable{TableId: "demo.test", IsDeleted: false, IsEnable: true, DataLabel: nil}
	db.Delete(rtObj)
	assert.NoError(t, rtObj.Create(db))
	rtObj1 := resulttable.ResultTable{TableId: "demo.test1", IsDeleted: false, IsEnable: true, DataLabel: nil}
	db.Delete(rtObj1)
	assert.NoError(t, rtObj1.Create(db))

	// 添加数据源和结果表关系
	dsRtObj := resulttable.DataSourceResultTable{BkDataId: 100001, TableId: "demo.test"}
	db.Delete(dsRtObj, "table_id=?", dsRtObj.TableId)
	assert.NoError(t, dsRtObj.Create(db))
	dsRtObj1 := resulttable.DataSourceResultTable{BkDataId: 100002, TableId: "demo.test1"}
	db.Delete(dsRtObj1, "table_id=?", dsRtObj1.TableId)
	assert.NoError(t, dsRtObj1.Create(db))

	spaceType, spaceId := "bksaas", "demo"
	data, err := NewSpacePusher().composeBksaasSpaceClusterTableIds(spaceType, spaceId, nil)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(data))
}

func TestClearSpaceToRt(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	// 添加space资源
	db := mysql.GetDBSession().DB
	spaceType, spaceId1, spaceId2, spaceId3 := "bkcc", "1", "2", "3"
	obj1 := space.Space{SpaceTypeId: spaceType, SpaceId: spaceId1, SpaceName: spaceId1}
	obj2 := space.Space{SpaceTypeId: spaceType, SpaceId: spaceId2, SpaceName: spaceId2}
	obj3 := space.Space{SpaceTypeId: spaceType, SpaceId: spaceId3, SpaceName: spaceId3}
	db.Delete(obj1, "space_id=?", obj1.SpaceId)
	db.Delete(obj2, "space_id=?", obj2.SpaceId)
	db.Delete(obj3, "space_id=?", obj3.SpaceId)
	assert.NoError(t, obj1.Create(db))
	assert.NoError(t, obj2.Create(db))
	assert.NoError(t, obj3.Create(db))

	// 初始化redis中数据
	redisClient, redisPatch := mocker.RedisMocker()
	defer redisPatch.Reset()

	redisClient.HKeysValue = append(redisClient.HKeysValue, "bkcc__1", "bkcc__2", "bkcc__4")

	// 清理数据
	clearer := NewSpaceRedisClearer()
	clearer.ClearSpaceToRt()

	assert.Equal(t, 2, len(redisClient.HKeysValue))
	assert.Equal(t, slicex.StringList2Set([]string{"bkcc__1", "bkcc__2"}), slicex.StringList2Set(redisClient.HKeysValue))
}

func TestClearDataLabelToRt(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	// 添加space资源
	db := mysql.GetDBSession().DB
	rt1, rt2, rt3 := "demo.test1", "demo.test2", "demo.test3"
	rtDl1, rtDl2, rtDl3 := "data_label1", "data_label2", "data_label3"
	rtObj1 := resulttable.ResultTable{TableId: rt1, IsDeleted: false, IsEnable: true, DataLabel: &rtDl1}
	rtObj2 := resulttable.ResultTable{TableId: rt2, IsDeleted: false, IsEnable: true, DataLabel: &rtDl2}
	rtObj3 := resulttable.ResultTable{TableId: rt3, IsDeleted: false, IsEnable: true, DataLabel: &rtDl3}
	db.Delete(rtObj1, "table_id=?", rtObj1.TableId)
	db.Delete(rtObj2, "table_id=?", rtObj2.TableId)
	db.Delete(rtObj3, "table_id=?", rtObj3.TableId)
	assert.NoError(t, rtObj1.Create(db))
	assert.NoError(t, rtObj2.Create(db))
	assert.NoError(t, rtObj3.Create(db))

	// 初始化redis中数据
	redisClient, redisPatch := mocker.RedisMocker()
	defer redisPatch.Reset()

	redisClient.HKeysValue = append(redisClient.HKeysValue, "data_label1", "data_label2", "data_label4")

	// 清理数据
	clearer := NewSpaceRedisClearer()
	clearer.ClearDataLabelToRt()

	assert.Equal(t, 2, len(redisClient.HKeysValue))
	assert.Equal(t, slicex.StringList2Set([]string{"data_label1", "data_label2"}), slicex.StringList2Set(redisClient.HKeysValue))
}

func TestClearRtDetail(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	// 添加space资源
	db := mysql.GetDBSession().DB
	rt1, rt2, rt3 := "demo.test1", "demo.test2", "demo.test3"
	rtDl1, rtDl2, rtDl3 := "data_label1", "data_label2", "data_label3"
	rtObj1 := resulttable.ResultTable{TableId: rt1, IsDeleted: false, IsEnable: true, DataLabel: &rtDl1}
	rtObj2 := resulttable.ResultTable{TableId: rt2, IsDeleted: true, IsEnable: false, DataLabel: &rtDl2}
	rtObj3 := resulttable.ResultTable{TableId: rt3, IsDeleted: false, IsEnable: true, DataLabel: &rtDl3}
	db.Delete(rtObj1, "table_id=?", rtObj1.TableId)
	db.Delete(rtObj2, "table_id=?", rtObj2.TableId)
	db.Delete(rtObj3, "table_id=?", rtObj3.TableId)
	assert.NoError(t, rtObj1.Create(db))
	assert.NoError(t, rtObj2.Create(db))
	assert.NoError(t, rtObj3.Create(db))

	// 初始化redis中数据
	redisClient, redisPatch := mocker.RedisMocker()
	defer redisPatch.Reset()

	redisClient.HKeysValue = append(redisClient.HKeysValue, "demo.test1", "demo.test2", "demo.test4")

	// 清理数据
	clearer := NewSpaceRedisClearer()
	clearer.ClearRtDetail()

	assert.Equal(t, 1, len(redisClient.HKeysValue))
	assert.Equal(t, slicex.StringList2Set([]string{"demo.test1"}), slicex.StringList2Set(redisClient.HKeysValue))
}

func TestComposeEsTableIdOptions(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	// 初始数据
	db := mysql.GetDBSession().DB
	// 创建rt
	rt1, rt2, rt3 := "demo.test1", "demo.test2", "demo.test3"
	rtObj1 := resulttable.ResultTable{TableId: rt1, IsDeleted: false, IsEnable: true}
	rtObj2 := resulttable.ResultTable{TableId: rt2, IsDeleted: true, IsEnable: false}
	rtObj3 := resulttable.ResultTable{TableId: rt3, IsDeleted: false, IsEnable: true}
	db.Delete(rtObj1, "table_id=?", rtObj1.TableId)
	db.Delete(rtObj2, "table_id=?", rtObj2.TableId)
	db.Delete(rtObj3, "table_id=?", rtObj3.TableId)
	assert.NoError(t, rtObj1.Create(db))
	assert.NoError(t, rtObj2.Create(db))
	assert.NoError(t, rtObj3.Create(db))
	// 创建选项
	op1, op2, op3 := "op1", "op2", "op3"
	val1, val2, val3 := `{"name": "v1"}`, `{"name": "v2"}`, `{"name": "v3"}`
	opVal1 := models.OptionBase{Value: val1, ValueType: "dict", Creator: "system"}
	rtOp1 := resulttable.ResultTableOption{OptionBase: opVal1, TableID: rt1, Name: op1}
	opVal2 := models.OptionBase{Value: val2, ValueType: "dict", Creator: "system"}
	rtOp2 := resulttable.ResultTableOption{OptionBase: opVal2, TableID: rt2, Name: op2}
	opVal3 := models.OptionBase{Value: val3, ValueType: "dict", Creator: "system"}
	rtOp3 := resulttable.ResultTableOption{OptionBase: opVal3, TableID: rt3, Name: op3}
	db.Delete(rtOp1, "table_id=? AND name=?", rtOp1.TableID, rtOp1.Name)
	db.Delete(rtOp2, "table_id=? AND name=?", rtOp2.TableID, rtOp2.Name)
	db.Delete(rtOp3, "table_id=? AND name=?", rtOp3.TableID, rtOp3.Name)
	assert.NoError(t, rtOp1.Create(db))
	assert.NoError(t, rtOp2.Create(db))
	assert.NoError(t, rtOp3.Create(db))

	// 获取正常数据
	data := SpacePusher{}.composeEsTableIdOptions([]string{rt1, rt2, rt3})
	assert.Equal(t, 3, len(data))
	assert.Equal(t, map[string]interface{}{"name": "v1"}, data[rt1][rtOp1.Name])

	// 获取不存在的rt数据
	data = SpacePusher{}.composeEsTableIdOptions([]string{"not_exist"})
	assert.Equal(t, 0, len(data))
}
