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
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/exceptionbeat/collector"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	ErrPatternDelimiter = errors.New("invalid pattern delimiter")
	ErrRegexMatch       = errors.New("regex does not match")
)

const (
	closeState = iota
	runningState
)

type ReportInfo struct {
	time  time.Time
	count int
	info  beat.MapStr
}

type Collector struct {
	dataid                  int32
	done                    chan bool
	state                   int
	coreFilePattern         string
	corePath                string
	pattern                 string
	patternList             [][]string
	coreWatcher             *fsnotify.Watcher
	isUsesPid               bool
	isCorePathAddSuccess    bool
	isCorePatternAddSuccess bool
	isCoreUsesPidAddSuccess bool

	reportTimeInfo map[string]*ReportInfo // 上报时间缓冲记录区, statistic 是单线程的，无需加锁保护
	reportTimeGap  time.Duration          // 事件上报缓冲时间间隔
}

func init() {
	collector.RegisterCollector(new(Collector))
}

func (c *Collector) Start(ctx context.Context, e chan<- define.Event, conf *configs.ExceptionBeatConfig) {
	logger.Info("collector is running...")
	if (conf.CheckBit & configs.Core) == 0 {
		logger.Info(" corefile collector closed by config: %s", conf.CheckMethod)
		return
	}
	if c.state == runningState {
		logger.Info(" corefile collector already started")
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

	logger.Infof("corefile collector start with data_id->[%d] report_gap->[%s]", c.dataid, c.reportTimeGap)
	go c.statistic(ctx, e)
}

func (c *Collector) Reload(conf *configs.ExceptionBeatConfig) {}

func (c *Collector) Stop() {
	if c.state == closeState {
		logger.Error("corefile collector stop failed: already closed")
		return
	}
	c.state = closeState
	close(c.done)
	logger.Info("corefile collector stopped")
}

func buildExtra(path string, dimensions beat.MapStr) beat.MapStr {
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
