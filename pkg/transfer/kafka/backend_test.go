// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka_test

import (
	"context"
	"testing"

	"github.com/Shopify/sarama"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/kafka"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// newProducerConfig:
type newProducerConfig func(conf define.Configuration) (*sarama.Config, error)

// testCase :
type testCase struct {
	tag, field bool
	data       string
}

// BackendSuit:
type BackendSuit struct {
	ETLSuite
	backend        *kafka.Backend
	newKafkaConfig func(configuration define.Configuration) (*sarama.Config, error)
}

// SetupTest :
func (s *BackendSuit) SetupTest() {
	consul := `{"etl_config":"bk_exporter","result_table_list":[{"schema_type":"free","shipper_list":[{"cluster_config":{"domain_name":"influxdb_proxy.bkmonitor.service.consul","port":10201},"storage_config":{"real_table_name":"table0","database":"2_exporter_zookeeper"},"auth_info":{"username":"","password":""},"cluster_type":"influxdb"},{"cluster_config":{"domain_name":"kafka.service.consul","port":9092},"storage_config":{"topic":"0bkmonitor_storage_2_Exporter_mssql6.osd_metrics_1","partition":3},"auth_info":{"username":"","password":""},"cluster_type":"kafka"}],"result_table":"2_exporter_zookeeper.table0","field_list":[{"default_value":"-1","alias_name":"","tag":"dimension","description":"\u4e1a\u52a1ID","type":"int","is_config_by_user":true,"field_name":"bk_biz_id","unit":""},{"default_value":"-1","alias_name":"","tag":"dimension","description":"\u4e91\u533a\u57dfID","type":"int","is_config_by_user":true,"field_name":"bk_cloud_id","unit":""},{"default_value":"-1","alias_name":"","tag":"dimension","description":"\u5f00\u53d1\u5546ID","type":"int","is_config_by_user":true,"field_name":"bk_supplier_id","unit":""},{"default_value":"","alias_name":"","tag":"dimension","description":"\u91c7\u96c6\u5668IP\u5730\u5740","type":"string","is_config_by_user":true,"field_name":"ip","unit":""},{"default_value":"","alias_name":"","tag":"","description":"\u6570\u636e\u4e0a\u62a5\u65f6\u95f4","type":"timestamp","is_config_by_user":true,"field_name":"time","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_approximate_data_size","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_avg_latency","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_ephemerals_count","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_followers","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_max_file_descriptor_count","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_max_latency","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_min_latency","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_num_alive_connections","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_open_file_descriptor_count","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_outstanding_requests","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_packets_received","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_packets_sent","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_pending_syncs","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_synced_followers","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_up","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_watch_count","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_znode_count","unit":""}]},{"schema_type":"free","shipper_list":[{"cluster_config":{"domain_name":"influxdb_proxy.bkmonitor.service.consul","port":10201},"storage_config":{"real_table_name":"table1","database":"2_exporter_zookeeper"},"auth_info":{"username":"","password":""},"cluster_type":"influxdb"}],"result_table":"2_exporter_zookeeper.table1","field_list":[{"default_value":"-1","alias_name":"","tag":"dimension","description":"\u4e1a\u52a1ID","type":"int","is_config_by_user":true,"field_name":"bk_biz_id","unit":""},{"default_value":"-1","alias_name":"","tag":"dimension","description":"\u4e91\u533a\u57dfID","type":"int","is_config_by_user":true,"field_name":"bk_cloud_id","unit":""},{"default_value":"-1","alias_name":"","tag":"dimension","description":"\u5f00\u53d1\u5546ID","type":"int","is_config_by_user":true,"field_name":"bk_supplier_id","unit":""},{"default_value":"","alias_name":"","tag":"dimension","description":"\u91c7\u96c6\u5668IP\u5730\u5740","type":"string","is_config_by_user":true,"field_name":"ip","unit":""},{"default_value":null,"alias_name":"","tag":"dimension","description":"","type":"string","is_config_by_user":true,"field_name":"state","unit":""},{"default_value":"","alias_name":"","tag":"","description":"\u6570\u636e\u4e0a\u62a5\u65f6\u95f4","type":"timestamp","is_config_by_user":true,"field_name":"time","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_server_state","unit":""}]}],"option":{},"mq_config":{"cluster_config":{"domain_name":"kafka.service.consul","port":9092},"storage_config":{"topic":"0bkmonitor_12001360","partition":1},"auth_info":{"username":"","password":""},"cluster_type":"kafka"},"data_id":1200136}`

	var pipe *config.PipelineConfig
	pipe = s.PipelineConfig
	s.NoError(json.Unmarshal([]byte(consul), &pipe))
	s.ETLSuite.SetupTest()

	mqConfig := config.NewMetaClusterInfo()
	kafkaConfig := mqConfig.AsKafkaCluster()
	kafkaConfig.SetDomain("127.0.0.1")
	kafkaConfig.SetPort(9092)
	auth := config.NewAuthInfo(mqConfig)
	kafkaConfig.SetTopic("testForReal")
	kafkaConfig.SetPartition(0)
	auth.SetPassword("")
	auth.SetUserName("")

	s.CTX = config.PipelineConfigIntoContext(context.Background(), pipe)
	s.CTX = config.ShipperConfigIntoContext(s.CTX, mqConfig)
}

// runBackendTest :
func (s *BackendSuit) runBackendTest(producerConfig newProducerConfig, cases []testCase) {
	mockCtrl := gomock.NewController(s.T())
	conf := config.Configuration
	s.CTX = config.IntoContext(s.CTX, conf)
	kafka.NewKafkaProducerConfig = producerConfig
	producer := NewMockProducer(mockCtrl)
	input := make(chan *sarama.ProducerMessage)
	// 模拟kafka消耗　input中的数据（详见sarama NewAsyncProducer）
	kafka.NewProducer = func(cluster []string, conf *sarama.Config) (kafka.Producer, error) {
		go func() {
			for range input {
				// 在这里可以写校对逻辑
			}
		}()
		return producer, nil
	}

	s.backend, _ = kafka.NewKafkaBackend(s.CTX, "test")

	producer.EXPECT().Input().Return(input).AnyTimes()
	producer.EXPECT().Errors().Return(nil).AnyTimes()
	producer.EXPECT().Close().Return(nil).AnyTimes()

	for _, v := range cases {
		payload := define.NewJSONPayloadFrom([]byte(v.data), 1)
		s.backend.Push(payload, s.KillCh)
	}
	s.CheckKillChan(s.KillCh)
	s.NotNil(s.backend)
	// time.Sleep(1 * time.Second) // 等待 input 获取数据完成
	s.NoError(s.backend.Close())
	close(input)
}

// TestPush : 提交正常数据
func (s *BackendSuit) TestPush() {
	cases := []testCase{
		{
			false, true,
			`{"time":1558494970,"dimensions":{"tag":null},"metrics":{"field":1}}`,
		},
		{
			false, true,
			`{"time":1558494970,"dimensions":{"tag":"1"},"metrics":{"field":1}}`,
		},
		{
			false, true,
			`{"time":1558494970,"dimensions":{"tag":"2"},"metrics":{"field":1}}`,
		},
		{
			false, true,
			`{"time":1558494970,"dimensions":{"tag":"3"},"metrics":{"field":1}}`,
		},
		{
			false, true,
			`{"time":1558494970,"dimensions":{"tag":"4"},"metrics":{"field":1}}`,
		},
		{
			false, true,
			`{"time":1558494970,"dimensions":{"tag":"5"},"metrics":{"field":1}}`,
		},
	}

	s.runBackendTest(func(conf define.Configuration) (*sarama.Config, error) {
		c := sarama.NewConfig()
		c.ClientID = define.ProcessID
		c.Producer.Partitioner = sarama.NewHashPartitioner
		return c, c.Validate()
	}, cases)
}

// 提交有错误的格式;但仍能正常提交
// 错误数+1，其他正常提交
// TestPushErrFormat :
func (s *BackendSuit) TestPushErrFormat() {
	cases := []testCase{
		{
			false, true,
			`:15584-xx!!94970,"rics":"field":1}}`,
		},
		{
			false, true,
			`{"time":1558494970,"dimensions":{"tag":""},"metrics":{"field":11111}}`,
		},
	}

	s.runBackendTest(func(conf define.Configuration) (*sarama.Config, error) {
		c := sarama.NewConfig()
		c.Producer.Partitioner = sarama.NewRandomPartitioner
		c.Producer.Return.Successes = false
		return c, c.Validate()
	}, cases)
}

// TestBackend :
func TestBackend(t *testing.T) {
	suite.Run(t, new(BackendSuit))
}
