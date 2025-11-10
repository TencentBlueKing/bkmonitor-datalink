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
	"time"

	redis "github.com/go-redis/redis/v8"

	rdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/broker/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/service/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/service/scheduler/periodic"
	commonUtils "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/worker"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type WorkerService struct {
	ctx context.Context

	worker *worker.Worker
	mux    *worker.WorkerMux

	maintainer *WorkerHealthMaintainer
}

func (w *WorkerService) Run() {
	for p, h := range scheduler.GetAsyncTaskMapping() {
		w.mux.HandleFunc(p, h.Handler)
	}
	for p, h := range periodic.GetPeriodicTaskMapping() {
		w.mux.HandleFunc(p, h.Handler)
	}
	err := w.worker.Run(w.mux)
	if err != nil {
		logger.Errorf("Failed to run the worker, task may not be executed. error: %s", err)
	}
	go w.maintainer.Start()
}

func (w *WorkerService) Stop() {
	w.worker.Shutdown()
}

func (w *WorkerService) GetWorkerId() string {
	return w.maintainer.id
}

func NewWorkerService(ctx context.Context, queues []string) (*WorkerService, error) {
	// todo support more configurations

	qs := make(map[string]int)
	if len(queues) > 0 {
		for i, q := range queues {
			qs[q] = i + 1
		}
	}

	w, err := worker.NewWorker(worker.WorkerConfig{
		Concurrency: config.WorkerConcurrency,
		BaseContext: func() context.Context { return ctx },
		Queues:      qs,
	})
	if err != nil {
		logger.Errorf("Failed to create worker. error: %s", err)
		return nil, err
	}
	maintainer, err := NewWorkerHealthMaintainer(ctx, queues)
	if err != nil {
		return nil, err
	}

	return &WorkerService{ctx: ctx, worker: w, mux: worker.NewServeMux(), maintainer: maintainer}, nil
}

type MaintainerOptions struct {
	checkInternal time.Duration
	infoTtl       time.Duration
}

type WorkerHealthMaintainer struct {
	ctx context.Context

	id          string
	queues      []string
	config      MaintainerOptions
	redisClient redis.UniversalClient
}

type WorkerInfo struct {
	Id        string
	StartTime time.Time
}

func (w *WorkerHealthMaintainer) Start() {
	ticker := time.NewTicker(w.config.checkInternal)

	logger.Infof("Worker starts with the Id: %s to enable periodic heartbeat reporting.", w.id)
	fixInfo := WorkerInfo{Id: w.id, StartTime: time.Now()}

	for {
		select {
		case <-ticker.C:
			data, _ := jsonx.Marshal(fixInfo)
			for _, queueName := range w.queues {
				workerKey := common.WorkerKey(queueName, w.id)
				w.redisClient.Set(w.ctx, workerKey, data, w.config.infoTtl)
			}
		case <-w.ctx.Done():
			logger.Infof("Worker health maintainer stopped.")
			ticker.Stop()
			return
		}
	}
}

func NewWorkerHealthMaintainer(ctx context.Context, queues []string) (*WorkerHealthMaintainer, error) {
	options := MaintainerOptions{
		checkInternal: config.WorkerHealthCheckInterval,
		infoTtl:       config.WorkerHealthCheckInfoDuration,
	}

	broker := rdb.GetRDB()

	return &WorkerHealthMaintainer{
		id:          commonUtils.GenerateProcessorId(),
		ctx:         ctx,
		config:      options,
		redisClient: broker.Client(),
		queues:      queues,
	}, nil
}

type WorkerTaskMaintainer struct {
	ctx context.Context
}
