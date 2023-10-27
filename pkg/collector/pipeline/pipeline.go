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
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor"
)

// Pipeline 流水线接口定义
type Pipeline interface {
	// Name 流水线名称
	Name() string

	// RecordType 流水线数据类型
	RecordType() define.RecordType

	// AllProcessors 返回所有 Processor
	AllProcessors() []string

	// PreCheckProcessors 返回所有 PreCheck 类型 Processor
	PreCheckProcessors() []string

	// SchedProcessors 返回所有调度类型 Processor
	SchedProcessors() []string

	// Validate 流水线配置校验
	Validate() bool
}

type pipeline struct {
	name       string
	recordType define.RecordType
	processors []processor.Instance
}

func NewPipeline(name string, rtype define.RecordType, ps ...processor.Instance) Pipeline {
	return &pipeline{
		name:       name,
		recordType: rtype,
		processors: ps,
	}
}

func (p *pipeline) Name() string                  { return p.name }
func (p *pipeline) RecordType() define.RecordType { return p.recordType }

func (p *pipeline) String() string {
	return fmt.Sprintf("Name=%s, RecordType=%v, Processors=%v", p.Name(), p.RecordType(), p.AllProcessors())
}

func (p *pipeline) PreCheckProcessors() []string {
	ps := make([]string, 0, len(p.processors))
	for _, v := range p.processors {
		if v.IsPreCheck() {
			ps = append(ps, v.ID())
		}
	}
	return ps
}

func (p *pipeline) SchedProcessors() []string {
	ps := make([]string, 0, len(p.processors))
	for _, v := range p.processors {
		if !v.IsPreCheck() {
			ps = append(ps, v.ID())
		}
	}
	return ps
}

func (p *pipeline) AllProcessors() []string {
	ps := make([]string, 0, len(p.processors))
	for _, v := range p.processors {
		ps = append(ps, v.ID())
	}
	return ps
}

func (p *pipeline) Validate() bool {
	var schedProcessorsIndex []int
	var preCheckProcessorsIndex []int

	for i, v := range p.processors {
		if v.IsPreCheck() {
			preCheckProcessorsIndex = append(preCheckProcessorsIndex, i)
			continue
		}
		schedProcessorsIndex = append(schedProcessorsIndex, i)
	}

	for _, i := range schedProcessorsIndex {
		for _, j := range preCheckProcessorsIndex {
			if i < j {
				return false
			}
		}
	}
	return true
}
