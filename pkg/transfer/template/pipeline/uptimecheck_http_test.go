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

// UptimecheckHTTPPipelineSuite :
type UptimecheckHTTPPipelineSuite struct {
	ETLPipelineSuite
}

// SetupTest :
func (s *UptimecheckHTTPPipelineSuite) SetupTest() {
	s.ConsulConfig = `{"etl_config":"bk_uptimecheck_http","result_table_list":[{"schema_type":"fixed","shipper_list":[{"cluster_config":{"domain_name":"influxdb.service.consul","port":5260},"storage_config":{"real_table_name":"http","database":"uptimecheck"},"cluster_type":"influxdb"}],"result_table":"uptimecheck.http","field_list":[{"default_value":null,"type":"double","is_config_by_user":true,"tag":"metric","field_name":"available"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"bk_biz_id"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"bk_cloud_id"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"bk_supplier_id"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"charset"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"metric","field_name":"content_length"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"error_code"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"media_type"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"message"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"method"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"node_id"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"response_code"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"status"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"metric","field_name":"steps"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"metric","field_name":"task_duration"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"task_id"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"task_type"},{"default_value":null,"type":"timestamp","is_config_by_user":true,"tag":"","field_name":"time"},{"default_value":null,"type":"string","is_config_by_user":true,"tag":"dimension","field_name":"url"}]}],"mq_config":{"cluster_config":{"domain_name":"kafka.service.consul","port":9092},"storage_config":{"topic":"0bkmonitor_10110","partition":1},"cluster_type":"kafka"},"data_id":1011}`
	s.PipelineName = "bk_uptimecheck_http"
	s.ETLPipelineSuite.SetupTest()
}

// TestRun :
func (s *UptimecheckHTTPPipelineSuite) TestRun() {
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
{"available":1.000000,"bkmonitorbeat":{"address":["127.0.0.1"],"hostname":"rbtnode1","name":"rbtnode1","version":"1.3.2"},"bizid":0,"bk_biz_id":2,"bk_cloud_id":0,"charset":"","cloudid":0,"content_length":81,"dataid":1011,"error_code":0,"gseindex":65,"ip":"127.0.0.1","media_type":"","message":"200 OK","method":"GET","node_id":3,"response_code":200,"status":0,"steps":1,"task_duration":104,"task_id":75,"task_type":"http","timestamp":1550561175,"type":"uptimecheckbeat","url":"http://baidu.com"}
{"available":1.000000,"bkmonitorbeat":{"address":["127.0.0.1"],"hostname":"rbtnode1","name":"rbtnode1","version":"1.3.2"},"bizid":0,"bk_biz_id":2,"bk_cloud_id":0,"charset":"utf-8","cloudid":0,"content_length":0,"dataid":1011,"error_code":0,"gseindex":189557,"ip":"127.0.0.1","media_type":"","message":"200 OK","method":"GET","node_id":3,"response_code":200,"status":0,"steps":1,"task_duration":46,"task_id":19,"task_type":"http","timestamp":1550213239,"type":"uptimecheckbeat","url":"http://www.qq.com"}
`
	wg.Add(2)
	pipe := s.BuildPipe(func(payload define.Payload) {
		wg.Done()
	}, func(i map[string]interface{}) {
		wg.Done()
	})

	s.RunPipe(pipe, wg.Wait)
}

// TestUptimecheckHTTPPipelineSuite :
func TestUptimecheckHTTPPipelineSuite(t *testing.T) {
	suite.Run(t, new(UptimecheckHTTPPipelineSuite))
}
