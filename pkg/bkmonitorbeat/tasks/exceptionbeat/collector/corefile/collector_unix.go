// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package corefile

import (
	"context"
	"regexp"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/exceptionbeat/collector"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	runningState = iota
	closeState
)

type ReportInfo struct {
	time  time.Time
	count int
	info  beat.MapStr
}

type CoreFileCollector struct {
	dataid                  int32
	done                    chan bool
	state                   int
	coreFilePattern         string
	matchRegx               *regexp.Regexp
	corePath                string
	pattern                 string
	patternArr              [][]string
	coreWatcher             *fsnotify.Watcher
	isUsesPid               bool
	isCorePathAddSuccess    bool
	isCorePatternAddSuccess bool
	isCoreUsesPidAddSuccess bool

	reportTimeInfo map[string]*ReportInfo // 上报时间缓冲记录区, 注意，这个地方由于有map，在处理循环时注意加锁。目前由于statistic是单线程的，所以并没有加锁保护
	reportTimeGap  time.Duration          // 事件上报缓冲时间间隔
}

func init() {
	tmpCollector := new(CoreFileCollector)
	tmpCollector.state = closeState
	collector.RegisterCollector(tmpCollector)
}

func (c *CoreFileCollector) Start(ctx context.Context, e chan<- define.Event, conf *configs.ExceptionBeatConfig) {
	logger.Info("CoreFileCollector is running...")
	if 0 == (conf.CheckBit & configs.Core) {
		logger.Infof("CoreFileCollector closed by config: %s", conf.CheckMethod)
		return
	}
	if runningState == c.state {
		logger.Info("CoreFileCollector has been already started")
		return
	}
	c.dataid = conf.DataID
	c.done = make(chan bool)
	c.state = runningState
	if c.reportTimeGap == 0 {
		c.reportTimeGap = time.Minute
	}
	c.reportTimeInfo = make(map[string]*ReportInfo)
	c.coreFilePattern = conf.CoreFilePattern
	if conf.CoreFileMatchRegex != "" {
		r, err := regexp.Compile(conf.CoreFileMatchRegex)
		if err != nil {
			logger.Errorf("faield to compile regex pattern(%s), err: %v", conf.CoreFileMatchRegex, err)
		} else {
			c.matchRegx = r
		}
	}

	logger.Infof("CoreFileColletor start success with config data_id->[%d] report_gap->[%s]", c.dataid, c.reportTimeGap)
	go c.statistic(ctx, e)
}

func (c *CoreFileCollector) Reload(conf *configs.ExceptionBeatConfig) {}

func (c *CoreFileCollector) Stop() {
	if closeState == c.state {
		logger.Errorf("CoreFileColletor stop failed: collector not open")
		return
	}
	c.state = closeState
	close(c.done)
	logger.Info("CoreFileColletor stopped")
}

func (c *CoreFileCollector) buildExtra(path string, dimensions beat.MapStr) beat.MapStr {
	extra := beat.MapStr{
		"bizid":    collector.BizID,
		"cloudid":  collector.CloudID,
		"host":     collector.NodeIP,
		"type":     collector.CoreEventType,
		"corefile": path,
		"filesize": 0, // 文件大小目前已经不再关注，因此此处上报的大小总是0
	}
	for k, v := range dimensions {
		extra[k] = v
	}
	return extra
}
