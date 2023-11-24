// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline

// 用于处理时序流水线

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
)

// StandardV2TSConfigBuilder
type StandardV2TSConfigBuilder struct {
	*ConfigBuilder
}

func (b *StandardV2TSConfigBuilder) getStandardProcessors() []string {
	processors := []string{
		"timeseries_v2_pre",
		"metrics_reporter",
	}

	return processors
}

// ConnectStandardNodesByETLName
func (b *StandardV2TSConfigBuilder) ConnectStandardNodesByETLName(ctx context.Context, from Node, to Node) error {
	nodes := []Node{from}
	standards, err := b.GetDataProcessors(ctx, b.getStandardProcessors()...)
	if err != nil {
		return err
	}

	nodes = append(nodes, standards...)
	nodes = append(nodes, to)
	b.ConnectNodes(nodes...)
	return nil
}

// NewStandardV2TSConfigBuilder
func NewStandardV2TSConfigBuilder(ctx context.Context, name string) (*StandardV2TSConfigBuilder, error) {
	builder := NewConfigBuilder(ctx, name)
	builder.PipeConfigInitFn = config.InitTSV2PipelineOptions
	builder.TableConfigInitFn = config.InitTSV2ResultTableOptions

	return &StandardV2TSConfigBuilder{
		ConfigBuilder: builder,
	}, nil
}
