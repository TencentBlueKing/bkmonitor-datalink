// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package testsuite

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

// ConfigSuite :
type ConfigSuite struct {
	ContextSuite
	Config            define.Configuration
	ResultTableConfig *config.MetaResultTableConfig
	PipelineConfig    *config.PipelineConfig
	MQConfig          *config.MetaClusterInfo
	ShipperConfig     *config.MetaClusterInfo
}

// SetupTest :
func (s *ConfigSuite) SetupTest() {
	s.ContextSuite.SetupTest()

	if s.Config == nil {
		s.Config = config.NewConfiguration()
	}
	ctx := config.IntoContext(s.CTX, s.Config)

	if s.ResultTableConfig == nil {
		s.ResultTableConfig = &config.MetaResultTableConfig{
			ResultTable: "test.table",
			FieldList: []*config.MetaFieldConfig{
				{
					FieldName: define.RecordBizIDFieldName,
				},
			},
		}
	}

	if s.PipelineConfig == nil {
		s.PipelineConfig = config.NewPipelineConfig()
		s.PipelineConfig.ETLConfig = "test"
		s.PipelineConfig.DataID = 1
		s.PipelineConfig.ResultTableList = append(s.PipelineConfig.ResultTableList, s.ResultTableConfig)
		s.PipelineConfig.Option = map[string]interface{}{}
	} else if len(s.PipelineConfig.ResultTableList) > 0 {
		s.ResultTableConfig = s.PipelineConfig.ResultTableList[0]
	}
	s.NoError(s.PipelineConfig.Clean())
	if s.ResultTableConfig.ShipperList == nil {
		s.ShipperConfig = &config.MetaClusterInfo{
			ClusterType:   "test",
			ClusterConfig: map[string]interface{}{},
			StorageConfig: map[string]interface{}{},
			AuthInfo:      map[string]interface{}{},
		}
		s.ResultTableConfig.ShipperList = []*config.MetaClusterInfo{s.ShipperConfig}
	}

	s.MQConfig = s.PipelineConfig.MQConfig
	ctx = config.ResultTableConfigIntoContext(ctx, s.ResultTableConfig)
	ctx = config.PipelineConfigIntoContext(ctx, s.PipelineConfig)
	ctx = config.MQConfigIntoContext(ctx, s.MQConfig)
	ctx = config.ShipperConfigIntoContext(ctx, s.ShipperConfig)

	s.CTX = ctx
}
