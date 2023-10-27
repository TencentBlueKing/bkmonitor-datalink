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
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
)

// TSConfigBuilderSuite
type TSConfigBuilderSuite struct {
	BuilderSuite
}

// TestGetStandardProcessors
func (s *TSConfigBuilderSuite) TestGetStandardProcessors() {
	stdPipe := config.PipelineConfig{
		Option:          map[string]interface{}{},
		ResultTableList: []*config.MetaResultTableConfig{},
	}

	stdTable := config.MetaResultTableConfig{
		ResultTable: "test",
		SchemaType:  config.ResultTableSchemaTypeFree,
		ShipperList: []*config.MetaClusterInfo{},
		FieldList:   []*config.MetaFieldConfig{},
	}

	cases := []struct {
		pipe    config.PipelineConfig
		table   config.MetaResultTableConfig
		exists  []string
		missing []string
	}{
		{
			stdPipe, stdTable,
			[]string{"group_injector", "ts_format"},
			[]string{"time_injector"},
		},
		{
			config.PipelineConfig{Option: map[string]interface{}{
				config.PipelineConfigOptEnableDimensionGroup: true,
			}},
			stdTable,
			[]string{"group_injector"},
			[]string{},
		},
		{
			config.PipelineConfig{Option: map[string]interface{}{
				config.PipelineConfigOptEnableDimensionGroup: false,
			}},
			stdTable,
			[]string{},
			[]string{"group_injector"},
		},
		{
			config.PipelineConfig{Option: map[string]interface{}{
				config.PipelineConfigOptUseSourceTime: false,
			}},
			stdTable,
			[]string{"time_injector"},
			[]string{},
		},
		{
			config.PipelineConfig{Option: map[string]interface{}{
				config.PipelineConfigOptUseSourceTime: true,
			}},
			stdTable,
			[]string{},
			[]string{"time_injector"},
		},
		{
			stdPipe,
			config.MetaResultTableConfig{Option: map[string]interface{}{
				config.ResultTableOptSchemaDiscovery: true,
			}},
			[]string{},
			[]string{"sampling_reporter"},
		},
		{
			stdPipe,
			config.MetaResultTableConfig{Option: map[string]interface{}{
				config.ResultTableOptSchemaDiscovery: true,
			}, SchemaType: config.ResultTableSchemaTypeFixed},
			[]string{},
			[]string{"sampling_reporter"},
		},
		{
			stdPipe,
			config.MetaResultTableConfig{Option: map[string]interface{}{
				config.ResultTableOptSchemaDiscovery: true,
			}, SchemaType: config.ResultTableSchemaTypeFree},
			[]string{"sampling_reporter"},
			[]string{},
		},
		{
			stdPipe,
			config.MetaResultTableConfig{Option: map[string]interface{}{
				config.ResultTableOptSchemaDiscovery: false,
			}, SchemaType: config.ResultTableSchemaTypeFree},
			[]string{},
			[]string{"sampling_reporter"},
		},
		{
			stdPipe, stdTable,
			[]string{"ts_format"},
			[]string{},
		},
		{
			config.PipelineConfig{Option: map[string]interface{}{
				config.PipelineConfigOptPayloadEncoding: "gbk",
			}},
			stdTable,
			[]string{"encoding"},
			[]string{},
		},
		{
			config.PipelineConfig{Option: map[string]interface{}{
				config.PipelineConfigOptPayloadEncoding: "",
			}},
			stdTable,
			[]string{},
			[]string{"encoding"},
		},
		{
			stdPipe, stdTable,
			[]string{},
			[]string{"encoding"},
		},
	}

	for i, c := range cases {
		result := make(map[string]bool, len(c.exists))
		builder, err := pipeline.NewTSConfigBuilder(s.CTX, "")
		s.NoError(err)
		for _, n := range builder.GetStandardProcessors("", &c.pipe, &c.table) {
			result[n] = true
		}
		for _, node := range c.exists {
			s.True(result[node], "exists for %d:%s", i, node)
		}
		for _, node := range c.missing {
			s.False(result[node], "missing for %d:%s", i, node)
		}
	}
}

// TestTSConfigBuilderSuite
func TestTSConfigBuilderSuite(t *testing.T) {
	suite.Run(t, new(TSConfigBuilderSuite))
}
