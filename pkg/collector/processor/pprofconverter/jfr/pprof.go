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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/pprofconverter/builder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/pprofconverter/models"
)

type jfrPprofBuilder struct {
	startTime       int64
	endTime         int64
	labelMapping    map[uint64]models.Labels
	buildersMapping models.LabelsCache[builder.ProfileBuilder]
	jfrLabels       *LabelsSnapshot

	samplerPeriod int64
	parser        *parser.Parser
}

func (j *jfrPprofBuilder) addStacktrace(sampleType int64, contextID uint64, ref types.StackTraceRef, values []int64) {
	// Step1: 获取事件对应的Label信息
	stacktrace := j.parser.GetStacktrace(ref)
	if stacktrace == nil {
		return
	}

	contextIdLabels := j.getLabels(contextID)
	// Step2: 根据时间ContextId获取ProfileBuilder
	pb := j.buildersMapping.GetOrCreate(sampleType, contextIdLabels)

	var factor int64 = 1
	if sampleType == sampleTypeCPU || sampleType == sampleTypeWall {
		factor = j.samplerPeriod
	}
	addValues := func(d []int64) {
		for i, value := range values {
			d[i] += value * factor
		}
	}

	visitedSample := pb.Value.FindExternalSample(ref)
	if visitedSample != nil {
		addValues(visitedSample.Value)
		return
	}

	locations := make([]*profile.Location, 0, len(stacktrace.Frames))

	// Step3: 逐个解析堆栈
	for _, frame := range stacktrace.Frames {
		locationId, exist := pb.Value.FindLocationId(frame.Method)
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
			locations, pb.Value.AddExternalFunction(
				fmt.Sprintf("%s.%s", j.parser.GetSymbolString(clz.Name), j.parser.GetSymbolString(method.Name)),
				frame.Method),
		)
	}

	vs := make([]int64, len(values))
	addValues(vs)
	pb.Value.AddExternalSample(locations, vs, ref)
}

func (j *jfrPprofBuilder) getLabels(contextId uint64) models.Labels {

	res, exist := j.labelMapping[contextId]
	if exist {
		return res
	}

	labels, success := j.getLabelsFromSnapshot(contextId)
	if !success {
		return models.NewLabels(nil)
	}
	j.labelMapping[contextId] = labels
	return labels
}

func (j *jfrPprofBuilder) getLabelsFromSnapshot(contextId uint64) (models.Labels, bool) {
	if contextId == 0 {
		return models.Labels{}, false
	}
	ctx, exist := j.jfrLabels.Contexts[int64(contextId)]
	if !exist {
		return models.Labels{}, false
	}
	var res []*models.Label
	for k, v := range ctx.Labels {
		res = append(res, &models.Label{Key: k, Value: v})
	}
	return models.NewLabels(res), true
}

func (j *jfrPprofBuilder) build() []*profile.Profile {
	res := make([]*profile.Profile, 0, len(j.buildersMapping.Map))

	for sampleType, entries := range j.buildersMapping.Map {
		for _, pb := range entries {
			pb.Value.TimeNanos = j.startTime
			pb.Value.DurationNanos = j.endTime - j.startTime
			switch sampleType {
			case sampleTypeCPU:
				pb.Value.AddSampleType(TypeCpu, UnitNanoseconds)
				pb.Value.AddPeriodType(TypeCpu, UnitNanoseconds)
			case sampleTypeWall:
				pb.Value.AddSampleType(TypeWall, UnitNanoseconds)
				pb.Value.AddPeriodType(TypeWall, UnitNanoseconds)
			case sampleTypeInTLAB:
				pb.Value.AddSampleType(TypeInTlabObjects, UnitCount)
				pb.Value.AddSampleType(TypeInTlabBytes, UnitBytes)
				pb.Value.AddPeriodType(TypeSpace, UnitBytes)
			case sampleTypeOutTLAB:
				pb.Value.AddSampleType(TypeOutTlabObjects, UnitCount)
				pb.Value.AddSampleType(TypeOutTlabBytes, UnitBytes)
				pb.Value.AddPeriodType(TypeSpace, UnitBytes)
			case sampleTypeLock:
				pb.Value.AddSampleType(TypeContentions, UnitCount)
				pb.Value.AddSampleType(TypeDelay, UnitNanoseconds)
				pb.Value.AddPeriodType(TypeMutex, UnitCount)
			case sampleTypeThreadPark:
				pb.Value.AddSampleType(TypeContentions, UnitCount)
				pb.Value.AddSampleType(TypeDelay, UnitNanoseconds)
				pb.Value.AddPeriodType(TypeBlock, UnitCount)
			case sampleTypeLiveObject:
				pb.Value.AddSampleType(TypeLive, UnitCount)
				pb.Value.AddPeriodType(TypeObjects, UnitCount)
			}
			res = append(res, pb.Value.Profile)
		}
	}

	return res
}

func newJfrPprofBuilders(p *parser.Parser, jfrLabels *LabelsSnapshot, m define.ProfileMetadata) *jfrPprofBuilder {
	var period int64
	if m.SampleRate == 0 {
		period = 0
	} else {
		// 周期单位: 纳秒
		period = 1e9 / int64(m.SampleRate)
	}

	return &jfrPprofBuilder{
		startTime: m.StartTime.UnixNano(),
		endTime:   m.EndTime.UnixNano(),

		buildersMapping: models.NewLabelsCache[builder.ProfileBuilder](
			func() *builder.ProfileBuilder {
				return builder.NewProfileBuilder()
			},
		),
		labelMapping:  make(map[uint64]models.Labels),
		jfrLabels:     jfrLabels,
		samplerPeriod: period,
		parser:        p,
	}
}
