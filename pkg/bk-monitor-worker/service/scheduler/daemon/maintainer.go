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
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	jsoniter "github.com/json-iterator/go"

	rdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/broker/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type runningBinding struct {
	errorReceiveChan chan error
	TaskBinding
	retryCount    int
	startTime     time.Time
	lastRetryTime time.Time
	nextRetryTime time.Time
	baseCtx       context.Context
	baseCtxCancel context.CancelFunc
	retryValid    bool
}

type RunMaintainerOptions struct {
	checkInterval         time.Duration
	RetryTolerateCount    int
	RetryTolerateInterval time.Duration
	RetryIntolerantFactor int
}

type RunMaintainer struct {
	ctx    context.Context
	config RunMaintainerOptions

	listenWorkerId string
	listenTaskKey  string

	redisClient           redis.UniversalClient
	methodOperatorMapping map[string]Operator
	runningInstance       *sync.Map
}

func (r *RunMaintainer) Run() {
	logger.Infof(
		"\nDaemonTask maintainer started. "+
			"\n - \t workerId: %s \n - \t listen: %s \n - \t queue: %s \n - \t interval: %s\n",
		r.listenWorkerId, r.listenWorkerId, r.listenTaskKey, r.config.checkInterval,
	)
	taskMarkMapping := make(map[string]bool)
loop:
	for {
		select {
		case <-r.ctx.Done():
			logger.Infof("DaemonTask maintainer stopped.")
			break loop
		default:
			currentTask := make(map[string]bool)
			taskHash, err := r.redisClient.HGetAll(r.ctx, r.listenTaskKey).Result()
			if err != nil {
				logger.Errorf("DaemonTask maintainer(%s) check %s change failed. error: %s", r.listenWorkerId, r.listenTaskKey, err)
				goto cont
			}

			for taskUniId, taskStr := range taskHash {
				var taskBinding TaskBinding
				if err = jsoniter.Unmarshal([]byte(taskStr), &taskBinding); err != nil {
					logger.Errorf(
						"failed to parse value to TaskBinding on key: %s field: %s. "+
							"error: %s", r.listenTaskKey, taskUniId, err,
					)
					continue
				}

				currentTask[taskBinding.UniId] = true
				if !taskMarkMapping[taskBinding.UniId] {
					r.handleAddTaskBinding(taskBinding)
				}
			}

			for t := range taskMarkMapping {
				if !currentTask[t] {
					r.handleDeleteTaskBinding(t)
				}
			}

			taskMarkMapping = currentTask
		cont:
			time.Sleep(r.config.checkInterval)
		}
	}
}

func (r *RunMaintainer) handleAddTaskBinding(taskBinding TaskBinding) {
	define, exist := r.methodOperatorMapping[taskBinding.Kind]
	if !exist {
		logger.Errorf("Failed to run method: %s which not exist in task defines", taskBinding.Kind)
		return
	}

	errorReceiveChan := make(chan error, 1)
	// pass the context of the running instance for upper-level management
	runInstanceCtx, runInstanceCtxCancel := context.WithCancel(r.ctx)
	go define.Start(runInstanceCtx, errorReceiveChan, taskBinding.Payload)
	now := time.Now()
	binding := &runningBinding{
		baseCtx:          runInstanceCtx,
		baseCtxCancel:    runInstanceCtxCancel,
		errorReceiveChan: errorReceiveChan,
		TaskBinding:      taskBinding,
		retryCount:       0,
		startTime:        now,
		retryValid:       false,
	}
	r.runningInstance.Store(taskBinding.UniId, binding)
	go r.listenRunningState(taskBinding.UniId, errorReceiveChan)
	logger.Infof(
		"Binding(%s <------> %s) is discovered, task is started, payload: %s",
		taskBinding.UniId, r.listenWorkerId, taskBinding.Payload,
	)
}

func (r *RunMaintainer) listenRunningState(taskUniId string, errorReceiveChan chan error) {
	retryTicker := &time.Ticker{}

	errorChan := errorReceiveChan
	for {
		select {
		case receiveErr, isOpen := <-errorChan:
			if !isOpen {
				logger.Infof("errorReceiveChan close, return")
				return
			}
			fmt.Printf("%s", receiveErr)
			v, _ := r.runningInstance.Load(taskUniId)
			rB := v.(*runningBinding)
			rB.baseCtxCancel()

			rB.retryCount++
			rB.lastRetryTime = time.Now()

			var nextRetryTime time.Duration
			if rB.retryCount < r.config.RetryTolerateCount {
				nextRetryTime = r.config.RetryTolerateInterval
			} else {
				nextRetryTime = r.config.RetryTolerateInterval * time.Duration(1<<(rB.retryCount-r.config.RetryIntolerantFactor))
			}
			rB.nextRetryTime = time.Now().Add(nextRetryTime)
			rB.errorReceiveChan = make(chan error, 1)
			newCtx, newCancel := context.WithCancel(r.ctx)
			rB.baseCtx = newCtx
			rB.baseCtxCancel = newCancel
			rB.retryValid = true
			r.runningInstance.Store(taskUniId, rB)
			// The place write to errorReceiveChan contains timeout processing,
			// so the reference will be GC after reassignment
			errorChan = rB.errorReceiveChan

			logger.Warnf(
				"[FAILED RETRY] ERROR: %s. Task: %s, %d retry failed. "+
					"The retry time of the next attempt is: %s, (%.2f seconds later)",
				receiveErr, taskUniId, rB.retryCount, rB.nextRetryTime, nextRetryTime.Seconds(),
			)
			retryTicker = time.NewTicker(nextRetryTime)
		case <-retryTicker.C:
			v, _ := r.runningInstance.Load(taskUniId)
			rB := v.(*runningBinding)
			if rB.retryValid {
				define, _ := r.methodOperatorMapping[rB.TaskBinding.Kind]
				go define.Start(rB.baseCtx, rB.errorReceiveChan, rB.SerializerTask.Payload)
				logger.Infof("[FAILED RETRY] Task: %s retry performed", taskUniId)
				retryTicker = &time.Ticker{}
			}
		case <-r.ctx.Done():
			logger.Infof("[RetryListen] stopped.")
			retryTicker.Stop()
			v, _ := r.runningInstance.Load(taskUniId)
			rB := v.(*runningBinding)
			rB.baseCtxCancel()
			close(errorChan)
			return
		}
	}
}

func (r *RunMaintainer) handleDeleteTaskBinding(taskUniId string) {
	ins, exist := r.runningInstance.Load(taskUniId)
	if !exist {
		logger.Errorf("Attempt to delete a task: %s that is not executing", taskUniId)
		return
	}
	rB := ins.(*runningBinding)
	rB.baseCtxCancel()
	close(rB.errorReceiveChan)
	r.runningInstance.Delete(taskUniId)
	logger.Infof("Binding runInstance removed. taskUniId: %s", taskUniId)
}

func NewDaemonTaskRunMaintainer(ctx context.Context, workerId string) *RunMaintainer {

	operatorMapping := make(map[string]Operator, len(taskDefine))

	for taskKind, define := range taskDefine {
		op, err := define.initialFunc(ctx)
		if err != nil {
			logger.Errorf(
				"[!WARNING!] Task: %s implementation initialization failed, "+
					"this task type will not be executed! error: %s",
				taskKind, err,
			)
			continue
		}
		operatorMapping[taskKind] = op
	}

	options := RunMaintainerOptions{
		checkInterval:         config.WorkerDaemonTaskMaintainerInterval,
		RetryTolerateCount:    config.WorkerDaemonTaskRetryTolerateCount,
		RetryTolerateInterval: config.WorkerDaemonTaskRetryTolerateInterval,
		RetryIntolerantFactor: config.WorkerDaemonTaskRetryIntolerantFactor,
	}

	return &RunMaintainer{
		ctx:                   ctx,
		config:                options,
		listenWorkerId:        workerId,
		listenTaskKey:         common.DaemonBindingWorker(workerId),
		redisClient:           rdb.GetRDB().Client(),
		methodOperatorMapping: operatorMapping,
		runningInstance:       &sync.Map{},
	}
}
