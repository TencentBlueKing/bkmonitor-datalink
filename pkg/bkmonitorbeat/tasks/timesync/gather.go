// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package timesync

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/output/gse"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type gatherState struct {
	taskConf *configs.TimeSyncConfig
	cli      *Client
}

type Gather struct {
	tasks.BaseTask

	stateLock sync.RWMutex
	state     *gatherState
}

func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.Init()

	taskConf := taskConfig.(*configs.TimeSyncConfig)
	gather.state = &gatherState{taskConf: taskConf, cli: newClient(taskConf)}

	return gather
}

func newClient(taskConf *configs.TimeSyncConfig) *Client {
	return NewClient(&Option{
		NtpdPath:    taskConf.NtpdPath,
		ChronyAddr:  taskConf.ChronyAddress,
		BkNTPSource: taskConf.BkNTPSource,
		Timeout:     taskConf.QueryTimeout,
	})
}

// Reload 在调度器热加载后，用最新 TaskConfig 一次性替换配置与 Client 快照。
// 调度器 reload 对同一 Gather 实例执行 SetConfig + Reload；基类 Reload 是空实现。
func (g *Gather) Reload() {
	taskConf, ok := g.TaskConfig.(*configs.TimeSyncConfig)
	if !ok {
		return
	}

	state := &gatherState{taskConf: taskConf, cli: newClient(taskConf)}
	g.stateLock.Lock()
	g.state = state
	g.stateLock.Unlock()
}

func (g *Gather) snapshot() *gatherState {
	g.stateLock.RLock()
	defer g.stateLock.RUnlock()
	return g.state
}

func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	state := g.snapshot()
	taskConf := state.taskConf

	g.PreRun(ctx)
	defer g.PostRun(ctx)

	bizID := taskConf.GetBizID()
	dataID := taskConf.GetDataID()
	labels := taskConf.GetLabels()

	bkConfigured := taskConf.BkNTPSource != ""
	stat, details, err := state.cli.QueryAll()
	if err != nil {
		if bkConfigured && taskConf.NtpdPath == "" && taskConf.ChronyAddress == "" {
			logger.Warnf("no local ntp source configured: %v", err)
		} else {
			logger.Errorf("failed to query stats: %v", err)
		}
	} else if stat != nil {
		e <- &Event{
			BizID:  bizID,
			DataID: dataID,
			Labels: labels,
			Data:   stats2Metrics(taskConf.Env, stat),
		}
	}

	bkResult, bkErr := state.cli.QueryBlueking()
	if bkConfigured && bkErr != nil {
		logger.Warnf("failed to query blueking ntp: %v", bkErr)
	}
	bkSuccess := bkConfigured && bkErr == nil && bkResult != nil

	// 本地偏移与蓝鲸查询相互独立：本地源查询成功就上报偏移，蓝鲸成败只影响蓝鲸指标。
	collectedAt := time.Now()
	localItems := buildLocalNTPMetrics(taskConf.Env, details, collectedAt)
	if len(localItems) > 0 {
		e <- &BatchEvent{
			BizID:       bizID,
			DataID:      dataID,
			Labels:      labels,
			TimeSeconds: collectedAt.Unix(),
			Data:        localItems,
		}
	}

	// 蓝鲸指标的时间轴使用蓝鲸 NTP 时间（T_blue）；未配置或查询失败时回落本地采集时间。
	bkTime := collectedAt
	if bkSuccess {
		bkTime = bkResult.TBlue
	}
	bkItems := buildBkMetrics(taskConf.Env, taskConf.BkNTPSource, bkResult, bkConfigured, bkTime)
	if len(bkItems) > 0 {
		e <- &BatchEvent{
			BizID:       bizID,
			DataID:      dataID,
			Labels:      labels,
			TimeSeconds: bkTime.Unix(),
			Data:        bkItems,
		}
	}
}

type Metrics struct {
	Metrics   map[string]float64
	Target    string
	Timestamp int64
	Dimension map[string]string
}

func (ms Metrics) AsMapStr() common.MapStr {
	return common.MapStr{
		"metrics":   ms.Metrics,
		"target":    ms.Target,
		"timestamp": ms.Timestamp,
		"dimension": ms.Dimension,
	}
}

func stats2Metrics(env string, stat *Stat) *Metrics {
	named := func(s string) string {
		return env + "_" + s
	}

	// Count == 0 时没有有效样本，Min/Max 仍是初始哨兵值（MaxFloat64 / -MaxFloat64），
	// 不能作为指标发出，统一置 0，由 timesync_query_count == 0 表达"无数据"。
	var minSeconds, maxSeconds, avg float64
	if stat.Count > 0 {
		minSeconds = stat.Min
		maxSeconds = stat.Max
		avg = stat.Sum / float64(stat.Count)
	}

	metrics := map[string]float64{
		named("timesync_query_seconds_min"): minSeconds,
		named("timesync_query_seconds_max"): maxSeconds,
		named("timesync_query_seconds_avg"): avg,
		named("timesync_query_count"):       float64(stat.Count),
		named("timesync_query_err"):         float64(stat.Err),
	}

	return &Metrics{
		Metrics:   metrics,
		Target:    stat.Source,
		Timestamp: time.Now().UnixMilli(),
		Dimension: hostDimensions(env),
	}
}

func hostDimensions(env string) map[string]string {
	dims := map[string]string{}
	if env == "host" {
		info, _ := gse.GetAgentInfo()
		dims = tasks.HostDimension(info)
	}
	return dims
}

// buildLocalNTPMetrics 按地址组装新增 NTP 指标。
// 每个已查询到的地址都会生成一条数据点：成功报 offset + available=1（真实 0 偏差照常上报），
// 失败只报 available=0。时间戳优先使用时间源自己的时间，缺少源时间时回落本地采集时间。
func buildLocalNTPMetrics(env string, details []SourceResult, fallback time.Time) []Metrics {
	items := make([]Metrics, 0, len(details))
	for _, detail := range details {
		metrics := map[string]float64{}
		if detail.Available {
			metrics["available"] = 1
			metrics["offset"] = detail.OffsetSeconds
		} else {
			metrics["available"] = 0
		}

		ts := detail.SourceTime
		if ts.IsZero() {
			ts = fallback
		}

		dims := hostDimensions(env)
		dims["time_source"] = detail.Address
		items = append(items, Metrics{
			Metrics:   metrics,
			Target:    detail.Target,
			Timestamp: ts.UnixMilli(),
			Dimension: dims,
		})
	}
	return items
}

// buildBkMetrics 组装蓝鲸 NTP 指标。未配置蓝鲸源时不生成任何数据点；
// 配置后查询成功时生成偏差和可用性，查询失败时只生成可用性 0。
// 数据点时间戳使用蓝鲸 NTP 时间（T_blue）；查询失败时由调用方传入本地采集时间。
func buildBkMetrics(env, bkSource string, bkResult *BkResult, bkConfigured bool, ts time.Time) []Metrics {
	if !bkConfigured {
		return nil
	}

	metrics := map[string]float64{}
	dims := hostDimensions(env)
	dims["time_source"] = bkSource

	if bkResult != nil {
		metrics["bk_offset"] = bkResult.OffsetSeconds
		metrics["bk_available"] = 1
	} else {
		metrics["bk_available"] = 0
	}

	return []Metrics{{
		Metrics:   metrics,
		Target:    "ntpd",
		Timestamp: ts.UnixMilli(),
		Dimension: dims,
	}}
}

type Event struct {
	BizID  int32
	DataID int32
	Labels []map[string]string
	Data   *Metrics
}

func (e *Event) GetType() string {
	return define.ModuleTimeSync
}

func (e *Event) IgnoreCMDBLevel() bool {
	return true
}

func (e *Event) AsMapStr() common.MapStr {
	ts := time.Now().Unix()
	if len(e.Labels) == 0 {
		return common.MapStr{
			"dataid":    e.DataID,
			"data":      []map[string]interface{}{e.Data.AsMapStr()},
			"time":      ts,
			"timestamp": ts,
		}
	}

	lbs := e.Labels[0] // 只会有一个元素
	for k, v := range lbs {
		if _, ok := e.Data.Dimension[k]; ok {
			e.Data.Dimension["exported_"+k] = v
			continue
		}
		e.Data.Dimension[k] = v
	}
	return common.MapStr{
		"dataid":    e.DataID,
		"data":      []map[string]interface{}{e.Data.AsMapStr()},
		"time":      ts,
		"timestamp": ts,
	}
}

type BatchEvent struct {
	BizID       int32
	DataID      int32
	Labels      []map[string]string
	TimeSeconds int64
	Data        []Metrics
}

func (e *BatchEvent) GetType() string {
	return define.ModuleTimeSync
}

func (e *BatchEvent) IgnoreCMDBLevel() bool {
	return true
}

func (e *BatchEvent) AsMapStr() common.MapStr {
	if len(e.Labels) > 0 {
		lbs := e.Labels[0] // 只会有一个元素
		for i := range e.Data {
			item := &e.Data[i]
			if item.Dimension == nil {
				item.Dimension = make(map[string]string)
			}
			for k, v := range lbs {
				if _, ok := item.Dimension[k]; ok {
					item.Dimension["exported_"+k] = v
					continue
				}
				item.Dimension[k] = v
			}
		}
	}

	data := make([]map[string]interface{}, 0, len(e.Data))
	for i := range e.Data {
		data = append(data, e.Data[i].AsMapStr())
	}
	return common.MapStr{
		"dataid":    e.DataID,
		"data":      data,
		"time":      e.TimeSeconds,
		"timestamp": e.TimeSeconds,
	}
}

type Stat struct {
	Source string
	Min    float64
	Max    float64
	Avg    float64
	Sum    float64
	Err    int
	Count  int
}

// newStat 构造统计对象。Min/Max 初值取 +MaxFloat64 / -MaxFloat64，
// 保证首个样本（含负偏移，即本地时钟超前 NTP 源的场景）也能正确更新 Max。
// 历史实现 Max 初值为 0，导致一次采集内偏移全为负时 Max 恒为 0。
func newStat(source string) *Stat {
	return &Stat{
		Source: source,
		Min:    math.MaxFloat64,
		Max:    -math.MaxFloat64,
	}
}

// Add 累加一个有效偏移样本（秒，带符号），并维护 Count/Sum/Min/Max。
func (s *Stat) Add(v float64) {
	s.Count++
	s.Sum += v
	if v > s.Max {
		s.Max = v
	}
	if v < s.Min {
		s.Min = v
	}
}

type Option struct {
	NtpdPath    string
	ChronyAddr  string
	BkNTPSource string
	Timeout     time.Duration
}
