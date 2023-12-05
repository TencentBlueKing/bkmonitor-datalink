// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package exporter_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/exporter"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

var exporterCollectedData = `{
  "@timestamp": "2019-02-06T07:21:56.241Z",
  "ip": "127.0.0.1",
  "bizid": 0,
  "cloudid": 0,
  "group_info": [{"tag": "aaa"}],
  "bk_cmdb_level":[{"bk_biz_id":2,"bk_biz_name":"蓝鲸","bk_module_id":31,"bk_module_name":"","bk_service_status":"1","bk_set_env":"3","bk_set_id":8,"bk_set_name":"配置平台"}],
  "prometheus": {
    "collector": {
      "metrics": [
        {
          "key": "consul_catalog_nodes_up",
          "labels": {
            "consul_datacenter": "dc",
            "consul_service_id": "fta"
          },
          "value": 1.000000,
          "timestamp": 1695023812
        },
        {
          "key": "consul_catalog_nodes_up",
          "labels": {
            "consul_datacenter": "dc",
            "consul_service_id": "influxdb"
          },
          "value": 2.000000,
          "timestamp": 1695023812
        },
        {
          "key": "consul_net_node_latency_p75",
          "labels": {
            "consul_datacenter": "dc"
          },
          "value": 5.124331
        }
      ]
    }
  }
}`

var exporterResultTableList = `[
  {
    "schema_type": "fixed",
    "field_list": [
      {
        "type": "int",
        "is_config_by_user": true,
        "tag": "metric",
        "field_name": "consul_catalog_nodes_up"
      },
      {
        "type": "string",
        "is_config_by_user": true,
        "tag": "dimension",
        "field_name": "consul_datacenter"
      },
      {
        "type": "string",
        "is_config_by_user": true,
        "tag": "dimension",
        "field_name": "consul_service_id"
      }
    ]
  },
  {
    "schema_type": "fixed",
    "field_list": [
      {
        "type": "float",
        "is_config_by_user": true,
        "tag": "metric",
        "field_name": "consul_net_node_latency_p75"
      },
      {
        "type": "string",
        "is_config_by_user": true,
        "tag": "dimension",
        "field_name": "consul_datacenter"
      }
    ]
  }
]`

// ExporterMetricsFilterProcessorSuite :
type ExporterMetricsFilterProcessorSuite struct {
	testsuite.StoreSuite
}

// SetupTest :
func (s *ExporterMetricsFilterProcessorSuite) SetupTest() {
	s.StoreSuite.SetupTest()
	rtList := make([]*config.MetaResultTableConfig, 0)
	s.NoError(json.Unmarshal([]byte(exporterResultTableList), &rtList))
	s.PipelineConfig.ResultTableList = rtList
}

// TestUsage :
func (s *ExporterMetricsFilterProcessorSuite) TestUsage() {
	payload := define.NewDefaultPayload()
	s.NoError(payload.From([]byte(exporterCollectedData)))

	var wg sync.WaitGroup
	outputChan := make(chan define.Payload)
	killChan := make(chan error)

	wg.Add(1)
	go func() {
		for err := range killChan {
			panic(err)
		}
		wg.Done()
	}()

	processor := exporter.NewFilterProcessor(s.CTX, "test")
	wg.Add(1)
	go func() {
		processor.Process(payload, outputChan, killChan)
		close(killChan)
		close(outputChan)
		wg.Done()
	}()

	counter := map[string]int{}
	for output := range outputChan {
		data := make(map[string]interface{})
		s.NoError(output.To(&data))

		metrics := data["metrics"].(map[string]interface{})
		for k := range metrics {
			counter[k]++
		}

		if _, ok := metrics["consul_net_node_latency_p75"]; ok {
			s.Equal(1549437716.0, data["time"])
		} else {
			s.Equal(1695023812.0, data["time"])
		}

		s.NotPanics(func() {
			dimensions := data["dimensions"].(map[string]interface{})
			s.Equal("0", dimensions[define.RecordCloudIDFieldName])
			s.Equal("0", dimensions[define.RecordSupplierIDFieldName])
			s.Equal("127.0.0.1", dimensions[define.RecordIPFieldName])
			s.True(len(dimensions) > 3)
			s.NotNil(data["metrics"])
		})
	}
	wg.Wait()

	s.Equal(counter["consul_net_node_latency_p75"], 1)
	s.Equal(counter["consul_catalog_nodes_up"], 2)
}

// TestExporterMetricsFilterProcessor :
func TestExporterMetricsFilterProcessor(t *testing.T) {
	suite.Run(t, new(ExporterMetricsFilterProcessorSuite))
}
