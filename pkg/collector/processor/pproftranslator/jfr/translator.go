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
	"io"

	"github.com/grafana/jfr-parser/parser"
	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	sampleTypeCPU         = 0
	sampleTypeWall        = 1
	sampleTypeInTLAB      = 2
	sampleTypeOutTLAB     = 3
	sampleTypeLock        = 4
	sampleTypeThreadPark  = 5
	sampleTypeLiveObject  = 6
	sampleTypeAllocSample = 7
	sampleTypeMalloc      = 8
)

// Translator JFR 数据解析器
type Translator struct{}

func (c *Translator) convertBody(body any) ([]byte, *LabelsSnapshot, error) {
	jfrData, ok := body.(define.ProfileJfrFormatOrigin)
	if !ok {
		return nil, nil, errors.Errorf("excepted JfrFormatOrigin type, but got %T", body)
	}
	depressedData, err := Decompress(jfrData.Jfr)
	if err != nil {
		return nil, nil, errors.Wrap(err, "decompress jfr data failed")
	}

	jfrLabels := new(LabelsSnapshot)
	depressedLabels, err := Decompress(jfrData.Labels)
	if err != nil {
		logger.Warnf("decompress jfr labels failed, error: %s", err)
	} else {
		err = proto.Unmarshal(depressedLabels, jfrLabels)
		if err != nil {
			logger.Warnf("unmarshal jfr labels failed, error: %s", err)
		}
	}
	return depressedData, jfrLabels, nil
}

// Translate jfr 数据格式解析主方法
func (c *Translator) Translate(pd define.ProfilesRawData) (*define.ProfilesData, error) {
	depressedData, jfrLabels, err := c.convertBody(pd.Data)
	if err != nil {
		return nil, err
	}

	jfrParser := parser.NewParser(depressedData, parser.Options{SymbolProcessor: processSymbols})
	builders := newJfrPprofBuilders(jfrParser, jfrLabels, pd.Metadata)

	values := [2]int64{1, 0}
	var event string
	for {
		eventType, err := jfrParser.ParseEvent()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, errors.Wrap(err, "jfr parser ParseEvent failed")
		}

		switch eventType {
		case jfrParser.TypeMap.T_EXECUTION_SAMPLE:
			ts := jfrParser.GetThreadState(jfrParser.ExecutionSample.State)
			correlation := StacktraceCorrelation{
				ContextId: jfrParser.ExecutionSample.ContextId,
				SpanId:    jfrParser.ExecutionSample.SpanId,
				SpanName:  jfrParser.ExecutionSample.SpanName,
			}
			if ts != nil && ts.Name != "STATE_SLEEPING" {
				builders.addStacktrace(sampleTypeCPU, correlation, jfrParser.ExecutionSample.StackTrace, values[:1])
			}
			if event == "wall" {
				builders.addStacktrace(sampleTypeWall, correlation, jfrParser.ExecutionSample.StackTrace, values[:1])
			}
		case jfrParser.TypeMap.T_WALL_CLOCK_SAMPLE:
			values[0] = int64(jfrParser.WallClockSample.Samples)
			builders.addStacktrace(sampleTypeWall, StacktraceCorrelation{}, jfrParser.WallClockSample.StackTrace, values[:1])
		case jfrParser.TypeMap.T_ALLOC_IN_NEW_TLAB:
			values[1] = int64(jfrParser.ObjectAllocationInNewTLAB.TlabSize)
			correlation := StacktraceCorrelation{
				ContextId: jfrParser.ObjectAllocationInNewTLAB.ContextId,
				SpanId:    jfrParser.ObjectAllocationInNewTLAB.SpanId,
				SpanName:  jfrParser.ObjectAllocationInNewTLAB.SpanName,
			}
			builders.addStacktrace(sampleTypeInTLAB, correlation, jfrParser.ObjectAllocationInNewTLAB.StackTrace, values[:2])
		case jfrParser.TypeMap.T_ALLOC_OUTSIDE_TLAB:
			values[1] = int64(jfrParser.ObjectAllocationOutsideTLAB.AllocationSize)
			correlation := StacktraceCorrelation{
				ContextId: jfrParser.ObjectAllocationOutsideTLAB.ContextId,
				SpanId:    jfrParser.ObjectAllocationOutsideTLAB.SpanId,
				SpanName:  jfrParser.ObjectAllocationOutsideTLAB.SpanName,
			}
			builders.addStacktrace(sampleTypeOutTLAB, correlation, jfrParser.ObjectAllocationOutsideTLAB.StackTrace, values[:2])
		case jfrParser.TypeMap.T_ALLOC_SAMPLE:
			values[1] = int64(jfrParser.ObjectAllocationSample.Weight)
			builders.addStacktrace(sampleTypeAllocSample, StacktraceCorrelation{}, jfrParser.ObjectAllocationSample.StackTrace, values[:2])
		case jfrParser.TypeMap.T_MONITOR_ENTER:
			values[1] = int64(jfrParser.JavaMonitorEnter.Duration)
			correlation := StacktraceCorrelation{
				ContextId: jfrParser.JavaMonitorEnter.ContextId,
				SpanId:    jfrParser.JavaMonitorEnter.SpanId,
				SpanName:  jfrParser.JavaMonitorEnter.SpanName,
			}
			builders.addStacktrace(sampleTypeLock, correlation, jfrParser.JavaMonitorEnter.StackTrace, values[:2])
		case jfrParser.TypeMap.T_THREAD_PARK:
			values[1] = int64(jfrParser.ThreadPark.Duration)
			builders.addStacktrace(sampleTypeThreadPark, StacktraceCorrelation{}, jfrParser.ThreadPark.StackTrace, values[:2])
		case jfrParser.TypeMap.T_LIVE_OBJECT:
			builders.addStacktrace(sampleTypeLiveObject, StacktraceCorrelation{}, jfrParser.LiveObject.StackTrace, values[:1])
		case jfrParser.TypeMap.T_MALLOC:
			values[1] = int64(jfrParser.Malloc.Size)
			builders.addStacktrace(sampleTypeMalloc, StacktraceCorrelation{}, jfrParser.Malloc.StackTrace, values[:2])
		case jfrParser.TypeMap.T_ACTIVE_SETTING:
			if jfrParser.ActiveSetting.Name == "event" {
				event = jfrParser.ActiveSetting.Value
			}
		}
	}

	return &define.ProfilesData{Metadata: pd.Metadata, Profiles: builders.build()}, nil
}
