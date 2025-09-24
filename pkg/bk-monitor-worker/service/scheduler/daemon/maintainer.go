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
	"encoding/json"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"

	rdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/broker/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/errors"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type runningBinding struct {
	errorReceiveChan chan error
	TaskBinding
	retryCount    int
	reloadCount   int
	startTime     time.Time
	lastRetryTime time.Time
	nextRetryTime time.Time
	baseCtx       context.Context
	baseCtxCancel context.CancelFunc
	retryValid    bool

	stateCheckerCtx    context.Context
	stateCheckerCancel context.CancelFunc
}

type RunMaintainerOptions struct {
	checkInterval      time.Duration
	RetryTolerateCount int
}

type RunMaintainer struct {
	ctx    context.Context
	config RunMaintainerOptions

	listenWorkerId  string
	listenTaskKey   string
	listenReloadKey string

	redisClient           redis.UniversalClient
	methodOperatorMapping map[string]Operator
	runningInstance       *sync.Map
}

// ReloadSignal The error type of the overload request, which will be sent to errorreceivechan
type ReloadSignal struct{}

func (e ReloadSignal) Error() string {
	return "reload-signal"
}

func (r *RunMaintainer) Run() {
	go r.listenReloadSignal()

	logger.Infof(
		"\nDaemonTask maintainer started. "+
			"\n - \t workerId: %s \n - \t listen: %s \n - \t queue: %s \n - \t interval: %s\n",
		r.listenWorkerId, r.listenWorkerId, r.listenTaskKey, r.config.checkInterval,
	)
	taskMarkMapping := make(map[string]bool)
	ticker := time.NewTicker(r.config.checkInterval)

	for {
		select {
		case <-r.ctx.Done():
			logger.Infof("DaemonTask maintainer stopped.")
			ticker.Stop()
			return
		case <-ticker.C:
			currentTask := make(map[string]bool)
			taskHash, err := r.redisClient.HGetAll(r.ctx, r.listenTaskKey).Result()
			if err != nil {
				logger.Errorf("DaemonTask maintainer(%s) check %s change failed. error: %s", r.listenWorkerId, r.listenTaskKey, err)
				continue
			}

			for taskUniId, taskStr := range taskHash {
				var taskBinding TaskBinding
				if err = jsonx.Unmarshal([]byte(taskStr), &taskBinding); err != nil {
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
	checkerCtx, checkerCancel := context.WithCancel(r.ctx)
	now := time.Now()
	binding := &runningBinding{
		baseCtx:          runInstanceCtx,
		baseCtxCancel:    runInstanceCtxCancel,
		errorReceiveChan: errorReceiveChan,
		TaskBinding:      taskBinding,
		retryCount:       0,
		reloadCount:      0,
		startTime:        now,
		retryValid:       false,

		stateCheckerCtx:    checkerCtx,
		stateCheckerCancel: checkerCancel,
	}
	r.runningInstance.Store(taskBinding.UniId, binding)
	go r.listenRunningState(taskBinding.UniId, errorReceiveChan, checkerCtx, define.GetTaskDimension(taskBinding.Payload))
	logger.Infof(
		"Binding(%s <------> %s) is discovered, task is started, payload: %s",
		taskBinding.UniId, r.listenWorkerId, taskBinding.Payload,
	)
}

func (r *RunMaintainer) listenRunningState(
	taskUniId string, errorReceiveChan chan error, lifeline context.Context, taskDimension string,
) {
	retryTicker := &time.Ticker{}
	errorChan := errorReceiveChan
	runningTicker := time.NewTicker(30 * time.Second)

	tolerateInterval := 10 * time.Second
	maxTolerateInterval := 15 * time.Minute

	for {
		select {
		case <-runningTicker.C:
			metrics.RecordDaemonTask(taskDimension)
		case receiveErr, isOpen := <-errorChan:
			if !isOpen {
				logger.Infof("errorReceiveChan close, return")
				return
			}
			v, _ := r.runningInstance.LoadAndDelete(taskUniId)
			rB := v.(*runningBinding)
			rB.baseCtxCancel()
			rB.lastRetryTime = time.Now()
			var nextRetryTime time.Duration

			if errors.Is(ReloadSignal{}, receiveErr) {
				rB.reloadCount++
				nextRetryTime = tolerateInterval
			} else {
				rB.retryCount++
				if rB.retryCount < r.config.RetryTolerateCount {
					nextRetryTime = tolerateInterval
				} else {
					nextRetryTime = maxTolerateInterval
				}
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
				"[RETRY] receive ERROR: %s. Task: %s, retryCount: %d reloadCount: %d "+
					"The retry time of the next attempt is: %s, (%.2f seconds later)",
				receiveErr, taskUniId, rB.retryCount, rB.reloadCount, rB.nextRetryTime, nextRetryTime.Seconds(),
			)
			if retryTicker != nil {
				retryTicker.Stop()
			}
			retryTicker = time.NewTicker(nextRetryTime)
		case <-retryTicker.C:
			v, _ := r.runningInstance.Load(taskUniId)
			rB := v.(*runningBinding)
			if rB.retryValid {
				define, _ := r.methodOperatorMapping[rB.TaskBinding.Kind]
				go define.Start(rB.baseCtx, rB.errorReceiveChan, rB.SerializerTask.Payload)
				logger.Infof(
					"\n!!![RETRY]!!! Task: %s retry performed.\n-----\nParams: %s\n-----\n",
					taskUniId, rB.SerializerTask.Payload,
				)
				if retryTicker != nil {
					retryTicker.Stop()
				}
				retryTicker = &time.Ticker{}
				metrics.RecordDaemonTaskRetryCount(taskDimension)
			}
		case <-r.ctx.Done():
			logger.Infof("[RetryListen] receive root context done singal, stopped and return")
			retryTicker.Stop()
			v, _ := r.runningInstance.LoadAndDelete(taskUniId)
			rB, ok := v.(*runningBinding)
			if ok {
				rB.baseCtxCancel()
				rB.stateCheckerCancel()
				logger.Warnf("[RetryListen] runningBinding still in mapping! canceled")
			}
			return
		case <-lifeline.Done():
			logger.Infof("[RetryListen] receive lifeline context done singal, stopped and return")
			retryTicker.Stop()
			v, _ := r.runningInstance.LoadAndDelete(taskUniId)
			rB, ok := v.(*runningBinding)
			if ok {
				rB.baseCtxCancel()
				logger.Warnf("[RetryListen] runningBinding still in mapping! canceled")
			}
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
	rB.stateCheckerCancel()
	close(rB.errorReceiveChan)
	r.runningInstance.Delete(taskUniId)
	logger.Infof("Binding runInstance removed. taskUniId: %s", taskUniId)
}

func (r *RunMaintainer) listenReloadSignal() {
	logger.Infof(
		"\nDaemonTask maintainer listen reload signal. "+
			"\n - \t workerId: %s \n - \t listen: %s \n - \t queue: %s \n - \t interval: %s\n",
		r.listenWorkerId, r.listenWorkerId, r.listenReloadKey, r.config.checkInterval,
	)

	sub := rdb.GetRDB().Client().Subscribe(r.ctx, r.listenReloadKey)
	ch := sub.Channel()
	for {
		select {
		case <-r.ctx.Done():
			sub.Close()
			logger.Infof("[ReloadSignalListener] receive lifeline context done singal, stopped and return")
			return
		case msg := <-ch:
			bindingStr := msg.Payload
			var binding TaskBinding
			if err := json.Unmarshal([]byte(bindingStr), &binding); err != nil {
				logger.Errorf(
					"[listenReloadSignal] "+
						"Failed to unmarshal channel:%s data to binding, data: %s, error: %s",
					r.listenReloadKey, bindingStr, err,
				)
				continue
			}
			r.handleReloadBinding(binding)
		}
	}
}

func (r *RunMaintainer) handleReloadBinding(taskBinding TaskBinding) {
	v, exist := r.runningInstance.Load(taskBinding.UniId)
	if !exist {
		logger.Errorf(
			"[handleReloadBinding] receive taskUniId: %s reload request, "+
				"but not in runningInstance!, ignored", taskBinding.UniId,
		)
		return
	}
	runningInstance := v.(*runningBinding)
	runningInstance.TaskBinding = taskBinding
	runningInstance.errorReceiveChan <- ReloadSignal{}
	logger.Infof(
		"[handleReloadBinding] send reload signal to errorReceiveChan, "+
			"taskUniId: %s errorReceiveChan", runningInstance.UniId,
	)
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
		checkInterval:      config.WorkerDaemonTaskMaintainerInterval,
		RetryTolerateCount: config.WorkerDaemonTaskRetryTolerateCount,
	}

	return &RunMaintainer{
		ctx:                   ctx,
		config:                options,
		listenWorkerId:        workerId,
		listenTaskKey:         common.DaemonBindingWorker(workerId),
		listenReloadKey:       common.DaemonReloadExecQueue(workerId),
		redisClient:           rdb.GetRDB().Client(),
		methodOperatorMapping: operatorMapping,
		runningInstance:       &sync.Map{},
	}
}
