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

func TestDbfieldIsNull(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB

	var esStorage storage.ESStorage
	storage.NewESStorageQuerySet(db).
		Select(storage.ESStorageDBSchema.TableID, storage.ESStorageDBSchema.StorageClusterID, storage.ESStorageDBSchema.SourceType, storage.ESStorageDBSchema.IndexSet).
		TableIDEq("system.net").One(&esStorage)

	s := &SpacePusher{}
	t.Run("TestDbfieldIsNull", func(t *testing.T) {
		_, detailStr, _ := s.composeEsTableIdDetail("system.net", 3, "log", esStorage.IndexSet)
		assert.Equal(t, `{"storage_id": 3,"db":"system_net_*_read","measurement": "__default__"}`, detailStr)
	})
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

func TestSpacePusher_composeEsTableIdDetail(t *testing.T) {
	tests := []struct {
		tableId          string
		storageClusterId uint
		sourceType       string
		indexSet         string
		want             string
	}{
		{tableId: "apache.net", storageClusterId: 3, sourceType: "log", indexSet: "index.1,index.2,index.3", want: `{"storage_id": 3,"db":"index_1_*_read,index_2_*_read,index_3_*_read","measurement": "__default__"}`},
		{tableId: "apache.net", storageClusterId: 3, sourceType: "log", indexSet: "", want: `{"storage_id": 3,"db":"apache_net_*_read","measurement": "__default__"}`},
		{tableId: "apache.net", storageClusterId: 3, sourceType: "bkdata", indexSet: "index.1,index.2,index.3", want: `{"storage_id": 3,"db":"index.1_*,index.2_*,index.3_*","measurement": "__default__"}`},
		{tableId: "apache.net", storageClusterId: 3, sourceType: "es", indexSet: "index.1,index.2,index.3", want: `{"storage_id": 3,"db":"index.1,index.2,index.3","measurement": "__default__"}`},
		{tableId: "apache.net", storageClusterId: 3, sourceType: "es1234", indexSet: "index.1,index.2,index.3", want: ""},
	}

	s := &SpacePusher{}
	for _, tt := range tests {
		t.Run(tt.tableId+tt.sourceType, func(t *testing.T) {
			_, detailStr, _ := s.composeEsTableIdDetail(tt.tableId, tt.storageClusterId, tt.sourceType, tt.indexSet)
			assert.Equal(t, tt.want, detailStr)
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
	dataList, ok := cache.Get(cachedClusterDataIdKey)
	assert.True(t, ok)
	assert.Equal(t, []uint{100001, 100002}, dataList.([]uint))
}

func TestComposeEsTableIdDetail(t *testing.T) {
	defaultStorageClusterId := 1
	sourceType := "es"
	indexSet := "system"
	tests := []struct {
		name            string
		tableId         string
		expectedTableId string
	}{
		{"table_id_with_dot", "test.demo", "test.demo"},
		{"table_id_without_dot", "test_demo", "test_demo.__default__"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualTableId, _, _ := NewSpacePusher().composeEsTableIdDetail(tt.tableId, uint(defaultStorageClusterId), sourceType, indexSet)
			assert.Equal(t, tt.expectedTableId, actualTableId)
		})
	}

	// 检验 key
	_, detailStr, _ := NewSpacePusher().composeEsTableIdDetail("test.demo", uint(defaultStorageClusterId), sourceType, indexSet)
	var detail map[string]any
	err := jsonx.UnmarshalString(detailStr, &detail)
	assert.NoError(t, err)
	expectedKey := mapset.NewSet[string]("storage_id", "db", "measurement")
	actualKey := mapset.NewSet[string]()
	for key, _ := range detail {
		actualKey.Add(key)
	}
	assert.True(t, expectedKey.Equal(actualKey))
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
}
