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
	"fmt"
	"strconv"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/output/gse"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Gather struct {
	tasks.BaseTask
	cli *Client
}

func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.Init()

	taskConf := taskConfig.(*configs.TimeSyncConfig)
	gather.cli = NewClient(&Option{
		NtpdPath:   taskConf.NtpdPath,
		ChronyAddr: taskConf.ChronyAddress,
		Timeout:    taskConf.QueryTimeout,
	})

	return gather
}

func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	g.PreRun(ctx)
	defer g.PostRun(ctx)

	stat, err := g.cli.Query()
	if err != nil {
		logger.Errorf("failed to query stats: %v", err)
		return
	}

	taskConf := g.TaskConfig.(*configs.TimeSyncConfig)

	e <- &Event{
		BizID:  g.TaskConfig.GetBizID(),
		DataID: g.TaskConfig.GetDataID(),
		Labels: g.TaskConfig.GetLabels(),
		Data:   stats2Metrics(taskConf.Env, stat),
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

	var avg float64
	if stat.Count > 0 {
		avg = stat.Sum / float64(stat.Count)
	}

	metrics := map[string]float64{
		named("timesync_query_seconds_min"): stat.Min,
		named("timesync_query_seconds_max"): stat.Max,
		named("timesync_query_seconds_avg"): avg,
		named("timesync_query_count"):       float64(stat.Count),
		named("timesync_query_err"):         float64(stat.Err),
	}

	dims := map[string]string{}
	if env == "host" {
		info, _ := gse.GetAgentInfo()
		dims = map[string]string{
			"bk_cloud_id":  strconv.Itoa(int(info.Cloudid)),
			"bk_target_ip": info.IP,
			"bk_agent_id":  info.BKAgentID,
			"bk_host_id":   strconv.Itoa(int(info.HostID)),
			"bk_biz_id":    strconv.Itoa(int(info.BKBizID)),
			"node_id":      fmt.Sprintf("%d:%s", info.Cloudid, info.IP),
			"hostname":     info.Hostname,
		}
	}
	return &Metrics{
		Metrics:   metrics,
		Target:    stat.Source,
		Timestamp: time.Now().UnixMilli(),
		Dimension: dims,
	}
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

type Stat struct {
	Source string
	Min    float64
	Max    float64
	Avg    float64
	Sum    float64
	Err    int
	Count  int
}

type Option struct {
	NtpdPath   string
	ChronyAddr string
	Timeout    time.Duration
}
