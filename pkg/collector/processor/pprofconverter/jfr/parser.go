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
	"io"

	"github.com/grafana/jfr-parser/parser"
	"github.com/pkg/errors"
	"google.golang.org/protobuf/proto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	sampleTypeCPU        = 0
	sampleTypeWall       = 1
	sampleTypeInTLAB     = 2
	sampleTypeOutTLAB    = 3
	sampleTypeLock       = 4
	sampleTypeThreadPark = 5
	sampleTypeLiveObject = 6

	TypeCpu            = "cpu"
	TypeWall           = "wall"
	TypeInTlabObjects  = "alloc_in_new_tlab_objects"
	TypeInTlabBytes    = "alloc_in_new_tlab_bytes"
	TypeSpace          = "space"
	TypeOutTlabObjects = "alloc_outside_tlab_objects"
	TypeOutTlabBytes   = "alloc_outside_tlab_bytes"
	TypeContentions    = "contentions"
	TypeDelay          = "delay"
	TypeMutex          = "mutex"
	TypeBlock          = "block"
	TypeLive           = "live"
	TypeObjects        = "objects"

	UnitNanoseconds = "nanoseconds"
	UnitCount       = "count"
	UnitBytes       = "bytes"
)

// Converter JFR数据解析器
type Converter struct{}

func (c *Converter) convertBody(body any) ([]byte, *LabelsSnapshot, error) {
	jfrData, ok := body.(define.ProfileJfrFormatOrigin)
	if !ok {
		return nil, nil, errors.Errorf("jfr converter failed, can not convert body to JfrOrigin")
	}
	depressedData, err := Decompress(jfrData.Jfr)
	if err != nil {
		return nil, nil, errors.Errorf("jfr converter failed, can not decompress jfr data, error: %s", err)
	}
	jfrLabels := new(LabelsSnapshot)
	depressedLabels, err := Decompress(jfrData.Labels)
	if err != nil {
		logger.Warnf("can not decompress jfr labels, error: %s", err)
	} else {
		err = proto.Unmarshal(depressedLabels, jfrLabels)
		if err != nil {
			logger.Warnf("can not convert jfr labels, error: %s", err)
		}
	}
	return depressedData, jfrLabels, nil
}

// ParseToPprof jfr解析主方法
func (c *Converter) ParseToPprof(pd define.ProfilesRawData) (*define.ProfilesData, error) {
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
			return nil, fmt.Errorf("jfr parser ParseEvent error: %w", err)
		}

		switch eventType {
		case jfrParser.TypeMap.T_EXECUTION_SAMPLE:
			ts := jfrParser.GetThreadState(jfrParser.ExecutionSample.State)
			if ts != nil && ts.Name == "STATE_RUNNABLE" {
				builders.addStacktrace(
					sampleTypeCPU,
					jfrParser.ExecutionSample.ContextId,
					jfrParser.ExecutionSample.StackTrace, values[:1],
				)
			}
			if event == "wall" {
				builders.addStacktrace(
					sampleTypeWall,
					jfrParser.ExecutionSample.ContextId,
					jfrParser.ExecutionSample.StackTrace, values[:1],
				)
			}
		case jfrParser.TypeMap.T_ALLOC_IN_NEW_TLAB:
			values[1] = int64(jfrParser.ObjectAllocationInNewTLAB.TlabSize)
			builders.addStacktrace(
				sampleTypeInTLAB,
				jfrParser.ObjectAllocationInNewTLAB.ContextId,
				jfrParser.ObjectAllocationInNewTLAB.StackTrace, values[:2],
			)
		case jfrParser.TypeMap.T_ALLOC_OUTSIDE_TLAB:
			values[1] = int64(jfrParser.ObjectAllocationOutsideTLAB.AllocationSize)
			builders.addStacktrace(
				sampleTypeOutTLAB,
				jfrParser.ObjectAllocationOutsideTLAB.ContextId,
				jfrParser.ObjectAllocationOutsideTLAB.StackTrace, values[:2],
			)
		case jfrParser.TypeMap.T_MONITOR_ENTER:
			values[1] = int64(jfrParser.JavaMonitorEnter.Duration)
			builders.addStacktrace(
				sampleTypeLock,
				jfrParser.JavaMonitorEnter.ContextId,
				jfrParser.JavaMonitorEnter.StackTrace, values[:2],
			)
		case jfrParser.TypeMap.T_THREAD_PARK:
			values[1] = int64(jfrParser.ThreadPark.Duration)
			builders.addStacktrace(
				sampleTypeThreadPark,
				jfrParser.ThreadPark.ContextId,
				jfrParser.ThreadPark.StackTrace, values[:2],
			)
		case jfrParser.TypeMap.T_LIVE_OBJECT:
			builders.addStacktrace(sampleTypeLiveObject, 0, jfrParser.LiveObject.StackTrace, values[:1])
		case jfrParser.TypeMap.T_ACTIVE_SETTING:
			if jfrParser.ActiveSetting.Name == "event" {
				event = jfrParser.ActiveSetting.Value
			}
		}
	}

	return &define.ProfilesData{Metadata: pd.Metadata, Profiles: builders.build()}, nil
}
