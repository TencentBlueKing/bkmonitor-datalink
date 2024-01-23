// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package diskro

import (
	"context"
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
	dataid    int
	ticker    *time.Ticker
	done      chan bool
	state     int
	whiteList []string // 必然需要告警（无论是否存在RW到RO变化）的白名单
	blackList []string // 必然不进行告警（即使存在了RW到RO的变化）的黑名单

	deviceMap map[string]bool // device:exists
}

var partitionFunc = disk.Partitions

func init() {
	collector.RegisterCollector(new(Collector))
}

func (c *Collector) Start(ctx context.Context, e chan<- define.Event, conf *configs.ExceptionBeatConfig) {
	logger.Infof("diskro collector is running...")
	if (conf.CheckBit & configs.DiskRO) == 0 {
		logger.Infof("diskro collector closed by config: %s", conf.CheckMethod)
		return
	}
	if c.state == runningState {
		logger.Info("diskro collector already started")
		return
	}

	c.dataid = int(conf.DataID)
	c.whiteList = conf.DiskRoWhiteList
	c.blackList = conf.DiskRoBlackList

	c.ticker = time.NewTicker(conf.CheckDisRoInterval)
	c.done = make(chan bool)
	c.state = runningState
	c.deviceMap = make(map[string]bool)

	logger.Info("diskro collector start successful")
	go c.statistic(ctx, e)
}

func (c *Collector) Reload(_ *configs.ExceptionBeatConfig) {}

func (c *Collector) Stop() {
	if c.state == closeState {
		logger.Error("diskro collector stop failed: already closed")
		return
	}
	logger.Info("diskro collector stopped")
	c.state = closeState
	close(c.done)
}

func (c *Collector) statistic(ctx context.Context, e chan<- define.Event) {
	for {
		select {
		case <-ctx.Done():
			c.Stop()
			logger.Info("diskro collector exit")
			return

		case <-c.ticker.C:
			infoList := c.getRODisk()
			if infoList == nil {
				break
			}

			// 为了兼容后台逻辑，事件需要逐一发送
			for _, data := range infoList {
				extraData := c.buildExtra([]beat.MapStr{data})
				if extraData == nil {
					break
				}
				collector.Send(c.dataid, extraData, e)
			}

		case _, ok := <-c.done:
			if !ok {
				return
			}
			break
		}
	}
}

func (c *Collector) buildExtra(rolist []beat.MapStr) beat.MapStr {
	extra := beat.MapStr{
		"bizid":   collector.BizID,
		"cloudid": collector.CloudID,
		"host":    collector.NodeIP,
		"type":    collector.DiskROEventType,
		"ro":      rolist,
	}
	return extra
}

func (c *Collector) getRODisk() []beat.MapStr {
	var (
		retList            []beat.MapStr
		MountPointInfoList []*MountPointInfo
		shouldReport       bool
	)

	// 此处只关心物理设备的分区，其他系统生成的分区不必关注
	partitions, err := partitionFunc(false)
	if err != nil {
		logger.Errorf("get disk information failed, err: %v", err)
		return nil
	}
	for device := range c.deviceMap {
		c.deviceMap[device] = false
	}

	MountPointInfoList = NewBatchMountPointInfo(partitions)
	for _, mp := range MountPointInfoList {
		c.deviceMap[mp.Device] = true
		// 判断是否满足白名单，如果是直接返回告警
		if mp.IsReadOnly() && mp.IsMatchRule(c.whiteList) {
			logger.Infof("mount_point->[%s] is ro and match white_list, will report", mp.MountPoint)
			shouldReport = true
		}

		// 判断是否存在RW 到RO的变化
		if mp.IsReadOnlyStatusChange() {
			logger.Infof("mount_point->[%s] is detect change now, will report", mp.MountPoint)
			shouldReport = true
		}

		// 如果需要上报，则需要判断是否存在黑名单的过滤
		if shouldReport && mp.IsMatchRule(c.blackList) {
			logger.Infof("mount_point->[%s] should report but match black list, nothing will report", mp.MountPoint)
			shouldReport = false
		}

		// 最终判断是否需要上报
		if shouldReport {
			retList = append(retList, beat.MapStr{
				"fs":       mp.Device,
				"position": mp.MountPoint,
				"type":     mp.FileSystem,
			})
			logger.Infof("mount_point->[%s] is add to report list now", mp.MountPoint)
		}

		// 保留当次状态内容
		if err := mp.SaveStatus(); err != nil {
			logger.Errorf("failed to save mount_point->[%s], err: %v", mp.MountPoint, err)
			continue
		}
		logger.Debugf("mount_point->[%s] status is saved now", mp.MountPoint)

	}
	for device, exists := range c.deviceMap {
		if !exists {
			delete(c.deviceMap, device)
		}
	}

	if len(retList) == 0 {
		logger.Infof("no ro status change mount point is found, nothing will report")
		return nil
	}

	logger.Debugf("total found->[%d] ro mount point", len(retList))
	return retList
}
