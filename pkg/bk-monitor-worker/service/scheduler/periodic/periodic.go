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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	cmESTask "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/clustermetrics/es"
	cmInfluxdbTask "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/clustermetrics/influxdb"
	metadataTask "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/relation"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/worker"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type PeriodicTask struct {
	Cron    string
	Handler processor.HandlerFunc
	Payload []byte
	Option  []task.Option
}

// NOTE: 周期任务添加 peyload 的动态支持
// NOTE: 后续增加针对不同的任务，使用不同的调度器
func getPeriodicTasks() map[string]PeriodicTask {
	refreshTsMetric := "periodic:metadata:refresh_ts_metric"
	refreshDatasource := "periodic:metadata:refresh_datasource"
	RefreshKafkaTopicInfo := "periodic:metadata:refresh_kafka_topic_info"
	ReportInfluxdbClusterMetrics := "periodic:cluster_metrics:report_influxdb"
	PushAndPublishSpaceRouterInfo := "periodic:cluster_metrics:push_and_publish_space_router_info"
	ReportESClusterMetrics := "periodic:cluster_metrics:report_es"
	ClearDeprecatedRedisKey := "periodic:metadata:clear_deprecated_redis_key"
	CleanDataIdConsulPath := "periodic:metadata:clean_data_id_consul_path"

	SloPush := "periodic:metadata:slo_push"

	ReportCustomRelation := "periodic:relation:report_custom_resource_relation"

	return map[string]PeriodicTask{
		refreshTsMetric: {
			Cron:    "*/5 * * * *",
			Handler: metadataTask.RefreshTimeSeriesMetric,
			Option:  []task.Option{task.Timeout(10 * time.Minute)},
		},
		refreshDatasource: {
			Cron:    "*/20 * * * *",
			Handler: metadataTask.RefreshDatasource,
			Option:  []task.Option{task.Timeout(40 * time.Minute)},
		},
		RefreshKafkaTopicInfo: {
			Cron:    "*/10 * * * *",
			Handler: metadataTask.RefreshKafkaTopicInfo,
			Option:  []task.Option{task.Timeout(20 * time.Minute)},
		},
		ReportInfluxdbClusterMetrics: {
			Cron:    "*/1 * * * *",
			Handler: cmInfluxdbTask.ReportInfluxdbClusterMetric,
			Option:  []task.Option{task.Timeout(2 * time.Minute)},
		},
		PushAndPublishSpaceRouterInfo: {
			Cron:    "*/15 * * * *",
			Handler: metadataTask.PushAndPublishSpaceRouterInfo,
			Option:  []task.Option{task.Queue(cfg.BigResourceTaskQueueName), task.Timeout(30 * time.Minute)},
		},
		ReportESClusterMetrics: {
			Cron:    "*/1 * * * *",
			Handler: cmESTask.ReportESClusterMetrics,
			Option:  []task.Option{task.Queue(cfg.ESClusterMetricQueueName), task.Timeout(5 * time.Minute)},
		},
		ClearDeprecatedRedisKey: {
			Cron:    "0 0 */14 * *",
			Handler: metadataTask.ClearDeprecatedRedisKey,
			Option:  []task.Option{task.Timeout(24 * time.Hour)},
		},
		CleanDataIdConsulPath: {
			Cron:    "0 2 * * *", // 每天凌晨2点执行
			Handler: metadataTask.CleanDataIdConsulPath,
			Option:  []task.Option{task.Timeout(2 * time.Hour)},
		},
		SloPush: {
			Cron:    "*/5 * * * *",
			Handler: metadataTask.SloPush,
			Option:  []task.Option{task.Timeout(10 * time.Minute)},
		},
		ReportCustomRelation: {
			Cron:    "*/1 * * * *",
			Handler: relation.ReportCustomRelation,
			Option:  []task.Option{task.Timeout(2 * time.Minute)},
		},
	}
}

var initPeriodicTaskOnce sync.Once

func GetPeriodicTaskMapping() map[string]PeriodicTask {
	initPeriodicTaskOnce.Do(func() {
		// TODO Synchronize scheduled tasks from redis
	})
	return getPeriodicTasks()
}

type PeriodicTaskScheduler struct {
	scheduler *worker.Scheduler

	// fullTaskMapping Contains the tasks defined in the code + the tasks defined in redis.
	fullTaskMapping map[string]PeriodicTask

	ctx context.Context
}

func (p *PeriodicTaskScheduler) Run() {
	for taskName, config := range p.fullTaskMapping {
		opts := config.Option
		// 添加 task id
		opts = append(opts, task.TaskID(taskName))
		// NOTE: 现阶段所有任务设置默认全局唯一
		uniqueTTLExist := false
		for _, opt := range opts {
			if opt.Type() == task.UniqueOpt {
				uniqueTTLExist = true
				break
			}
		}
		// 如果不存在配置，则添加
		if uniqueTTLExist == false {
			opts = append(opts, task.Unique(common.DefaultUniqueTTL))
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

func NewPeriodicTaskScheduler(ctx context.Context) (*PeriodicTaskScheduler, error) {
	scheduler, err := worker.NewScheduler(ctx, worker.SchedulerOpts{})
	if err != nil {
		return nil, err
	}
	taskMapping := GetPeriodicTaskMapping()
	return &PeriodicTaskScheduler{scheduler: scheduler, fullTaskMapping: taskMapping, ctx: ctx}, nil
}
