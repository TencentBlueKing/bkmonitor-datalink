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
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

//func TestSpacePusher_getDataIdByCluster(t *testing.T) {
//	cfg.FilePath = "../../../bmw.yaml"
//	mocker.PatchDBSession()
//	db := mysql.GetDBSession().DB
//	defer db.Close()
//	cluster := &bcs.BCSClusterInfo{
//		ClusterID:          "BCS-K8S-00000",
//		BCSApiClusterId:    "BCS-K8S-00000",
//		BkBizId:            2,
//		BkCloudId:          new(int),
//		ProjectId:          "xxxxx",
//		Status:             models.BcsClusterStatusRunning,
//		DomainName:         "www.xxx.com",
//		Port:               80,
//		ServerAddressPath:  "clusters",
//		ApiKeyType:         "authorization",
//		ApiKeyContent:      "xxxxxx",
//		ApiKeyPrefix:       "Bearer",
//		IsSkipSslVerify:    true,
//		K8sMetricDataID:    1572864,
//		CustomMetricDataID: 1572865,
//		K8sEventDataID:     1572866,
//		Creator:            "system",
//		CreateTime:         time.Now(),
//		LastModifyTime:     time.Now(),
//		LastModifyUser:     "system",
//	}
//	db.Delete(cluster, "cluster_id=?", cluster.ClusterID)
//	err := cluster.Create(db)
//	assert.NoError(t, err)
//	pusher := NewSpacePusher()
//	clusterDataIdMap, err := pusher.getDataIdByCluster(cluster.ClusterID)
//	assert.NoError(t, err)
//	assert.ElementsMatch(t, clusterDataIdMap[cluster.ClusterID], []uint{cluster.K8sMetricDataID, cluster.CustomMetricDataID})
//}

//func TestSpacePusher_isNeedAddFilter(t *testing.T) {
//	var defaultDataId uint = 1234567
//	cfg.FilePath = "../../../bmw.yaml"
//	mocker.PatchDBSession()
//	db := mysql.GetDBSession().DB
//	defer db.Close()
//	ds := resulttable.DataSource{
//		BkDataId:         defaultDataId,
//		Token:            "xxx",
//		DataName:         "test_ds1",
//		DataDescription:  "test_ds1",
//		EtlConfig:        "",
//		IsPlatformDataId: false,
//		SpaceTypeId:      "bkcc",
//		SpaceUid:         "bkcc__2",
//	}
//	db.Delete(ds)
//	err := ds.Create(db)
//	assert.NoError(t, err)
//
//	pusher := NewSpacePusher()
//	// bk_traditional_measurement
//	should, err := pusher.isNeedAddFilter(models.MeasurementTypeBkTraditional, "bkcc", "2", defaultDataId)
//	assert.NoError(t, err)
//	assert.True(t, should)
//	// bk_exporter same space_uid
//	should, err = pusher.isNeedAddFilter(models.MeasurementTypeBkExporter, "bkcc", "2", defaultDataId)
//	assert.NoError(t, err)
//	assert.False(t, should)
//	// bk_exporter diff space_uid
//	should, err = pusher.isNeedAddFilter(models.MeasurementTypeBkExporter, "bkcc", "3", defaultDataId)
//	assert.NoError(t, err)
//	assert.True(t, should)
//	// not found datasource
//	should, err = pusher.isNeedAddFilter(models.MeasurementTypeBkExporter, "bkcc", "3", defaultDataId+100)
//	assert.NoError(t, err)
//	assert.True(t, should)
//
//}
//
//func TestSpacePusher_getData(t *testing.T) {
//	cfg.FilePath = "../../../bmw.yaml"
//	mocker.PatchDBSession()
//	sdr := space.SpaceDataSource{
//		SpaceTypeId:       "bkcc",
//		SpaceId:           "123",
//		BkDataId:          1002,
//		FromAuthorization: false,
//	}
//	db := mysql.GetDBSession().DB
//	defer db.Close()
//	db.Delete(sdr, "space_type_id = ? and space_id = ?", sdr.SpaceTypeId, sdr.SpaceId)
//	err := sdr.Create(db)
//	assert.NoError(t, err)
//	pusher := NewSpacePusher()
//	err = pusher.getData("bkcc", "123", "", nil)
//	assert.NoError(t, err)
//	assert.NotNil(t, pusher.tableDataIdMap)
//	assert.NotNil(t, pusher.tableIdTableMap)
//	assert.NotNil(t, pusher.measurementTypeMap)
//	assert.NotNil(t, pusher.tableIdList)
//	assert.NotNil(t, pusher.tableFieldMap)
//	fieldValueMap, err := pusher.composeBizId("bkcc", "123", "bkcc", "123", nil, true)
//	assert.NoError(t, err)
//	assert.Equal(t, len(pusher.tableDataIdMap), len(fieldValueMap))
//}

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
	cfg.FilePath = "../../../bmw.yaml"
	mocker.PatchDBSession()
	db := mysql.GetDBSession().DB
	itableName := "i_table_test.dbname"
	iTable := storage.InfluxdbStorage{TableID: itableName, RealTableName: "i_table_test", Database: "dbname"}
	db.Delete(&iTable)
	err := iTable.Create(db)
	assert.NoError(t, err)

	vmTableName := "vm_table_name"
	vmTable := storage.AccessVMRecord{ResultTableId: vmTableName}
	db.Delete(&vmTable)
	err = vmTable.Create(db)
	assert.NoError(t, err)

	notExistTable := "not_exist_rt"

	ids, err := NewSpacePusher().refineTableIds([]string{itableName, notExistTable, vmTableName})
	assert.ElementsMatch(t, []string{itableName, vmTableName}, ids)
}

func TestSpacePusher_GetSpaceTableIdDataId(t *testing.T) {
	cfg.FilePath = "../../../bmw.yaml"
	mocker.PatchDBSession()
	db := mysql.GetDBSession().DB

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

	pusher := NewSpacePusher()
	// 指定rtList
	dataMap, err := pusher.GetSpaceTableIdDataId("", "", []string{"rt_18000", "rt_18002"}, nil, nil)
	assert.NoError(t, err)
	assert.Equal(t, map[string]uint{"rt_18000": 18000, "rt_18002": 18002}, dataMap)

	// 测试排除
	dataMap, err = pusher.GetSpaceTableIdDataId("bkcc_t", "2", nil, []uint{18000, 18002}, nil)
	assert.NoError(t, err)
	assert.Equal(t, map[string]uint{"rt_18001": 18001}, dataMap)
}

func TestSpacePusher_getTableInfoForInfluxdbAndVm(t *testing.T) {
	cfg.FilePath = "../../../bmw.yaml"
	mocker.PatchDBSession()
	db := mysql.GetDBSession().DB
	s := storage.InfluxdbProxyStorage{
		ProxyClusterId:      2,
		InstanceClusterName: "instance_cluster_name",
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

	vmTableName := "vm_table_name"
	vmTable := storage.AccessVMRecord{
		ResultTableId:   vmTableName,
		VmResultTableId: "vm_result_table_id",
	}
	db.Delete(&vmTable)
	err = vmTable.Create(db)
	assert.NoError(t, err)

	data, err := NewSpacePusher().getTableInfoForInfluxdbAndVm([]string{itableName, vmTableName})
	assert.NoError(t, err)
	assert.Equal(t, 2, len(data))
	vmData, err := jsonx.MarshalString(data[vmTableName])
	assert.NoError(t, err)
	assert.JSONEq(t, `{"cluster_name":"","db":"","measurement":"","tags_key":[],"vm_rt":"vm_result_table_id"}`, vmData)
	itableData, err := jsonx.MarshalString(data[itableName])
	assert.NoError(t, err)
	assert.JSONEq(t, `{"cluster_name":"instance_cluster_name","db":"dbname","measurement":"i_table_test","storage_id":2,"tags_key":["t1","t2"],"vm_rt":""}`, itableData)
}

func TestSpaceRedisSvc_PushAndPublishSpaceRouter(t *testing.T) {
	cfg.FilePath = "../../../bmw.yaml"
	mocker.PatchDBSession()
	gomonkey.ApplyMethod(&http.Client{}, "Do", func(t *http.Client, req *http.Request) (*http.Response, error) {
		data := ``
		body := io.NopCloser(strings.NewReader(data))
		return &http.Response{
			Status:        "ok",
			StatusCode:    200,
			Body:          body,
			ContentLength: int64(len(data)),
			Request:       req,
		}, nil
	})
	// no panic
	err := NewSpaceRedisSvc(1).PushAndPublishSpaceRouter("", "", nil)
	assert.NoError(t, err)
}
