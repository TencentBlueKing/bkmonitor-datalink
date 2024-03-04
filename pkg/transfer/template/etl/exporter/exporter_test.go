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

const exporterResultTableList = `[
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
      },
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
	s.CTX = config.ResultTableConfigIntoContext(s.CTX, rtList[0])
}

// TestUsage :
func (s *ExporterMetricsFilterProcessorSuite) TestUsage() {
	payload := define.NewDefaultPayload()

	const input = `{
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

	s.NoError(payload.From([]byte(input)))

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

	processor, _ := exporter.NewFilterProcessor(s.CTX, "usage")

	wg.Add(1)
	go func() {
		processor.Process(payload, outputChan, killChan)
		close(killChan)
		close(outputChan)
		wg.Done()
	}()

	counter := map[string]int{}
	for output := range outputChan {
		var data define.GroupETLRecord
		s.NoError(output.To(&data))
		if _, ok := data.Metrics["consul_net_node_latency_p75"]; ok {
			s.Equal(int64(1549437716), *data.Time)
		} else {
			s.Equal(int64(1695023812), *data.Time)
		}

		for k := range data.Metrics {
			counter[k]++
		}
		s.NotPanics(func() {
			dimensions := data.Dimensions
			s.Equal("0", dimensions[define.RecordCloudIDFieldName])
			s.Equal("0", dimensions[define.RecordSupplierIDFieldName])
			s.Equal("127.0.0.1", dimensions[define.RecordIPFieldName])
			s.True(len(dimensions) > 3)
		})
	}
	wg.Wait()

	s.Equal(1, counter["consul_net_node_latency_p75"])
	s.Equal(2, counter["consul_catalog_nodes_up"])
}

func (s *ExporterMetricsFilterProcessorSuite) TestUsageWithExemplar() {
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

	processor, err := exporter.NewFilterProcessor(s.CTX, "exemplar")
	s.NoError(err)

	payload := define.NewDefaultPayload()
	const input = `{
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
          "timestamp": 1695023812,
          "exemplar": {
            "bk_span_id":"span",
            "bk_trace_id":"trace",
            "bk_trace_timestamp":1655195411375,
            "bk_trace_value":1
          }
        }
      ]
    }
  }
}`

	s.NoError(payload.From([]byte(input)))

	wg.Add(1)
	go func() {
		processor.Process(payload, outputChan, killChan)
		close(killChan)
		close(outputChan)
		wg.Done()
	}()

	pushed := 0
	for output := range outputChan {
		var data define.ETLRecord
		s.NoError(output.To(&data))
		s.Equal("span", data.Exemplar["bk_span_id"])
		s.Equal("trace", data.Exemplar["bk_trace_id"])
		s.Equal(float64(1655195411375), data.Exemplar["bk_trace_timestamp"])
		s.Equal(float64(1), data.Exemplar["bk_trace_value"])

		pushed++
	}

	wg.Wait()
	s.Equal(1, pushed)
}

// TestExporterMetricsFilterProcessor :
func TestExporterMetricsFilterProcessor(t *testing.T) {
	suite.Run(t, new(ExporterMetricsFilterProcessorSuite))
}
