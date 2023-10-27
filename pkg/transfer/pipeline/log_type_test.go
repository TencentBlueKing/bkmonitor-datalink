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

// LogConfigBuilderSuite
type LogConfigBuilderSuite struct {
	BuilderSuite
}

// TestGetStandardProcessors
func (s *LogConfigBuilderSuite) TestGetStandardProcessors() {
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
			[]string{"log_format"},
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
		builder, err := pipeline.NewLogConfigBuilder(s.CTX, "")
		s.NoError(err)
		for _, n := range builder.GetStandardProcessors("", &c.pipe, &c.table) {
			result[n] = true
		}
		for _, node := range c.exists {
			s.True(result[node], "%d:%s", i, node)
		}
		for _, node := range c.missing {
			s.False(result[node], "%d:%s", i, node)
		}
	}
}

// TestLogConfigBuilderSuite
func TestLogConfigBuilderSuite(t *testing.T) {
	suite.Run(t, new(LogConfigBuilderSuite))
}
