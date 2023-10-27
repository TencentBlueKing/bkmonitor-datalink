// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/Shopify/sarama"
	"github.com/golang/mock/gomock"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/mocktest"
)

type KafkaSuite struct {
	suite.Suite
}

var (
	topicName = "testTopic"
	testData  = "testData"
)

// test message struct
type TestMessage struct {
	data  string
	topic string
}

func (tm *TestMessage) Matches(x interface{}) bool {
	cp := x.(*sarama.ProducerMessage)
	return cp.Topic == ("_"+tm.topic) && cp.Value == sarama.StringEncoder(tm.data)
}

func (tm *TestMessage) String() string {
	return tm.data
}

func NewTestMessage(data, topicName string) gomock.Matcher {
	return &TestMessage{
		data:  data,
		topic: topicName,
	}
}

// test message struct
type TestTopicDetail struct {
	partition int
	replace   int
}

func (tt *TestTopicDetail) Matches(x interface{}) bool {
	cp := x.(*sarama.TopicDetail)
	return cp.NumPartitions == int32(tt.partition) && cp.ReplicationFactor == int16(tt.partition)
}

func (tt *TestTopicDetail) String() string {
	return fmt.Sprintf("%d: %d", tt.partition, tt.replace)
}

func NewTestTopicDetail(partition, replace int) gomock.Matcher {
	return &TestTopicDetail{
		partition: partition,
		replace:   replace,
	}
}

func (s *KafkaSuite) TestKafkaStorage() {
	var client influxdb.StorageBackup

	// mock
	ctrl := gomock.NewController(s.T())
	defer ctrl.Finish()

	var mc *mocktest.KafkaClient
	kcStubs := gostub.Stub(&influxdb.NewClient, func(addrs []string, conf *sarama.Config) (sarama.Client, error) {
		mc = mocktest.NewKafkaClient(ctrl)
		mc.EXPECT().Partitions("_"+topicName).AnyTimes().Return([]int32{1}, nil)
		mc.EXPECT().Close().AnyTimes()
		mc.EXPECT().GetOffset("_"+topicName, int32(1), gomock.Any()).AnyTimes().Return(int64(1), nil)
		// mc.EXPECT().GetOffset("_"+topicName, int32(1), sarama.OffsetOldest).Times(1).Return(int64(1), nil)
		return mc, nil
	})
	defer kcStubs.Reset()

	spStubs := gostub.Stub(&influxdb.NewSyncProducer, func(addrs []string, config *sarama.Config) (sarama.SyncProducer, error) {
		sp := mocktest.NewMockSyncProducer(ctrl)
		sp.EXPECT().SendMessage(NewTestMessage(testData, topicName)).Return(int32(1), int64(1), nil).Times(1)
		return sp, nil
	})
	defer spStubs.Reset()

	cgStubs := gostub.Stub(&influxdb.NewKafkaConsumerGroup, func(groupID string, client sarama.Client) (sarama.ConsumerGroup, error) {
		cg := mocktest.NewMockConsumerGroup(ctrl)
		cg.EXPECT().Consume(gomock.Any(), []string{"_" + topicName}, gomock.Any()).AnyTimes()
		cg.EXPECT().Close().Times(1)
		return cg, nil
	})
	defer cgStubs.Reset()

	omStubs := gostub.Stub(&influxdb.NewOffsetManagerFromClient, func(group string, client sarama.Client) (sarama.OffsetManager, error) {
		pom := mocktest.NewMockPartitionOffsetManager(ctrl)
		pom.EXPECT().NextOffset().Return(int64(1), "").Times(1)
		pom.EXPECT().Close().Times(1)

		om := mocktest.NewMockOffsetManager(ctrl)
		om.EXPECT().ManagePartition("_"+topicName, int32(1)).Times(1).Return(pom, nil)
		om.EXPECT().Close().Times(1)
		return om, nil
	})
	defer omStubs.Reset()

	caStubs := gostub.Stub(&influxdb.NewClusterAdmin, func(addrs []string, conf *sarama.Config) (sarama.ClusterAdmin, error) {
		ca := mocktest.NewMockClusterAdmin(ctrl)
		ca.EXPECT().CreateTopic(topicName, NewTestTopicDetail(1, 1), false).Return(nil).Times(1)
		ca.EXPECT().Close().Times(1)
		return ca, nil
	})
	defer caStubs.Reset()

	common.Config.SetDefault(common.ConfigKeyKafkaVersion, "0.10.2.0")
	client, _ = influxdb.NewKafkaBackup(context.Background(), topicName)

	// test Push
	err := client.Push(testData)
	s.Equal(err, nil, "push must success and no error.")

	// test Pull
	ctx := context.Background()
	handler := func(data string) {}
	err = client.Pull(ctx, handler)
	s.Equal(err, nil, "Pull must success and no error.")

	// test HasData
	result, err := client.HasData()
	s.Equal(result, false, "HasData must return true")
	s.Equal(err, nil, "HasData must success")

	mc.EXPECT().GetOffset("_"+topicName, int32(1), gomock.Any()).AnyTimes().Return(int64(0), nil)
	result, err = client.HasData()
	s.Equal(result, false, "HasData must return true")
	s.Equal(err, nil, "HasData must success")
}

// TestBackendSuite :
func TestKafkaSuite(t *testing.T) {
	suite.Run(t, new(KafkaSuite))
}
