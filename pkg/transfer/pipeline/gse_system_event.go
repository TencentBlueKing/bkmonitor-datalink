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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
)

// 用于处理gse事件格式

type GseSystemEventConfigBuilder struct {
	*ConfigBuilder
}

func (b *GseSystemEventConfigBuilder) GetStandardProcessors(_ string, _ *config.PipelineConfig, _ *config.MetaResultTableConfig) []string {
	processors := []string{"gse_system_event", "event_v2_standard", "event_v2_handler"}
	return processors
}

func (b *GseSystemEventConfigBuilder) ConnectStandardNodesByETLName(ctx context.Context, name string, from Node, to Node) error {
	nodes := []Node{from}

	pipe := config.PipelineConfigFromContext(ctx)
	rt := config.ResultTableConfigFromContext(ctx)

	// 事件内容强制要求为动态类型
	if rt.SchemaType != config.ResultTableSchemaTypeFree {
		return errors.WithMessagef(define.ErrOperationForbidden, "event schema should be free")
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

func (b *GseSystemEventConfigBuilder) BuildStandardBranchingByETLName(etl string) (*Pipeline, error) {
	return b.BuildBranching(nil, false, func(ctx context.Context, from Node, to Node) error {
		return b.ConnectStandardNodesByETLName(ctx, etl, from, to)
	})
}

// NewGseSystemEventConfigBuilder 创建gse事件builder
func NewGseSystemEventConfigBuilder(ctx context.Context, name string) (*GseSystemEventConfigBuilder, error) {
	builder := NewConfigBuilder(ctx, name)
	builder.PipeConfigInitFn = config.InitEventPipelineOption
	builder.TableConfigInitFn = config.InitEventResultTableOption
	return &GseSystemEventConfigBuilder{
		ConfigBuilder: builder,
	}, nil
}
