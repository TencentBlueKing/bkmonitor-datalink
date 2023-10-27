// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/models"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/formatter"
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/procport"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// ProcPipelineSuite :
type ProcPipelineSuite struct {
	ETLPipelineSuite
}

// SetupTest :
func (s *ProcPipelineSuite) SetupTest() {
	s.ConsulConfig = `{"etl_config":"bk_system_proc_port","result_table_list":[{"schema_type":"fixed","shipper_list":[{"cluster_config":{"domain_name":"influxdb.service.consul","port":5260},"storage_config":{"real_table_name":"proc_port","database":"system"},"cluster_type":"influxdb"}],"result_table":"system.proc_port","field_list":[{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"bind_ip"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"bk_biz_id"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"bk_cloud_id"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"bk_supplier_id"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"display_name"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"hostname"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"ip"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"listen"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"nonlisten"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"not_accurate_listen"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"param_regex"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"metric","field_name":"port_health"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"metric","field_name":"proc_exists"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"proc_name"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"protocol"},{"default_value":null,"type":"timestamp","is_config_by_user":true,"tag":"","field_name":"time"}]}],"mq_config":{"cluster_config":{"domain_name":"kafka.service.consul","port":9092},"storage_config":{"topic":"0bkmonitor_10130","partition":1},"cluster_type":"kafka"},"data_id":1013}`
	s.PipelineName = "bk_system_proc_port"
	s.ETLPipelineSuite.SetupTest()
}

// TestRun :
func (s *ProcPipelineSuite) TestRun() {
	s.StoreHost(&models.CCHostInfo{
		IP:      "127.0.0.1",
		CloudID: 0,
	}).AnyTimes()
	s.StoreHost(&models.CCHostInfo{
		IP:      "127.0.0.1",
		CloudID: 0,
	}).AnyTimes()

	var wg sync.WaitGroup

	wg.Add(2)
	s.FrontendPulled = `
{"bkmonitorbeat":{"address":["127.0.0.1"],"hostname":"zk-2","name":"zk-2","version":"1.2.10"},"bizid":0,"cloudid":0,"data":{"processes":[{"bindip":"","displayname":"zk-java","exists":1,"listen":[2181,3888],"name":"java","nonlisten":[],"notaccuratelisten":[],"paramregex":"org.apache.zookeeper.server.quorum.QuorumPeerMain","porthealth":1,"protocol":"tcp"},{"bindip":"","displayname":"es-java","exists":1,"listen":[10004,9300],"name":"java","nonlisten":[],"notaccuratelisten":[],"paramregex":"org.elasticsearch.bootstrap.Elasticsearch","porthealth":1,"protocol":"tcp"},{"bindip":"","displayname":"etcd","exists":1,"listen":[2379,2380],"name":"etcd","nonlisten":[],"notaccuratelisten":[],"paramregex":"","porthealth":1,"protocol":"tcp"},{"bindip":"","displayname":"kafka-java","exists":1,"listen":[9092],"name":"java","nonlisten":[],"notaccuratelisten":[],"paramregex":"kafkaServer-gc.log","porthealth":1,"protocol":"tcp"},{"bindip":"","displayname":"redis-server","exists":1,"listen":[6379],"name":"redis-server","nonlisten":[],"notaccuratelisten":[],"paramregex":":6379","porthealth":1,"protocol":"tcp"},{"bindip":"","displayname":"redis-sentinel","exists":1,"listen":[16379],"name":"redis-server","nonlisten":[],"notaccuratelisten":[],"paramregex":"sentinel","porthealth":1,"protocol":"tcp"},{"bindip":"","displayname":"consul-agent","exists":1,"listen":[8500,53,8301],"name":"consul","nonlisten":[],"notaccuratelisten":[],"paramregex":"","porthealth":1,"protocol":"tcp"},{"bindip":"","displayname":"rpcbind","exists":1,"listen":[111],"name":"rpcbind","nonlisten":[],"notaccuratelisten":[],"paramregex":"","porthealth":1,"protocol":"tcp"},{"bindip":"","displayname":"ceph-radosgw","exists":1,"listen":[7480],"name":"radosgw","nonlisten":[],"notaccuratelisten":[],"paramregex":"","porthealth":1,"protocol":"tcp"},{"bindip":"","displayname":"ceph-mon","exists":1,"listen":[],"name":"ceph-mon","nonlisten":[12345],"notaccuratelisten":[],"paramregex":"","porthealth":0,"protocol":"tcp"}]},"dataid":1013,"datetime":"2019-02-19 12:19:02","gseindex":121036,"ip":"127.0.0.1","timezone":8,"type":"processbeat","utctime":"2019-02-19 04:19:02"}
{"bkmonitorbeat":{"address":["127.0.0.1"],"hostname":"zk-2","name":"zk-2","version":"1.2.10"},"bizid":0,"cloudid":0,"data":{"processes":[{"bindip":"","displayname":"zk-java","exists":1,"listen":[2181,3888],"name":"java","nonlisten":[],"notaccuratelisten":[],"paramregex":"org.apache.zookeeper.server.quorum.QuorumPeerMain","porthealth":1,"protocol":"tcp"},{"bindip":"","displayname":"es-java","exists":1,"listen":[10004,9300],"name":"java","nonlisten":[],"notaccuratelisten":[],"paramregex":"org.elasticsearch.bootstrap.Elasticsearch","porthealth":1,"protocol":"tcp"},{"bindip":"","displayname":"etcd","exists":1,"listen":[2379,2380],"name":"etcd","nonlisten":[],"notaccuratelisten":[],"paramregex":"","porthealth":1,"protocol":"tcp"},{"bindip":"","displayname":"kafka-java","exists":1,"listen":[9092],"name":"java","nonlisten":[],"notaccuratelisten":[],"paramregex":"kafkaServer-gc.log","porthealth":1,"protocol":"tcp"},{"bindip":"","displayname":"redis-server","exists":1,"listen":[6379],"name":"redis-server","nonlisten":[],"notaccuratelisten":[],"paramregex":":6379","porthealth":1,"protocol":"tcp"},{"bindip":"","displayname":"redis-sentinel","exists":1,"listen":[16379],"name":"redis-server","nonlisten":[],"notaccuratelisten":[],"paramregex":"sentinel","porthealth":1,"protocol":"tcp"},{"bindip":"","displayname":"consul-agent","exists":1,"listen":[8500,53,8301],"name":"consul","nonlisten":[],"notaccuratelisten":[],"paramregex":"","porthealth":1,"protocol":"tcp"},{"bindip":"","displayname":"rpcbind","exists":1,"listen":[111],"name":"rpcbind","nonlisten":[],"notaccuratelisten":[],"paramregex":"","porthealth":1,"protocol":"tcp"},{"bindip":"","displayname":"ceph-radosgw","exists":1,"listen":[7480],"name":"radosgw","nonlisten":[],"notaccuratelisten":[],"paramregex":"","porthealth":1,"protocol":"tcp"},{"bindip":"","displayname":"ceph-mon","exists":1,"listen":[],"name":"ceph-mon","nonlisten":[12345],"notaccuratelisten":[],"paramregex":"","porthealth":0,"protocol":"tcp"}]},"dataid":1013,"datetime":"2019-02-19 12:19:02","gseindex":121036,"ip":"127.0.0.1","timezone":8,"type":"processbeat","utctime":"2019-02-19 04:19:02"}
`
	wg.Add(20)
	pipe := s.BuildPipe(func(payload define.Payload) {
		wg.Done()
	}, func(i map[string]interface{}) {
		wg.Done()
	})

	s.RunPipe(pipe, wg.Wait)
}

// TestProcPipelineSuite :
func TestProcPipelineSuite(t *testing.T) {
	suite.Run(t, new(ProcPipelineSuite))
}
