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
)

// 用于处理GSE自定义字符串

type GseCustomStringConfigBuilder struct {
	*ConfigBuilder
}

// ConnectStandardNodesByETLName 连接标准节点
func (b *GseCustomStringConfigBuilder) ConnectStandardNodesByETLName(ctx context.Context, from Node, to Node) error {
	nodes := []Node{from}
	standards, err := b.GetDataProcessors(ctx, "gse_custom_string", "event_v2_standard", "event_v2_handler")
	if err != nil {
		return err
	}

	nodes = append(nodes, standards...)
	nodes = append(nodes, to)
	b.ConnectNodes(nodes...)
	return nil
}

// NewGseCustomStringConfigBuilder 创建GSE自定义字符串builder
func NewGseCustomStringConfigBuilder(ctx context.Context, name string) (*GseCustomStringConfigBuilder, error) {
	builder := NewConfigBuilder(ctx, name)
	builder.PipeConfigInitFn = config.InitTSV2PipelineOptions
	builder.TableConfigInitFn = config.InitTSV2ResultTableOptions

	return &GseCustomStringConfigBuilder{
		ConfigBuilder: builder,
	}, nil
}
