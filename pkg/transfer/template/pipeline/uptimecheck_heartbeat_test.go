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
	_ "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/uptimecheck"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// UptimecheckHeartBeatPipelineSuite :
type UptimecheckHeartBeatPipelineSuite struct {
	ETLPipelineSuite
}

// SetupTest :
func (s *UptimecheckHeartBeatPipelineSuite) SetupTest() {
	s.ConsulConfig = `{"etl_config":"bk_uptimecheck_heartbeat","result_table_list":[{"schema_type":"fixed","shipper_list":[{"cluster_config":{"domain_name":"influxdb.service.consul","port":5260},"storage_config":{"real_table_name":"heartbeat","database":"uptimecheck"},"cluster_type":"influxdb"}],"result_table":"uptimecheck.heartbeat","field_list":[{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"bk_biz_id"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"bk_cloud_id"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"bk_supplier_id"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"metric","field_name":"error"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"metric","field_name":"fail"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"metric","field_name":"loaded_tasks"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"node_id"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"metric","field_name":"reload"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"metric","field_name":"reload_timestamp"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"metric","field_name":"running_tasks"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"status"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"metric","field_name":"success"},{"default_value":null,"type":"timestamp","is_config_by_user":true,"tag":"","field_name":"time"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"metric","field_name":"uptime"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"version"}]}],"mq_config":{"cluster_config":{"domain_name":"kafka.service.consul","port":9092},"storage_config":{"topic":"0bkmonitor_10080","partition":1},"cluster_type":"kafka"},"data_id":1008}`
	s.PipelineName = "bk_uptimecheck_heartbeat"
	s.ETLPipelineSuite.SetupTest()
}

// TestRun :
func (s *UptimecheckHeartBeatPipelineSuite) TestRun() {
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
{"bkmonitorbeat":{"address":["127.0.0.1"],"hostname":"VM_1_16_centos","name":"VM_1_16_centos","version":"1.3.2"},"bizid":0,"bk_biz_id":2,"bk_cloud_id":0,"cloudid":0,"dataid":1008,"error":0,"fail":0,"gseindex":1,"ip":"127.0.0.1","loaded_tasks":0,"node_id":7,"reload":0,"reload_timestamp":1550559929,"running_tasks":0,"status":0,"success":0,"timestamp":1550559929,"type":"uptimecheckbeat","uptime":24,"version":"1.3.2"}
{"bkmonitorbeat":{"address":["127.0.0.1"],"hostname":"zk-2","name":"zk-2","version":"1.3.2"},"bizid":0,"bk_biz_id":2,"bk_cloud_id":0,"cloudid":0,"dataid":1008,"error":0,"fail":0,"gseindex":14131,"ip":"127.0.0.1","loaded_tasks":0,"node_id":10,"reload":0,"reload_timestamp":1548731512,"running_tasks":0,"status":0,"success":0,"timestamp":1549522432,"type":"uptimecheckbeat","uptime":790920000,"version":"1.3.2"}
`
	wg.Add(2)
	pipe := s.BuildPipe(func(payload define.Payload) {
		wg.Done()
	}, func(i map[string]interface{}) {
		wg.Done()
	})

	s.RunPipe(pipe, wg.Wait)
}

// TestUptimecheckHeartBeatPipelineSuite :
func TestUptimecheckHeartBeatPipelineSuite(t *testing.T) {
	suite.Run(t, new(UptimecheckHeartBeatPipelineSuite))
}
