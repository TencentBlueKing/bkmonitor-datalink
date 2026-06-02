// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch_test

import (
	"bytes"
	"context"
	stdjson "encoding/json"
	"io"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/elasticsearch"
	transferjson "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

type FlowBulkHandlerSuite struct {
	testsuite.ETLSuite
	mockBulkWriter *testsuite.MockBulkWriter
	newBulkWriter  func(version string, config map[string]interface{}) (elasticsearch.BulkWriter, error)
}

func (s *FlowBulkHandlerSuite) SetupTest() {
	s.ETLSuite.SetupTest()
	s.newBulkWriter = elasticsearch.NewBulkWriter

	s.ResultTableConfig = &config.MetaResultTableConfig{
		ResultTable: "flow_raw_test",
		SchemaType:  config.ResultTableSchemaTypeFree,
		ShipperList: []*config.MetaClusterInfo{{
			ClusterType: "elasticsearch",
			ClusterConfig: map[string]interface{}{
				"version":     "7.10.0",
				"domain_name": "127.0.0.1",
				"port":        9200,
				"schema":      "http",
			},
			StorageConfig: map[string]interface{}{
				"base_index": "flow_raw_test",
			},
			AuthInfo: map[string]interface{}{},
		}},
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

	s.mockBulkWriter = testsuite.NewMockBulkWriter(s.Ctrl)
	elasticsearch.NewBulkWriter = func(version string, cfg map[string]interface{}) (elasticsearch.BulkWriter, error) {
		return s.mockBulkWriter, nil
	}
}

func (s *FlowBulkHandlerSuite) TearDownTest() {
	s.ETLSuite.TearDownTest()
	elasticsearch.NewBulkWriter = s.newBulkWriter
}

func (s *FlowBulkHandlerSuite) TestFlowBulkHandlerFlattensETLRecord() {
	cluster := s.ResultTableConfig.ShipperList[0].AsElasticSearchCluster()
	handler, err := elasticsearch.NewBulkHandler(cluster, s.ResultTableConfig, time.Second, nil, elasticsearch.FixedIndexRender("flow_raw_test"))
	s.NoError(err)

	s.mockBulkWriter.EXPECT().Write(gomock.Any(), "flow_raw_test", gomock.Any()).DoAndReturn(func(ctx context.Context, index string, records elasticsearch.Records) (*elasticsearch.Response, error) {
		s.Len(records, 1)
		doc := records[0].Document
		s.Equal(float64(1603635), doc["dataid"])
		s.Equal("127.0.0.1", doc["sampler_address"])
		s.Equal("91.82.52.165", doc["src_addr"])
		s.Equal("19.222.145.184", doc["dst_addr"])
		s.Equal(float64(240), doc["bytes"])
		s.Equal(float64(432), doc["packets"])
		s.Equal(float64(240), doc["flow_bytes"])
		s.Equal(float64(432), doc["flow_packets"])
		s.Equal(float64(1779421614), doc["stat_time"])
		s.Equal(float64(1779421614), doc["@timestamp"])
		s.NotNil(doc["time"])

		body, marshalErr := stdjson.Marshal(map[string]interface{}{
			"took":   1,
			"errors": false,
			"items": []map[string]interface{}{{
				"index": map[string]interface{}{"status": 201},
			}},
		})
		s.NoError(marshalErr)
		return &elasticsearch.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBuffer(body))}, nil
	})

	payload := define.NewJSONPayload(0)
	record := define.ETLRecord{}
	s.NoError(transferjson.Unmarshal([]byte(`{"time":1779421614,"dimensions":{"dataid":1603635,"sampler_address":"127.0.0.1","src_addr":"91.82.52.165","dst_addr":"19.222.145.184","src_port":31885,"dst_port":45816,"proto":"TCP","in_if":0,"out_if":0,"etype":"IPv4","type":"NETFLOW_V5"},"metrics":{"time_flow_start_ns":1779421614,"time_flow_end_ns":1779421614,"time_received_ns":1779421615,"bytes":240,"packets":432,"sampling_rate":0,"stat_time":1779421614,"@timestamp":1779421614,"flow_bytes":240,"flow_packets":432}}`), &record))
	s.NoError(payload.From(&record))

	result, _, ok := handler.Handle(s.CTX, payload, s.KillCh)
	s.True(ok)
	count, err := handler.Flush(s.CTX, []interface{}{result})
	s.NoError(err)
	s.Equal(1, count)
}

func TestFlowBulkHandlerSuite(t *testing.T) {
	suite.Run(t, new(FlowBulkHandlerSuite))
}
