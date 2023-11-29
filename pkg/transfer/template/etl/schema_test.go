// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package etl_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// PrepareByResultTablePluginSuite
type ProcessorSuite struct {
	testsuite.ETLSuite
	fields []*config.MetaFieldConfig
}

// SetupTest
func (s *ProcessorSuite) SetupTest() {
	s.ETLSuite.SetupTest()
	s.fields = []*config.MetaFieldConfig{
		{
			IsConfigByUser: true,
			Type:           define.MetaFieldTypeTimestamp,
			FieldName:      define.TimeFieldName,
			Tag:            define.MetaFieldTagTime,
		},
		{
			IsConfigByUser: true,
			Type:           define.MetaFieldTypeString,
			FieldName:      "key",
			Tag:            define.MetaFieldTagDimension,
		},
		{
			IsConfigByUser: true,
			Type:           define.MetaFieldTypeString,
			FieldName:      "value",
			Tag:            define.MetaFieldTagMetric,
		},
	}
	s.ResultTableConfig.FieldList = s.fields
}

// TestAsSimpleFlatProcessor
func (s *ProcessorSuite) TestAsSimpleFlatProcessor() {
	schema, err := etl.NewSchema(s.CTX)
	s.NoError(err)
	processor := etl.NewRecordProcessor("x", s.PipelineConfig, schema)

	s.Run(`{"time": 1574233401, "key": "x", "value": 1}`, processor, func(result map[string]interface{}) {
		s.EqualRecord(result, map[string]interface{}{
			"time": 1574233401,
			"dimensions": map[string]interface{}{
				"key": "x",
			},
			"metrics": map[string]interface{}{
				"value": "1",
			},
		})
	})
}

// TestTimeAliasFlatProcessor
func (s *ProcessorSuite) TestTimeAliasFlatProcessor() {
	schema, err := etl.NewSchema(s.CTX)
	s.NoError(err)
	processor := etl.NewRecordProcessor("x", s.PipelineConfig, schema)
	s.T().Logf("option %#v", s.ResultTableConfig.Option)

	s.Run(`{"timestamp": 1574233401, "key": "x", "value": 1}`, processor, func(result map[string]interface{}) {
		s.EqualRecord(result, map[string]interface{}{
			"time": 1574233401,
			"dimensions": map[string]interface{}{
				"key": "x",
			},
			"metrics": map[string]interface{}{
				"value": "1",
			},
		})
	})
}

// TestAsBeatsFlatProcessor
func (s *ProcessorSuite) TestAsBeatsFlatProcessor() {
	s.ResultTableConfig.FieldList = append(s.ResultTableConfig.FieldList,
		&config.MetaFieldConfig{
			IsConfigByUser: true,
			Type:           define.MetaFieldTypeString,
			FieldName:      "ip",
			Tag:            define.MetaFieldTagDimension,
		},
		&config.MetaFieldConfig{
			IsConfigByUser: true,
			Type:           define.MetaFieldTypeString,
			FieldName:      "bk_cloud_id",
			Tag:            define.MetaFieldTagDimension,
		},
	)

	schema, err := etl.NewSchema(s.CTX)
	s.NoError(err)
	processor := etl.NewRecordProcessor("x", s.PipelineConfig, schema)

	s.Run(`{"time": 1574233401, "key": "x", "value": 1, "ip": "127.0.0.1", "cloudid": 0}`, processor, func(result map[string]interface{}) {
		s.EqualRecord(result, map[string]interface{}{
			"time": 1574233401,
			"dimensions": map[string]interface{}{
				"key":         "x",
				"ip":          "127.0.0.1",
				"bk_cloud_id": "0",
			},
			"metrics": map[string]interface{}{
				"value": "1",
			},
		})
	})
}

// TestPreparePlugin
func (s *ProcessorSuite) TestPreparePlugin() {
	s.Stubs.Stub(&s.ResultTableConfig.Option, map[string]interface{}{
		config.ResultTableOptSeparatorAction:     "json",
		config.ResultTableOptSeparatorNodeSource: "value",
		config.ResultTableOptSeparatorNode:       "x_value",
	})
	s.ResultTableConfig.FieldList = append(s.ResultTableConfig.FieldList, &config.MetaFieldConfig{
		IsConfigByUser: true,
		Type:           define.MetaFieldTypeString,
		FieldName:      "data",
		Tag:            define.MetaFieldTagMetric,
		Option: map[string]interface{}{
			config.MetaFieldOptRealPath: "x_value.data",
		},
	})

	schema, err := etl.NewSchema(s.CTX)
	s.NoError(err)
	processor := etl.NewRecordProcessor("x", s.PipelineConfig, schema)

	s.Run(`{"time": 1574233401, "key": "x", "value": "{\"data\": 1}", "ip": "127.0.0.1", "cloudid": 0}`, processor, func(result map[string]interface{}) {
		s.EqualRecord(result, map[string]interface{}{
			"time": 1574233401,
			"dimensions": map[string]interface{}{
				"key": "x",
			},
			"metrics": map[string]interface{}{
				"value": "{\"data\": 1}",
				"data":  "1",
			},
		})
	})
}

// TestProcessorSuite
func TestProcessorSuite(t *testing.T) {
	suite.Run(t, new(ProcessorSuite))
}
