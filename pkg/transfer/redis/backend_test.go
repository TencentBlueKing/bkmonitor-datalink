// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis_test

import (
	"context"
	"testing"
	"time"

	"github.com/Shopify/sarama"
	"github.com/cstockton/go-conv"
	goredis "github.com/go-redis/redis"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/redis"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// newProducerConfig:
type newProducerOption func(conf define.Configuration) *goredis.Options

// testCase :
type testCase struct {
	tag, field bool
	data       string
}

// BackendSuit:
type BackendSuit struct {
	ETLSuite
	backend        *redis.Backend
	newKafkaConfig func(configuration define.Configuration) (*sarama.Config, error)
}

// SetupTest :
func (s *BackendSuit) SetupTest() {
	s.ETLSuite.SetupTest()
	consul := `{"etl_config":"bk_exporter","result_table_list":[{"schema_type":"free","shipper_list":[{"cluster_config":{"domain_name":"influxdb_proxy.bkmonitor.service.consul","port":10201},"storage_config":{"real_table_name":"table0","database":"2_exporter_zookeeper"},"auth_info":{"username":"x","password":"xxyuyy"},"cluster_type":"influxdb"},{"cluster_config":{"domain_name":"kafka.service.consul","port":9092},"storage_config":{"topic":"0bkmonitor_storage_2_Exporter_mssql6.osd_metrics_1","partition":3},"auth_info":{"username":"","password":""},"cluster_type":"kafka"}],"result_table":"2_exporter_zookeeper.table0","field_list":[{"default_value":"-1","alias_name":"","tag":"dimension","description":"\u4e1a\u52a1ID","type":"int","is_config_by_user":true,"field_name":"bk_biz_id","unit":""},{"default_value":"-1","alias_name":"","tag":"dimension","description":"\u4e91\u533a\u57dfID","type":"int","is_config_by_user":true,"field_name":"bk_cloud_id","unit":""},{"default_value":"-1","alias_name":"","tag":"dimension","description":"\u5f00\u53d1\u5546ID","type":"int","is_config_by_user":true,"field_name":"bk_supplier_id","unit":""},{"default_value":"","alias_name":"","tag":"dimension","description":"\u91c7\u96c6\u5668IP\u5730\u5740","type":"string","is_config_by_user":true,"field_name":"ip","unit":""},{"default_value":"","alias_name":"","tag":"","description":"\u6570\u636e\u4e0a\u62a5\u65f6\u95f4","type":"timestamp","is_config_by_user":true,"field_name":"time","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_approximate_data_size","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_avg_latency","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_ephemerals_count","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_followers","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_max_file_descriptor_count","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_max_latency","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_min_latency","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_num_alive_connections","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_open_file_descriptor_count","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_outstanding_requests","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_packets_received","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_packets_sent","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_pending_syncs","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_synced_followers","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_up","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_watch_count","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_znode_count","unit":""}]},{"schema_type":"free","shipper_list":[{"cluster_config":{"domain_name":"influxdb_proxy.bkmonitor.service.consul","port":10201},"storage_config":{"real_table_name":"table1","database":"2_exporter_zookeeper"},"auth_info":{"username":"","password":""},"cluster_type":"influxdb"}],"result_table":"2_exporter_zookeeper.table1","field_list":[{"default_value":"-1","alias_name":"","tag":"dimension","description":"\u4e1a\u52a1ID","type":"int","is_config_by_user":true,"field_name":"bk_biz_id","unit":""},{"default_value":"-1","alias_name":"","tag":"dimension","description":"\u4e91\u533a\u57dfID","type":"int","is_config_by_user":true,"field_name":"bk_cloud_id","unit":""},{"default_value":"-1","alias_name":"","tag":"dimension","description":"\u5f00\u53d1\u5546ID","type":"int","is_config_by_user":true,"field_name":"bk_supplier_id","unit":""},{"default_value":"","alias_name":"","tag":"dimension","description":"\u91c7\u96c6\u5668IP\u5730\u5740","type":"string","is_config_by_user":true,"field_name":"ip","unit":""},{"default_value":null,"alias_name":"","tag":"dimension","description":"","type":"string","is_config_by_user":true,"field_name":"state","unit":""},{"default_value":"","alias_name":"","tag":"","description":"\u6570\u636e\u4e0a\u62a5\u65f6\u95f4","type":"timestamp","is_config_by_user":true,"field_name":"time","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"zk_server_state","unit":""}]}],"option":{},"mq_config":{"cluster_config":{"domain_name":"kafka.service.consul","port":9092},"storage_config":{"topic":"0bkmonitor_12001360","partition":1},"auth_info":{"username":"","password":""},"cluster_type":"kafka"},"data_id":1200136}`
	var pipe config.PipelineConfig
	s.NoError(json.Unmarshal([]byte(consul), &pipe))

	mqConfig := pipe.MQConfig

	redisOptions := mqConfig.AsRedisCluster()
	redisOptions.SetDomain("127.0.0.1")
	redisOptions.SetPort(26379)
	redisOptions.SetKey("tt")
	redisOptions.SetDB(0)
	redisOptions.SetIsSentinel(true)
	redisOptions.SetMaster("mymaster")
	authInfo := config.NewAuthInfo(mqConfig)
	authInfo.SetPassword("admin")

	s.CTX = config.PipelineConfigIntoContext(context.Background(), &pipe)
	s.CTX = config.ShipperConfigIntoContext(s.CTX, mqConfig)
}

// option 客户端,cases 测试数据,conf 配置,countExec 预计会触发几次提交
func (s *BackendSuit) runTestInLocalHostEnv(option newProducerOption, cases []testCase, conf define.Configuration, countExec int) {
	var (
		input  = len(cases) // 输入长度
		output int          // 输出长度
		temp   int          // 临时变量
		count  int          // exec 执行次数,用于测试批次,缓存满触发提交,close 时触发的提交
	)

	mockCtrl := gomock.NewController(s.T())
	s.CTX = config.IntoContext(s.CTX, conf)
	pipe := NewMockPipeliner(mockCtrl)
	client := NewMockClientOfRedis(mockCtrl)

	redis.NewRedisClient = func(dbInfo *config.RedisMetaClusterInfo, auth *config.SimpleMetaAuthInfo) redis.ClientOfRedis {
		return client
	}

	redis.ClientPing = func(client redis.ClientOfRedis) error {
		return nil
	}

	redis.NewRedisPipeline = func(cli redis.ClientOfRedis) goredis.Pipeliner {
		return pipe
	}

	client.EXPECT().LLen(gomock.Any()).DoAndReturn(func(key string) *goredis.IntCmd {
		return goredis.NewIntCmd(output)
	}).AnyTimes()

	client.EXPECT().Close().Return(nil).AnyTimes()
	client.EXPECT().Ping().Return(nil).AnyTimes()

	pipe.EXPECT().LPush(gomock.Any(), gomock.Any()).DoAndReturn(func(key string, value ...interface{}) *goredis.IntCmd {
		temp++
		for _, v := range value {
			// 测试数据
			logging.Info(conv.String(v))
		}
		return goredis.NewIntCmd(1)
	}).AnyTimes()

	pipe.EXPECT().Exec().DoAndReturn(func() (*goredis.Cmd, error) {
		output += temp
		temp = 0
		count++
		return nil, nil
	}).AnyTimes()

	pipe.EXPECT().Close().Return(nil).AnyTimes()

	s.backend, _ = redis.NewBackend(s.CTX, "test")
	for _, value := range cases {
		payload := define.NewJSONPayloadFrom([]byte(value.data), 0)
		s.backend.Push(payload, s.KillCh)
	}
	time.Sleep(1 * time.Second) // 防止测试太早结束,影响结果
	s.CheckKillChan(s.KillCh)
	s.NotNil(s.backend)
	s.NoError(s.backend.Close())
	s.Equal(input, output)
	logging.Infof("Exec %d times totally", count)
	if countExec != 0 { // 如果countExec 为零 说明不想测试exec 的次数
		s.Equal(count, countExec)
	}
}

// TestBackend_Push : 正常提交
func (s *BackendSuit) TestBackend_Push() {
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
			`{"time":1558494970,"dimensions":{"tag":"2"},"metrics":{"field":2}}`,
		},
		{
			false, true,
			`{"time":1558494970,"dimensions":{"tag":"3"},"metrics":{"field":3}}`,
		},
		{
			false, true,
			`{"time":1558494970,"dimensions":{"tag":"4"},"metrics":{"field":4}}`,
		},
		{
			false, true,
			`{"time":1558494970,"dimensions":{"tag":"5"},"metrics":{"field":5}}`,
		},
	}
	countExec := 0 // 因lpush 而触发exec 的次数
	conf := config.Configuration
	conf.Set(redis.PayloadRedisBatchSize, 2)
	conf.Set(redis.PayloadRedisBufferSize, 1)
	conf.Set(redis.PayloadRedisFlushInterval, 5*time.Second)
	conf.Set(redis.PayloadRedisFlushRetries, 3)
	s.runTestInLocalHostEnv(func(conf define.Configuration) *goredis.Options {
		return &goredis.Options{
			DB: 0,
		}
	}, cases, conf, countExec)
}

// TestBackend_Push : 因关闭而触发提交
func (s *BackendSuit) TestBackend_ClosePush() {
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
			`{"time":1558494970,"dimensions":{"tag":"2"},"metrics":{"field":2}}`,
		},
		{
			false, true,
			`{"time":1558494970,"dimensions":{"tag":"3"},"metrics":{"field":3}}`,
		},
		{
			false, true,
			`{"time":1558494970,"dimensions":{"tag":"4"},"metrics":{"field":4}}`,
		},
		{
			false, true,
			`{"time":1558494970,"dimensions":{"tag":"5"},"metrics":{"field":5}}`,
		},
	}
	countExec := 1 // 当close时,必定会触发一次提交,len(case) == 6 << 批次最大长度;所以,一共会触发一次提交
	conf := config.Configuration
	conf.Set(redis.PayloadRedisBatchSize, 200)
	conf.Set(redis.PayloadRedisBufferSize, 100000)
	conf.Set(redis.PayloadRedisFlushInterval, 5*time.Second)
	s.runTestInLocalHostEnv(func(conf define.Configuration) *goredis.Options {
		return &goredis.Options{
			DB: 0,
		}
	}, cases, conf, countExec)
}

// TestBackend_Push : 因批次满而触发提交
func (s *BackendSuit) TestBackend_FullPush() {
	cases := []testCase{
		{
			false, true,
			`{"time":1558494970,"dimensions":{"tag":null},"metrics":{"field":1}}`,
		},
		{
			false, true,
			`{"time":1558494970,"dimensions":{"tag":"1"},"metrics":{"field":1}}`,
		},
	}
	countExec := 3 // 当close时,必定会触发一次提交,len(case) == 2;批次最大长度 为1;所以,一共会触发2 + 1 次提交
	conf := config.Configuration
	conf.Set(redis.PayloadRedisBatchSize, 1)
	conf.Set(redis.PayloadRedisBufferSize, 100000)
	conf.Set(redis.PayloadRedisFlushInterval, 5*time.Second)
	// conf.Set()
	s.runTestInLocalHostEnv(func(conf define.Configuration) *goredis.Options {
		return &goredis.Options{
			DB: 0,
		}
	}, cases, conf, countExec)
}

// TestBackend_Push : 因超时而触发提交
func (s *BackendSuit) TestBackend_IntervalPush() {
	cases := []testCase{
		{
			false, true,
			`{"time":1558494970,"dimensions":{"tag":null},"metrics":{"field":1}}`,
		},
	}
	countExec := 0 //
	conf := config.Configuration
	conf.Set(redis.PayloadRedisBatchSize, 1)
	conf.Set(redis.PayloadRedisBufferSize, 100000)
	// todo 更优雅的方式
	// msg="Exec n times totally" 当n >> 大于 2时,说明等待close时 已经触发了多次提交
	conf.Set(redis.PayloadRedisFlushInterval, 1*time.Millisecond)

	s.runTestInLocalHostEnv(func(conf define.Configuration) *goredis.Options {
		return &goredis.Options{
			DB: 0,
		}
	}, cases, conf, countExec)
}

// TestBackend :
func TestBackend(t *testing.T) {
	suite.Run(t, new(BackendSuit))
}
