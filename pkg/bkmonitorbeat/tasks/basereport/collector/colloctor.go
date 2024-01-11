// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	bkcommon "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	lastT int64

	globalDiskInfo   *DiskReport
	diskJob          = &JobMgr{}
	globalSystemInfo *SystemReport
	systemJob        = &JobMgr{}
	globalEnvInfo    *EnvReport
	envJob           = &JobMgr{}
)

const (
	defaultTolerate = 59 // 默认允许 1s 误差
)

func deepCopyDiskInfo(d *DiskReport) (*DiskReport, error) {
	if d == nil {
		return nil, errors.New("diskreport is nil")
	}

	bs, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}

	var dst DiskReport
	err = json.Unmarshal(bs, &dst)
	if err != nil {
		return nil, err
	}

	return &dst, nil
}

func deepCopySystemInfo(d *SystemReport) (*SystemReport, error) {
	if d == nil {
		return nil, errors.New("systemreport is nil")
	}

	bs, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}

	var dst SystemReport
	err = json.Unmarshal(bs, &dst)
	if err != nil {
		return nil, err
	}

	return &dst, nil
}

func deepCopyEnvInfo(d *EnvReport) (*EnvReport, error) {
	if d == nil {
		return nil, errors.New("envreport is nil")
	}

	bs, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}

	var dst EnvReport
	err = json.Unmarshal(bs, &dst)
	if err != nil {
		return nil, err
	}

	return &dst, nil
}

type JobMgr struct {
	run int32
}

func (jm *JobMgr) Running() bool {
	return atomic.LoadInt32(&jm.run) == 1
}

func (jm *JobMgr) MarkWork() {
	atomic.StoreInt32(&jm.run, 1)
}

func (jm *JobMgr) MarkFinished() {
	atomic.StoreInt32(&jm.run, 0)
}

func GetDateTime(tolerate int64) (string, string, int) {
	t := time.Now()

	if tolerate <= 0 {
		tolerate = defaultTolerate
	}

	if t.Unix()-lastT == tolerate {
		time.Sleep(time.Second)
	}

	const TimeFormat = "2006-01-02 15:04:05"
	const TimeZoneFormat = "Z07"

	lastT = time.Now().Unix()

	zone, _ := strconv.Atoi(t.Format(TimeZoneFormat))
	return t.Format(TimeFormat), t.UTC().Format(TimeFormat), zone
}

func Collect(config configs.BasereportConfig, firstRun bool) (ReportData, error) {
	logger.Info("Basereport collecting...")
	var data ReportData
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		t0 := time.Now()
		defer wg.Done()
		var err error
		data.Cpu, err = GetCPUInfo(config.Cpu)
		if err != nil {
			logger.Errorf("collector cpu info failed: %v", err)
			data.Cpu = nil
		}
		logger.Debugf("GetCPUInfo take: %v", time.Since(t0))
	}()

	wg.Add(1)
	go func() {
		t0 := time.Now()
		defer wg.Done()
		var err error
		data.Mem, err = GetMemInfo(config.Mem)
		if err != nil {
			logger.Errorf("collector mem info failed: %v", err)
			data.Mem = nil
		}
		logger.Debugf("GetMemInfo take: %v", time.Since(t0))
	}()

	wg.Add(1)
	go func() {
		t0 := time.Now()
		defer wg.Done()
		var err error
		data.Net, err = GetNetInfo(config.Net)
		if err != nil {
			logger.Errorf("collector net info failed: %v", err)
			data.Net = nil
		}
		logger.Debugf("GetNetInfo take: %v", time.Since(t0))
	}()
	wg.Wait()

	var err error

	if !diskJob.Running() {
		diskJob.MarkWork()

		go func() {
			t0 := time.Now()
			defer diskJob.MarkFinished()
			var err error
			globalDiskInfo, err = GetDiskInfo(config.Disk)
			if err != nil {
				logger.Errorf("collector disk info failed: %v", err)
			}
			logger.Debugf("GetDiskInfo take: %v", time.Since(t0))
		}()
	}
	data.Disk, err = deepCopyDiskInfo(globalDiskInfo)
	if err != nil && !firstRun {
		logger.Errorf("failed to get diskinfo: %v", err)
	}

	if !systemJob.Running() {
		systemJob.MarkWork()

		go func() {
			t0 := time.Now()
			defer systemJob.MarkFinished()
			var err error
			globalSystemInfo, err = GetSystemInfo()
			if err != nil {
				logger.Errorf("collector system info failed: %v", err)
			}
			logger.Debugf("GetSystemInfo take: %v", time.Since(t0))
		}()
	}
	data.System, err = deepCopySystemInfo(globalSystemInfo)
	if err != nil && !firstRun {
		logger.Errorf("collector system info failed: %v", err)
	}

	// collect once in one period
	data.Country, data.City, _ = bkcommon.GetLocation()
	data.City = strings.TrimSpace(data.City)

	data.Load, err = GetLoadInfo()
	if err != nil {
		logger.Errorf("collector load info failed: %v", err)
		data.Load = nil
	}

	// 默认赋值一个env的内容，防止数据依赖方使用了jsonschema等检查工具引发异常报错
	logger.Debug("env report is enable at least one config, will report it.")
	if !envJob.Running() {
		envJob.MarkWork()

		go func() {
			t0 := time.Now()
			defer envJob.MarkFinished()
			var err error
			globalEnvInfo, err = GetEnvInfo()
			if err != nil {
				logger.Errorf("collector env info failed: %v", err)
			}
			logger.Debugf("GetEnvInfo take: %v", time.Since(t0))
		}()
	}
	data.Env, err = deepCopyEnvInfo(globalEnvInfo)
	if err != nil && !firstRun {
		logger.Errorf("collector some env info failed: %v", err)
	}

	data.Datetime, data.UTCTime, data.Zone = GetDateTime(config.TimeTolerate)
	logger.Info("collect done")
	return data, nil
}

type ReportData struct {
	bkcommon.DateTime
	Cpu    *CpuReport    `json:"cpu"`
	Env    *EnvReport    `json:"env"`
	Disk   *DiskReport   `json:"disk"`
	Load   *LoadReport   `json:"load"`
	Mem    *MemReport    `json:"mem"`
	Net    *NetReport    `json:"net"`
	System *SystemReport `json:"system"`
}

func CounterDiff(now, before uint64) uint64 {
	// 数值倒流则直接返回 0
	if before > now {
		return 0
	}

	return now - before
}
