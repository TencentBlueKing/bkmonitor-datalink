// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline

// 用于处理日志流水线

import (
	"context"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// LogConfigBuilder
type LogConfigBuilder struct {
	*ConfigBuilder
}

// GetStandardProcessors
func (b *LogConfigBuilder) GetStandardProcessors(etl string, pipe *config.PipelineConfig, rt *config.MetaResultTableConfig) []string {
	processors := make([]string, 0)

	helper := utils.NewMapHelper(pipe.Option)
	encoding, ok := helper.GetString(config.PipelineConfigOptPayloadEncoding)
	if ok && encoding != "" {
		processors = append(processors, "encoding")
	}

	processors = append(processors, etl, "log_format")

	return processors
}

// ConnectStandardNodesByETLName
func (b *LogConfigBuilder) ConnectStandardNodesByETLName(ctx context.Context, name string, from Node, to Node) error {
	nodes := []Node{from}

	pipe := config.PipelineConfigFromContext(ctx)
	rt := config.ResultTableConfigFromContext(ctx)
	// 日志结构强制要求为动态类型
	if rt.SchemaType != config.ResultTableSchemaTypeFree {
		return errors.WithMessagef(define.ErrOperationForbidden, "log schema should be free")
	}

	standards, err := b.GetDataProcessors(ctx, b.GetStandardProcessors(name, pipe, rt)...)
	if err != nil {
		return err
	}

	nodes = append(nodes, standards...)
	nodes = append(nodes, to)
	b.ConnectNodes(nodes...)
	return nil
}

// BuildStandardBranching
func (b *LogConfigBuilder) BuildStandardBranchingByETLName(etl string) (*Pipeline, error) {
	return b.BuildBranching(nil, false, func(ctx context.Context, from Node, to Node) error {
		return b.ConnectStandardNodesByETLName(ctx, etl, from, to)
	})
}

// NewLogConfigBuilder
func NewLogConfigBuilder(ctx context.Context, name string) (*LogConfigBuilder, error) {
	builder := NewConfigBuilder(ctx, name)
	builder.PipeConfigInitFn = config.InitLogPipelineOptions
	builder.TableConfigInitFn = config.InitLogResultTableOptions

	return &LogConfigBuilder{
		ConfigBuilder: builder,
	}, nil
}
