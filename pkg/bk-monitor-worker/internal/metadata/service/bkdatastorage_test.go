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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestGenBkdataRtIdWithoutBizId(t *testing.T) {
	config.InitConfig()
	type args struct {
		tableId string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{name: "abcdef123456789011121314151617181920.group1", args: args{tableId: "abcdef123456789011121314151617181920.group1"}, want: fmt.Sprintf("%s_%s", config.GlobalBkdataRtIdPrefix, "6789011121314151617181920_group1")},
		{name: "abcdef.group1", args: args{tableId: "abcdef.group1"}, want: fmt.Sprintf("%s_%s", config.GlobalBkdataRtIdPrefix, "abcdef_group1")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, GenBkdataRtIdWithoutBizId(tt.args.tableId), "GenBkdataRtIdWithoutBizId(%v)", tt.args.tableId)
		})
	}
}

func TestBkDataStorageSvc_CreateDatabusClean(t *testing.T) {
	config.FilePath = "../../../bmw.yaml"
	mocker.PatchDBSession()

	db := mysql.GetDBSession().DB
	defer db.Close()
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
