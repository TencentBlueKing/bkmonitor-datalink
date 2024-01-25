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
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/hashconsul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestResultTableSvc_CreateResultTable(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	gomonkey.ApplyPrivateMethod(InfluxdbStorageSvc{}, "syncDb", func(_ InfluxdbStorageSvc) error { return nil })
	gomonkey.ApplyFunc(hashconsul.Put, func(c *consul.Instance, key, val string) error { return nil })
	gomonkey.ApplyMethod(&http.Client{}, "Do", func(t *http.Client, req *http.Request) (*http.Response, error) {
		var data string
		if strings.Contains(req.URL.Path, "v1/kv") {
			data = fmt.Sprintf(`{"message":"ok","result":true,"code":0,"data":{}`)
		}
		body := io.NopCloser(strings.NewReader(data))
		return &http.Response{
			Status:        "ok",
			StatusCode:    200,
			Body:          body,
			ContentLength: int64(len(data)),
			Request:       req,
		}, nil
	})
	db := mysql.GetDBSession().DB
	var dataId uint = 1800000
	// 跳过此dataid的推送
	IgnoreConsulSyncDataIdList = append(IgnoreConsulSyncDataIdList, dataId)
	tableId := "create_rt_table_id_test.base"
	ds := resulttable.DataSource{
		BkDataId:          dataId,
		DataName:          "create_rt_ts",
		SourceSystem:      "bkmonitor",
		IsEnable:          true,
		TransferClusterId: "default",
	}
	influxdbClusterInfo := storage.InfluxdbClusterInfo{
		HostName:     "127.0.0.1:12345",
		ClusterName:  "default",
		HostReadable: true,
	}
	db.Delete(&influxdbClusterInfo, "host_name = ?", influxdbClusterInfo.HostName)
	err := influxdbClusterInfo.Create(db)
	assert.NoError(t, err)
	p := storage.InfluxdbProxyStorage{
		ProxyClusterId:      2,
		InstanceClusterName: "name",
		ServiceName:         "svc_name",
		IsDefault:           true,
	}
	db.Delete(&p, "instance_cluster_name = ?", p.InstanceClusterName)
	err = p.Create(db)
	assert.NoError(t, err)
	db.Delete(&ds, "bk_data_id = ?", ds.BkDataId)
	db.Delete(&resulttable.ResultTable{}, "table_id = ?", tableId)
	db.Delete(&resulttable.ResultTableField{}, "table_id = ?", tableId)
	db.Delete(&resulttable.ResultTableFieldOption{}, "table_id = ?", tableId)
	db.Delete(&resulttable.DataSourceResultTable{}, "bk_data_id = ?", ds.BkDataId)
	db.Delete(&space.SpaceDataSource{}, "bk_data_id = ?", ds.BkDataId)
	db.Delete(&storage.InfluxdbStorage{}, "table_id = ?", tableId)
	err = ds.Create(db)
	assert.NoError(t, err)
	err = NewResultTableSvc(nil).CreateResultTable(dataId, 2, tableId, tableId, true, models.ResultTableSchemaTypeFree, "test", models.StorageTypeInfluxdb, nil, TSStorageFieldList, false, nil, "other_rt", nil)
	assert.NoError(t, err)
	var rt resulttable.ResultTable
	err = resulttable.NewResultTableQuerySet(db).TableIdEq(tableId).One(&rt)
	assert.NoError(t, err)
	var rtds resulttable.DataSourceResultTable
	err = resulttable.NewDataSourceResultTableQuerySet(db).BkDataIdEq(dataId).TableIdEq(tableId).One(&rtds)
	assert.NoError(t, err)
	var st storage.InfluxdbStorage
	err = storage.NewInfluxdbStorageQuerySet(db).TableIDEq(tableId).One(&st)
	assert.NoError(t, err)
	var rtf resulttable.ResultTableField
	err = resulttable.NewResultTableFieldQuerySet(db).TableIDEq(tableId).FieldNameEq("target").One(&rtf)
	assert.NoError(t, err)
}

func TestResultTableSvc_CreateResultTable_for_event(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	gomonkey.ApplyPrivateMethod(InfluxdbStorageSvc{}, "syncDb", func(_ InfluxdbStorageSvc) error { return nil })
	gomonkey.ApplyMethod(&http.Client{}, "Do", func(t *http.Client, req *http.Request) (*http.Response, error) {
		var data string
		if strings.Contains(req.URL.Path, "v1/kv") {
			data = fmt.Sprintf(`{"message":"ok","result":true,"code":0,"data":{}`)
		}
		body := io.NopCloser(strings.NewReader(data))
		return &http.Response{
			Status:        "ok",
			StatusCode:    200,
			Body:          body,
			ContentLength: int64(len(data)),
			Request:       req,
		}, nil
	})
	db := mysql.GetDBSession().DB
	var dataId uint = 1900000
	// 跳过此dataid的推送
	IgnoreConsulSyncDataIdList = append(IgnoreConsulSyncDataIdList, dataId)
	tableId := "create_rt_table_id_test_event.base"
	ds := resulttable.DataSource{
		BkDataId:          dataId,
		DataName:          "create_rt_event",
		SourceSystem:      "bkmonitor",
		IsEnable:          true,
		TransferClusterId: "default",
	}
	version := "7"
	schema := "http"
	cluster := storage.ClusterInfo{
		ClusterName:      "es_test_default",
		ClusterType:      models.StorageTypeES,
		DomainName:       "127.0.0.1",
		Port:             9200,
		IsDefaultCluster: true,
		Schema:           &schema,
		Version:          &version,
		RegisteredSystem: "bkmonitor",
	}
	db.Delete(&cluster, "cluster_name = ?", cluster.ClusterName)
	err := cluster.Create(db)
	assert.NoError(t, err)
	db.Delete(&ds, "bk_data_id = ?", ds.BkDataId)
	db.Delete(&resulttable.ResultTable{}, "table_id = ?", tableId)
	db.Delete(&resulttable.ResultTableField{}, "table_id = ?", tableId)
	db.Delete(&resulttable.ResultTableFieldOption{}, "table_id = ?", tableId)
	db.Delete(&resulttable.DataSourceResultTable{}, "bk_data_id = ?", ds.BkDataId)
	db.Delete(&space.SpaceDataSource{}, "bk_data_id = ?", ds.BkDataId)
	db.Delete(&storage.ESStorage{}, "table_id = ?", tableId)
	err = ds.Create(db)
	assert.NoError(t, err)
	err = NewResultTableSvc(nil).CreateResultTable(dataId, 2, tableId, tableId, true, models.ResultTableSchemaTypeFree, "test", models.StorageTypeES, map[string]interface{}{"enable_create_index": false}, EventStorageFieldList, false, nil, "other_rt", nil)
	assert.NoError(t, err)
	var rt resulttable.ResultTable
	err = resulttable.NewResultTableQuerySet(db).TableIdEq(tableId).One(&rt)
	assert.NoError(t, err)
	var rtds resulttable.DataSourceResultTable
	err = resulttable.NewDataSourceResultTableQuerySet(db).BkDataIdEq(dataId).TableIdEq(tableId).One(&rtds)
	assert.NoError(t, err)
	var st storage.ESStorage
	err = storage.NewESStorageQuerySet(db).TableIDEq(tableId).One(&st)
	assert.NoError(t, err)
	var rtfList []resulttable.ResultTableField
	err = resulttable.NewResultTableFieldQuerySet(db).TableIDEq(tableId).FieldNameIn("event", "target", "dimensions", "event_name").All(&rtfList)
	assert.NoError(t, err)
	assert.Equal(t, 4, len(rtfList))
}
