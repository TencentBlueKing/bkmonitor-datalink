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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

type NetworkFlowPipelineSuite struct {
	testsuite.FreeSchemaETLPipelineSuite
}

func (s *NetworkFlowPipelineSuite) SetupTest() {
	s.PipelineName = "bk_networkflow"
	s.ConsulConfig = `{"data_id":1603635,"etl_config":"bk_networkflow","result_table_list":[{"result_table":"flow_raw_test","schema_type":"free","shipper_list":[{"cluster_type":"elasticsearch","cluster_config":{"domain_name":"es.service.consul","port":9200},"storage_config":{"base_index":"flow_raw_test"}}],"field_list":[{"field_name":"dataid","type":"int","tag":"dimension","is_config_by_user":true},{"field_name":"sampler_address","type":"string","tag":"dimension","is_config_by_user":true},{"field_name":"src_addr","type":"string","tag":"dimension","is_config_by_user":true},{"field_name":"dst_addr","type":"string","tag":"dimension","is_config_by_user":true},{"field_name":"src_port","type":"int","tag":"dimension","is_config_by_user":true},{"field_name":"dst_port","type":"int","tag":"dimension","is_config_by_user":true},{"field_name":"proto","type":"string","tag":"dimension","is_config_by_user":true},{"field_name":"in_if","type":"int","tag":"dimension","is_config_by_user":true},{"field_name":"out_if","type":"int","tag":"dimension","is_config_by_user":true},{"field_name":"etype","type":"string","tag":"dimension","is_config_by_user":true},{"field_name":"type","type":"string","tag":"dimension","is_config_by_user":true},{"field_name":"time_flow_start_ms","type":"timestamp","tag":"metric","is_config_by_user":true},{"field_name":"time_flow_end_ms","type":"timestamp","tag":"metric","is_config_by_user":true},{"field_name":"time_received_ms","type":"timestamp","tag":"metric","is_config_by_user":true},{"field_name":"bytes","type":"int","tag":"metric","is_config_by_user":true},{"field_name":"packets","type":"int","tag":"metric","is_config_by_user":true},{"field_name":"sampling_rate","type":"int","tag":"metric","is_config_by_user":true},{"field_name":"stat_time","type":"timestamp","tag":"metric","is_config_by_user":true},{"field_name":"@timestamp","type":"timestamp","tag":"metric","is_config_by_user":true},{"field_name":"flow_bytes","type":"int","tag":"metric","is_config_by_user":true},{"field_name":"flow_packets","type":"int","tag":"metric","is_config_by_user":true}]}]}`
	s.FreeSchemaETLPipelineSuite.SetupTest()

	helper := utils.NewMapHelper(s.PipelineConfig.Option)
	helper.Set(config.PipelineConfigOptAllowMetricsMissing, false)
	helper.Set(config.PipelineConfigOptAllowDimensionsMissing, false)
	s.PipelineConfig.Option = helper.Data
	s.CTX = config.PipelineConfigIntoContext(s.CTX, s.PipelineConfig)
	s.CTX = config.ResultTableConfigIntoContext(s.CTX, s.ResultTableConfig)
}

func (s *NetworkFlowPipelineSuite) TestFlowRawMetadataRouting() {
	s.Equal("bk_networkflow", s.PipelineConfig.ETLConfig)
	s.Equal("flow_raw_test", s.ResultTableConfig.ResultTable)
	s.Len(s.ResultTableConfig.ShipperList, 1)
	s.Equal("elasticsearch", s.ResultTableConfig.ShipperList[0].ClusterType)
}

func (s *NetworkFlowPipelineSuite) TestFlowElasticsearchBackendPayload() {
	s.StoreHost(&models.CCHostInfo{
		IP:      "127.0.0.1",
		CloudID: 0,
	}).AnyTimes()

	s.FrontendPulled = `{"dataid":1603635,"sampler_address":"127.0.0.1","time_flow_start_ns":1779421614709835690,"time_flow_end_ns":1779421614773835690,"time_received_ns":1779421615216283724,"bytes":240,"packets":432,"sampling_rate":0,"src_addr":"91.82.52.165","dst_addr":"19.222.145.184","src_port":31885,"dst_port":45816,"proto":"TCP","in_if":0,"out_if":0,"etype":"IPv4","type":"NETFLOW_V5"}`

	var wg sync.WaitGroup
	wg.Add(1)
	pipe := s.BuildPipe(func(payload define.Payload) {
	}, func(data map[string]interface{}) {
		defer wg.Done()
		dimensions := data["dimensions"].(map[string]interface{})
		metrics := data["metrics"].(map[string]interface{})
		s.Equal(float64(1603635), dimensions["dataid"])
		s.Equal("127.0.0.1", dimensions["sampler_address"])
		s.Equal("91.82.52.165", dimensions["src_addr"])
		s.Equal("19.222.145.184", dimensions["dst_addr"])
		s.Equal("TCP", dimensions["proto"])
		s.Equal("NETFLOW_V5", dimensions["type"])
		s.Equal(float64(240), metrics["bytes"])
		s.Equal(float64(432), metrics["packets"])
		s.Equal(float64(240), metrics["flow_bytes"])
		s.Equal(float64(432), metrics["flow_packets"])
		s.Equal(float64(1779421614709), metrics["time_flow_start_ms"])
		s.Equal(float64(1779421614773), metrics["time_flow_end_ms"])
		s.Equal(float64(1779421615216), metrics["time_received_ms"])
		s.Equal(float64(1779421614773), metrics["stat_time"])
		s.Equal(float64(1779421614773), metrics["@timestamp"])
	})

	s.RunPipe(pipe, wg.Wait)
}

func TestFlowPipelineSuite(t *testing.T) {
	suite.Run(t, new(NetworkFlowPipelineSuite))
}
