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

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// StandardPipelineSuite :
type StandardPipelineSuite struct {
	testsuite.FreeSchemaETLPipelineSuite
}

// SetupTest :
func (s *StandardPipelineSuite) SetupTest() {
	s.PipelineName = "bk_standard"
	s.ConsulConfig = `{"etl_config":"bk_standard","result_table_list":[{"schema_type":"free","shipper_list":[{"cluster_config":{"domain_name":"influxdb_proxy.bkmonitor.service.consul","port":10201},"storage_config":{"real_table_name":"fdsfds","database":"2_script_script_07291504"},"auth_info":{"username":"","password":""},"cluster_type":"influxdb"}],"result_table":"2_script_script_07291504.fdsfds","field_list":[{"default_value":"-1","alias_name":"","tag":"dimension","description":"\u4e1a\u52a1ID","type":"int","is_config_by_user":true,"field_name":"bk_biz_id","unit":""},{"default_value":"-1","alias_name":"","tag":"dimension","description":"\u4e91\u533a\u57dfID","type":"int","is_config_by_user":true,"field_name":"bk_cloud_id","unit":""},{"default_value":"-1","alias_name":"","tag":"dimension","description":"\u5f00\u53d1\u5546ID","type":"int","is_config_by_user":true,"field_name":"bk_supplier_id","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"cpu","unit":""},{"default_value":"","alias_name":"","tag":"dimension","description":"\u91c7\u96c6\u5668IP\u5730\u5740","type":"string","is_config_by_user":true,"field_name":"ip","unit":""},{"default_value":null,"alias_name":"","tag":"metric","description":"","type":"float","is_config_by_user":true,"field_name":"mem","unit":""},{"default_value":null,"alias_name":"","tag":"group","description":"","type":"group","is_config_by_user":true,"field_name":"taga","unit":""},{"default_value":null,"alias_name":"","tag":"group","description":"","type":"group","is_config_by_user":true,"field_name":"tagb","unit":""},{"default_value":"","alias_name":"","tag":"","description":"\u6570\u636e\u4e0a\u62a5\u65f6\u95f4","type":"timestamp","is_config_by_user":true,"field_name":"time","unit":""}]}],"option":{},"mq_config":{"cluster_config":{"domain_name":"kafka.service.consul","port":9092},"storage_config":{"topic":"0bkmonitor_12000070","partition":1},"auth_info":{"username":"","password":""},"cluster_type":"kafka"},"data_id":1200007}`
	s.FreeSchemaETLPipelineSuite.SetupTest()
}

// TestUsage :
func (s *StandardPipelineSuite) TestUsage() {
	s.StoreHost(&models.CCHostInfo{
		IP:      "127.0.0.1",
		CloudID: 0,
	}).AnyTimes()

	var wg sync.WaitGroup
	wg.Add(1)
	s.FrontendPulled = `{"bkmonitorbeat":{"address":["127.0.0.1"],"hostname":"VM_1_10_centos","name":"VM_1_10_centos","version":"1.7.0"},"bizid":0,"bk_biz_id":2,"bk_cloud_id":0,"cloudid":0,"cost_time":767,"dataid":1200007,"dimensions":{},"error_code":0,"group_info":[{"taga":"taga_value_2","tagb":"tagb_value_2"},{"taga":"taga_value_1","tagb":"tagb_value_1"}],"gseindex":388296,"ip":"127.0.0.1","localtime":"2019-07-30 17:12:12","message":"success","metrics":{"cpu":11,"mem":22},"node_id":0,"task_id":432,"task_type":"script","time":1564477933,"type":"bkmonitorbeat","usertime":"2019-07-30 09:12:13","utctime":"2019-07-30 09:12:12"}`
	s.ConsulClient.EXPECT().Put(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	wg.Add(2)
	pipe := s.BuildPipe(func(payload define.Payload) {
		wg.Done()
	}, func(i map[string]interface{}) {
		wg.Done()
	})

	s.RunPipe(pipe, wg.Wait)
}

// TestStandardPipelineSuite :
func TestStandardPipelineSuite(t *testing.T) {
	suite.Run(t, new(StandardPipelineSuite))
}
