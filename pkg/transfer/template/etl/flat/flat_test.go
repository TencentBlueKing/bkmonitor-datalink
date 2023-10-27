// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package flat_test

import (
	"context"
	_ "embed"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/flat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// FlatTest
type FlatTest struct {
	testsuite.ETLSuite
}

//go:embed fixture/flat_test_data.json
var flatTestData string

//go:embed fixture/flat_test_consul_data.json
var flatTestConsulData string

// TestUsage
func (s *FlatTest) TestUsage() {
	s.CTX = testsuite.PipelineConfigStringInfoContext(
		s.CTX, s.PipelineConfig, flatTestConsulData)

	processor, err := flat.NewFlatProcessor(s.CTX, "test")
	s.NoError(err)

	s.Run(
		flatTestData,
		processor,
		func(result map[string]interface{}) {
			s.EqualRecord(result, map[string]interface{}{
				"dimensions": map[string]interface{}{
					"ip":             "127.0.0.1",
					"bk_supplier_id": "0",
					"bk_cloud_id":    "0",
					"bk_agent_id":    "010000525400c48bdc1670385834306k",
					"bk_biz_id":      2.0,
					"bk_host_id":     "30145",
					"testD":          "testD",
				},
				"metrics": map[string]interface{}{
					"testM": "10086",
				},

				"group_info": []map[string]string{
					{"tag": "aaa", "tag1": "aaa1"}, {"tag": "bbb", "tag1": "bbb1"},
				},
				"time": 1554094763,
			})
		},
	)
}

// TestServletTest :
func TestFlatTest(t *testing.T) {
	suite.Run(t, new(FlatTest))
}

// BenchmarkNewFlatProcessor :
func BenchmarkNewFlatProcessor(b *testing.B) {
	testsuite.ETLBenchmarkTest(b, func(ctx context.Context, name string) define.DataProcessor {
		processor, err := log.NewJSONLogProcessor(ctx, name)
		utils.CheckError(err)
		return processor
	}, []byte(`{"available":1.000000,"bkmonitorbeat":{"address":["127.0.0.1"],"hostname":"VM_1_10_centos","name":"VM_1_10_centos","version":"1.4.9"},"bizid":0,"bk_biz_id":2,"bk_cloud_id":0,"cloudid":0,"dataid":1009,"error_code":0,"gseindex":440779,"ip":"127.0.0.1","node_id":6,"status":0,"target_host":"127.0.0.1","target_port":8001,"task_duration":0,"task_id":16,"task_type":"tcp","timestamp":1554652696,"type":"uptimecheckbeat"}`))
}
