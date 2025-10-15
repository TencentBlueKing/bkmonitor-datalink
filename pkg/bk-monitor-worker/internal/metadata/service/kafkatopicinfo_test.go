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
	"testing"

	"github.com/Shopify/sarama"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestKafkaTopicInfoSvc_RefreshTopicInfo(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	topicInfo := storage.KafkaTopicInfo{
		BkDataId: 1223322,
		Topic:    "kafka_topic_info_test",
	}
	db.Delete(&topicInfo, "topic = ?", topicInfo.Topic)
	err := topicInfo.Create(db)
	assert.NoError(t, err)
	ds := resulttable.DataSource{
		BkDataId:       topicInfo.BkDataId,
		DataName:       "kafka_topic_info_test",
		MqClusterId:    12345,
		IsCustomSource: true,
		IsEnable:       true,
	}
	db.Delete(&ds, "bk_data_id = ?", ds.BkDataId)
	err = ds.Create(db)
	assert.NoError(t, err)
	schema := "http"
	cluster := storage.ClusterInfo{
		ClusterID:   ds.MqClusterId,
		ClusterName: "kafka_topic_test_cluster",
		ClusterType: models.StorageTypeKafka,
		DomainName:  "127.0.0.1",
		Port:        9092,
		Schema:      &schema,
	}
	db.Delete(&cluster, "cluster_id = ?", cluster.ClusterID)
	err = cluster.Create(db)
	assert.NoError(t, err)

	mockerClient := &mocker.KafkaClientMocker{PartitionMap: map[string][]int32{topicInfo.Topic: {0, 1, 2}}}
	gomonkey.ApplyFunc(ClusterInfoSvc.GetKafkaClient, func(svc ClusterInfoSvc) (sarama.Client, error) {
		return mockerClient, nil
	})
	err = NewKafkaTopicInfoSvc(&topicInfo).RefreshTopicInfo(cluster, mockerClient)
	assert.NoError(t, err)
	var result storage.KafkaTopicInfo
	err = storage.NewKafkaTopicInfoQuerySet(db).TopicEq(topicInfo.Topic).One(&result)
	assert.NoError(t, err)
	assert.True(t, len(mockerClient.PartitionMap[result.Topic]) == result.Partition)
}
