// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build linux
// +build linux

package collector

import (
	"errors"
	"testing"

	"github.com/shirou/gopsutil/v3/mem"
	"github.com/stretchr/testify/assert"
)

func TestPhysicalMemoryInfo(t *testing.T) {
	testInfo, err := PhysicalMemoryInfo(true)
	assert.NoError(t, err)

	vInfo, err := mem.VirtualMemory()
	assert.NoError(t, err)
	assert.Equal(t, testInfo.UsedPercent, float64(vInfo.Used)/float64(vInfo.Total)*100.0)
}

func TestGetSwapInfo(t *testing.T) {
	in, out, err := GetSwapInfo()
	assert.NoError(t, err)

	if in <= 0 || out <= 0 {
		t.Errorf("Invalid swap info: in=%f, out=%f", in, out)
	}
}

func TestGetSwapinfoLogic(t *testing.T) {
	//  Test case 1: SwapMemory returns error containing "no swap devices"
	sinfo := &swapinfo{}
	expectedSinfo := &swapinfo{}
	expectedSinfo.Sin = 50
	expectedSinfo.Sout = 100
	result, err := getSwapInfoLogic(sinfo)
	assert.NoError(t, err)
	assert.Equal(t, expectedSinfo, result)

	//  Test case 2: SwapMemory returns valid swap memory information
	sinfo = &swapinfo{}
	expectedSinfo = &swapinfo{}
	expectedSinfo.Sin = 100
	expectedSinfo.Sout = 200
	result, err = getSwapInfoLogic(sinfo)
	assert.NoError(t, err)
	assert.Equal(t, expectedSinfo, result)

	//  Test case 3: SwapMemory returns unknown error
	sinfo = &swapinfo{}
	expectedErr := errors.New("unknown error")
	result, err = getSwapInfoLogic(sinfo)
	assert.Error(t, err)
	assert.Equal(t, expectedErr, err)
}
