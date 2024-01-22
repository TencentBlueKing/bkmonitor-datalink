// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package outofmem

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"strconv"
	"strings"
	"time"

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

const (
	iegDockerFile = "/etc/ieg-docker.conf"
)

// OomInstance struct that contains information related to an OOM kill instance
type OomInstance struct {
	// process id of the killed process
	Pid int
	// the name of the killed process
	ProcessName string
	// the time that the process was reported to be killed,
	// accurate to the minute
	TimeOfDeath time.Time
	// the absolute name of the container that OOMed
	ContainerName string
	// the absolute name of the container that was killed
	// due to the OOM.
	VictimContainerName string
	// the constraint that triggered the OOM.  One of CONSTRAINT_NONE,
	// CONSTRAINT_CPUSET, CONSTRAINT_MEMORY_POLICY, CONSTRAINT_MEMCG
	Constraint string
}

type OOMInfo struct {
	*OomInstance
}

type OutOfMemCollector struct {
	dataid int
	state  int

	oomch     chan *OOMInfo
	oomctx    context.Context
	ctxCancel context.CancelFunc
	startup   int64
	reportGap time.Duration
}

func init() {
	tmpCollector := new(OutOfMemCollector)
	tmpCollector.startup = time.Now().Unix()
	collector.RegisterCollector(tmpCollector)
}

func (c *OutOfMemCollector) Start(ctx context.Context, e chan<- define.Event, conf *configs.ExceptionBeatConfig) {
	logger.Info("oom collector is running...")
	if (conf.CheckBit & configs.OOM) == 0 {
		logger.Infof("oom collector closed by config: %s", conf.CheckMethod)
		return
	}

	if c.state == runningState {
		logger.Info("oom collector has been already started")
		return
	}

	c.dataid = int(conf.DataID)
	c.state = runningState
	c.reportGap = conf.OutOfMemReportGap
	if c.reportGap <= 0 {
		c.reportGap = configs.DefaultExceptionBeatConfig.OutOfMemReportGap
	}

	// 判断是否存在docker标记位，如果是，则不启动oom实际监控
	if _, err := os.Stat(iegDockerFile); err == nil {
		logger.Warnf("IEG docker file->[%s] exists, this is docker os, oom service won't start", iegDockerFile)
		return
	}

	if isInDocker() {
		logger.Warn("process is running in the docker environment")
		return
	}
	go c.StartTraceOOM()
	time.Sleep(time.Second) // 确保先被初始化
	go c.WatchOOMEvents(ctx, e)
}

func (c *OutOfMemCollector) StartTraceOOM() {
	c.oomch = make(chan *OOMInfo, 100)
	c.oomctx, c.ctxCancel = context.WithCancel(context.Background())
	if err := startTraceOOM(c.oomctx, c.oomch); err != nil {
		logger.Errorf("oom discover, error when start oom trace: %s", err)
		return
	}
}

// 上报并清空事件map
func flushEventMap(dataid int, evtMap map[string]beat.MapStr, e chan<- define.Event) {
	evtList := make([]beat.MapStr, 0)
	for key, evt := range evtMap {
		logger.Infof("oom event: %+v", evt)
		evtList = append(evtList, evt)
		delete(evtMap, key)
	}
	if len(evtList) > 0 {
		collector.SendBulk(dataid, evtList, e)
	}
}

func (c *OutOfMemCollector) WatchOOMEvents(ctx context.Context, e chan<- define.Event) {
	reportTicker := time.NewTicker(c.reportGap)
	defer reportTicker.Stop()
	// 按进程名缓存事件
	eventMap := make(map[string]beat.MapStr)
	// 退出时上报缓存中未上报的事件
	defer func() {
		flushEventMap(c.dataid, eventMap, e)
	}()
	for {
		select {
		case info := <-c.oomch:
			// 如果是启动之前发生的 OOM 事件直接忽略
			if info.TimeOfDeath.Unix() < c.startup {
				continue
			}
			if evt, ok := eventMap[info.ProcessName]; ok {
				// 已出现过的进程仅增加计数
				evt["total"] = evt["total"].(uint64) + 1
			} else {
				// 未出现过的进程插入
				eventMap[info.ProcessName] = beat.MapStr{
					"bizid":      collector.BizID,
					"cloudid":    collector.CloudID,
					"host":       collector.NodeIP,
					"type":       collector.OutOfMemEventType,
					"total":      uint64(1),
					"process":    info.ProcessName,
					"oom_memcg":  info.ContainerName,
					"task_memcg": info.VictimContainerName,
					"task":       info.ProcessName,
					"constraint": info.Constraint,
					"message":    "系统发生OOM异常事件",
				}
			}
		case <-reportTicker.C:
			// 定期上报
			flushEventMap(c.dataid, eventMap, e)
		case <-ctx.Done():
			c.Stop()
			logger.Info("oom collector exit")
			return
		}
	}
}

func (c *OutOfMemCollector) Reload(_ *configs.ExceptionBeatConfig) {
	c.state = closeState
}

func (c *OutOfMemCollector) Stop() {
	c.ctxCancel()
	c.state = closeState
}

type mount struct {
	Device     string
	Path       string
	Filesystem string
	Flags      string
}

// 当且仅当 /.dockerenv 的前提下 文件存在且 cgroup 权限为 ro 或者 /proc/1/sched 进程号不为 1
func isInDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return ifCgroupReadonly() || ifSchedProc()
	}

	return false
}

// $ cat /proc/self/mounts
// ...
// cgroup /sys/fs/cgroup/systemd cgroup rw,nosuid,nodev,noexec,relatime,xattr,release_agent=/usr/lib/systemd/systemd-cgroups-agent,name=systemd 0 0
// pstore /sys/fs/pstore pstore rw,nosuid,nodev,noexec,relatime 0 0
// cgroup /sys/fs/cgroup/memory cgroup rw,nosuid,nodev,noexec,relatime,memory 0 0
// cgroup /sys/fs/cgroup/perf_event cgroup rw,nosuid,nodev,noexec,relatime,perf_event 0 0
// cgroup /sys/fs/cgroup/devices cgroup rw,nosuid,nodev,noexec,relatime,devices 0 0
// cgroup /sys/fs/cgroup/freezer cgroup rw,nosuid,nodev,noexec,relatime,freezer 0 0
// cgroup /sys/fs/cgroup/blkio cgroup rw,nosuid,nodev,noexec,relatime,blkio 0 0
// cgroup /sys/fs/cgroup/cpu,cpuacct cgroup rw,nosuid,nodev,noexec,relatime,cpuacct,cpu 0 0
// cgroup /sys/fs/cgroup/cpuset cgroup rw,nosuid,nodev,noexec,relatime,cpuset 0 0
// cgroup /sys/fs/cgroup/net_cls,net_prio cgroup rw,nosuid,nodev,noexec,relatime,net_prio,net_cls 0 0
// cgroup /sys/fs/cgroup/hugetlb cgroup rw,nosuid,nodev,noexec,relatime,hugetlb 0 0
// cgroup /sys/fs/cgroup/pids cgroup rw,nosuid,nodev,noexec,relatime,pids 0 0
// ...
func ifCgroupReadonly() bool {
	bs, err := os.ReadFile("/proc/self/mounts")
	if err != nil {
		logger.Errorf("failed to read /proc/self/mounts, err:%v", err)
		return false
	}

	scanner := bufio.NewScanner(bytes.NewReader(bs))
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), " ", 5)
		if len(parts) != 5 {
			continue
		}

		m := mount{parts[0], parts[1], parts[2], parts[3]}
		if m.Device == "cgroup" && isContainRo(m.Flags) {
			return true
		}
	}

	return false
}

func isContainRo(s string) bool {
	for _, f := range strings.Split(s, ",") {
		if f == "ro" {
			return true
		}
	}

	return false
}

// $ cat /proc/1/sched
// systemd (963838, #threads: 1)
// ---------------------------------------------------------
// se.exec_start                      :    8479909718.879537
// se.vruntime                        :          2223.728642
// se.sum_exec_runtime                :          1381.767962
// nr_switches                        :                10278
// nr_voluntary_switches              :                 8845
// nr_involuntary_switches            :                 1433
// se.load.weight                     :                 1024
// policy                             :                    0
// prio                               :                  120
// clock-delta                        :                   79
// ...
func ifSchedProc() bool {
	bs, err := os.ReadFile("/proc/1/sched")
	if err != nil {
		logger.Errorf("failed to read /proc/1/sched, err:%v", err)
		return false
	}

	var line string
	scanner := bufio.NewScanner(bytes.NewReader(bs))
	// 只读取第一行数据
	for scanner.Scan() {
		line = scanner.Text()
		break
	}

	if line == "" {
		return false
	}

	split := strings.SplitN(line, " ", 2)
	if len(split) != 2 {
		return false
	}

	s := split[1]
	l := strings.Index(s, "(")
	r := strings.Index(s, ",")
	if l+1 >= r {
		return false
	}

	i, err := strconv.Atoi(s[l+1 : r])
	if err != nil {
		return false
	}

	// 进程号不为 1 则代表是在容器环境内
	if i != 1 {
		return true
	}

	return false
}
