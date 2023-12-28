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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestDataSourceSvc_ToJson(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	ds := &resulttable.DataSource{
		BkDataId:          99999,
		Token:             "9e679720296f4ad7abf5ad95ac0acbdf",
		DataName:          "test_data_source",
		DataDescription:   "data source for test",
		MqClusterId:       1,
		MqConfigId:        21,
		EtlConfig:         "bk_standard_v2_event",
		IsCustomSource:    true,
		Creator:           "admin",
		CreateTime:        time.Time{},
		LastModifyUser:    "admin",
		LastModifyTime:    time.Time{},
		TypeLabel:         "event",
		SourceLabel:       "bk_monitor",
		IsEnable:          true,
		TransferClusterId: "default",
		SpaceTypeId:       "all",
		SpaceUid:          "",
	}
	kafkaTopic := &storage.KafkaTopicInfo{
		BkDataId:  ds.BkDataId,
		Topic:     "0bkmonitor_999990",
		Partition: 1,
	}
	rt := resulttable.ResultTable{
		TableId:        "test_data_source_table_id",
		IsCustomTable:  true,
		SchemaType:     "",
		DefaultStorage: "influxDB",
		IsEnable:       true,
		Label:          "others",
	}
	dsrt := resulttable.DataSourceResultTable{
		BkDataId: ds.BkDataId,
		TableId:  rt.TableId,
	}
	// 初始化数据
	db := mysql.GetDBSession().DB

	db.Where("bk_data_id=?", kafkaTopic.BkDataId).Delete(&kafkaTopic)
	kafkaTopic.Create(db)
	ds.Delete(db)
	err := ds.Create(db)
	assert.Nil(t, err)
	db.Where("table_id=?", rt.TableId).Delete(&rt)
	err = rt.Create(db)
	assert.Nil(t, err)
	db.Where("table_id=?", dsrt.TableId).Delete(&dsrt)
	err = dsrt.Create(db)
	assert.Nil(t, err)

	dsSvc := NewDataSourceSvc(ds)
	dsConfig, err := dsSvc.ToJson(true, true)
	assert.Nil(t, err)
	marshalString, err := jsonx.MarshalString(dsConfig)
	assert.Nil(t, err)
	assert.True(t, len(marshalString) != 0)
}

func TestDataSourceSvc_AddBuiltInChannelIdToGse(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	gomonkey.ApplyMethod(&http.Client{}, "Do", func(t *http.Client, req *http.Request) (*http.Response, error) {
		data := `{"message":"ok","result":true,"code":0,"data":{}}`
		body := io.NopCloser(strings.NewReader(data))
		return &http.Response{
			Status:        "ok",
			StatusCode:    200,
			Body:          body,
			ContentLength: int64(len(data)),
			Request:       req,
		}, nil
	})
	cluster := storage.ClusterInfo{
		ClusterName:   "kafka_test_cluster",
		ClusterType:   models.StorageTypeKafka,
		DomainName:    "127.0.0.1",
		Port:          9096,
		GseStreamToId: 0,
	}
	db.Delete(&cluster, "cluster_name = ?", cluster.ClusterName)
	err := cluster.Create(db)
	assert.NoError(t, err)
	topic := storage.KafkaTopicInfo{
		BkDataId:  1199999,
		Topic:     "test_kafka_topic_1",
		Partition: 1,
	}
	db.Delete(&topic)
	err = topic.Create(db)
	assert.NoError(t, err)
	ds := resulttable.DataSource{BkDataId: 1199999, MqClusterId: cluster.ClusterID}
	err = NewDataSourceSvc(&ds).AddBuiltInChannelIdToGse()
	assert.NoError(t, err)
}
