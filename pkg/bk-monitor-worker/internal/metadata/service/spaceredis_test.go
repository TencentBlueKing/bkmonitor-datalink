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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/jinzhu/gorm"
	"github.com/stretchr/testify/assert"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/bcs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/migrate"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/tenant"
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

	expectedResults := map[string]map[string]any{
		"table1.__default__": {
			"filters": []map[string]any{
				{"bcs_cluster_id": "BCS-K8S-00000", "namespace": nil},
			},
		},
		"table2.__default__": {
			"filters": []map[string]any{
				{"bcs_cluster_id": "BCS-K8S-00000", "namespace": nil},
			},
		},
		"table3.__default__": {
			"filters": []map[string]any{
				{"bcs_cluster_id": "BCS-K8S-00001", "namespace": "bkm-test-4"},
			},
		},
		"table4.__default__": {
			"filters": []map[string]any{
				{"bcs_cluster_id": "BCS-K8S-00001", "namespace": "bkm-test-4"},
			},
		},
		"table5.__default__": {
			"filters": []map[string]any{
				{"bcs_cluster_id": "BCS-K8S-00002", "namespace": "bkm-test-1"},
				{"bcs_cluster_id": "BCS-K8S-00002", "namespace": "bkm-test-2"},
				{"bcs_cluster_id": "BCS-K8S-00002", "namespace": "bkm-test-3"},
			},
		},
		"table6.__default__": {
			"filters": []map[string]any{
				{"bcs_cluster_id": "BCS-K8S-00002", "namespace": "bkm-test-1"},
				{"bcs_cluster_id": "BCS-K8S-00002", "namespace": "bkm-test-2"},
				{"bcs_cluster_id": "BCS-K8S-00002", "namespace": "bkm-test-3"},
			},
		},
	}

	assert.Equal(t, expectedResults, result)
}

func TestSpacePusher_getTableIdClusterId(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB

	// 创建 BCSClusterInfo 数据
	clusterInfos := []bcs.BCSClusterInfo{
		{
			ClusterID:          "BCS-K8S-00000",
			K8sMetricDataID:    1001,
			CustomMetricDataID: 2001,
			BkTenantId:         tenant.DefaultTenantId,
		},
		{
			ClusterID:          "BCS-K8S-00001",
			K8sMetricDataID:    1002,
			CustomMetricDataID: 2002,
			Status:             models.BcsClusterStatusDeleted, // 已删除
			IsDeletedAllowView: true,
			BkTenantId:         tenant.DefaultTenantId,
		},
		{
			ClusterID:          "BCS-K8S-00002",
			K8sMetricDataID:    1003,
			CustomMetricDataID: 2003,
			Status:             models.BcsRawClusterStatusDeleted, // 已删除
			BkTenantId:         tenant.DefaultTenantId,
		},
	}
	migrate.Migrate(context.TODO(), &bcs.BCSClusterInfo{})
	db.Delete(&bcs.BCSClusterInfo{})
	for _, ci := range clusterInfos {
		err := db.Create(&ci).Error
		assert.NoError(t, err)
	}
	// 创建 DataSourceResultTable 数据
	dataSourceResultTables := []resulttable.DataSourceResultTable{
		{
			BkTenantId: tenant.DefaultTenantId,
			BkDataId:   1001,
			TableId:    "table1",
		},
		{
			BkTenantId: tenant.DefaultTenantId,
			BkDataId:   2001,
			TableId:    "table2",
		},
		{
			BkTenantId: tenant.DefaultTenantId,
			BkDataId:   1002,
			TableId:    "table3",
		},
		{
			BkTenantId: tenant.DefaultTenantId,
			BkDataId:   2002,
			TableId:    "table4",
		},
		{
			BkTenantId: tenant.DefaultTenantId,
			BkDataId:   1003,
			TableId:    "table5",
		},
		{
			BkTenantId: tenant.DefaultTenantId,
			BkDataId:   2003,
			TableId:    "table6",
		},
	}
	db.Delete(&resulttable.DataSourceResultTable{})
	for _, dsrt := range dataSourceResultTables {
		err := db.Create(&dsrt).Error
		assert.NoError(t, err)
	}

	tableIds := []string{"table1", "table2", "table3", "table4", "table5", "table6"}
	data, err := NewSpacePusher().getTableIdBcsClusterId(tenant.DefaultTenantId, tableIds)
	assert.NoError(t, err)

	// 验证结果
	expected := map[string]string{
		"table1": "BCS-K8S-00000",
		"table2": "BCS-K8S-00000",
		"table3": "BCS-K8S-00001",
		"table4": "BCS-K8S-00001",
		"table5": "",
		"table6": "",
	}
	assert.Equal(t, expected, data)
}

func TestSpacePusher_refineTableIds(t *testing.T) {
	// 初始化测试数据库配置
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB

	// 创建 Influxdb 表数据
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

	// 创建 VM 表数据
	vmTableName := "vm_table_name"
	vmTable := storage.AccessVMRecord{ResultTableId: vmTableName}
	db.Delete(&vmTable)
	err = vmTable.Create(db)
	assert.NoError(t, err)

	// 创建 ES 表数据
	esTableName := "es_table_name"
	esTable := storage.ESStorage{TableID: esTableName, NeedCreateIndex: true}
	db.Delete(&esTable)
	err = esTable.Create(db)
	assert.NoError(t, err)

	// 不存在的表
	notExistTable := "not_exist_rt"

	// 调用 refineTableIds 方法
	ids, err := NewSpacePusher().refineTableIds([]string{itableName, itableName1, notExistTable, vmTableName, esTableName})

	// 断言结果，期望返回正确的表 ID
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{itableName, itableName1, vmTableName, esTableName}, ids)
}

func TestSpacePusher_refineEsTableIds(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	itableName := "i_table_test.dbname"
	iTable := storage.ESStorage{TableID: itableName, SourceType: models.EsSourceTypeLOG, NeedCreateIndex: true}
	db.Delete(&iTable)
	err := iTable.Create(db)
	assert.NoError(t, err)

	itableName1 := "i_table_test1.dbname1"
	iTable1 := storage.ESStorage{TableID: itableName1, SourceType: models.EsSourceTypeBKDATA, NeedCreateIndex: true}
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

	db.Delete(&space.Space{})

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

	var nilMap map[string]map[string]any

	tests := []struct {
		spaceType string
		spaceId   string
		hasError  bool
		want      map[string]map[string]any
	}{
		{spaceType: "bkcc", spaceId: "3", hasError: true, want: nilMap}, // 数据库无该记录
		{spaceType: "bkcc", spaceId: "2", hasError: false, want: map[string]map[string]any{
			"apache.net": {"filters": []map[string]any{}},
			"system.net": {"filters": []map[string]any{}},
		}},
		{spaceType: "bkci", spaceId: "test", hasError: false, want: map[string]map[string]any{"system.mem": {"filters": []map[string]any{}}}},
		{spaceType: "bksaas", spaceId: "test2", hasError: false, want: map[string]map[string]any{"system.io": {"filters": []map[string]any{}}}},
	}

	s := &SpacePusher{}
	for _, tt := range tests {
		t.Run(tt.spaceType+tt.spaceId, func(t *testing.T) {
			datavalues, err := s.ComposeEsTableIds(tt.spaceType, tt.spaceId)
			if tt.hasError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.want, datavalues)
		})
	}
}

func TestSpacePusher_ComposeDorisTableIds(t *testing.T) {
	t.Run("TestSpacePusher_GetBizIdBySpace", TestSpacePusher_GetBizIdBySpace)
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB

	obj := resulttable.ResultTable{TableId: "apache.net", BkBizId: 2, DefaultStorage: models.StorageTypeDoris, IsDeleted: false, IsEnable: true}
	obj2 := resulttable.ResultTable{TableId: "system.mem", BkBizId: -5, DefaultStorage: models.StorageTypeDoris, IsDeleted: false, IsEnable: true}
	obj3 := resulttable.ResultTable{TableId: "system.net", BkBizId: 2, DefaultStorage: models.StorageTypeDoris, IsDeleted: false, IsEnable: true}
	obj4 := resulttable.ResultTable{TableId: "system.io", BkBizId: -6, DefaultStorage: models.StorageTypeDoris, IsDeleted: false, IsEnable: true}

	db.Delete(obj)
	db.Delete(obj2)
	db.Delete(obj3)
	db.Delete(obj4)

	assert.NoError(t, obj.Create(db))
	assert.NoError(t, obj2.Create(db))
	assert.NoError(t, obj3.Create(db))
	assert.NoError(t, obj4.Create(db))

	var nilMap map[string]map[string]any

	tests := []struct {
		spaceType string
		spaceId   string
		hasError  bool
		want      map[string]map[string]any
	}{
		{spaceType: "bkcc", spaceId: "3", hasError: true, want: nilMap}, // 数据库无该记录
		{spaceType: "bkcc", spaceId: "2", hasError: false, want: map[string]map[string]any{
			"apache.net": {"filters": []map[string]any{}},
			"system.net": {"filters": []map[string]any{}},
		}},
		{spaceType: "bkci", spaceId: "test", hasError: false, want: map[string]map[string]any{"system.mem": {"filters": []map[string]any{}}}},
		{spaceType: "bksaas", spaceId: "test2", hasError: false, want: map[string]map[string]any{"system.io": {"filters": []map[string]any{}}}},
	}

	s := &SpacePusher{}
	for _, tt := range tests {
		t.Run(tt.spaceType+tt.spaceId, func(t *testing.T) {
			datavalues, err := s.ComposeDorisTableIds(tt.spaceType, tt.spaceId)
			if tt.hasError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.want, datavalues)
		})
	}
}

func TestSpacePusher_GetSpaceTableIdDataId(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
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
			BkTenantId: tenant.DefaultTenantId,
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
		BkTenantId: tenant.DefaultTenantId,
		BkDataId:   platformDataId,
		TableId:    platformRt,
		CreateTime: time.Now(),
	}
	err := dsrt.Create(db)
	assert.NoError(t, err)
	db.Delete(&resulttable.DataSource{}, "bk_data_id = ?", platformDataId)
	ds := resulttable.DataSource{
		BkTenantId:       tenant.DefaultTenantId,
		BkDataId:         platformDataId,
		IsPlatformDataId: true,
	}
	err = ds.Create(db)
	assert.NoError(t, err)

	pusher := NewSpacePusher()
	// 指定rtList
	dataMap, err := pusher.GetSpaceTableIdDataId(tenant.DefaultTenantId, "", "", []string{"rt_18000", "rt_18002"}, nil, nil)
	assert.NoError(t, err)
	assert.Equal(t, map[string]uint{"rt_18000": 18000, "rt_18002": 18002}, dataMap)

	// 执行类型，不指定结果表
	dataMap, err = pusher.GetSpaceTableIdDataId(tenant.DefaultTenantId, "bkcc_t", "2", nil, nil, nil)
	assert.NoError(t, err)
	assert.Equal(t, map[string]uint{"rt_18000": 18000, "rt_18001": 18001, "rt_18002": 18002}, dataMap)

	// 测试排除
	dataMap, err = pusher.GetSpaceTableIdDataId(tenant.DefaultTenantId, "bkcc_t", "2", nil, []uint{18000, 18002}, nil)
	assert.NoError(t, err)
	assert.Equal(t, map[string]uint{"rt_18001": 18001}, dataMap)

	// 不包含全局数据源
	opt := optionx.NewOptions(map[string]any{"includePlatformDataId": false})
	dataMap, err = pusher.GetSpaceTableIdDataId(tenant.DefaultTenantId, "bkcc_t", "2", nil, nil, opt)
	fmt.Println(dataMap)
}

func TestSpacePusher_getTableInfoForInfluxdbAndVm(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")

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
		BkTenantId:             tenant.DefaultTenantId,
	}
	db.Delete(&iTable)
	err = iTable.Create(db)
	assert.NoError(t, err)

	cluster := storage.ClusterInfo{
		ClusterName: "vm_cluster_abc",
		ClusterType: models.StorageTypeVM,
		IsAuth:      false,
		ClusterID:   6,
	}
	db.Delete(&cluster, "cluster_name = ?", cluster.ClusterName)
	err = cluster.Create(db)
	assert.NoError(t, err)
	vmTableName := "vm_table_name"
	vmTable := storage.AccessVMRecord{
		ResultTableId:   vmTableName,
		VmResultTableId: "vm_result_table_id",
		VmClusterId:     cluster.ClusterID,
		BkTenantId:      tenant.DefaultTenantId,
	}
	db.Delete(&vmTable)
	err = vmTable.Create(db)
	assert.NoError(t, err)

	opVal1 := models.OptionBase{Value: "test_vmrt_cmdb_level", ValueType: "string", Creator: "system"}
	vmrtOption := resulttable.ResultTableOption{
		TableID:    vmTableName,
		Name:       "cmdb_level_vm_rt",
		OptionBase: opVal1,
	}
	db.Delete(&vmrtOption)
	err = vmrtOption.Create(db)
	assert.NoError(t, err)

	data, err := NewSpacePusher().getTableInfoForInfluxdbAndVm(tenant.DefaultTenantId, []string{itableName, vmTableName})
	assert.NoError(t, err)
	assert.Equal(t, 2, len(data))
	vmData, err := jsonx.MarshalString(data[vmTableName])
	assert.NoError(t, err)

	assert.JSONEq(t, `{"cluster_name":"","cmdb_level_vm_rt":"test_vmrt_cmdb_level","db":"","measurement":"","storage_name":"vm_cluster_abc","tags_key":[],"storage_id":6,"vm_rt":"vm_result_table_id","storage_type":"victoria_metrics"}`, vmData)
	itableData, err := jsonx.MarshalString(data[itableName])
	assert.NoError(t, err)
	assert.JSONEq(t, `{"cluster_name":"default","db":"dbname","measurement":"i_table_test","storage_id":2,"storage_name":"","tags_key":["t1","t2"],"vm_rt":"","storage_type":"influxdb"}`, itableData)
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
		mapFilter := filter.([]map[string]any)
		assert.Equal(t, len(mapFilter), 1)
	}
}

func TestSpaceRedisSvc_ComposeEsTableIds(t *testing.T) {
	// 初始化数据库配置
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB

	// 清理所有相关表数据
	cleanTestData := func() {
		db.Delete(&space.SpaceResource{})
		db.Delete(&space.Space{})
		db.Delete(&resulttable.ResultTable{})
	}
	cleanTestData()       // 测试开始前清理数据
	defer cleanTestData() // 测试结束后清理数据

	// 准备测试用数据
	resourceIdTest1 := "1"
	spaceResources := []space.SpaceResource{
		{
			SpaceTypeId:  "bkci",
			SpaceId:      "test6",
			ResourceType: "bkcc",
			ResourceId:   &resourceIdTest1,
		},
		{
			SpaceTypeId:  "bkci",
			SpaceId:      "test7",
			ResourceType: "bkcc",
			ResourceId:   &resourceIdTest1,
		},
	}
	insertTestData(t, db, spaceResources)

	// 测试 GetRelatedSpaces
	relatedSpaceIds, err := NewSpacePusher().GetRelatedSpaces("bkcc", "1", "bkci")
	assert.NoError(t, err)
	assert.Equal(t, len(relatedSpaceIds), 2)
	assert.ElementsMatch(t, relatedSpaceIds, []string{"test6", "test7"}) // 无序比较

	// 准备 Space 测试数据
	spaceObjs := []space.Space{
		{
			SpaceTypeId: "bkci",
			SpaceId:     "test6",
			SpaceName:   "testSpace6",
			Id:          1050,
		},
		{
			SpaceTypeId: "bkci",
			SpaceId:     "test7",
			SpaceName:   "testSpace7",
			Id:          1051,
		},
	}
	insertTestData(t, db, spaceObjs)

	// 准备 ResultTable 测试数据
	resultTable := resulttable.ResultTable{
		TableId:        "-1050_space_test.__default__",
		BkBizId:        -1050,
		DefaultStorage: models.StorageTypeES,
		IsDeleted:      false,
		IsEnable:       true,
	}
	err = resultTable.Create(db)
	assert.NoError(t, err)

	resultTable2 := resulttable.ResultTable{
		TableId:        "-1051_space_test.__default__",
		BkBizId:        -1050,
		DefaultStorage: models.StorageTypeDoris,
		IsDeleted:      false,
		IsEnable:       true,
	}
	err = resultTable2.Create(db)
	assert.NoError(t, err)

	// 测试 ResultTable 查询
	var rtList []resulttable.ResultTable
	err = resulttable.NewResultTableQuerySet(db).
		Select(resulttable.ResultTableDBSchema.TableId).
		BkBizIdEq(-1050).
		DefaultStorageIn(models.StorageTypeES, models.StorageTypeDoris).
		IsDeletedEq(false).
		IsEnableEq(true).
		All(&rtList)
	assert.NoError(t, err)
	assert.NotEmpty(t, rtList)
	assert.Equal(t, rtList[0].TableId, "-1050_space_test.__default__")

	// 测试 getBizIdsBySpace
	relatedBizIds, err := NewSpacePusher().getBizIdsBySpace("bkcc", relatedSpaceIds)
	assert.NoError(t, err)
	assert.Equal(t, len(relatedBizIds), 2)
	assert.ElementsMatch(t, relatedBizIds, []int{-1050, -1051}) // 无序比较

	// 测试 ComposeRelatedBkciTableIds
	data, err := NewSpacePusher().ComposeRelatedBkciTableIds("bkcc", "1")
	assert.NoError(t, err)
	assert.NotNil(t, data)
	// 验证 ComposeRelatedBkciTableIds 的返回结果
	expectedTableId := "-1050_space_test.__default__"
	assert.Contains(t, data, expectedTableId, "Expected table ID not found in the result")

	expectedTableId2 := "-1051_space_test.__default__"
	assert.Contains(t, data, expectedTableId2, "Expected table ID not found in the result")
}

// 通用数据插入函数
func insertTestData[T any](t *testing.T, db *gorm.DB, objs []T) {
	for _, obj := range objs {
		err := db.Create(&obj).Error
		assert.NoError(t, err)
		t.Logf("Inserted data: %+v", obj) // 打印插入的数据
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
	println(data)
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
	obj := resulttable.ResultTable{TableId: "not_data_label", DataLabel: nil, BkTenantId: tenant.DefaultTenantId}
	db.Delete(obj)
	assert.NoError(t, obj.Create(db))
	// with data_label
	dataLabel := "data_label_value"
	obj = resulttable.ResultTable{TableId: "data_label", DataLabel: &dataLabel, BkTenantId: tenant.DefaultTenantId}
	db.Delete(obj)
	assert.NoError(t, obj.Create(db))
	// with data_label_comma
	dataLabelComma := "data_label_value1,data_label_value2"
	obj = resulttable.ResultTable{TableId: "data_label_comma", DataLabel: &dataLabelComma, BkTenantId: tenant.DefaultTenantId}
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
		{"table_id with data_label_comma", []string{"data_label_comma"}, []string{"data_label_value1", "data_label_value2"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualList, _ := NewSpacePusher().getDataLabelByTableId(tenant.DefaultTenantId, tt.tableIdList)
			assert.Equal(t, tt.expectedList, actualList)
		})
	}
}

func TestGetDataLabelTableIdMap(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB

	// 准备测试数据
	// 创建不带数据标签的结果表
	obj1 := resulttable.ResultTable{
		TableId:    "table_without_label",
		IsEnable:   true,
		IsDeleted:  false,
		DataLabel:  nil,
		BkTenantId: tenant.DefaultTenantId,
	}
	db.Delete(obj1)
	assert.NoError(t, obj1.Create(db))

	// 创建带单个数据标签的结果表
	singleLabel := "test_label_1"
	obj2 := resulttable.ResultTable{
		TableId:    "table_with_single_label",
		IsEnable:   true,
		IsDeleted:  false,
		DataLabel:  &singleLabel,
		BkTenantId: tenant.DefaultTenantId,
	}
	db.Delete(obj2)
	assert.NoError(t, obj2.Create(db))

	// 创建带多个数据标签的结果表
	multiLabel := "test_label_1,test_label_2,test_label_3"
	obj3 := resulttable.ResultTable{
		TableId:    "table_with_multi_label",
		IsEnable:   true,
		IsDeleted:  false,
		DataLabel:  &multiLabel,
		BkTenantId: tenant.DefaultTenantId,
	}
	db.Delete(obj3)
	assert.NoError(t, obj3.Create(db))

	// 创建另一个带相同标签的结果表
	obj4 := resulttable.ResultTable{
		TableId:    "table_with_same_label",
		IsEnable:   true,
		IsDeleted:  false,
		DataLabel:  &singleLabel,
		BkTenantId: tenant.DefaultTenantId,
	}
	db.Delete(obj4)
	assert.NoError(t, obj4.Create(db))

	// 创建已删除的结果表（不应该被包含）
	deletedLabel := "deleted_label"
	obj5 := resulttable.ResultTable{
		TableId:    "deleted_table",
		IsEnable:   true,
		IsDeleted:  true,
		DataLabel:  &deletedLabel,
		BkTenantId: tenant.DefaultTenantId,
	}
	db.Delete(obj5)
	assert.NoError(t, obj5.Create(db))

	// 创建已禁用的结果表（不应该被包含）
	disabledLabel := "disabled_label"
	obj6 := resulttable.ResultTable{
		TableId:    "disabled_table",
		IsEnable:   false,
		IsDeleted:  false,
		DataLabel:  &disabledLabel,
		BkTenantId: tenant.DefaultTenantId,
	}
	db.Delete(obj6)
	assert.NoError(t, obj6.Create(db))

	tests := []struct {
		name          string
		dataLabelList []string
		expectedMap   map[string][]string
		expectedError bool
	}{
		{
			name:          "空数据标签列表",
			dataLabelList: []string{},
			expectedMap:   nil,
			expectedError: true,
		},
		{
			name:          "查询单个存在的数据标签",
			dataLabelList: []string{"test_label_1"},
			expectedMap: map[string][]string{
				"test_label_1": {"table_with_single_label", "table_with_multi_label", "table_with_same_label"},
			},
			expectedError: false,
		},
		{
			name:          "查询多个存在的数据标签",
			dataLabelList: []string{"test_label_1", "test_label_2"},
			expectedMap: map[string][]string{
				"test_label_1": {"table_with_single_label", "table_with_multi_label", "table_with_same_label"},
				"test_label_2": {"table_with_multi_label"},
			},
			expectedError: false,
		},
		{
			name:          "查询不存在的数据标签",
			dataLabelList: []string{"non_existent_label"},
			expectedMap:   map[string][]string{},
			expectedError: false,
		},
		{
			name:          "查询已删除标签的数据",
			dataLabelList: []string{"deleted_label"},
			expectedMap:   map[string][]string{},
			expectedError: false,
		},
		{
			name:          "查询已禁用标签的数据",
			dataLabelList: []string{"disabled_label"},
			expectedMap:   map[string][]string{},
			expectedError: false,
		},
		{
			name:          "查询重复的数据标签",
			dataLabelList: []string{"test_label_1", "test_label_1", "test_label_2"},
			expectedMap: map[string][]string{
				"test_label_1": {"table_with_single_label", "table_with_multi_label", "table_with_same_label"},
				"test_label_2": {"table_with_multi_label"},
			},
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualMap, err := NewSpacePusher().getDataLabelTableIdMap(tenant.DefaultTenantId, tt.dataLabelList)

			if tt.expectedError {
				assert.Error(t, err)
				assert.Nil(t, actualMap)
			} else {
				assert.NoError(t, err)

				// 验证映射的键数量
				assert.Equal(t, len(tt.expectedMap), len(actualMap))

				// 验证每个数据标签对应的结果表
				for dataLabel, expectedTableIds := range tt.expectedMap {
					actualTableIds, exists := actualMap[dataLabel]
					assert.True(t, exists, "数据标签 %s 应该存在", dataLabel)

					// 由于数据库查询结果的顺序可能不固定，需要排序后比较
					sort.Strings(expectedTableIds)
					sort.Strings(actualTableIds)
					assert.Equal(t, expectedTableIds, actualTableIds, "数据标签 %s 对应的结果表不匹配", dataLabel)
				}
			}
		})
	}
}

func TestGetAllDataLabelTableId(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	// 初始数据
	db := mysql.GetDBSession().DB
	// not data_label
	obj := resulttable.ResultTable{TableId: "not_data_label", IsEnable: true, DataLabel: nil, BkTenantId: "test"}
	db.Delete(obj)
	assert.NoError(t, obj.Create(db))
	// with data_label
	dataLabel := "data_label_value"
	obj = resulttable.ResultTable{TableId: "data_label", IsEnable: true, DataLabel: &dataLabel, BkTenantId: "test"}
	db.Delete(obj)
	assert.NoError(t, obj.Create(db))

	dataLabel1 := "data_label_value1"
	obj = resulttable.ResultTable{TableId: "data_label1", IsEnable: true, DataLabel: &dataLabel1, BkTenantId: "test"}
	db.Delete(obj)
	assert.NoError(t, obj.Create(db))

	dataLabel2 := "data_label_value,data_label_value2"
	obj = resulttable.ResultTable{TableId: "data_label2", IsEnable: true, DataLabel: &dataLabel2, BkTenantId: "test"}
	db.Delete(obj)
	assert.NoError(t, obj.Create(db))

	obj = resulttable.ResultTable{TableId: "test_1_dbm.cpu_detail", IsEnable: true, DataLabel: nil, BkTenantId: "test"}
	db.Delete(obj)
	assert.NoError(t, obj.Create(db))

	obj = resulttable.ResultTable{TableId: "test_1_sys.cpu_detail", IsEnable: true, DataLabel: nil, BkTenantId: "test"}
	db.Delete(obj)
	assert.NoError(t, obj.Create(db))

	obj = resulttable.ResultTable{TableId: "test_1_sys.cpu_detail", IsEnable: true, DataLabel: nil, BkTenantId: "test"}
	db.Delete(obj)
	assert.NoError(t, obj.Create(db))

	obj = resulttable.ResultTable{TableId: "test_1_sys.hhh", IsEnable: true, DataLabel: nil, BkTenantId: "test"}
	db.Delete(obj)
	assert.NoError(t, obj.Create(db))

	cfg.EnableMultiTenantMode = true
	data, err := NewSpacePusher().getAllDataLabelTableId("test")
	assert.NoError(t, err)
	dataLabelSet := mapset.NewSet[string]()
	for dataLabel := range data {
		dataLabelSet.Add(dataLabel)
	}
	expectedSet := mapset.NewSet("data_label_value|test", "data_label_value1|test", "data_label_value2|test", "system.cpu_detail|test", "dbm_system.cpu_detail|test")
	t.Logf("dataLabelSet: %v", dataLabelSet)
	t.Logf("expectedSet: %v", expectedSet)
	assert.True(t, expectedSet.IsSubset(dataLabelSet))
	t.Logf("data: %v", data)
	assert.Equal(t, []string{"data_label", "data_label2"}, data["data_label_value|test"])
	assert.Equal(t, []string{"test_1_sys.cpu_detail"}, data["system.cpu_detail|test"])
	assert.Equal(t, []string{"test_1_dbm.cpu_detail"}, data["dbm_system.cpu_detail|test"])

	cfg.EnableMultiTenantMode = false
	data, err = NewSpacePusher().getAllDataLabelTableId("test")
	assert.NoError(t, err)
	dataLabelSet = mapset.NewSet[string]()
	for dataLabel := range data {
		dataLabelSet.Add(dataLabel)
	}
	expectedSet = mapset.NewSet("data_label_value", "data_label_value1", "data_label_value2")
	assert.True(t, expectedSet.IsSubset(dataLabelSet))
	assert.Equal(t, []string{"data_label", "data_label2"}, data["data_label_value"])
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

// func TestClearSpaceToRt(t *testing.T) {
// 	mocker.InitTestDBConfig("../../../bmw_test.yaml")
// 	// 添加space资源
// 	db := mysql.GetDBSession().DB
// 	spaceType, spaceId1, spaceId2, spaceId3 := "bkcc", "1", "2", "3"
// 	obj1 := space.Space{SpaceTypeId: spaceType, SpaceId: spaceId1, SpaceName: spaceId1, BkTenantId: tenant.DefaultTenantId}
// 	obj2 := space.Space{SpaceTypeId: spaceType, SpaceId: spaceId2, SpaceName: spaceId2, BkTenantId: "test"}
// 	obj3 := space.Space{SpaceTypeId: spaceType, SpaceId: spaceId3, SpaceName: spaceId3, BkTenantId: "test2"}
// 	db.Delete(space.Space{})
// 	assert.NoError(t, obj1.Create(db))
// 	assert.NoError(t, obj2.Create(db))
// 	assert.NoError(t, obj3.Create(db))

// 	// 多租户
// 	cfg.EnableMultiTenantMode = true
// 	redisClient.HKeysValue = append(redisClient.HKeysValue, "bkcc__1|system", "bkcc__2|test", "bkcc__4|test2")

// 	// 清理数据
// 	clearer := NewSpaceRedisClearer()
// 	clearer.ClearSpaceToRt()

// 	t.Logf("redisClient.HKeysValue: %v", redisClient.HKeysValue)
// 	assert.Equal(t, 2, len(redisClient.HKeysValue))
// 	assert.Equal(t, slicex.StringList2Set([]string{"bkcc__1|system", "bkcc__2|test"}), slicex.StringList2Set(redisClient.HKeysValue))

// 	// 单租户
// 	cfg.EnableMultiTenantMode = false
// 	redisClient.HKeysValue = append(redisClient.HKeysValue, "bkcc__1", "bkcc__2", "bkcc__4")

// 	// 清理数据
// 	clearer.ClearSpaceToRt()

// 	t.Logf("redisClient.HKeysValue: %v", redisClient.HKeysValue)
// 	assert.Equal(t, 2, len(redisClient.HKeysValue))
// 	assert.Equal(t, slicex.StringList2Set([]string{"bkcc__1", "bkcc__2"}), slicex.StringList2Set(redisClient.HKeysValue))
// }

// func TestClearDataLabelToRt(t *testing.T) {
// 	mocker.InitTestDBConfig("../../../bmw_test.yaml")
// 	// 添加space资源
// 	db := mysql.GetDBSession().DB
// 	rt1, rt2, rt3 := "demo.test1", "demo.test2", "demo.test3"
// 	rtDl1, rtDl2, rtDl3 := "data_label1", "data_label2", "data_label3"
// 	rtObj1 := resulttable.ResultTable{TableId: rt1, IsDeleted: false, IsEnable: true, DataLabel: &rtDl1, BkTenantId: tenant.DefaultTenantId}
// 	rtObj2 := resulttable.ResultTable{TableId: rt2, IsDeleted: false, IsEnable: true, DataLabel: &rtDl2, BkTenantId: "test"}
// 	rtObj3 := resulttable.ResultTable{TableId: rt3, IsDeleted: false, IsEnable: true, DataLabel: &rtDl3, BkTenantId: "test2"}
// 	db.Delete(&resulttable.ResultTable{})
// 	assert.NoError(t, rtObj1.Create(db))
// 	assert.NoError(t, rtObj2.Create(db))
// 	assert.NoError(t, rtObj3.Create(db))

// 	// 多租户
// 	cfg.EnableMultiTenantMode = true
// 	redisClient.HKeysValue = append(redisClient.HKeysValue, "data_label1|system", "data_label2|test", "data_label4|test2")

// 	// 清理数据
// 	clearer := NewSpaceRedisClearer()
// 	clearer.ClearDataLabelToRt()

// 	assert.Equal(t, 2, len(redisClient.HKeysValue))
// 	assert.Equal(t, slicex.StringList2Set([]string{"data_label1|system", "data_label2|test"}), slicex.StringList2Set(redisClient.HKeysValue))

// 	// 单租户
// 	cfg.EnableMultiTenantMode = false
// 	redisClient.HKeysValue = append(redisClient.HKeysValue, "data_label1", "data_label2", "data_label4")

// 	// 清理数据
// 	clearer.ClearDataLabelToRt()

// 	assert.Equal(t, 2, len(redisClient.HKeysValue))
// 	assert.Equal(t, slicex.StringList2Set([]string{"data_label1", "data_label2"}), slicex.StringList2Set(redisClient.HKeysValue))
// }

// func TestClearRtDetail(t *testing.T) {
// 	mocker.InitTestDBConfig("../../../bmw_test.yaml")
// 	// 添加space资源
// 	db := mysql.GetDBSession().DB
// 	rt1, rt2, rt3 := "demo.test1", "demo.test2", "demo.test3"
// 	rtDl1, rtDl2, rtDl3 := "data_label1", "data_label2", "data_label3"
// 	rtObj1 := resulttable.ResultTable{TableId: rt1, IsDeleted: false, IsEnable: true, DataLabel: &rtDl1, BkTenantId: tenant.DefaultTenantId}
// 	rtObj2 := resulttable.ResultTable{TableId: rt2, IsDeleted: true, IsEnable: false, DataLabel: &rtDl2, BkTenantId: "test"}
// 	rtObj3 := resulttable.ResultTable{TableId: rt3, IsDeleted: false, IsEnable: true, DataLabel: &rtDl3, BkTenantId: "test2"}
// 	db.Delete(&resulttable.ResultTable{})
// 	assert.NoError(t, rtObj1.Create(db))
// 	assert.NoError(t, rtObj2.Create(db))
// 	assert.NoError(t, rtObj3.Create(db))

// 	// 多租户
// 	cfg.EnableMultiTenantMode = true
// 	redisClient.HKeysValue = append(redisClient.HKeysValue, "demo.test1|system", "demo.test2|test", "demo.test4|test2")

// 	// 清理数据
// 	clearer := NewSpaceRedisClearer()
// 	clearer.ClearRtDetail()

// 	assert.Equal(t, 1, len(redisClient.HKeysValue))
// 	assert.Equal(t, slicex.StringList2Set([]string{"demo.test1|system"}), slicex.StringList2Set(redisClient.HKeysValue))

// 	// 单租户
// 	cfg.EnableMultiTenantMode = false
// 	redisClient.HKeysValue = append(redisClient.HKeysValue, "demo.test1", "demo.test2", "demo.test4")

// 	// 清理数据
// 	clearer.ClearRtDetail()

// 	assert.Equal(t, 1, len(redisClient.HKeysValue))
// 	assert.Equal(t, slicex.StringList2Set([]string{"demo.test1"}), slicex.StringList2Set(redisClient.HKeysValue))
// }

func TestComposeEsTableIdOptions(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	// mocker.InitTestDBConfig("../../../bmw_test.yaml")
	// 初始数据
	db := mysql.GetDBSession().DB

	migrate.Migrate(context.TODO(), &resulttable.ResultTableOption{}, &resulttable.ResultTable{})

	// 创建rt
	rt1, rt2, rt3 := "demo.test1", "demo.test2", "demo.test3"
	rtObj1 := resulttable.ResultTable{TableId: rt1, IsDeleted: false, IsEnable: true}
	rtObj2 := resulttable.ResultTable{TableId: rt2, IsDeleted: true, IsEnable: false}
	rtObj3 := resulttable.ResultTable{TableId: rt3, IsDeleted: false, IsEnable: true}
	db.Delete(&resulttable.ResultTable{})
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
	db.Delete(&resulttable.ResultTableOption{})
	assert.NoError(t, rtOp1.Create(db))
	assert.NoError(t, rtOp2.Create(db))
	assert.NoError(t, rtOp3.Create(db))

	// 获取正常数据
	spacePusher := NewSpacePusher()
	data := spacePusher.composeEsTableIdOptions([]string{rt1, rt2, rt3})
	assert.Equal(t, 3, len(data))
	assert.Equal(t, map[string]any{"name": "v1"}, data[rt1][rtOp1.Name])

	// 获取不存在的rt数据
	data = spacePusher.composeEsTableIdOptions([]string{"not_exist"})
	assert.Equal(t, 0, len(data))
}

func TestSpacePusher_PushBkAppToSpace(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")

	db := mysql.GetDBSession().DB

	db.Delete(&space.Space{})
	spaces := []space.Space{
		{
			SpaceTypeId: "bkcc",
			SpaceId:     "1",
			SpaceName:   "1",
			BkTenantId:  tenant.DefaultTenantId,
		},
		{
			SpaceTypeId: "bkcc",
			SpaceId:     "2",
			SpaceName:   "2",
			BkTenantId:  "test",
		},
		{
			SpaceTypeId: "bkci",
			SpaceId:     "3",
			SpaceName:   "3",
			BkTenantId:  "test2",
		},
	}
	for _, space := range spaces {
		assert.NoError(t, db.Create(&space).Error)
	}

	data := space.BkAppSpaces{
		{
			BkAppCode: "default_app_code",
			SpaceUID:  "*",
			IsEnable:  true,
		},
		{
			BkAppCode: "other_code",
			SpaceUID:  "bkcc__1",
			IsEnable:  true,
		},
		{
			BkAppCode: "my_code",
			SpaceUID:  "bkcc__2",
			IsEnable:  true,
		},
		{
			BkAppCode: "my_code",
			SpaceUID:  "bkci__3",
			IsEnable:  true,
		},
	}

	n := time.Now()

	migrate.Migrate(context.TODO(), &space.BkAppSpaceRecord{})

	db.Delete(space.BkAppSpaceRecord{})

	for _, d := range data {
		d.CreateTime = n
		d.UpdateTime = n
		err := db.Create(d).Error

		assert.NoError(t, err)
	}

	err := db.Model(space.BkAppSpaceRecord{}).Where("bk_app_code = ?", "other_code").Updates(map[string]bool{"is_enable": false}).Error
	assert.NoError(t, err)

	client := redis.GetStorageRedisInstance()
	_ = client.Delete(cfg.BkAppToSpaceKey)

	pusher := NewSpacePusher()
	err = pusher.PushBkAppToSpace()
	assert.NoError(t, err)

	actual := client.HGetAll(cfg.BkAppToSpaceKey)

	expected := map[string]string{
		"my_code":          `["bkcc__2","bkci__3"]`,
		"default_app_code": `["*"]`,
		"other_code":       `[]`,
	}

	assert.Equal(t, expected, actual)

	cfg.EnableMultiTenantMode = true

	err = pusher.PushBkAppToSpace()
	assert.NoError(t, err)

	actual = client.HGetAll(cfg.BkAppToSpaceKey)
	expected = map[string]string{
		"my_code":          `["bkcc__2|test","bkci__3|test2"]`,
		"default_app_code": `["*"]`,
		"other_code":       `[]`,
	}
	assert.Equal(t, expected, actual)
}

func TestSpacePusher_PushEsTableIdDetail(t *testing.T) {
	// 初始化数据库
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	// 准备测试数据
	tableID := "bklog.test_rt"
	storageClusterID := uint(1)
	sourceType := "log"
	indexSet := "index_1"

	rtObj1 := resulttable.ResultTable{TableId: tableID, IsDeleted: false, IsEnable: true}
	db.Delete(rtObj1, "table_id=?", rtObj1.TableId)
	assert.NoError(t, rtObj1.Create(db))

	db.AutoMigrate(&storage.ESStorage{}, &resulttable.ResultTableOption{}, &storage.ClusterRecord{})

	// 插入 ESStorage 数据
	esStorages := []storage.ESStorage{
		{
			TableID:          tableID,
			StorageClusterID: storageClusterID,
			SourceType:       sourceType,
			IndexSet:         indexSet,
			NeedCreateIndex:  true,
			OriginTableId:    "bklog.real_rt",
		},
	}
	for _, esStorage := range esStorages {
		db.Delete(&storage.ESStorage{}, "table_id = ?", esStorage.TableID)
		err := db.Create(&esStorage).Error
		assert.NoError(t, err, "Failed to insert ESStorage")
	}

	// 插入 ResultTableOption 数据
	tableOption := resulttable.ResultTableOption{
		TableID: tableID,
		Name:    "shard_count",
		OptionBase: models.OptionBase{
			Value:      `{"shards": 3}`,
			ValueType:  "json",
			Creator:    "system",
			CreateTime: time.Now(),
		},
	}
	assert.NoError(t, db.Create(&tableOption).Error, "Failed to insert ResultTableOption")

	now := time.Now()
	// 插入StorageClusterRecord数据
	testRecords := []storage.ClusterRecord{
		{
			TableID:     "bklog.real_rt",
			ClusterID:   1,
			IsDeleted:   false,
			IsCurrent:   true,
			Creator:     "test_creator",
			CreateTime:  now,
			EnableTime:  &now,
			DisableTime: nil,
			DeleteTime:  nil,
		},
		{
			TableID:     "bklog.real_rt",
			ClusterID:   2,
			IsDeleted:   false,
			IsCurrent:   true,
			Creator:     "test_creator",
			CreateTime:  now,
			EnableTime:  &now,
			DisableTime: nil,
			DeleteTime:  nil,
		},
	}

	// 执行插入
	for _, record := range testRecords {
		db.Delete(&storage.ClusterRecord{}, "table_id = ? AND cluster_id = ?", tableID, record.ClusterID)
		err := db.Create(&record).Error
		assert.NoError(t, err, "Failed to insert StorageClusterRecord")
	}

	fieldAliasRecords := []resulttable.ESFieldQueryAliasOption{
		{
			TableID:    tableID,
			FieldPath:  "__ext.pod_name",
			PathType:   "keyword",
			QueryAlias: "pod_name",
			IsDeleted:  false,
		},
		{
			TableID:    tableID,
			FieldPath:  "__ext.pod_id",
			PathType:   "keyword",
			QueryAlias: "pod_id",
			IsDeleted:  false,
		},
	}
	// 执行插入
	for _, record := range fieldAliasRecords {
		db.Delete(&resulttable.ESFieldQueryAliasOption{}, "table_id = ? AND field_path = ?", tableID, record.FieldPath)
		err := db.Create(&record).Error
		assert.NoError(t, err, "Failed to insert ESFieldQueryAliasOption")
	}

	// 捕获日志输出
	var logBuffer bytes.Buffer
	log.SetOutput(&logBuffer) // 将日志输出到 buffer
	defer log.SetOutput(nil)  // 恢复原始日志输出

	// 执行测试方法
	pusher := NewSpacePusher()
	err := pusher.PushEsTableIdDetail([]string{tableID}, false)
	assert.NoError(t, err, "PushEsTableIdDetail should not return an error")
}

func TestSpacePusher_PushDorisTableIdDetail(t *testing.T) {
	// 初始化数据库
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	// 准备测试数据
	tableID := "bklog.test_rt"
	storageClusterID := uint(1)
	dataLabel := "test_label"

	rtObj1 := resulttable.ResultTable{TableId: tableID, IsDeleted: false, IsEnable: true, DataLabel: &dataLabel}
	db.Delete(rtObj1, "table_id=?", rtObj1.TableId)
	assert.NoError(t, rtObj1.Create(db))

	db.AutoMigrate(&storage.ESStorage{}, &resulttable.ResultTableOption{}, &storage.ClusterRecord{})

	// 创建DorisStorage记录
	dorisStorages := []storage.DorisStorage{
		{
			TableID:          tableID,
			BkbaseTableID:    "bklog_test_rt_bkbase",
			StorageClusterID: storageClusterID,
			IndexSet:         "index_1",
			SourceType:       "log",
		},
	}
	for _, dorisStorage := range dorisStorages {
		db.Delete(&storage.ESStorage{}, "table_id = ?", dorisStorage.TableID)
		err := db.Create(&dorisStorage).Error
		assert.NoError(t, err, "Failed to insert DorisStorage")
	}

	fieldAliasRecords := []resulttable.ESFieldQueryAliasOption{
		{
			TableID:    tableID,
			FieldPath:  "__ext.pod_name",
			PathType:   "keyword",
			QueryAlias: "pod_name",
			IsDeleted:  false,
		},
		{
			TableID:    tableID,
			FieldPath:  "__ext.pod_id",
			PathType:   "keyword",
			QueryAlias: "pod_id",
			IsDeleted:  false,
		},
	}
	// 执行插入
	for _, record := range fieldAliasRecords {
		db.Delete(&resulttable.ESFieldQueryAliasOption{}, "table_id = ? AND field_path = ?", tableID, record.FieldPath)
		err := db.Create(&record).Error
		assert.NoError(t, err, "Failed to insert ESFieldQueryAliasOption")
	}

	// 捕获日志输出
	var logBuffer bytes.Buffer
	log.SetOutput(&logBuffer) // 将日志输出到 buffer
	defer log.SetOutput(nil)  // 恢复原始日志输出

	// 执行测试方法
	pusher := NewSpacePusher()
	err := pusher.PushDorisTableIdDetail([]string{tableID}, false)
	assert.NoError(t, err, "PushEsTableIdDetail should not return an error")
}

func TestSpacePusher_ComposeData(t *testing.T) {
	// 初始化数据库
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	db.AutoMigrate(&storage.ESStorage{}, &resulttable.ResultTable{}, &storage.ClusterRecord{})

	// 准备测试数据
	spaceType := "bkcc"
	spaceId := "1001"
	tableID1 := "1001_bkmonitor_time_series_50010.__default__"
	tableID2 := "1001_bkmonitor_time_series_50011.__default__"
	tableID3 := "1001_bkmonitor_time_series_50012.__default__"
	var defaultFilters []map[string]any

	// 数据源表
	dataSources := []resulttable.DataSource{
		{
			BkDataId:               50010,
			DataName:               "data_link_test",
			EtlConfig:              "bk_standard_v2_time_series",
			IsPlatformDataId:       true,
			IsTenantSpecificGlobal: true,
			BkTenantId:             tenant.DefaultTenantId,
		},
		{
			BkDataId:               50011,
			DataName:               "data_link_test_2",
			EtlConfig:              "bk_standard_v2_time_series",
			IsPlatformDataId:       true,
			IsTenantSpecificGlobal: true,
			BkTenantId:             tenant.DefaultTenantId,
		},
		{
			BkDataId:               50012,
			DataName:               "data_link_test_3",
			EtlConfig:              "test",
			IsPlatformDataId:       false,
			IsTenantSpecificGlobal: false,
			BkTenantId:             tenant.DefaultTenantId,
		},
	}

	// 插入 DataSource 数据
	for _, ds := range dataSources {
		db.Delete(&resulttable.DataSource{}, "bk_data_id = ?", ds.BkDataId)
		assert.NoError(t, db.Create(&ds).Error, "Failed to insert DataSource")
	}

	// 插入 ResultTable 数据
	resultTables := []resulttable.ResultTable{
		{
			TableId:      tableID1,
			BkBizId:      1001,
			BkBizIdAlias: "appid",
			BkTenantId:   tenant.DefaultTenantId,
		},
		{
			TableId:      tableID2,
			BkBizId:      1001,
			BkBizIdAlias: "",
			BkTenantId:   tenant.DefaultTenantId,
		},
		{
			TableId:      tableID3,
			BkBizId:      1002,
			BkBizIdAlias: "",
			BkTenantId:   tenant.DefaultTenantId,
		},
	}
	for _, rt := range resultTables {
		db.Delete(&resulttable.ResultTable{}, "table_id = ?", rt.TableId)
		assert.NoError(t, db.Create(&rt).Error, "Failed to insert ResultTable")
	}

	// 插入 SpaceDataSource 数据
	spaceDataSources := []space.SpaceDataSource{
		{
			SpaceTypeId: "bkcc",
			SpaceId:     "1001",
			BkDataId:    50010,
		},
		{
			SpaceTypeId: "bkcc",
			SpaceId:     "1001",
			BkDataId:    50011,
		},
		{
			SpaceTypeId: "bkcc",
			SpaceId:     "1002",
			BkDataId:    50012,
		},
	}
	for _, sds := range spaceDataSources {
		db.Delete(&space.SpaceDataSource{}, "bk_data_id = ?", sds.BkDataId)
		assert.NoError(t, db.Create(&sds).Error, "Failed to insert SpaceDataSource")
	}

	// 插入 AccessVMRecord 数据
	accessVMRecords := []storage.AccessVMRecord{
		{
			ResultTableId:   tableID1,
			BkBaseDataId:    50010,
			VmResultTableId: "1001_vm_test_50010",
			BkBaseDataName:  "data_link_test",
			BkTenantId:      tenant.DefaultTenantId,
		},
		{
			ResultTableId:   tableID2,
			BkBaseDataId:    50011,
			VmResultTableId: "1001_vm_test_50011",
			BkBaseDataName:  "data_link_test_2",
			BkTenantId:      tenant.DefaultTenantId,
		},
		{
			ResultTableId:   tableID3,
			BkBaseDataId:    50012,
			VmResultTableId: "1001_vm_test_50012",
			BkBaseDataName:  "data_link_test_3",
			BkTenantId:      tenant.DefaultTenantId,
		},
	}
	for _, avm := range accessVMRecords {
		db.Delete(&storage.AccessVMRecord{}, "result_table_id = ?", avm.ResultTableId)
		assert.NoError(t, db.Create(&avm).Error, "Failed to insert AccessVMRecord")
	}

	dsRts := []resulttable.DataSourceResultTable{
		{
			TableId:    tableID1,
			BkDataId:   50010,
			BkTenantId: tenant.DefaultTenantId,
		},
		{
			TableId:    tableID2,
			BkDataId:   50011,
			BkTenantId: tenant.DefaultTenantId,
		},
		{
			TableId:    tableID3,
			BkDataId:   50012,
			BkTenantId: tenant.DefaultTenantId,
		},
	}
	for _, dsrt := range dsRts {
		db.Delete(&resulttable.DataSourceResultTable{}, "table_id = ?", dsrt.TableId)
		assert.NoError(t, db.Create(&dsrt).Error, "Failed to insert DataSourceResultTable")
	}

	// 捕获日志输出
	var logBuffer bytes.Buffer
	log.SetOutput(&logBuffer)
	defer log.SetOutput(nil)

	// 执行测试方法
	pusher := NewSpacePusher()

	// 测试空间 1001 的 composeData
	valuesForCreator, err := pusher.composeData(tenant.DefaultTenantId, spaceType, spaceId, []string{}, defaultFilters, nil)
	assert.NoError(t, err, "composeData should not return an error")

	expectedForCreator := map[string]map[string]any{
		tableID1: {"filters": []map[string]any{}},
		"1001_bkmonitor_time_series_50011.__default__": {"filters": []map[string]any{}},
	}
	assert.Equal(t, expectedForCreator, valuesForCreator, "Unexpected result for space 1001")

	// 测试空间 1003 的 composeData
	valuesForOthers, err := pusher.composeData(tenant.DefaultTenantId, spaceType, "1003", []string{}, defaultFilters, nil)
	assert.NoError(t, err, "composeData should not return an error")

	expectedForOthers := map[string]map[string]any{
		tableID1: {"filters": []map[string]any{{"appid": "1003"}}},
		"1001_bkmonitor_time_series_50011.__default__": {"filters": []map[string]any{{"bk_biz_id": "1003"}}},
	}
	assert.Equal(t, expectedForOthers, valuesForOthers, "Unexpected result for space 1003")
}

func TestSpacePusher_composeEsTableIdDetail(t *testing.T) {
	// 初始化数据库
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	db.AutoMigrate(&storage.ESStorage{}, &resulttable.ResultTable{}, &storage.ClusterRecord{})

	// 准备测试数据
	tableID1 := "1001_bkmonitor_time_series_50010.__default__"
	tableID2 := "1001_bkmonitor_time_series_50011.__default__"
	dataLabel1 := "a" // 初始化为字符串

	// 插入 ResultTable 数据
	resultTables := []resulttable.ResultTable{
		{
			TableId:      tableID1,
			BkBizId:      1001,
			BkBizIdAlias: "appid",
			DataLabel:    &dataLabel1, // 使用字符串指针
		},
		{
			TableId:      tableID2,
			BkBizId:      1001,
			BkBizIdAlias: "",
			DataLabel:    nil,
		},
	}
	for _, rt := range resultTables {
		db.Delete(&resulttable.ResultTable{}, "table_id = ?", rt.TableId)
		assert.NoError(t, db.Create(&rt).Error, "Failed to insert ResultTable")
	}

	//// 插入 ResultTable 数据
	//resultTable := resulttable.ResultTable{
	//	TableId:      tableID1,
	//	BkBizId:      1001,
	//	BkBizIdAlias: "appid",
	//	DataLabel:    &dataLabel1, // 使用字符串指针
	//}
	//
	//// 确保数据不存在后重新插入
	//db.Delete(&resulttable.ResultTable{}, "table_id = ?", resultTable.TableId)
	//assert.NoError(t, db.Create(&resultTable).Error, "Failed to insert ResultTable")

	// 准备 SpacePusher 实例
	spacePusher := SpacePusher{}
	// 调用测试方法
	tableID, detailStr, err := spacePusher.composeEsTableIdDetail(
		tableID1,
		map[string]any{"option1": "value1"},
		1,
		"sourceType1",
		"indexSet1",
		nil,
	)

	// 断言返回结果无错误
	assert.NoError(t, err, "composeEsTableIdDetail should not return an error")
	assert.Equal(t, tableID1, tableID, "TableID should match")

	// 期望的 JSON 数据（单个对象）
	expectedDetail := map[string]any{
		"measurement":             "__default__",
		"source_type":             "sourceType1",
		"options":                 map[string]any{"option1": "value1"},
		"storage_cluster_records": []any{},
		"data_label":              "a",
		"storage_type":            "elasticsearch",
		"storage_id":              float64(1), // 修改为 float64
		"db":                      "indexSet1",
		"field_alias":             map[string]any{},
	}

	// 将 detailStr 转换为 map 以便比较
	var actualDetail map[string]any
	err = json.Unmarshal([]byte(detailStr), &actualDetail)
	assert.NoError(t, err, "detailStr should be valid JSON")

	// 比较预期值和实际值
	assert.Equal(t, expectedDetail, actualDetail, "detailStr should match expected JSON")
	// 调用测试方法
	resTid, detailStr2, err := spacePusher.composeEsTableIdDetail(
		tableID2,
		map[string]any{"option1": "value1"},
		1,
		"sourceType1",
		"indexSet1",
		nil,
	)

	expectedDetail2 := map[string]any{
		"measurement":             "__default__",
		"source_type":             "sourceType1",
		"options":                 map[string]any{"option1": "value1"},
		"storage_cluster_records": []any{},
		"data_label":              nil,
		"storage_type":            "elasticsearch",
		"storage_id":              float64(1), // 修改为 float64
		"db":                      "indexSet1",
		"field_alias":             map[string]any{},
	}

	// 将 detailStr 转换为 map 以便比较
	var actualDetail2 map[string]any
	err = json.Unmarshal([]byte(detailStr2), &actualDetail2)
	assert.NoError(t, err, "detailStr should be valid JSON")

	assert.NoError(t, err, "composeEsTableIdDetail should not return an error")
	assert.Equal(t, resTid, tableID2, "TableID should match")

	assert.Equal(t, expectedDetail2, actualDetail2, "detailStr should match expected JSON")
}

func TestSpacePusher_pushBkccSpaceTableIds(t *testing.T) {
	mocker.InitTestDBConfig("../../dist/bmw.yaml")
	db := mysql.GetDBSession().DB

	// 数据源表
	dataSources := []resulttable.DataSource{
		{
			BkDataId:               50010,
			DataName:               "data_link_test",
			EtlConfig:              "bk_standard_v2_time_series",
			IsPlatformDataId:       false,
			IsTenantSpecificGlobal: false,
		},
		{
			BkDataId:               50011,
			DataName:               "data_link_test_2",
			EtlConfig:              "bk_standard_v2_time_series",
			IsPlatformDataId:       true,
			IsTenantSpecificGlobal: false,
		},
	}

	// 插入 DataSource 数据
	for _, ds := range dataSources {
		db.Delete(&resulttable.DataSource{}, "bk_data_id = ?", ds.BkDataId)
		assert.NoError(t, db.Create(&ds).Error, "Failed to insert DataSource")
	}

	// 准备测试数据
	tableID1 := "1001_bkmonitor_time_series_50010.__default__"
	tableID2 := "1001_bkmonitor_time_series_50011.__default__"
	tableID3 := "1001_test_doris.__default__"
	dataLabel1 := "a" // 初始化为字符串

	// 插入 ResultTable 数据
	resultTables := []resulttable.ResultTable{
		{
			TableId:      tableID1,
			BkBizId:      1001,
			BkBizIdAlias: "appid",
			DataLabel:    &dataLabel1, // 使用字符串指针
		},
		{
			TableId:      tableID2,
			BkBizId:      1001,
			BkBizIdAlias: "",
			DataLabel:    nil,
		},
		{
			TableId:        tableID3,
			BkBizId:        1001,
			BkBizIdAlias:   "",
			DataLabel:      nil,
			DefaultStorage: models.StorageTypeDoris,
			IsDeleted:      false,
			IsEnable:       true,
		},
	}
	for _, rt := range resultTables {
		db.Delete(&resulttable.ResultTable{}, "table_id = ?", rt.TableId)
		assert.NoError(t, db.Create(&rt).Error, "Failed to insert ResultTable")
	}

	obj := space.Space{Id: 1, SpaceTypeId: "bkcc", SpaceId: "1001", BkTenantId: "system"}
	obj2 := space.Space{Id: 5, SpaceTypeId: "bkci", SpaceId: "bkmonitor", BkTenantId: "system"}
	obj3 := space.Space{Id: 6, SpaceTypeId: "bksaas", SpaceId: "monitor_saas", BkTenantId: "system"}

	db.Delete(obj)
	db.Delete(obj2)
	db.Delete(obj3)

	assert.NoError(t, obj.Create(db))
	assert.NoError(t, obj2.Create(db))
	assert.NoError(t, obj3.Create(db))

	spaceDataSources := []space.SpaceDataSource{
		{
			SpaceTypeId:       "bkcc",
			SpaceId:           "1001",
			BkDataId:          50010,
			FromAuthorization: false,
		},
	}

	for _, sds := range spaceDataSources {
		db.Delete(&space.SpaceDataSource{}, "bk_data_id = ?", sds.BkDataId)
		assert.NoError(t, db.Create(&sds).Error, "Failed to insert SpaceDataSource")
	}

	// 创建 DataSourceResultTable 数据
	dataSourceResultTables := []resulttable.DataSourceResultTable{
		{
			BkDataId: 50010,
			TableId:  tableID1,
		},
		{
			BkDataId: 50011,
			TableId:  tableID2,
		},
	}

	for _, dsrt := range dataSourceResultTables {
		db.Delete(&resulttable.DataSourceResultTable{}, "bk_data_id = ? and table_id = ?", dsrt.BkDataId, dsrt.TableId)
		assert.NoError(t, db.Create(&dsrt).Error, "Failed to insert DataSourceResultTable")
	}

	// 插入 AccessVMRecord 数据
	accessVMRecords := []storage.AccessVMRecord{
		{
			ResultTableId:   tableID1,
			BkBaseDataId:    50010,
			VmResultTableId: "1001_vm_test_50010",
			BkBaseDataName:  "data_link_test",
		},
		{
			ResultTableId:   tableID2,
			BkBaseDataId:    50011,
			VmResultTableId: "1001_vm_test_50011",
			BkBaseDataName:  "data_link_test_2",
		},
	}
	for _, avm := range accessVMRecords {
		db.Delete(&storage.AccessVMRecord{}, "result_table_id = ?", avm.ResultTableId)
		assert.NoError(t, db.Create(&avm).Error, "Failed to insert AccessVMRecord")
	}

	// 准备 SpacePusher 实例
	spacePusher := SpacePusher{}

	isPublish, err := spacePusher.pushBkccSpaceTableIds(tenant.DefaultTenantId, "bkcc", "1001", nil)
	if err != nil {
		return
	}
	println(isPublish)
}

func TestSpacePusher_pushBkciSpaceTableIds(t *testing.T) {
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

	// 准备 SpacePusher 实例
	spacePusher := SpacePusher{}

	isPublish, err := spacePusher.pushBkciSpaceTableIds(tenant.DefaultTenantId, "bkci", "bcs_project")
	if err != nil {
		return
	}
	println(isPublish)
}

func TestSpacePusher_pushBksaasSpaceTableIds(t *testing.T) {
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

	// 准备 SpacePusher 实例
	spacePusher := SpacePusher{}
	isPublish, err := spacePusher.pushBksaasSpaceTableIds(tenant.DefaultTenantId, "bksaas", "demo", nil)
	if err != nil {
		return
	}
	println(isPublish)
}

func TestBuildFiltersByUsage(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")

	tests := []struct {
		name           string
		ctx            FilterBuildContext
		usage          FilterUsage
		expectedResult []map[string]any
	}{
		{
			name: "UsageComposeData",
			ctx: FilterBuildContext{
				SpaceType:   "bkcc",
				SpaceId:     "1001",
				TableId:     "table_1",
				FilterAlias: "bk_biz_id",
			},
			usage: UsageComposeData,
			expectedResult: []map[string]any{
				{"bk_biz_id": "1001"},
			},
		},
		{
			name: "UsageComposeBcsSpaceBizTableIds",
			ctx: FilterBuildContext{
				SpaceType:   "bkci",
				SpaceId:     "1001",
				TableId:     "table_1",
				BkBizId:     "2001",
				FilterAlias: "bk_biz_id",
			},
			usage: UsageComposeBcsSpaceBizTableIds,
			expectedResult: []map[string]any{
				{"bk_biz_id": "2001"},
			},
		},
		{
			name: "UsageComposeBkciLevelTableIds",
			ctx: FilterBuildContext{
				SpaceType:   "bkci",
				SpaceId:     "1001",
				TableId:     "table_1",
				FilterAlias: "projectId",
			},
			usage: UsageComposeBkciLevelTableIds,
			expectedResult: []map[string]any{
				{"projectId": "1001"},
			},
		},
		{
			name: "UsageComposeAllTypeTableIds",
			ctx: FilterBuildContext{
				SpaceType:      "bkci",
				SpaceId:        "1001",
				TableId:        "table_1",
				ExtraStringVal: "-1001",
				FilterAlias:    "bk_biz_id",
			},
			usage: UsageComposeAllTypeTableIds,
			expectedResult: []map[string]any{
				{"bk_biz_id": "-1001"},
			},
		},
		{
			name: "UsageComposeBksaasSpaceClusterTableIds - Shared Cluster",
			ctx: FilterBuildContext{
				SpaceType:     "bksaas",
				SpaceId:       "1001",
				TableId:       "table_1",
				ClusterId:     "cluster_1",
				NamespaceList: []string{"namespace_1", "namespace_2"},
				IsShared:      true,
			},
			usage: UsageComposeBksaasSpaceClusterTableIds,
			expectedResult: []map[string]any{
				{"bcs_cluster_id": "cluster_1", "namespace": "namespace_1"},
				{"bcs_cluster_id": "cluster_1", "namespace": "namespace_2"},
			},
		},
		{
			name: "UsageComposeBksaasSpaceClusterTableIds - Single Cluster",
			ctx: FilterBuildContext{
				SpaceType:     "bksaas",
				SpaceId:       "1001",
				TableId:       "table_1",
				ClusterId:     "cluster_1",
				IsShared:      false,
				NamespaceList: []string{},
			},
			usage: UsageComposeBksaasSpaceClusterTableIds,
			expectedResult: []map[string]any{
				{"bcs_cluster_id": "cluster_1", "namespace": nil},
			},
		},
	}

	spacePusher := SpacePusher{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock SpacePusher
			// Call the buildFiltersByUsage function
			result := spacePusher.buildFiltersByUsage(tt.ctx, tt.usage)

			// Assert that the result matches the expected value
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestSpaceRedisSvc_ComposeApmAll(t *testing.T) {
	// 初始化数据库配置
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB

	// 清理所有相关表数据
	cleanTestData := func() {
		db.Delete(&space.Space{})
		db.Delete(&resulttable.ResultTable{})
	}
	cleanTestData()       // 测试开始前清理数据
	defer cleanTestData() // 测试结束后清理数据

	// 准备测试用数据

	// 准备 Space 测试数据
	spaceObjs := []space.Space{
		{
			SpaceTypeId: "bkci",
			SpaceId:     "test_bkci_space",
			SpaceName:   "testSpace6",
			Id:          1050,
		},
		{
			SpaceTypeId: "bksaas",
			SpaceId:     "test_bksaas_space",
			SpaceName:   "testSpace7",
			Id:          1051,
		},
	}
	insertTestData(t, db, spaceObjs)

	// 准备 ResultTable 测试数据
	resultTable := resulttable.ResultTable{
		TableId:        "apm_global.precalculate_storage_1",
		BkBizId:        0,
		DefaultStorage: models.StorageTypeES,
		IsDeleted:      false,
		IsEnable:       true,
		BkBizIdAlias:   "biz_id",
	}
	err := resultTable.Create(db)
	assert.NoError(t, err)

	resultTable2 := resulttable.ResultTable{
		TableId:        "apm_global.precalculate_storage_2",
		BkBizId:        0,
		DefaultStorage: models.StorageTypeES,
		IsDeleted:      false,
		IsEnable:       true,
		BkBizIdAlias:   "biz_id",
	}
	err = resultTable2.Create(db)
	assert.NoError(t, err)

	// 测试 composeApmAllTypeTableIds
	bkciData, err := NewSpacePusher().composeApmAllTypeTableIds("bkci", "test_bkci_space")
	expected := map[string]map[string]any(map[string]map[string]any{"apm_global.precalculate_storage_1": {"filters": []map[string]any{{"biz_id": "-1050"}}}, "apm_global.precalculate_storage_2": {"filters": []map[string]any{{"biz_id": "-1050"}}}})

	assert.NoError(t, err)
	assert.Equal(t, expected, bkciData, "Expected 2 table IDs for bkci space")

	bksaasData, err := NewSpacePusher().composeApmAllTypeTableIds("bksaas", "test_bksaas_space")
	expected = map[string]map[string]any{"apm_global.precalculate_storage_1": {"filters": []map[string]any{{"biz_id": "-1051"}}}, "apm_global.precalculate_storage_2": {"filters": []map[string]any{{"biz_id": "-1051"}}}}
	assert.Equal(t, expected, bksaasData, "Expected 2 table IDs for bkci space")
}

func TestSpaceRedisSvc_composeBkciLevelTableIds(t *testing.T) {
	// 初始化数据库配置
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB

	// 清理所有相关表数据
	cleanTestData := func() {
		db.Delete(&space.Space{})
		db.Delete(&resulttable.ResultTable{})
		db.Delete(&resulttable.DataSource{})
		db.Delete(&resulttable.DataSourceResultTable{})
		db.Delete(&storage.ESStorage{})
	}
	cleanTestData()       // 测试开始前清理数据
	defer cleanTestData() // 测试结束后清理数据

	// 准备测试用数据

	// 准备 Space 测试数据
	spaceObjs := []space.Space{
		{
			SpaceTypeId: "bkci",
			SpaceId:     "test_bkci_space",
			SpaceName:   "testSpace6",
			Id:          1050,
			BkTenantId:  "system",
		},
	}
	insertTestData(t, db, spaceObjs)

	// 准备 ResultTable 测试数据
	resultTable := resulttable.ResultTable{
		TableId:        "bkmonitor_event_60010",
		BkBizId:        0,
		DefaultStorage: models.StorageTypeES,
		IsDeleted:      false,
		IsEnable:       true,
		BkBizIdAlias:   "dimensions.project_id",
		BkTenantId:     "system",
	}
	err := resultTable.Create(db)
	assert.NoError(t, err)

	// 准备 DataSource 测试数据
	dataSource := resulttable.DataSource{
		BkDataId:         60010,
		IsPlatformDataId: true,
		SpaceTypeId:      "bkci",
		DataName:         "test_event",
		BkTenantId:       "system",
	}
	err = dataSource.Create(db)
	assert.NoError(t, err)

	// 准备 DataSourceResultTable 测试数据
	dataSourceResultTable := resulttable.DataSourceResultTable{
		BkDataId:   60010,
		TableId:    "bkmonitor_event_60010",
		BkTenantId: "system",
	}
	err = dataSourceResultTable.Create(db)
	assert.NoError(t, err)

	// 准备 ESStorage 测试数据
	esStorage := storage.ESStorage{
		TableID:          "bkmonitor_event_60010",
		StorageClusterID: 11,
		NeedCreateIndex:  true,
	}
	err = esStorage.Create(db)
	assert.NoError(t, err)

	cfg.SpecialRtRouterAliasResultTableList = []string{"bkmonitor_event_60010"}

	// 测试 composeBkciLevelTableIds
	bkciData, err := NewSpacePusher().composeBkciLevelTableIds("system", "bkci", "test_bkci_space")
	expected := map[string]map[string]any{"bkmonitor_event_60010.__default__": {"filters": []map[string]any{{"dimensions.project_id": "test_bkci_space"}}}}
	assert.NoError(t, err)
	assert.Equal(t, expected, bkciData, "Expected 1 table IDs for bkci space")
}

func TestSpaceRedisSvc_composeTableIdFields(t *testing.T) {
	// 初始化数据库配置
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB

	// 清理所有相关表数据
	cleanTestData := func() {
		db.Delete(&space.Space{})
		db.Delete(&resulttable.ResultTable{})
		db.Delete(&resulttable.ResultTableField{})
	}
	cleanTestData()       // 测试开始前清理数据
	defer cleanTestData() // 测试结束后清理数据

	// 准备测试用数据
	resultTableFields := []resulttable.ResultTableField{
		{
			TableID:        "1001_test.__default__",
			FieldName:      "field1",
			FieldType:      "string",
			IsConfigByUser: true,
			Tag:            "metric",
			BkTenantId:     "system",
		},
		{
			TableID:        "1001_test.__default__",
			FieldName:      "field2",
			FieldType:      "string",
			IsConfigByUser: true,
			Tag:            "metric",
			BkTenantId:     "system",
		},
		{
			TableID:        "1001_test.__default__",
			FieldName:      "field3",
			FieldType:      "string",
			IsConfigByUser: true,
			Tag:            "metric",
			BkTenantId:     "system",
		},
		{
			TableID:        "1001_test.__default__",
			FieldName:      "field4",
			FieldType:      "string",
			IsConfigByUser: true,
			Tag:            "metric",
			BkTenantId:     "system",
		},
	}

	// 插入 ResultTableField 数据
	for _, resultTableField := range resultTableFields {
		err := resultTableField.Create(db)
		assert.NoError(t, err)
	}

	// 准备 ResultTable 测试数据
	resultTable := resulttable.ResultTable{
		TableId:        "1001_test.__default__",
		BkBizId:        1001,
		DefaultStorage: models.StorageTypeVM,
		BkTenantId:     "system",
	}
	err := resultTable.Create(db)
	assert.NoError(t, err)

	// TimeSeriesGroup
	timeSeriesGroup := customreport.TimeSeriesGroup{
		CustomGroupBase: customreport.CustomGroupBase{
			TableID:  "1001_test.__default__",
			BkDataID: 50010,
			BkBizID:  1001,
		},
		TimeSeriesGroupID:   60011,
		TimeSeriesGroupName: "test_group",
		BkTenantId:          "system",
	}
	db.Delete(&timeSeriesGroup, "table_id = ?", timeSeriesGroup.TableID)
	err = timeSeriesGroup.Create(db)
	assert.NoError(t, err, "Failed to insert TimeSeriesGroup")

	// TimeSeriesMetric
	timeSeriesMetrics := []customreport.TimeSeriesMetric{
		{
			GroupID:   60011,
			TableID:   "1001_test.__default__",
			FieldName: "field1",
		},
		{
			GroupID:   60011,
			TableID:   "1001_test.__default__",
			FieldName: "field2",
		},
		{
			GroupID:   60011,
			TableID:   "1001_test.__default__",
			FieldName: "field3",
		},
		{
			GroupID:   60011,
			TableID:   "1001_test.__default__",
			FieldName: "field4",
		},
	}
	for _, timeSeriesMetric := range timeSeriesMetrics {
		db.Delete(&timeSeriesMetric, "table_id = ? AND field_name = ?", timeSeriesMetric.TableID, timeSeriesMetric.FieldName)
		err = timeSeriesMetric.Create(db)
		assert.NoError(t, err, "Failed to insert TimeSeriesMetric")
	}

	actualData, err := NewSpacePusher().composeTableIdFields("system", []string{"1001_test.__default__"})
	assert.NoError(t, err, "composeTableIdFields should not return an error")
	assert.Equal(t, map[string][]string{
		"1001_test.__default__": {"field1", "field2", "field3", "field4"},
	}, actualData, "composeTableIdFields should return the expected data")
}

// TestSpaceRedisSvc_composeTableIdFields_WithRetentionTime 测试指标过期相关逻辑
func TestSpaceRedisSvc_composeTableIdFields_WithRetentionTime(t *testing.T) {
	// 初始化数据库配置
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB

	// 清理测试数据
	cleanTestData := func() {
		db.Delete(&space.Space{})
		db.Delete(&resulttable.ResultTable{})
		db.Delete(&resulttable.ResultTableField{})
		db.Delete(&customreport.TimeSeriesGroup{})
		db.Delete(&customreport.TimeSeriesMetric{})
		db.Delete(&storage.ClusterInfo{})
		db.Delete(&storage.AccessVMRecord{})
	}
	cleanTestData()
	defer cleanTestData()

	// 创建两个 VM 集群，设置不同的保留时间
	// 集群1：保留30天
	cluster1 := storage.ClusterInfo{
		ClusterID:       1001,
		ClusterName:     "vm_cluster_30d",
		ClusterType:     models.StorageTypeVM,
		DomainName:      "vm1.example.com",
		Port:            8428,
		DefaultSettings: `{"retention_time": 2592000}`, // 30天 = 2592000秒
	}
	err := cluster1.Create(db)
	assert.NoError(t, err, "Failed to create cluster1")

	// 集群2：保留90天
	cluster2 := storage.ClusterInfo{
		ClusterID:       1002,
		ClusterName:     "vm_cluster_90d",
		ClusterType:     models.StorageTypeVM,
		DomainName:      "vm2.example.com",
		Port:            8428,
		DefaultSettings: `{"retention_time": 7776000}`, // 90天 = 7776000秒
	}
	err = cluster2.Create(db)
	assert.NoError(t, err, "Failed to create cluster2")

	// 集群3：无保留时间配置（测试默认值）
	cluster3 := storage.ClusterInfo{
		ClusterID:       1003,
		ClusterName:     "vm_cluster_default",
		ClusterType:     models.StorageTypeVM,
		DomainName:      "vm3.example.com",
		Port:            8428,
		DefaultSettings: `{}`, // 空配置，使用默认60天
	}
	err = cluster3.Create(db)
	assert.NoError(t, err, "Failed to create cluster3")

	// 创建结果表
	tables := []resulttable.ResultTable{
		{
			TableId:        "1001_table_30d.__default__",
			BkBizId:        1001,
			DefaultStorage: models.StorageTypeVM,
			BkTenantId:     "system",
		},
		{
			TableId:        "1002_table_90d.__default__",
			BkBizId:        1002,
			DefaultStorage: models.StorageTypeVM,
			BkTenantId:     "system",
		},
		{
			TableId:        "1003_table_default.__default__",
			BkBizId:        1003,
			DefaultStorage: models.StorageTypeVM,
			BkTenantId:     "system",
		},
	}
	for _, table := range tables {
		err = table.Create(db)
		assert.NoError(t, err, "Failed to create result table")
	}

	// 创建 AccessVMRecord 关联关系
	accessRecords := []storage.AccessVMRecord{
		{
			BkTenantId:    "system",
			ResultTableId: "1001_table_30d.__default__",
			VmClusterId:   1001, // 关联到30天保留期的集群
		},
		{
			BkTenantId:    "system",
			ResultTableId: "1002_table_90d.__default__",
			VmClusterId:   1002, // 关联到90天保留期的集群
		},
		{
			BkTenantId:    "system",
			ResultTableId: "1003_table_default.__default__",
			VmClusterId:   1003, // 关联到默认保留期的集群
		},
	}
	for _, record := range accessRecords {
		err = record.Create(db)
		assert.NoError(t, err, "Failed to create AccessVMRecord")
	}

	// 创建 TimeSeriesGroup
	tsGroups := []customreport.TimeSeriesGroup{
		{
			CustomGroupBase: customreport.CustomGroupBase{
				TableID:  "1001_table_30d.__default__",
				BkDataID: 50010,
				BkBizID:  1001,
			},
			TimeSeriesGroupID:   60001,
			TimeSeriesGroupName: "group_30d",
			BkTenantId:          "system",
		},
		{
			CustomGroupBase: customreport.CustomGroupBase{
				TableID:  "1002_table_90d.__default__",
				BkDataID: 50020,
				BkBizID:  1002,
			},
			TimeSeriesGroupID:   60002,
			TimeSeriesGroupName: "group_90d",
			BkTenantId:          "system",
		},
		{
			CustomGroupBase: customreport.CustomGroupBase{
				TableID:  "1003_table_default.__default__",
				BkDataID: 50030,
				BkBizID:  1003,
			},
			TimeSeriesGroupID:   60003,
			TimeSeriesGroupName: "group_default",
			BkTenantId:          "system",
		},
	}
	for _, group := range tsGroups {
		err = group.Create(db)
		assert.NoError(t, err, "Failed to create TimeSeriesGroup")
	}

	now := time.Now()

	// 创建 ResultTableField 数据（composeTableIdFields 需要）
	rtfList := []resulttable.ResultTableField{
		// 集群1 (30天保留期)
		{TableID: "1001_table_30d.__default__", FieldName: "active_metric_1", Tag: models.ResultTableFieldTagMetric, BkTenantId: "system"},
		{TableID: "1001_table_30d.__default__", FieldName: "active_metric_2", Tag: models.ResultTableFieldTagMetric, BkTenantId: "system"},
		{TableID: "1001_table_30d.__default__", FieldName: "expired_metric_1", Tag: models.ResultTableFieldTagMetric, BkTenantId: "system"},
		{TableID: "1001_table_30d.__default__", FieldName: "expired_metric_2", Tag: models.ResultTableFieldTagMetric, BkTenantId: "system"},
		// 集群2 (90天保留期)
		{TableID: "1002_table_90d.__default__", FieldName: "active_metric_90d_1", Tag: models.ResultTableFieldTagMetric, BkTenantId: "system"},
		{TableID: "1002_table_90d.__default__", FieldName: "active_metric_90d_2", Tag: models.ResultTableFieldTagMetric, BkTenantId: "system"},
		{TableID: "1002_table_90d.__default__", FieldName: "expired_metric_90d", Tag: models.ResultTableFieldTagMetric, BkTenantId: "system"},
		// 集群3 (默认60天保留期)
		{TableID: "1003_table_default.__default__", FieldName: "active_metric_default", Tag: models.ResultTableFieldTagMetric, BkTenantId: "system"},
		{TableID: "1003_table_default.__default__", FieldName: "expired_metric_default", Tag: models.ResultTableFieldTagMetric, BkTenantId: "system"},
	}
	for _, rtf := range rtfList {
		err = rtf.Create(db)
		assert.NoError(t, err, "Failed to create ResultTableField")
	}

	// 创建 TimeSeriesMetric，测试不同的过期场景
	tsMetrics := []customreport.TimeSeriesMetric{
		// 集群1 (30天保留期) - 活跃指标
		{
			GroupID:        60001,
			TableID:        "1001_table_30d.__default__",
			FieldName:      "active_metric_1",
			LastModifyTime: now.Add(-10 * 24 * time.Hour), // 10天前，在保留期内
		},
		{
			GroupID:        60001,
			TableID:        "1001_table_30d.__default__",
			FieldName:      "active_metric_2",
			LastModifyTime: now.Add(-25 * 24 * time.Hour), // 25天前，在保留期内
		},
		// 集群1 (30天保留期) - 过期指标
		{
			GroupID:        60001,
			TableID:        "1001_table_30d.__default__",
			FieldName:      "expired_metric_1",
			LastModifyTime: now.Add(-40 * 24 * time.Hour), // 40天前，已过期
		},
		{
			GroupID:        60001,
			TableID:        "1001_table_30d.__default__",
			FieldName:      "expired_metric_2",
			LastModifyTime: now.Add(-100 * 24 * time.Hour), // 100天前，已过期
		},

		// 集群2 (90天保留期) - 活跃指标
		{
			GroupID:        60002,
			TableID:        "1002_table_90d.__default__",
			FieldName:      "active_metric_90d_1",
			LastModifyTime: now.Add(-50 * 24 * time.Hour), // 50天前，在保留期内
		},
		{
			GroupID:        60002,
			TableID:        "1002_table_90d.__default__",
			FieldName:      "active_metric_90d_2",
			LastModifyTime: now.Add(-85 * 24 * time.Hour), // 85天前，在保留期内
		},
		// 集群2 (90天保留期) - 过期指标
		{
			GroupID:        60002,
			TableID:        "1002_table_90d.__default__",
			FieldName:      "expired_metric_90d",
			LastModifyTime: now.Add(-100 * 24 * time.Hour), // 100天前，已过期
		},

		// 集群3 (默认60天保留期) - 活跃指标
		{
			GroupID:        60003,
			TableID:        "1003_table_default.__default__",
			FieldName:      "active_metric_default",
			LastModifyTime: now.Add(-30 * 24 * time.Hour), // 30天前，在保留期内
		},
		// 集群3 (默认60天保留期) - 过期指标
		{
			GroupID:        60003,
			TableID:        "1003_table_default.__default__",
			FieldName:      "expired_metric_default",
			LastModifyTime: now.Add(-70 * 24 * time.Hour), // 70天前，已过期
		},
	}

	for i, metric := range tsMetrics {
		// 手动设置 LastModifyTime 和 field_id，避免被 BeforeCreate 覆盖
		fieldID := uint(70000 + i) // 使用唯一的 field_id
		result := db.Exec(`INSERT INTO metadata_timeseriesmetric (field_id, group_id, table_id, field_name, last_modify_time, tag_list, last_index, label) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			fieldID, metric.GroupID, metric.TableID, metric.FieldName, metric.LastModifyTime, "[]", 0, "")
		assert.NoError(t, result.Error, "Failed to insert TimeSeriesMetric")
	}

	// 测试：集群1（30天保留期）应该只返回未过期的指标
	actualData1, err := NewSpacePusher().composeTableIdFields("system", []string{"1001_table_30d.__default__"})
	assert.NoError(t, err, "composeTableIdFields should not return an error")
	assert.Len(t, actualData1["1001_table_30d.__default__"], 2, "Should only return 2 active metrics for 30-day retention")
	assert.Contains(t, actualData1["1001_table_30d.__default__"], "active_metric_1")
	assert.Contains(t, actualData1["1001_table_30d.__default__"], "active_metric_2")
	assert.NotContains(t, actualData1["1001_table_30d.__default__"], "expired_metric_1")
	assert.NotContains(t, actualData1["1001_table_30d.__default__"], "expired_metric_2")

	// 测试：集群2（90天保留期）应该只返回未过期的指标
	actualData2, err := NewSpacePusher().composeTableIdFields("system", []string{"1002_table_90d.__default__"})
	assert.NoError(t, err, "composeTableIdFields should not return an error")
	assert.Len(t, actualData2["1002_table_90d.__default__"], 2, "Should only return 2 active metrics for 90-day retention")
	assert.Contains(t, actualData2["1002_table_90d.__default__"], "active_metric_90d_1")
	assert.Contains(t, actualData2["1002_table_90d.__default__"], "active_metric_90d_2")
	assert.NotContains(t, actualData2["1002_table_90d.__default__"], "expired_metric_90d")

	// 测试：集群3（默认60天保留期）应该只返回未过期的指标
	actualData3, err := NewSpacePusher().composeTableIdFields("system", []string{"1003_table_default.__default__"})
	assert.NoError(t, err, "composeTableIdFields should not return an error")
	assert.Len(t, actualData3["1003_table_default.__default__"], 1, "Should only return 1 active metric for default retention")
	assert.Contains(t, actualData3["1003_table_default.__default__"], "active_metric_default")
	assert.NotContains(t, actualData3["1003_table_default.__default__"], "expired_metric_default")

	// 测试：同时查询多个表
	actualDataAll, err := NewSpacePusher().composeTableIdFields("system", []string{
		"1001_table_30d.__default__",
		"1002_table_90d.__default__",
		"1003_table_default.__default__",
	})
	assert.NoError(t, err, "composeTableIdFields should not return an error for multiple tables")
	assert.Len(t, actualDataAll, 3, "Should return data for all 3 tables")
	assert.Len(t, actualDataAll["1001_table_30d.__default__"], 2, "Table 1 should have 2 active metrics")
	assert.Len(t, actualDataAll["1002_table_90d.__default__"], 2, "Table 2 should have 2 active metrics")
	assert.Len(t, actualDataAll["1003_table_default.__default__"], 1, "Table 3 should have 1 active metric")
}
