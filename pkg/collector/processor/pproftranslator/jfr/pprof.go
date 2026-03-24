// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package jfr

import (
	"fmt"

	"github.com/google/pprof/profile"
	"github.com/grafana/jfr-parser/parser"
	"github.com/grafana/jfr-parser/parser/types"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

type jfrPprofBuilder struct {
	builders      map[int64]*ProfileBuilder
	timeNanos     int64
	durationNanos int64
	jfrLabels     *LabelsSnapshot

	samplerPeriod int64
	parser        *parser.Parser
}

func (j *jfrPprofBuilder) addStacktrace(
	sampleType int64,
	correlation StacktraceCorrelation,
	ref types.StackTraceRef,
	values []int64,
) {
	p := j.profileBuilderForSampleType(sampleType)
	stacktrace := j.parser.GetStacktrace(ref)
	if stacktrace == nil {
		return
	}

	addValues := func(dst []int64) {
		mul := 1
		if sampleType == sampleTypeCPU || sampleType == sampleTypeWall {
			mul = int(j.samplerPeriod)
		}
		for i, value := range values {
			dst[i] += value * int64(mul)
		}
	}

	visitedSample := p.FindExternalSample(ref)
	if visitedSample != nil {
		addValues(visitedSample.Value)
		return
	}

	locations := make([]*profile.Location, 0, len(stacktrace.Frames))
	for _, frame := range stacktrace.Frames {
		locationId, exist := p.FindLocationId(frame.Method)
		if exist {
			locations = append(locations, locationId)
			continue
		}

		method := j.parser.GetMethod(frame.Method)
		if method == nil {
			continue
		}

		clz := j.parser.GetClass(method.Type)
		if clz == nil {
			continue
		}

		locations = append(
			locations, p.AddExternalFunction(
				fmt.Sprintf("%s.%s", j.parser.GetSymbolString(clz.Name), j.parser.GetSymbolString(method.Name)),
				frame.Method),
		)
	}

	vs := make([]int64, len(values))
	addValues(vs)
	p.AddExternalSampleWithLabels(locations, vs, ref, j.contextLabels(correlation.ContextId), j.jfrLabels, correlation)
}

func (j *jfrPprofBuilder) profileBuilderForSampleType(sampleType int64) *ProfileBuilder {
	if build, ok := j.builders[sampleType]; ok {
		return build
	}

	newBuilder := NewProfileBuilder()
	newBuilder.TimeNanos = j.timeNanos
	newBuilder.DurationNanos = j.durationNanos
	switch sampleType {
	case sampleTypeCPU:
		newBuilder.AddSampleType("cpu", "nanoseconds")
		newBuilder.AddPeriodType("cpu", "nanoseconds")

	case sampleTypeWall:
		newBuilder.AddSampleType("wall", "nanoseconds")
		newBuilder.AddPeriodType("wall", "nanoseconds")

	case sampleTypeInTLAB:
		newBuilder.AddSampleType("alloc_in_new_tlab_objects", "count")
		newBuilder.AddSampleType("alloc_in_new_tlab_bytes", "bytes")
		newBuilder.AddPeriodType("space", "bytes")

	case sampleTypeOutTLAB:
		newBuilder.AddSampleType("alloc_outside_tlab_objects", "count")
		newBuilder.AddSampleType("alloc_outside_tlab_bytes", "bytes")
		newBuilder.AddPeriodType("space", "bytes")

	case sampleTypeLock:
		newBuilder.AddSampleType("contentions", "count")
		newBuilder.AddSampleType("delay", "nanoseconds")
		newBuilder.AddPeriodType("mutex", "count")

	case sampleTypeThreadPark:
		newBuilder.AddSampleType("contentions", "count")
		newBuilder.AddSampleType("delay", "nanoseconds")
		newBuilder.AddPeriodType("block", "count")

	case sampleTypeLiveObject:
		newBuilder.AddSampleType("live", "count")
		newBuilder.AddPeriodType("objects", "count")

	case sampleTypeAllocSample:
		newBuilder.AddSampleType("alloc_sample_objects", "count")
		newBuilder.AddSampleType("alloc_sample_bytes", "bytes")
		newBuilder.AddPeriodType("space", "bytes")

	case sampleTypeMalloc:
		newBuilder.AddSampleType("malloc_objects", "count")
		newBuilder.AddSampleType("malloc_bytes", "bytes")
	}
	j.builders[sampleType] = newBuilder
	return newBuilder
}

func (j *jfrPprofBuilder) contextLabels(contextID uint64) *Context {
	if j.jfrLabels == nil {
		return nil
	}
	return j.jfrLabels.Contexts[int64(contextID)]
}

func (j *jfrPprofBuilder) build() []*profile.Profile {
	profiles := make([]*profile.Profile, 0, len(j.builders))
	for _, build := range j.builders {
		profiles = append(profiles, build.Profile)
	}
	return profiles
}

func newJfrPprofBuilders(p *parser.Parser, jfrLabels *LabelsSnapshot, m define.ProfileMetadata) *jfrPprofBuilder {
	st := m.StartTime.UnixNano()
	et := m.EndTime.UnixNano()
	var period int64
	if m.SampleRate == 0 {
		period = 0
	} else {
		period = 1e9 / int64(m.SampleRate)
	}

	return &jfrPprofBuilder{
		builders:      make(map[int64]*ProfileBuilder),
		timeNanos:     st,
		durationNanos: et - st,
		jfrLabels:     jfrLabels,
		samplerPeriod: period,
		parser:        p,
	}
}
