// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package selfstats

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/elastic/beats/libbeat/common"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

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

type Gather struct {
	running atomic.Bool
	config  *configs.ShellHistoryConfig
	tasks.BaseTask
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

	metrics, err := prometheus.DefaultGatherer.Gather()
	if err != nil {

	}

	for i := 0; i < len(metrics); i++ {
		metric := metrics[i]
		metric.Metric[i].Histogram
	}

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
