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

// FTABuilder 故障自愈事件清洗流水线 builder
type FTABuilder struct {
	*ConfigBuilder
}

// ConnectStandardNodesByETLName 根据 ETL 名称连接标准节点
func (b *FTABuilder) ConnectStandardNodesByETLName(ctx context.Context, name string, from Node, to Node) error {
	nodes := []Node{from}

	standards, err := b.GetDataProcessors(ctx, "flat-batch", "fta-alert")
	if err != nil {
		return err
	}

	nodes = append(nodes, standards...)
	nodes = append(nodes, to)
	b.ConnectNodes(nodes...)
	return nil
}

// BuildStandardBranchingByETLName 根据 ETL 名称构建标准分支流水线
func (b *FTABuilder) BuildStandardBranchingByETLName(etl string) (*Pipeline, error) {
	return b.BuildBranching(nil, false, func(ctx context.Context, from Node, to Node) error {
		return b.ConnectStandardNodesByETLName(ctx, etl, from, to)
	})
}

// NewFTAConfigBuilder 创建故障自愈事件清洗流水线 builder
func NewFTAConfigBuilder(ctx context.Context, name string) (*FTABuilder, error) {
	builder := NewConfigBuilder(ctx, name)
	builder.PipeConfigInitFn = config.InitFTAPipelineOptions
	builder.TableConfigInitFn = config.InitFTAResultTableOptions

	return &FTABuilder{ConfigBuilder: builder}, nil
}
