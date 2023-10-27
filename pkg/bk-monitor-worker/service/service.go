// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package service

import (
	"context"
	"sync"

	"github.com/gin-contrib/pprof"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/http"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	redisWatcher "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/watcher/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/worker"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	ginModePath           = "service.gin_mode"
	workerConcurrencyPath = "worker.concurrency"
)

func init() {
	viper.SetDefault(ginModePath, "release")
	// 默认为0，通过动态获取逻辑 cpu 核数
	viper.SetDefault(workerConcurrencyPath, 0)
}

func prometheusHandler() gin.HandlerFunc {
	ph := promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{Registry: metrics.Registry})

	return func(c *gin.Context) {
		ph.ServeHTTP(c.Writer, c.Request)
	}
}

// NewHTTPService new a http service
func NewHTTPService(enableApi bool) *gin.Engine {
	svr := gin.Default()
	gin.SetMode(viper.GetString(ginModePath))

	pprof.Register(svr)

	// 注册任务
	if enableApi {
		svr.POST("/task/", http.CreateTask)
	}

	// metrics
	svr.GET("/metrics", prometheusHandler())

	return svr
}

// NewWatcherService new a watcher service
func NewWatcherService(ctx context.Context) error {
	watcher := redisWatcher.NewWatcher(ctx, new(sync.WaitGroup))
	return watcher.Watch(ctx)
}

// NewWorkerService new a worker service
func NewWorkerService() (*worker.Worker, error) {
	// TODO: 暂时不指定队列
	w, err := worker.NewWorker(
		worker.WorkerConfig{
			Concurrency: viper.GetInt(workerConcurrencyPath),
		},
	)
	if err != nil {
		logger.Errorf("start a worker service error, %v", err)
		return w, err
	}
	// init async task handle
	mux := worker.NewServeMux()
	for p, h := range internal.RegisterTaskHandleFunc {
		mux.HandleFunc(p, h)
	}
	// init periodic task handler
	for p, h := range internal.RegisterPeriodicTaskHandlerFunc {
		mux.HandleFunc(p, h)
	}
	if err := w.Run(mux); err != nil {
		logger.Errorf("run worker error, %v", err)
		return w, err
	}
	return w, err
}

// NewPeriodicTaskService new a periodic task scheduler
func NewPeriodicTaskSchedulerService() error {
	scheduler, err := worker.NewScheduler(nil)
	if err != nil {
		return err
	}
	// init periodic task
	internal.InitPeriodicTask()

	pt := internal.GetRegisterPeriodicTaskDetail()
	pt.Range(func(name, detail interface{}) bool {
		// periodic task retry is 0
		retryOpt := task.MaxRetry(0)
		nameStr, ok := name.(string)
		if !ok {
			logger.Errorf("task: %v not string", name)
			return false
		}
		d, ok := detail.(map[string]interface{})
		if !ok {
			logger.Errorf("task: %s value not map[string]interface, value: %v", nameStr, detail)
			return false
		}
		cronSpec, ok := d["cronSpec"].(string)
		if !ok {
			logger.Errorf("task: %s value: %v, cronSpec not string", nameStr, detail)
			return false
		}
		periodicTask := task.NewPeriodicTask(cronSpec, nameStr, nil, retryOpt)
		entryID, err := scheduler.Register(periodicTask.CronSpec, periodicTask.Task, task.TaskID(nameStr))
		if err != nil {
			logger.Errorf("register task error, kind: %s, entry id: %d", periodicTask.Task.Kind, entryID)
			return false
		}
		return true
	})

	if err := scheduler.Run(); err != nil {
		logger.Errorf("start scheduler error, %v", err)
		return err
	}

	return nil
}

// NewController new a controller
func NewController() error {
	return nil
}
