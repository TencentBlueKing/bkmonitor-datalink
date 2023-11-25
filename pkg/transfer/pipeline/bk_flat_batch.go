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
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

type FlatBatchConfigBuilder struct {
	*ConfigBuilder
}

func (b *FlatBatchConfigBuilder) GetStandardProcessors(pipe *config.PipelineConfig, rt *config.MetaResultTableConfig) []string {
	processors := make([]string, 0)

	helper := utils.NewMapHelper(pipe.Option)
	encoding, ok := helper.GetString(config.PipelineConfigOptPayloadEncoding)
	if ok && encoding != "" {
		processors = append(processors, "encoding")
	}

	processors = append(processors, "flat_batch_handler")
	if rt != nil && rt.ResultTable != "" {
		processors = append(processors, "ts_format")
	}

	return processors
}

func (b *FlatBatchConfigBuilder) ConnectStandardNodesByETLName(ctx context.Context, from Node, to Node) error {
	nodes := []Node{from}

	pipe := config.PipelineConfigFromContext(ctx)
	rt := config.ResultTableConfigFromContext(ctx)
	standards, err := b.GetDataProcessors(ctx, b.GetStandardProcessors(pipe, rt)...)
	if err != nil {
		return err
	}

	nodes = append(nodes, standards...)
	nodes = append(nodes, to)
	b.ConnectNodes(nodes...)
	return nil
}

func NewFlatBatchConfigBuilder(ctx context.Context, name string) (*FlatBatchConfigBuilder, error) {
	builder := NewConfigBuilder(ctx, name)
	builder.PipeConfigInitFn = config.InitTSPipelineOptions
	builder.TableConfigInitFn = config.InitTSResultTableOptions

	return &FlatBatchConfigBuilder{
		ConfigBuilder: builder,
	}, nil
}
