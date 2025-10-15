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
	"testing"
	"time"

	"github.com/grafana/jfr-parser/parser"
	"github.com/grafana/jfr-parser/parser/types"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

func TestAddStacktrace(t *testing.T) {
	methodIdMap := make(map[types.MethodRef]uint32)
	methodIdMap[types.MethodRef(1)] = 1
	methodIdMap[types.MethodRef(2)] = 2

	// 构造一个堆栈
	mockParser := &parser.Parser{
		Stacktrace: types.StackTraceList{
			IDMap:      map[types.StackTraceRef]uint32{types.StackTraceRef(0): 0},
			StackTrace: []types.StackTrace{{Truncated: false, Frames: []types.StackFrame{{Method: 1}}}},
		},
		Methods: types.MethodList{
			IDMap: methodIdMap,
			Method: []types.Method{
				{Type: sampleTypeCPU, Name: sampleTypeLock},
				{Type: sampleTypeCPU, Name: sampleTypeWall},
			},
		},
	}

	mockLabelsSnapshot := &LabelsSnapshot{}
	mockProfileMetadata := define.ProfileMetadata{
		StartTime: time.Now(),
		EndTime:   time.Now().Add(time.Minute),
	}
	builder := newJfrPprofBuilders(mockParser, mockLabelsSnapshot, mockProfileMetadata)

	t.Run("add stacktrace", func(t *testing.T) {
		builder.addStacktrace(0, StacktraceCorrelation{}, 0, []int64{1, 2, 3})
		assert.NotEqual(t, len(builder.builders), 0)
	})

	t.Run("stacktrace found", func(t *testing.T) {
		builder.addStacktrace(0, StacktraceCorrelation{}, 0, []int64{1, 2, 3})
		trace := mockParser.GetStacktrace(0)
		assert.Len(t, trace.Frames, 1)
		assert.Equal(t, trace.Frames[0].Method, types.MethodRef(1))
	})
}

func TestGetLabelsFromSnapshot(t *testing.T) {
	mockParser := &parser.Parser{}
	mockLabelsSnapshot := &LabelsSnapshot{
		Contexts: map[int64]*Context{
			1: {
				Labels: map[int64]int64{
					1: 1,
					2: 2,
				},
			},
		},
	}

	mockProfileMetadata := define.ProfileMetadata{
		StartTime: time.Now(),
		EndTime:   time.Now().Add(time.Minute),
	}
	builder := newJfrPprofBuilders(mockParser, mockLabelsSnapshot, mockProfileMetadata)

	t.Run("contextId not found", func(t *testing.T) {
		c, success := builder.jfrLabels.Contexts[0]
		assert.False(t, success)
		assert.Empty(t, c)
	})

	t.Run("contextId found", func(t *testing.T) {
		c, success := builder.jfrLabels.Contexts[1]
		assert.True(t, success)
		assert.Len(t, c.Labels, 2)
	})
}
