// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package collector

import (
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/mem"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type swapinfo struct {
	Sin  uint64
	Sout uint64
}

func PhysicalMemoryInfo(specialSource bool) (info *mem.VirtualMemoryStat, err error) {
	// 原本的方案 usedPercent = total-available/total
	info, err = mem.VirtualMemory()

	// 内部特殊富容器的监控支持
	getIEGMemInfo(info)

	if !specialSource {
		return
	}

	if err != nil {
		return nil, err
	}

	// 使用特殊方案时，usedPercent = Used/total
	info.UsedPercent = float64(info.Used) / float64(info.Total) * 100.0
	return

}

func GetSwapInfo() (in, out float64, lasterr error) {
	var wg sync.WaitGroup
	var sinfobefore, sinfoafter = new(swapinfo), new(swapinfo)

	wg.Add(2)
	go func(sinfobefore *swapinfo) {
		defer wg.Done()
		sinfobefore, err := getSwapInfoLogic(sinfobefore)
		if err != nil {
			lasterr = err
		}
	}(sinfobefore)

	// 隔一秒，取下一秒的值
	go func(sinfoafter *swapinfo) {
		defer wg.Done()
		time.Sleep(1 * time.Second)
		sinfoafter, err := getSwapInfoLogic(sinfoafter)
		if err != nil {
			lasterr = err
		}
	}(sinfoafter)

	wg.Wait()
	if lasterr != nil {
		return 0, 0, lasterr
	}

	logger.Debugf("sinfobefore: in: %d, out:%d", sinfobefore.Sin, sinfobefore.Sout)
	logger.Debugf("sinfoafter: in: %d, out:%d", sinfoafter.Sin, sinfoafter.Sout)
	in = float64(sinfoafter.Sin - sinfobefore.Sin)
	out = float64(sinfoafter.Sout - sinfobefore.Sout)
	return in, out, nil
}

func getSwapInfoLogic(sinfo *swapinfo) (*swapinfo, error) {
	swapInfo, err := mem.SwapMemory()
	if err != nil {
		if strings.Contains(err.Error(), "no swap devices") {
			return sinfo, nil
		}
		return nil, err
	}

	const kb uint64 = 1024
	sinfo.Sin = swapInfo.Sin / kb
	sinfo.Sout = swapInfo.Sout / kb
	return sinfo, nil
}
