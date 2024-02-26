// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package builder

import (
	"github.com/google/pprof/profile"
	"github.com/grafana/jfr-parser/parser/types"
)

type ProfileBuilder struct {
	*profile.Profile

	visitedSampleIdMapping   map[types.StackTraceRef]*profile.Sample
	visitedFunctionIdMapping map[types.MethodRef]*profile.Location
}

func (p *ProfileBuilder) FindExternalSample(stacktraceId types.StackTraceRef) *profile.Sample {
	return p.visitedSampleIdMapping[stacktraceId]
}

func (p *ProfileBuilder) FindLocationId(functionId types.MethodRef) (*profile.Location, bool) {
	r, o := p.visitedFunctionIdMapping[functionId]
	return r, o
}

func (p *ProfileBuilder) AddExternalFunction(frameName string, functionRef types.MethodRef) *profile.Location {
	functionId := len(p.Profile.Function) + 1
	f := &profile.Function{
		ID:   uint64(functionId),
		Name: frameName,
	}
	p.Profile.Function = append(p.Profile.Function, f)

	loc := &profile.Location{
		ID:   uint64(len(p.Profile.Location) + 1),
		Line: []profile.Line{{Function: f}},
	}
	p.Profile.Location = append(p.Profile.Location, loc)
	p.visitedFunctionIdMapping[functionRef] = loc
	return loc
}

func (p *ProfileBuilder) AddExternalSample(locations []*profile.Location, value []int64, stacktraceRef types.StackTraceRef) {
	sample := &profile.Sample{
		Location: locations,
		Value:    value,
	}
	p.visitedSampleIdMapping[stacktraceRef] = sample
	p.Profile.Sample = append(p.Profile.Sample, sample)
}

func (p *ProfileBuilder) AddSampleType(typ, unit string) {
	p.Profile.SampleType = append(p.Profile.SampleType, &profile.ValueType{
		Type: typ,
		Unit: unit,
	})
}

func (p *ProfileBuilder) AddPeriodType(typ, unit string) {
	p.Profile.PeriodType = &profile.ValueType{
		Type: typ,
		Unit: unit,
	}
}

func NewProfileBuilder() *ProfileBuilder {
	return &ProfileBuilder{
		Profile: &profile.Profile{},

		visitedSampleIdMapping:   make(map[types.StackTraceRef]*profile.Sample),
		visitedFunctionIdMapping: make(map[types.MethodRef]*profile.Location),
	}
}
