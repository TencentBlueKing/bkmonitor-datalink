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
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/disk"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	SectorSize = 512
)

type BKDiskStats struct {
	ReadCount        uint64  `json:"readCount"`
	MergedReadCount  uint64  `json:"mergedReadCount"`
	WriteCount       uint64  `json:"writeCount"`
	MergedWriteCount uint64  `json:"mergedWriteCount"`
	ReadBytes        uint64  `json:"readBytes"`
	WriteBytes       uint64  `json:"writeBytes"`
	ReadTime         uint64  `json:"readTime"`
	WriteTime        uint64  `json:"writeTime"`
	IopsInProgress   uint64  `json:"iopsInProgress"`
	IoTime           uint64  `json:"ioTime"`
	WeightedIO       uint64  `json:"weightedIO"`
	Name             string  `json:"name"`
	SerialNumber     string  `json:"serialNumber"`
	Label            string  `json:"label"`
	MajorNum         uint64  `json:"major"`
	MinorNum         uint64  `json:"minor"`
	ReadSectors      uint64  `json:"readSectors"`
	WriteSectors     uint64  `json:"writeSectors"`
	SpeedIORead      float64 `json:"speedIORead"`
	SpeedByteRead    float64 `json:"speedByteRead"`
	SpeedIOWrite     float64 `json:"speedIOWrite"`
	SpeedByteWrite   float64 `json:"speedByteWrite"`
	Util             float64 `json:"util"`
	AvgrqSz          float64 `json:"avgrq_sz"`
	AvgquSz          float64 `json:"avgqu_sz"`
	Await            float64 `json:"await"`
	Svctm            float64 `json:"svctm"`
}

type DiskReport struct {
	DiskStats  map[string]BKDiskStats `json:"diskstat"`
	Partitions []disk.PartitionStat   `json:"partition"`
	Usage      []disk.UsageStat       `json:"usage"`
}

func stringsHas(target []string, src string) bool {
	for _, t := range target {
		if strings.TrimSpace(t) == src {
			return true
		}
	}
	return false
}

func hostProc(combineWith ...string) string {
	return getEnv("HOST_PROC", "/proc", combineWith...)
}

func hostSys(combineWith ...string) string {
	return getEnv("HOST_SYS", "/sys", combineWith...)
}

func hostRun(combineWith ...string) string {
	return getEnv("HOST_RUN", "/run", combineWith...)
}

func getEnv(key string, dfault string, combineWith ...string) string {
	value := os.Getenv(key)
	if value == "" {
		value = dfault
	}

	switch len(combineWith) {
	case 0:
		return value
	case 1:
		return filepath.Join(value, combineWith[0])
	default:
		all := make([]string, len(combineWith)+1)
		all[0] = value
		copy(all[1:], combineWith)
		return filepath.Join(all...)
	}
}

func readLinesOffsetN(filename string, offset uint, n int) ([]string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return []string{""}, err
	}
	defer f.Close()

	var ret []string

	r := bufio.NewReader(f)
	for i := 0; i < n+int(offset) || n < 0; i++ {
		line, err := r.ReadString('\n')
		if err != nil {
			break
		}
		if i < int(offset) {
			continue
		}
		ret = append(ret, strings.Trim(line, "\n"))
	}

	return ret, nil
}

func getLabel(name string) string {
	// Try label based on devicemapper name
	dmnameFilename := hostSys(fmt.Sprintf("block/%s/dm/name", name))

	if !pathExists(dmnameFilename) {
		return ""
	}

	dmname, err := os.ReadFile(dmnameFilename)
	if err != nil {
		return ""
	} else {
		return string(dmname)
	}
}

func pathExists(filename string) bool {
	if _, err := os.Stat(filename); err == nil {
		return true
	}
	return false
}

// change map[string]IOCountersStat to map[string]BK_DiskStats
func (report *DiskReport) AssignDiskStats(stats map[string]BKDiskStats) {
	report.DiskStats = make(map[string]BKDiskStats, 0)
	for name, stat := range stats {
		report.DiskStats[name] = stat
	}
}

var diskLast map[string]BKDiskStats

func GetDiskInfo(config configs.DiskConfig) (*DiskReport, error) {
	var report DiskReport
	var err error

	// TODO 采样多次，取最大值
	count := config.StatTimes
	ticker := time.NewTicker(config.StatPeriod)
	defer ticker.Stop()
	for {
		logger.Debug("collect disk io")

		var once DiskReport
		iostats, err := IOCounters()
		if err != nil {
			logger.Error("get disk IOCounters fail")
			return nil, err
		}

		once.AssignDiskStats(iostats)

		// get speed
		// TODO 待修复：应该按照上个周期计算而不是第一个周期
		GetDiskSpeed(diskLast, once.DiskStats)
		diskLast = once.DiskStats

		// TODO select max iostat report by partition
		// 过滤黑白名单
		// 过滤以分区形式上报的数据
		report.DiskStats = FilterDiskIoStats(once.DiskStats, config)

		count--
		if count <= 0 {
			break
		}

		select {
		case <-ticker.C:
		}
	}
	// partition
	partitions, err := disk.Partitions(config.CollectAllDev)
	if err != nil {
		return nil, err
	}

	// 过滤掉不要的分区
	partitions = FilterPartitions(partitions, config)

	report.Partitions = make([]disk.PartitionStat, 0, len(partitions))
	report.Usage = make([]disk.UsageStat, 0, len(partitions))
	// usage
	for _, partition := range partitions {

		usage, err := disk.Usage(partition.Mountpoint)
		if err != nil {
			continue
		}

		if usage.UsedPercent < 0 || usage.UsedPercent > 100 {
			continue
		}

		report.Usage = append(report.Usage, *usage)
		report.Partitions = append(report.Partitions, partition)
	}

	return &report, nil
}
