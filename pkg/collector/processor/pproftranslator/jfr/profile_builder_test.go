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

	"github.com/google/pprof/profile"
	"github.com/grafana/jfr-parser/parser/types"
	"github.com/stretchr/testify/assert"
)

func TestAddExternalFunction(t *testing.T) {
	pb := NewProfileBuilder()
	frameName := "TestFunction"
	functionRef := types.MethodRef(1)

	location := pb.AddExternalFunction(frameName, functionRef)

	assert.NotNil(t, location)
	assert.Equal(t, frameName, location.Line[0].Function.Name)
	assert.Equal(t, uint64(1), location.Line[0].Function.ID)
	assert.Equal(t, uint64(1), location.ID)
}

func TestAddExternalSample(t *testing.T) {
	pb := NewProfileBuilder()
	stacktraceRef := types.StackTraceRef(1)
	locations := []*profile.Location{
		{
			ID: 1,
			Line: []profile.Line{
				{Function: &profile.Function{ID: 1, Name: "Function1"}},
			},
		},
	}
	value := []int64{1, 2, 3}

	pb.AddExternalSample(locations, value, stacktraceRef)

	sample := pb.FindExternalSample(stacktraceRef)

	assert.NotNil(t, sample)
	assert.Equal(t, locations, sample.Location)
	assert.Equal(t, value, sample.Value)
}

func TestAddSampleType(t *testing.T) {
	pb := NewProfileBuilder()
	typ := "SampleType"
	unit := "ms"

	pb.AddSampleType(typ, unit)

	assert.Len(t, pb.Profile.SampleType, 1)
	assert.Equal(t, typ, pb.Profile.SampleType[0].Type)
	assert.Equal(t, unit, pb.Profile.SampleType[0].Unit)
}

func TestAddPeriodType(t *testing.T) {
	pb := NewProfileBuilder()
	typ := "PeriodType"
	unit := "ms"

	pb.AddPeriodType(typ, unit)

	assert.NotNil(t, pb.Profile.PeriodType)
	assert.Equal(t, typ, pb.Profile.PeriodType.Type)
	assert.Equal(t, unit, pb.Profile.PeriodType.Unit)
}

func TestFindLocationId(t *testing.T) {
	pb := NewProfileBuilder()
	data, found := pb.FindLocationId(types.MethodRef(999))
	assert.False(t, found)
	assert.Nil(t, data)

	pb.AddExternalFunction("testFrame", types.MethodRef(999))
	data, found = pb.FindLocationId(types.MethodRef(999))
	assert.True(t, found)
	assert.Equal(t, data.ID, uint64(1))
}
