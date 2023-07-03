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

// UptimecheckUDPPipelineSuite :
type UptimecheckUDPPipelineSuite struct {
	ETLPipelineSuite
}

// SetupTest :
func (s *UptimecheckUDPPipelineSuite) SetupTest() {
	s.ConsulConfig = `{"etl_config":"bk_uptimecheck_udp","result_table_list":[{"schema_type":"fixed","shipper_list":[{"cluster_config":{"domain_name":"influxdb.service.consul","port":5260},"storage_config":{"real_table_name":"udp","database":"uptimecheck"},"cluster_type":"influxdb"}],"result_table":"uptimecheck.udp","field_list":[{"default_value":null,"type":"double","is_config_by_user":true,"tag":"metric","field_name":"available"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"bk_biz_id"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"bk_cloud_id"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"bk_supplier_id"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"error_code"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"node_id"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"status"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"target_host"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"target_port"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"metric","field_name":"task_duration"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"task_id"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"task_type"},{"default_value":null,"type":"timestamp","is_config_by_user":true,"tag":"","field_name":"time"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"metric","field_name":"times"}]}],"mq_config":{"cluster_config":{"domain_name":"kafka.service.consul","port":9092},"storage_config":{"topic":"0bkmonitor_10100","partition":1},"cluster_type":"kafka"},"data_id":1010}`
	s.PipelineName = "bk_uptimecheck_udp"
	s.ETLPipelineSuite.SetupTest()
}

// TestRun :
func (s *UptimecheckUDPPipelineSuite) TestRun() {
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
{"available":1.000000,"bkmonitorbeat":{"address":["127.0.0.1"],"hostname":"VM_1_16_centos","name":"VM_1_16_centos","version":"1.4.3"},"bizid":0,"bk_biz_id":2,"bk_cloud_id":0,"cloudid":0,"dataid":1010,"error_code":0,"gseindex":70468,"ip":"127.0.0.1","max_times":3,"node_id":4,"status":0,"target_host":"127.0.0.1","target_port":9211,"task_duration":5000,"task_id":109,"task_type":"udp","times":0,"timestamp":1552967059,"type":"uptimecheckbeat"}
{"available":1.000000,"bkmonitorbeat":{"address":["127.0.0.1"],"hostname":"zk-1","name":"zk-1","version":"1.3.2"},"bizid":0,"bk_biz_id":99,"bk_cloud_id":0,"cloudid":0,"dataid":1009,"error_code":0,"gseindex":66892,"ip":"127.0.0.1","node_id":6,"status":0,"target_host":"127.0.0.1","target_port":8301,"task_duration":0,"task_id":28,"task_type":"tcp","timestamp":1549527448,"type":"uptimecheckbeat"}
`
	wg.Add(2)
	pipe := s.BuildPipe(func(payload define.Payload) {
		wg.Done()
	}, func(i map[string]interface{}) {
		wg.Done()
	})

	s.RunPipe(pipe, wg.Wait)
}

// TestUptimecheckUDPPipelineSuite :
func TestUptimecheckUDPPipelineSuite(t *testing.T) {
	suite.Run(t, new(UptimecheckUDPPipelineSuite))
}
