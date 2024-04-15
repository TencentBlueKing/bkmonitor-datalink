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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestGenBkdataRtIdWithoutBizId(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	type args struct {
		tableId string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{name: "abcdef123456789011121314151617181920.group1", args: args{tableId: "abcdef123456789011121314151617181920.group1"}, want: "6789011121314151617181920_group1"},
		{name: "abcdef.group1", args: args{tableId: "abcdef.group1"}, want: fmt.Sprintf("%s_%s", config.BkdataRtIdPrefix, "abcdef_group1")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, GenBkdataRtIdWithoutBizId(tt.args.tableId), "GenBkdataRtIdWithoutBizId(%v)", tt.args.tableId)
		})
	}
}

func TestBkDataStorageSvc_CreateDatabusClean(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")

	db := mysql.GetDBSession().DB
	tableId := "bk_data_test_table_id3"
	bds := storage.BkDataStorage{
		TableID:   tableId,
		RawDataID: -1,
	}
	db.Delete(&bds, "table_id = ?", bds.TableID)
	err := bds.Create(db)
	assert.NoError(t, err)

	cluster := storage.ClusterInfo{
		ClusterName: "testVmCluster",
		ClusterType: models.StorageTypeVM,
		DomainName:  "kafka.test.com",
		Port:        9200,
	}
	db.Delete(&cluster, "cluster_name = ?", cluster.ClusterName)
	err = cluster.Create(db)

	kafkaStorage := storage.KafkaStorage{
		TableID:          tableId,
		Topic:            fmt.Sprintf("%s_%s", bds.TableID, "topic"),
		Partition:        1,
		StorageClusterID: cluster.ClusterID,
		Retention:        0,
	}
	db.Delete(&kafkaStorage)
	err = kafkaStorage.Create(db)
	assert.NoError(t, err)

	rt := resulttable.ResultTable{
		TableId:        tableId,
		TableNameZh:    tableId,
		IsCustomTable:  true,
		SchemaType:     "",
		DefaultStorage: models.StorageTypeBkdata,
		IsEnable:       true,
		Label:          "others",
	}
	db.Delete(&rt, "table_id = ?", tableId)
	err = rt.Create(db)
	assert.NoError(t, err)

	gomonkey.ApplyMethod(&http.Client{}, "Do", func(t *http.Client, req *http.Request) (*http.Response, error) {
		data := `{"result": true, "data": {"raw_data_id": 525069}, "code": "1500200", "message": "ok", "errors": null, "request_id": "08507b4dbc00405c9b9d08793f04d955"}`
		body := io.NopCloser(strings.NewReader(data))
		return &http.Response{
			Status:        "ok",
			StatusCode:    200,
			Body:          body,
			ContentLength: int64(len(data)),
			Request:       req,
		}, nil
	})

	svc := NewBkDataStorageSvc(&bds)
	err = svc.CreateDatabusClean(&rt)
	assert.NoError(t, err)
	err = storage.NewBkDataStorageQuerySet(db).TableIDEq(tableId).One(&bds)
	assert.NoError(t, err)
	assert.Equal(t, 525069, bds.RawDataID)
}

func TestBkDataStorageSvc_generateBkDataEtlConfig(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	bkdt := storage.BkDataStorage{
		TableID:             "rt_id_for_bk_data_test_1",
		RawDataID:           77665,
		EtlJSONConfig:       "",
		BkDataResultTableID: "",
	}
	fd1 := resulttable.ResultTableField{
		TableID:        bkdt.TableID,
		FieldName:      "fd1",
		FieldType:      models.ResultTableFieldTypeString,
		Tag:            models.ResultTableFieldTagDimension,
		IsConfigByUser: true,
	}
	fd2 := resulttable.ResultTableField{
		TableID:        bkdt.TableID,
		FieldName:      "fd2",
		FieldType:      models.ResultTableFieldTypeString,
		Tag:            models.ResultTableFieldTagGroup,
		IsConfigByUser: true,
	}
	fm1 := resulttable.ResultTableField{
		TableID:        bkdt.TableID,
		FieldName:      "fm1",
		FieldType:      models.ResultTableFieldTypeInt,
		Tag:            models.ResultTableFieldTagMetric,
		IsConfigByUser: true,
	}
	fm2 := resulttable.ResultTableField{
		TableID:        bkdt.TableID,
		FieldName:      "fm2",
		FieldType:      models.ResultTableFieldTypeFloat,
		Tag:            models.ResultTableFieldTagMetric,
		IsConfigByUser: true,
	}

	ft := resulttable.ResultTableField{
		TableID:        bkdt.TableID,
		FieldName:      "time",
		FieldType:      models.ResultTableFieldTypeString,
		Tag:            models.ResultTableFieldTagTimestamp,
		IsConfigByUser: true,
	}
	db.Delete(&resulttable.ResultTableField{}, "table_id = ?", bkdt.TableID)
	assert.NoError(t, fd1.Create(db))
	assert.NoError(t, fd2.Create(db))
	assert.NoError(t, fm1.Create(db))
	assert.NoError(t, fm2.Create(db))
	assert.NoError(t, ft.Create(db))

	svc := NewBkDataStorageSvc(&bkdt)
	etlConfig, fields, err := svc.generateBkDataEtlConfig()
	assert.NoError(t, err)
	assert.Len(t, fields, 5)
	etlConfigJson, _ := jsonx.MarshalString(etlConfig)
	equal, _ := jsonx.CompareJson(etlConfigJson, `{"extract":{"args":[],"type":"fun","label":"label6356db","result":"json","next":{"type":"branch","name":"","label":null,"next":[{"type":"access","label":"label5a9c45","result":"dimensions","next":{"type":"assign","label":"labelb2c1cb","subtype":"assign_obj","assign":[{"type":"string","key":"fd1","assign_to":"fd1"},{"type":"string","key":"fd2","assign_to":"fd2"}],"next":null},"key":"dimensions","subtype":"access_obj"},{"type":"access","label":"label65f2f1","result":"metrics","next":{"type":"assign","label":"labela6b250","subtype":"assign_obj","assign":[{"type":"long","key":"fm1","assign_to":"fm1"},{"type":"double","key":"fm2","assign_to":"fm2"}],"next":null},"key":"metrics","subtype":"access_obj"},{"type":"assign","label":"labelecd758","subtype":"assign_obj","assign":[{"type":"string","key":"time","assign_to":"time"}],"next":null}]},"method":"from_json"},"conf":{"timezone":8,"output_field_name":"timestamp","time_format":"Unix Time Stamp(seconds)","time_field_name":"time","timestamp_len":10,"encoding":"UTF-8"}}`)
	assert.True(t, equal)
	fieldsJson, _ := jsonx.MarshalString(fields)
	equal, _ = jsonx.CompareJson(fieldsJson, `[{"field_name":"fd1","field_type":"string","field_alias":"fd1","is_dimension":true,"field_index":1},{"field_name":"fd2","field_type":"string","field_alias":"fd2","is_dimension":true,"field_index":2},{"field_name":"fm1","field_type":"long","field_alias":"fm1","is_dimension":false,"field_index":3},{"field_name":"fm2","field_type":"double","field_alias":"fm2","is_dimension":false,"field_index":4},{"field_name":"time","field_type":"string","field_alias":"time","is_dimension":false,"field_index":5}]`)
	assert.True(t, equal)
}
