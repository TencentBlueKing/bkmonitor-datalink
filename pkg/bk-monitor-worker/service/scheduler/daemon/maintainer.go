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

	redis "github.com/go-redis/redis/v8"

	rdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/broker/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/errors"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type runningBinding struct {
	ctx    context.Context
	cancel context.CancelFunc
	reload chan TaskBinding
	done   chan struct{}
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

	ctx, cancel := context.WithCancel(r.ctx)
	instance := &runningBinding{ctx: ctx, cancel: cancel, reload: make(chan TaskBinding), done: make(chan struct{})}
	r.runningInstance.Store(taskBinding.UniId, instance)
	go r.listenRunningState(instance, taskBinding, define)
	logger.Infof("Binding(%s <------> %s) is discovered, task is started, payload: %s",
		taskBinding.UniId, r.listenWorkerId, taskBinding.Payload)
}

func (r *RunMaintainer) listenRunningState(instance *runningBinding, binding TaskBinding, define Operator) {
	defer close(instance.done)
	defer r.runningInstance.CompareAndDelete(binding.UniId, instance)
	defer instance.cancel()

	taskDimension := define.GetTaskDimension(binding.Payload)
	runningTicker := time.NewTicker(30 * time.Second)
	defer runningTicker.Stop()

	start := func() (chan error, context.CancelFunc) {
		ctx, cancel := context.WithCancel(instance.ctx)
		errChan := make(chan error, 1)
		go define.Start(ctx, errChan, binding.Payload)
		return errChan, cancel
	}
	errChan, cancelAttempt := start()
	defer func() { cancelAttempt() }()

	var retryTimer *time.Timer
	var retry <-chan time.Time
	defer func() {
		if retryTimer != nil {
			retryTimer.Stop()
		}
	}()
	var retryCount, reloadCount int
	for instance.ctx.Err() == nil {
		var err error
		select {
		case <-instance.ctx.Done():
			return
		case <-runningTicker.C:
			metrics.RecordDaemonTask(taskDimension)
			continue
		case <-retry:
			errChan, cancelAttempt = start()
			retry = nil
			metrics.RecordDaemonTaskRetryCount(taskDimension)
			logger.Infof("[RETRY] Task: %s retry performed", binding.UniId)
			continue
		case binding = <-instance.reload:
			err = ReloadSignal{}
		case received, open := <-errChan:
			if !open {
				return
			}
			err = received
		}

		cancelAttempt()
		errChan = nil // Ignore late errors from the stopped attempt.
		delay := 10 * time.Second
		if errors.Is(err, ReloadSignal{}) {
			reloadCount++
		} else {
			retryCount++
			if retryCount >= r.config.RetryTolerateCount {
				delay = 15 * time.Minute
			}
		}
		if retryTimer != nil {
			retryTimer.Stop()
		}
		retryTimer = time.NewTimer(delay)
		retry = retryTimer.C
		logger.Warnf("[RETRY] Task: %s received %v, retryCount: %d reloadCount: %d, next attempt in %s",
			binding.UniId, err, retryCount, reloadCount, delay)
	}
}

func (r *RunMaintainer) handleDeleteTaskBinding(taskUniId string) {
	v, exist := r.runningInstance.LoadAndDelete(taskUniId)
	if !exist {
		logger.Errorf("Attempt to delete a task: %s that is not executing", taskUniId)
		return
	}
	instance := v.(*runningBinding)
	instance.cancel()
	<-instance.done
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
	instance := v.(*runningBinding)
	select {
	case instance.reload <- taskBinding:
		logger.Infof("[handleReloadBinding] sent reload signal, taskUniId: %s", taskBinding.UniId)
	case <-instance.ctx.Done():
	}
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
