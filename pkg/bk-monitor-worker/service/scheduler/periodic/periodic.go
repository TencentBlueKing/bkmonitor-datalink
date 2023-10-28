// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package periodic

import (
	"context"
	"sync"

	metadataTask "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/worker"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type PeriodicTask struct {
	Cron    string
	Handler processor.HandlerFunc
	Payload []byte
}

var (
	refreshTsMetric       = "periodic:metadata:refresh_ts_metric"
	refreshEventDimension = "periodic:metadata:refresh_event_dimension"

	periodicTasksDefine = map[string]PeriodicTask{
		refreshTsMetric: {
			Cron:    "*/2 * * * *",
			Handler: metadataTask.RefreshTimeSeriesMetric,
		},
		refreshEventDimension: {
			Cron:    "*/3 * * * *",
			Handler: metadataTask.RefreshEventDimension,
		},
	}
)

var (
	initPeriodicTaskOnce sync.Once
)

func GetPeriodicTaskMapping() map[string]PeriodicTask {
	initPeriodicTaskOnce.Do(func() {
		// TODO Synchronize scheduled tasks from redis
	})
	return periodicTasksDefine
}

type PeriodicTaskScheduler struct {
	scheduler *worker.Scheduler

	// fullTaskMapping Contains the tasks defined in the code + the tasks defined in redis.
	fullTaskMapping map[string]PeriodicTask

	ctx context.Context
}

func (p *PeriodicTaskScheduler) Run() {
	for taskName, config := range p.fullTaskMapping {
		opts := []task.Option{
			task.TaskID(taskName),
		}

		taskInstance := task.NewTask(taskName, config.Payload, opts...)
		entryId, err := p.scheduler.Register(
			config.Cron,
			taskInstance,
			task.TaskID(taskName),
		)
		if err != nil {
			logger.Errorf("Failed to register scheduled task: %s. error: %s", taskName, err)
		} else {
			logger.Infof("Scheduled task: %s was registered, Cron: %s, entryId: %s", taskName, config.Cron, entryId)
		}
	}

	if err := p.scheduler.Run(); err != nil {
		logger.Errorf("Failed to start scheduler, periodic task may not actually be executed, error: %s", err)
	}
}

func NewPeriodicTaskScheduler(ctx context.Context) *PeriodicTaskScheduler {
	scheduler := worker.NewScheduler(ctx, worker.SchedulerOpts{})
	taskMapping := GetPeriodicTaskMapping()
	return &PeriodicTaskScheduler{scheduler: scheduler, fullTaskMapping: taskMapping, ctx: ctx}
}
