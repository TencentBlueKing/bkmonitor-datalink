// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline

import (
	"golang.org/x/net/context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
)

// 故障自愈事件清洗流水线 builder
type FTABuilder struct {
	*ConfigBuilder
}

// GetStandardProcessors
func (b *FTABuilder) GetStandardProcessors(_ string, _ *config.PipelineConfig, _ *config.MetaResultTableConfig) []string {
	processors := []string{
		"fta-flat",
		"fta-alert",
		"fta-map",
	}

	return processors
}

// ConnectStandardNodesByETLName
func (b *FTABuilder) ConnectStandardNodesByETLName(ctx context.Context, name string, from Node, to Node) error {
	nodes := []Node{from}

	pipe := config.PipelineConfigFromContext(ctx)
	rt := config.ResultTableConfigFromContext(ctx)

	standards, err := b.GetDataProcessors(ctx, b.GetStandardProcessors(name, pipe, rt)...)
	if err != nil {
		return err
	}

	nodes = append(nodes, standards...)
	nodes = append(nodes, to)
	b.ConnectNodes(nodes...)
	return nil
}

// BuildStandardBranching:  对于一个data_source存在多个resultTable的处理
func (b *FTABuilder) BuildStandardBranchingByETLName(etl string) (*Pipeline, error) {
	return b.BuildBranching(nil, false, func(ctx context.Context, from Node, to Node) error {
		return b.ConnectStandardNodesByETLName(ctx, etl, from, to)
	})
}

// NewFTAConfigBuilder
func NewFTAConfigBuilder(ctx context.Context, name string) (*FTABuilder, error) {
	builder := NewConfigBuilder(ctx, name)
	builder.PipeConfigInitFn = config.InitFTAPipelineOptions
	builder.TableConfigInitFn = config.InitFTAResultTableOptions

	return &FTABuilder{
		ConfigBuilder: builder,
	}, nil
}
