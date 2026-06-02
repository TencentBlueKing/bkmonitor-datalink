// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package flow_test

import (
	_ "embed"
	"sync"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	transferjson "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	flowetl "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/flow"
	formatter "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/formatter"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

//go:embed testdata/flow_collector_handoff.golden.json
var flowCollectorHandoffGolden string

//go:embed testdata/flow_collector_handoff.contract.md
var flowCollectorHandoffContract string

type FlowProcessorTest struct {
	testsuite.ETLSuite
}

func (s *FlowProcessorTest) SetupTest() {
	s.ETLSuite.SetupTest()
	s.PipelineConfig.ETLConfig = flowetl.ProcessorName
	s.ResultTableConfig.ResultTable = "flow_raw_test"
	s.ResultTableConfig.FieldList = []*config.MetaFieldConfig{}
}

func (s *FlowProcessorTest) TestFlowAuthorityMirrorFixture() {
	var payload map[string]interface{}
	s.NoError(transferjson.Unmarshal([]byte(flowCollectorHandoffGolden), &payload))

	expectedKeys := []string{
		"dataid",
		"sampler_address",
		"time_flow_start_ns",
		"time_flow_end_ns",
		"time_received_ns",
		"bytes",
		"packets",
		"sampling_rate",
		"src_addr",
		"dst_addr",
		"src_port",
		"dst_port",
		"proto",
		"in_if",
		"out_if",
		"etype",
		"type",
	}

	for _, key := range expectedKeys {
		_, ok := payload[key]
		s.Truef(ok, "missing key %s", key)
	}

	s.NotEmpty(flowCollectorHandoffContract)
	s.Len(payload, len(expectedKeys))
}

func (s *FlowProcessorTest) TestFlowRemovedFieldsStayAbsent() {
	var payload map[string]interface{}
	s.NoError(transferjson.Unmarshal([]byte(flowCollectorHandoffGolden), &payload))

	removedFields := []string{
		"peer_ip",
		"effective_exporter_key",
		"observation_domain_id",
		"export_time",
		"protocol",
		"proto_name",
		"flow_protocol_family",
		"community_id",
		"extra",
	}

	for _, field := range removedFields {
		_, ok := payload[field]
		s.Falsef(ok, "removed field %s should stay absent", field)
	}
}

func (s *FlowProcessorTest) TestFlowProcessorRegistration() {
	processor, err := define.NewDataProcessor(s.CTX, flowetl.ProcessorName)
	s.NoError(err)
	s.NotNil(processor)
}

func (s *FlowProcessorTest) TestFlowNonSFlowUnifiedFieldRule() {
	processor := flowetl.NewNetworkFlowProcessor(s.CTX, "test")
	s.Run(flowCollectorHandoffGolden, processor, func(result map[string]interface{}) {
		s.EqualRecord(result, map[string]interface{}{
			"time": int64(1779421614),
			"dimensions": map[string]interface{}{
				"dataid":          1603635.0,
				"sampler_address": "127.0.0.1",
				"src_addr":        "91.82.52.165",
				"dst_addr":        "19.222.145.184",
				"src_port":        31885.0,
				"dst_port":        45816.0,
				"proto":           "TCP",
				"in_if":           0.0,
				"out_if":          0.0,
				"etype":           "IPv4",
				"type":            "NETFLOW_V5",
			},
			"metrics": map[string]interface{}{
				"bytes":              240.0,
				"packets":            432.0,
				"sampling_rate":      0.0,
				"time_flow_start_ns": 1779421614.0,
				"time_flow_end_ns":   1779421614.0,
				"time_received_ns":   1779421615.0,
				"stat_time":          1779421614.0,
				"@timestamp":         1779421614.0,
				"flow_bytes":         240.0,
				"flow_packets":       432.0,
			},
		})
	})
}

func (s *FlowProcessorTest) TestFlowSFlowUnifiedFieldRule() {
	processor := flowetl.NewNetworkFlowProcessor(s.CTX, "test")
	s.Run(`{"dataid":1603635,"sampler_address":"10.11.10.26","time_flow_start_ns":1779421670084452198,"time_flow_end_ns":1779421670084452198,"time_received_ns":1779421670084452198,"bytes":70,"packets":1,"sampling_rate":10000,"src_addr":"10.11.10.19","dst_addr":"10.11.10.26","src_port":34810,"dst_port":9092,"proto":"TCP","in_if":2,"out_if":1073741823,"etype":"IPv4","type":"SFLOW_5"}`,
		processor,
		func(result map[string]interface{}) {
			s.EqualRecord(result, map[string]interface{}{
				"time": int64(1779421670),
				"dimensions": map[string]interface{}{
					"dataid":          1603635.0,
					"sampler_address": "10.11.10.26",
					"src_addr":        "10.11.10.19",
					"dst_addr":        "10.11.10.26",
					"src_port":        34810.0,
					"dst_port":        9092.0,
					"proto":           "TCP",
					"in_if":           2.0,
					"out_if":          1073741823.0,
					"etype":           "IPv4",
					"type":            "SFLOW_5",
				},
				"metrics": map[string]interface{}{
					"bytes":              70.0,
					"packets":            1.0,
					"sampling_rate":      10000.0,
					"time_flow_start_ns": 1779421670.0,
					"time_flow_end_ns":   1779421670.0,
					"time_received_ns":   1779421670.0,
					"stat_time":          1779421670.0,
					"@timestamp":         1779421670.0,
					"flow_bytes":         700000.0,
					"flow_packets":       10000.0,
				},
			})
		},
	)
}

func (s *FlowProcessorTest) TestFlowNetFlowV9UnifiedFieldRule() {
	processor := flowetl.NewNetworkFlowProcessor(s.CTX, "test")
	s.Run(`{"dataid":1603635,"sampler_address":"10.11.10.26","time_flow_start_ns":1779421670084452198,"time_flow_end_ns":1779421670084452198,"time_received_ns":1779421670084452198,"bytes":71,"packets":2,"sampling_rate":9999,"src_addr":"10.11.10.19","dst_addr":"10.11.10.26","src_port":34810,"dst_port":9092,"proto":"TCP","in_if":2,"out_if":1073741823,"etype":"IPv4","type":"NETFLOW_V9"}`,
		processor,
		func(result map[string]interface{}) {
			metrics := s.GetMetrics(result)
			s.Equal(71.0, metrics["flow_bytes"])
			s.Equal(2.0, metrics["flow_packets"])
			s.Equal(9999.0, metrics["sampling_rate"])
		},
	)
}

func (s *FlowProcessorTest) TestFlowIPFIXUnifiedFieldRule() {
	processor := flowetl.NewNetworkFlowProcessor(s.CTX, "test")
	s.Run(`{"dataid":1603635,"sampler_address":"10.11.10.26","time_flow_start_ns":1779421670084452198,"time_flow_end_ns":1779421670084452198,"time_received_ns":1779421670084452198,"bytes":88,"packets":9,"sampling_rate":123,"src_addr":"10.11.10.19","dst_addr":"10.11.10.26","src_port":34810,"dst_port":9092,"proto":"TCP","in_if":2,"out_if":1073741823,"etype":"IPv4","type":"IPFIX"}`,
		processor,
		func(result map[string]interface{}) {
			metrics := s.GetMetrics(result)
			s.Equal(88.0, metrics["flow_bytes"])
			s.Equal(9.0, metrics["flow_packets"])
			s.Equal(123.0, metrics["sampling_rate"])
		},
	)
}

func (s *FlowProcessorTest) TestFlowStatTimeRule() {
	processor := flowetl.NewNetworkFlowProcessor(s.CTX, "test")
	s.Run(`{"dataid":1603635,"sampler_address":"10.11.10.26","time_flow_start_ns":1779421670084452198,"time_flow_end_ns":0,"time_received_ns":1779421671084452198,"bytes":70,"packets":1,"sampling_rate":10000,"src_addr":"10.11.10.19","dst_addr":"10.11.10.26","src_port":34810,"dst_port":9092,"proto":"TCP","in_if":2,"out_if":1073741823,"etype":"IPv4","type":"SFLOW_5"}`,
		processor,
		func(result map[string]interface{}) {
			metrics := s.GetMetrics(result)
			s.Equal(int64(1779421671), s.GetTime(result))
			s.Equal(1779421671.0, metrics["stat_time"])
			s.Equal(1779421671.0, metrics["@timestamp"])
		},
	)
}

func (s *FlowProcessorTest) TestFlowRawIPv6AndUnknownStrings() {
	processor := flowetl.NewNetworkFlowProcessor(s.CTX, "test")
	s.Run(`{"dataid":1603635,"sampler_address":"2001:db8::1","time_flow_start_ns":1779421670084452198,"time_flow_end_ns":1779421670084452198,"time_received_ns":1779421670084452198,"bytes":70,"packets":1,"sampling_rate":10000,"src_addr":"2001:db8::2","dst_addr":"2001:db8::3","src_port":34810,"dst_port":9092,"proto":"SCTP","in_if":2,"out_if":1073741823,"etype":"UNKNOWN_V6","type":"NETFLOW_V5"}`,
		processor,
		func(result map[string]interface{}) {
			dimensions := s.GetDimensions(result)
			s.Equal("2001:db8::2", dimensions["src_addr"])
			s.Equal("2001:db8::3", dimensions["dst_addr"])
			s.Equal("SCTP", dimensions["proto"])
			s.Equal("UNKNOWN_V6", dimensions["etype"])
		},
	)
}

func (s *FlowProcessorTest) TestFlowUnknownTypeRejected() {
	processor := flowetl.NewNetworkFlowProcessor(s.CTX, "test")
	outputChan := make(chan define.Payload)
	killChan := make(chan error, 1)

	go func() {
		processor.Process(define.NewJSONPayloadFrom([]byte(`{"dataid":1603635,"sampler_address":"10.11.10.26","time_flow_start_ns":1779421670084452198,"time_flow_end_ns":1779421670084452198,"time_received_ns":1779421670084452198,"bytes":70,"packets":1,"sampling_rate":10000,"src_addr":"10.11.10.19","dst_addr":"10.11.10.26","src_port":34810,"dst_port":9092,"proto":"TCP","in_if":2,"out_if":1073741823,"etype":"IPv4","type":"UNKNOWN_FLOW"}`), 0), outputChan, killChan)
		close(outputChan)
		close(killChan)
	}()

	for range outputChan {
		s.Fail("unexpected output for unsupported flow type")
	}

	gotErr := define.ErrOperationForbidden
	for err := range killChan {
		if err == nil {
			continue
		}
		gotErr = err
	}

	s.Equal(define.ErrOperationForbidden, errors.Cause(gotErr))
}

func TestFlowProcessorTest(t *testing.T) {
	suite.Run(t, new(FlowProcessorTest))
}

type FlowFormatterSuite struct {
	testsuite.ETLSuite
}

func (s *FlowFormatterSuite) SetupTest() {
	s.ETLSuite.SetupTest()
	s.ResultTableConfig = &config.MetaResultTableConfig{
		ResultTable: "flow_raw_test",
		SchemaType:  config.ResultTableSchemaTypeFree,
		FieldList: []*config.MetaFieldConfig{
			{FieldName: "dataid", Type: define.MetaFieldTypeInt, Tag: define.MetaFieldTagDimension, IsConfigByUser: true},
			{FieldName: "sampler_address", Type: define.MetaFieldTypeString, Tag: define.MetaFieldTagDimension, IsConfigByUser: true},
			{FieldName: "src_addr", Type: define.MetaFieldTypeString, Tag: define.MetaFieldTagDimension, IsConfigByUser: true},
			{FieldName: "dst_addr", Type: define.MetaFieldTypeString, Tag: define.MetaFieldTagDimension, IsConfigByUser: true},
			{FieldName: "src_port", Type: define.MetaFieldTypeInt, Tag: define.MetaFieldTagDimension, IsConfigByUser: true},
			{FieldName: "dst_port", Type: define.MetaFieldTypeInt, Tag: define.MetaFieldTagDimension, IsConfigByUser: true},
			{FieldName: "proto", Type: define.MetaFieldTypeString, Tag: define.MetaFieldTagDimension, IsConfigByUser: true},
			{FieldName: "in_if", Type: define.MetaFieldTypeInt, Tag: define.MetaFieldTagDimension, IsConfigByUser: true},
			{FieldName: "out_if", Type: define.MetaFieldTypeInt, Tag: define.MetaFieldTagDimension, IsConfigByUser: true},
			{FieldName: "etype", Type: define.MetaFieldTypeString, Tag: define.MetaFieldTagDimension, IsConfigByUser: true},
			{FieldName: "type", Type: define.MetaFieldTypeString, Tag: define.MetaFieldTagDimension, IsConfigByUser: true},
			{FieldName: "time_flow_start_ns", Type: define.MetaFieldTypeTimestamp, Tag: define.MetaFieldTagMetric, IsConfigByUser: true},
			{FieldName: "time_flow_end_ns", Type: define.MetaFieldTypeTimestamp, Tag: define.MetaFieldTagMetric, IsConfigByUser: true},
			{FieldName: "time_received_ns", Type: define.MetaFieldTypeTimestamp, Tag: define.MetaFieldTagMetric, IsConfigByUser: true},
			{FieldName: "bytes", Type: define.MetaFieldTypeInt, Tag: define.MetaFieldTagMetric, IsConfigByUser: true},
			{FieldName: "packets", Type: define.MetaFieldTypeInt, Tag: define.MetaFieldTagMetric, IsConfigByUser: true},
			{FieldName: "sampling_rate", Type: define.MetaFieldTypeInt, Tag: define.MetaFieldTagMetric, IsConfigByUser: true},
			{FieldName: "stat_time", Type: define.MetaFieldTypeTimestamp, Tag: define.MetaFieldTagMetric, IsConfigByUser: true},
			{FieldName: "@timestamp", Type: define.MetaFieldTypeTimestamp, Tag: define.MetaFieldTagMetric, IsConfigByUser: true},
			{FieldName: "flow_bytes", Type: define.MetaFieldTypeInt, Tag: define.MetaFieldTagMetric, IsConfigByUser: true},
			{FieldName: "flow_packets", Type: define.MetaFieldTypeInt, Tag: define.MetaFieldTagMetric, IsConfigByUser: true},
		},
	}
	s.CTX = config.ResultTableConfigIntoContext(s.CTX, s.ResultTableConfig)

	pipeConfig := config.PipelineConfigFromContext(s.CTX)
	pipeConfig.Option[config.PipelineConfigOptAllowMetricsMissing] = false
	pipeConfig.Option[config.PipelineConfigOptAllowDimensionsMissing] = false
	s.CTX = config.PipelineConfigIntoContext(s.CTX, pipeConfig)
	// formatter 要求配置和 store 都在上下文里，testsuite 已经提供
	s.CTX = config.IntoContext(s.CTX, s.Config)
}

func (s *FlowFormatterSuite) TestFlowFormatterRetainsDerivedFields() {
	etlProcessor := flowetl.NewNetworkFlowProcessor(s.CTX, "flow")
	formatterProcessor, err := formatter.NewTSFormatter(s.CTX, "ts_format")
	s.NoError(err)

	outputChan := make(chan define.Payload)
	killChan := make(chan error, 1)

	go func() {
		etlProcessor.Process(define.NewJSONPayloadFrom([]byte(flowCollectorHandoffGolden), 0), outputChan, killChan)
		close(outputChan)
		close(killChan)
	}()

	var etlOutput define.Payload
	for output := range outputChan {
		etlOutput = output
	}
	for err = range killChan {
		s.NoError(err)
	}
	s.NotNil(etlOutput)

	formattedChan := make(chan define.Payload)
	formattedKillChan := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		formatterProcessor.Process(etlOutput, formattedChan, formattedKillChan)
		close(formattedChan)
		close(formattedKillChan)
	}()

	var formatted define.GroupETLRecord
	count := 0
	for output := range formattedChan {
		count++
		s.NoError(output.To(&formatted))
	}
	for err = range formattedKillChan {
		s.NoError(err)
	}
	wg.Wait()

	s.Equal(1, count)
	s.Equal("127.0.0.1", formatted.Dimensions["sampler_address"])
	s.Equal("91.82.52.165", formatted.Dimensions["src_addr"])
	s.Equal(float64(240), formatted.Metrics["bytes"])
	s.Equal(float64(432), formatted.Metrics["packets"])
	s.Equal(float64(240), formatted.Metrics["flow_bytes"])
	s.Equal(float64(432), formatted.Metrics["flow_packets"])
	s.Equal(float64(1779421614), formatted.Metrics["stat_time"])
	s.Equal(float64(1779421614), formatted.Metrics["@timestamp"])
}

func TestFlowFormatterSuite(t *testing.T) {
	suite.Run(t, new(FlowFormatterSuite))
}
