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
	"fmt"

	"github.com/emirpasic/gods/sets/hashset"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// TSConfigBuilder
type TSConfigBuilder struct {
	*ConfigBuilder
}

// GetStandardPrepareProcessors
func (b *TSConfigBuilder) GetStandardPrepareProcessors(pipe *config.PipelineConfig, rt *config.MetaResultTableConfig) []string {
	processors := make([]string, 0)
	option := utils.NewMapHelper(pipe.Option)
	rtOption := utils.NewMapHelper(rt.Option)
	// 加入cmdb_level 节点 且 未配置拆分结构
	if rtOption.GetOrDefault(config.PipelineConfigOptEnableDimensionCmdbLevel, true) == true && len(rtOption.GetOrDefault(config.ResultTableListConfigOptMetricSplitLevel, []interface{}{}).([]interface{})) == 0 {
		processors = append(processors, "cmdb_injector")
	}

	if option.GetOrDefault(config.PipelineConfigOptEnableDimensionGroup, true) == true {
		processors = append(processors, "group_injector")
	}
	if option.GetOrDefault(config.PipelineConfigOptUseSourceTime, true) == false {
		processors = append(processors, "time_injector")
	}
	if rt.SchemaType == config.ResultTableSchemaTypeFree && rtOption.GetOrDefault(config.ResultTableOptSchemaDiscovery, false) == true {
		processors = append(processors, "sampling_reporter")
	}
	if rtOption.GetOrDefault(config.ResultTableOptEnableBlackList, false) == true {
		processors = append(processors, "metrics_reporter")
	}

	return processors
}

// GetStandardShipperProcessors
func (b *TSConfigBuilder) GetStandardShipperProcessors(pipe *config.PipelineConfig, rt *config.MetaResultTableConfig) []string {
	processors := make([]string, 0)

	if rt != nil && rt.ResultTable != "" {
		processors = append(processors, "ts_format")
	}

	return processors
}

// GetStandardProcessors
func (b *TSConfigBuilder) GetStandardProcessors(etl string, pipe *config.PipelineConfig, rt *config.MetaResultTableConfig, frontNode ...string) []string {
	processors := make([]string, 0)

	helper := utils.NewMapHelper(pipe.Option)
	encoding, ok := helper.GetString(config.PipelineConfigOptPayloadEncoding)
	if ok && encoding != "" {
		processors = append(processors, "encoding")
	}

	for _, node := range frontNode {
		processors = append(processors, node)
	}

	processors = append(processors, etl)
	processors = append(processors, b.GetStandardPrepareProcessors(pipe, rt)...)
	processors = append(processors, b.GetStandardShipperProcessors(pipe, rt)...)

	return processors
}

// ConnectStandardNodesByETLName
func (b *TSConfigBuilder) ConnectStandardNodesByETLName(ctx context.Context, name string, from Node, to Node, frontNode ...string) error {
	nodes := []Node{from}

	pipe := config.PipelineConfigFromContext(ctx)
	rt := config.ResultTableConfigFromContext(ctx)
	standards, err := b.GetDataProcessors(ctx, b.GetStandardProcessors(name, pipe, rt, frontNode...)...)
	if err != nil {
		return err
	}

	nodes = append(nodes, standards...)
	nodes = append(nodes, to)
	b.ConnectNodes(nodes...)
	return nil
}

// ConnectStandardNodes
func (b *TSConfigBuilder) ConnectStandardNodes(ctx context.Context, from Node, to Node) error {
	rt := config.ResultTableConfigFromContext(ctx)
	return b.ConnectStandardNodesByETLName(ctx, rt.MappingResultTable(), from, to)
}

// BuildBranchingFor
func (b *TSConfigBuilder) BuildBranchingFor(table ...string) (*Pipeline, error) {
	tables := hashset.New()
	for _, name := range table {
		tables.Add(name)
	}

	return b.BuildBranchingWithGluttonous(nil, func(ctx context.Context, from Node, to Node) error {
		rt := config.ResultTableConfigFromContext(ctx)
		if !tables.Contains(rt.MappingResultTable()) {
			return fmt.Errorf("%v not supported", rt.MappingResultTable())
		}
		return b.ConnectStandardNodes(ctx, from, to)
	})
}

// NewTSConfigBuilder
func NewTSConfigBuilder(ctx context.Context, name string) (*TSConfigBuilder, error) {
	builder := NewConfigBuilder(ctx, name)
	builder.PipeConfigInitFn = config.InitTSPipelineOptions
	builder.TableConfigInitFn = config.InitTSResultTableOptions

	return &TSConfigBuilder{
		ConfigBuilder: builder,
	}, nil
}
