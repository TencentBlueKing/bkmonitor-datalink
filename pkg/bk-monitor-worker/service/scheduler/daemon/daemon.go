// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package daemon

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"

	rdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/broker/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/cmdbcache"
	apmTasks "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type Runnable interface {
	start()
}

type Watcher interface {
	Runnable

	handleAddTask(watchTaskMark)
	handleDeleteTask(watchTaskMark)

	handleAddWorker(watchWorkerMark)
	handleDeleteWorker(string, watchWorkerMark)
}

type Numerator interface {
	Runnable
}

type Operator interface {
	Start(runInstanceCtx context.Context, errorReceiveChan chan<- error, payload []byte)
	// GetTaskDimension get the metric dimension of the daemon task,
	// and the maintainer will report the metric regularly.
	GetTaskDimension(payload []byte) string
}

type OperatorDefine struct {
	handler     func() Operator
	initialFunc func(ctx context.Context) (Operator, error)
}

var taskDefine = map[string]OperatorDefine{
	"daemon:apm:pre_calculate": {initialFunc: func(ctx context.Context) (Operator, error) {
		op, err := apmTasks.Initial(ctx)
		if err != nil {
			return nil, err
		}
		runSuccessChan := make(chan error, 1)
		go op.Run(runSuccessChan)
		runErr := <-runSuccessChan
		close(runSuccessChan)
		if runErr != nil {
			return nil, errors.New(fmt.Sprintf("apm.pre_calculate failed to initial, error: %s", runErr))
		}
		return op, err
	}},
	"daemon:alarm:cmdb_resource_watch": {initialFunc: func(ctx context.Context) (Operator, error) {
		logger.Info("cmdb_resource_watch daemon task is initialized")
		return &cmdbcache.ResourceWatchDaemon{}, nil
	}},
	"daemon:alarm:cmdb_cache_refresh": {initialFunc: func(ctx context.Context) (Operator, error) {
		logger.Info("cmdb_cache_refresh daemon task is initialized")
		return &cmdbcache.CacheRefreshDaemon{}, nil
	}},
}

var daemonTaskDimensionOperatorMapping map[string]func(payload []byte) string

// getDimensionOperator because we need to get the dimension of the daemonTask to compute the taskUniId.
// And the taskUniId must be not change when daemonTask update
func getDimensionOperator(kind string) func(payload []byte) string {
	if daemonTaskDimensionOperatorMapping != nil {
		return daemonTaskDimensionOperatorMapping[kind]
	}
	daemonTaskDimensionOperatorMapping = make(map[string]func(payload []byte) string)
	for k, item := range taskDefine {
		// mock initial
		op, _ := item.initialFunc(context.Background())
		daemonTaskDimensionOperatorMapping[k] = op.GetTaskDimension
	}

	return daemonTaskDimensionOperatorMapping[kind]
}

type TaskScheduler struct {
	ctx context.Context

	watcher   Watcher
	numerator Numerator
}

func (d *TaskScheduler) Run() {
	d.watcher.start()
	d.numerator.start()

	for {
		select {
		case <-d.ctx.Done():
			logger.Info("Scheduler received the termination signal, stopped.")
			return
		}
	}
}

func NewDaemonTaskScheduler(ctx context.Context) *TaskScheduler {
	watcher := NewDefaultWatcher(ctx)
	numerator := NewDefaultNumerator(ctx)

	return &TaskScheduler{
		ctx:       ctx,
		watcher:   watcher,
		numerator: numerator,
	}
}

func ComputeTaskUniId(task task.SerializerTask) string {
	taskFunc := getDimensionOperator(task.Kind)
	if taskFunc == nil {
		return ""
	}

	dimension := taskFunc(task.Payload)

	if dimension == "" {
		// 如果任务没有定义唯一维度 则取参数作为唯一维度来计算 Id
		return fmt.Sprintf("%s-%s", task.Kind, hex.EncodeToString(task.Payload))
	}

	// 如果有定义唯一维度 则取维度来计算 Id
	return fmt.Sprintf("%s-%s", task.Kind, hex.EncodeToString([]byte(dimension)))
}

func computeWorkerId(t task.SerializerTask) (string, error) {
	ctx := context.Background()

	redisClient := rdb.GetRDB().Client()
	var queue string
	if t.Options.Queue != "" {
		queue = t.Options.Queue
	} else {
		queue = common.DefaultQueueName
	}

	var res service.WorkerInfo
	// 获取此队列下的所有worker
	queueWorkerPrefix := fmt.Sprintf("%s*", common.WorkerKeyQueuePrefix(queue))
	keys, err := redisClient.Keys(ctx, queueWorkerPrefix).Result()
	if err != nil {
		return "", fmt.Errorf("failed to obtain the workers with the prefix: %s from redis. Task: %s will not be attempted to schedule until the next numerator check", queueWorkerPrefix, t.Kind)
	}
	if len(keys) == 0 {
		return "", fmt.Errorf("the list of workers with prefix: %s from redis is empty, is no worker listening to this queue: %s?. Task: %s will not be attempted to schedule until the next numerator check", queueWorkerPrefix, queue, t.Kind)
	}

	// TODO 从worker列表中选择worker进行调度 待补充更多的调度规则 目前暂时使用随机选择
	data, _ := redisClient.Get(ctx, keys[rand.Intn(len(keys))]).Bytes()
	if err = jsonx.Unmarshal(data, &res); err != nil {
		return "", fmt.Errorf("parse workerInfo failed. error: %s", err)
	}
	return res.Id, nil
}
