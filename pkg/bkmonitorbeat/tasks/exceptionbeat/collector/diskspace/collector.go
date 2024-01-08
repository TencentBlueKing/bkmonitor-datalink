// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package diskspace

import (
	"context"
	"math"
	"time"

	"github.com/shirou/gopsutil/v3/disk"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/exceptionbeat/collector"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	closeState = iota
	runningState
)

type Collector struct {
	dataid     int
	timer      *time.Ticker
	done       chan bool
	state      int
	usageLimit int
	minSpace   int

	deviceMap map[string]bool // device:exists
}

func init() {
	collector.RegisterCollector(new(Collector))
}

func (c *Collector) Start(ctx context.Context, e chan<- define.Event, conf *configs.ExceptionBeatConfig) {
	logger.Info("collector is running...")
	if (conf.CheckBit & configs.DiskSpace) == 0 {
		logger.Infof("collector closed by config: %s", conf.CheckMethod)
		return
	}
	if c.state == runningState {
		logger.Info("collector already started")
		return
	}
	c.dataid = int(conf.DataID)
	c.timer = time.NewTicker(conf.CheckDiskSpaceInterval)
	c.done = make(chan bool)
	c.state = runningState

	c.usageLimit = conf.DiskUsagePercent
	c.minSpace = conf.DiskMinFreeSpace
	c.deviceMap = make(map[string]bool)

	logger.Infof("Collector start success with config: %v", c)
	go c.statistic(ctx, e)
}

func (c *Collector) Reload(conf *configs.ExceptionBeatConfig) {}

func (c *Collector) Stop() {
	if c.state == closeState {
		logger.Errorf("collector stop failed: collector not open")
		return
	}
	logger.Info("collector stopped")
	c.state = closeState
	close(c.done)
}

func (c *Collector) statistic(ctx context.Context, e chan<- define.Event) {
	for {
		select {
		case <-ctx.Done():
			c.Stop()
			logger.Info("diskspace collector exit")
			return

		case <-c.timer.C:
			extraList := c.getSpaceExceededDisk()
			if extraList == nil {
				break
			}
			collector.SendBulk(c.dataid, extraList, e)
		case _, ok := <-c.done:
			if !ok {
				return
			}
			break
		}
	}
}

func (c *Collector) getSpaceExceededDisk() []beat.MapStr {
	// 此处只关心物理设备的分区，其他系统生成的分区不必关注
	partitions, err := disk.Partitions(false)
	if err != nil {
		logger.Errorf("get disk information failed, err: %v", err)
		return nil
	}

	var extra []beat.MapStr
	for k := range c.deviceMap {
		c.deviceMap[k] = false
	}
	for _, partition := range partitions {
		c.deviceMap[partition.Device] = true
		diskInfo, err := disk.Usage(partition.Mountpoint)
		if err != nil {
			logger.Errorf("disk '%s' usage information load failed, err: %v", partition.Mountpoint, err)
			continue
		}
		usedpercent := int(math.Round(diskInfo.UsedPercent))
		logger.Debugf("disk: %s, used percent: %d, free: %d, total: %d", diskInfo.Path, usedpercent, diskInfo.Free, diskInfo.Total)

		// 判断 使用率大于了配置 同时 磁盘剩余空间小于配置项大小
		if diskInfo.UsedPercent >= float64(c.usageLimit) && diskInfo.Free < uint64(c.minSpace*1024*1024*1024) {
			extra = append(extra, beat.MapStr{
				"bizid":        collector.BizID,
				"cloudid":      collector.CloudID,
				"host":         collector.NodeIP,
				"type":         collector.DiskSpaceEventType,
				"disk":         diskInfo.Path,
				"file_system":  partition.Device,
				"fstype":       partition.Fstype,
				"avail":        diskInfo.Free / 1024,
				"size":         diskInfo.Total / 1024,
				"used":         diskInfo.Used / 1024,
				"free":         100 - usedpercent,
				"used_percent": usedpercent,
			})
		}
	}
	for device, exists := range c.deviceMap {
		if !exists {
			delete(c.deviceMap, device)
		}
	}
	if len(extra) == 0 {
		return nil
	}
	return extra
}
