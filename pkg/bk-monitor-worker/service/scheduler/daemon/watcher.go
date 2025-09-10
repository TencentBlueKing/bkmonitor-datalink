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
	"time"

	"github.com/go-redis/redis/v8"
	"golang.org/x/exp/maps"

	rdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/broker/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type watchWorkerMark struct {
	workerInfo service.WorkerInfo
}

type watchTaskMark struct {
	taskUniId  string
	task       task.SerializerTask
	newPayload []byte
}

type DefaultWatcherOptions struct {
	watchWorkerInterval time.Duration
	watchTaskInterval   time.Duration
}

type DefaultWatcher struct {
	ctx context.Context

	config      DefaultWatcherOptions
	redisClient redis.UniversalClient
}

func (d *DefaultWatcher) start() {
	// worker 更改监听
	go d.watchWorker()
	// task 更改监听
	go d.watchTask()
	// reload 更变监听
	go d.watchReloadRequest()
}

func (d *DefaultWatcher) watchWorker() {
	ticker := time.NewTicker(d.config.watchWorkerInterval)
	watchWorkerKeyPrefix := fmt.Sprintf("%squeue:*", common.WorkerKeyPrefix())
	logger.Infof("\nDaemonTask [DefaultWatcher] worker watcher started\n - \t "+
		"interval: %s \n - \t watchKey: %s", d.config.watchWorkerInterval, watchWorkerKeyPrefix)
	workerMarkMapping := make(map[string]watchWorkerMark)

	for {
		select {
		case <-ticker.C:
			keys, err := d.redisClient.Keys(d.ctx, watchWorkerKeyPrefix).Result()
			if err != nil {
				logger.Info("Watcher failed to list workers with prefix: %s, "+
					"may not be perceived. error: %s", watchWorkerKeyPrefix, err)
				continue
			}

			currentMarkMapping := make(map[string]watchWorkerMark)
			for _, key := range keys {
				workerInfo, err := d.toWorkerInfo(key)
				if err != nil {
					logger.Errorf("Failed to parse value to WorkerInfo from key: %s. error: %s", key, err)
					continue
				}
				currentMarkMapping[key] = watchWorkerMark{workerInfo: workerInfo}
				_, exist := workerMarkMapping[key]
				if !exist {
					d.handleAddWorker(currentMarkMapping[key])
				}
			}

			for key, value := range workerMarkMapping {
				_, exist := currentMarkMapping[key]
				if !exist {
					d.handleDeleteWorker(key, value)
				}
			}

			workerMarkMapping = currentMarkMapping
		case <-d.ctx.Done():
			logger.Info("DaemonTask [DefaultWatcher] worker watcher stopped")
			ticker.Stop()
			return
		}
	}
}

func (d *DefaultWatcher) toWorkerInfo(workerKey string) (service.WorkerInfo, error) {
	var res service.WorkerInfo
	bytesData, err := d.redisClient.Get(d.ctx, workerKey).Bytes()
	if err != nil {
		return res, err
	}

	if err = jsonx.Unmarshal(bytesData, &res); err != nil {
		return res, err
	}

	return res, nil
}

func (d *DefaultWatcher) handleAddWorker(workerMark watchWorkerMark) {
	// TODO Supplement the logic added by worker
	logger.Infof("[WORKER ADD] New worker: %s detected, "+
		"online time: %s", workerMark.workerInfo.Id, workerMark.workerInfo.StartTime)
}

func (d *DefaultWatcher) handleDeleteWorker(workerKey string, workerMark watchWorkerMark) {
	now := time.Now()
	survival := workerMark.workerInfo.StartTime.Sub(now)
	logger.Infof("[WORKER DELETE] Remove worker: %s detected, "+
		"offline time: %s, survival: %d", workerMark.workerInfo.Id, now, survival)

	if err := GetBinding().deleteWorkerBinding(workerMark.workerInfo.Id); err != nil {
		logger.Infof("Failed to delete worker(workerId: %s) binding, error: %s", workerMark.workerInfo.Id, err)
	}
}

func (d *DefaultWatcher) watchTask() {
	ticker := time.NewTicker(d.config.watchTaskInterval)
	watchKey := common.DaemonTaskKey()
	taskMarkMapping := make(map[string]watchTaskMark)
	logger.Infof("\nDaemonTask [DefaultWatcher] task watcher started\n - \t "+
		"interval: %s \n - \t watchKey: %s", d.config.watchTaskInterval, watchKey)

	for {
		select {
		case <-ticker.C:
			tasks, err := d.redisClient.SMembers(d.ctx, watchKey).Result()
			if err != nil {
				logger.Errorf("Failed to obtained task from queue which key: %s, "+
					"The tasks in the queue may not be processed correctly", watchKey)
				continue
			}

			currentTask := make(map[string]watchTaskMark)
			for index, t := range tasks {
				taskIns, err := d.toTask(t)
				if err != nil {
					logger.Errorf("F\failed to parse value to Task from key: %s[%d], value: %s", watchKey, index, t)
					continue
				}

				taskUniId := ComputeTaskUniId(taskIns)
				currentTask[taskUniId] = watchTaskMark{taskUniId: taskUniId, task: taskIns}
				_, exist := taskMarkMapping[taskUniId]
				if !exist {
					d.handleAddTask(currentTask[taskUniId])
				}
			}

			for key, value := range taskMarkMapping {
				_, exist := currentTask[key]
				if !exist {
					d.handleDeleteTask(value)
				}
			}

			taskMarkMapping = currentTask
		case <-d.ctx.Done():
			logger.Info("Daemon task scheduler task-watcher stopped.")
			ticker.Stop()
			return
		}
	}
}

func (d *DefaultWatcher) toTask(taskStr string) (task.SerializerTask, error) {
	var res task.SerializerTask
	if err := jsonx.Unmarshal([]byte(taskStr), &res); err != nil {
		return res, err
	}
	return res, nil
}

func (d *DefaultWatcher) handleAddTask(taskMark watchTaskMark) {
	GetBinding().addTaskWithUniId(taskMark.taskUniId, taskMark.task)
	logger.Infof("[TASK ADD] New Task: %s detect. taskUniId: %s", taskMark.task.Kind, taskMark.taskUniId)
}

func (d *DefaultWatcher) handleDeleteTask(taskMark watchTaskMark) {
	if err := GetBinding().deleteBinding(taskMark.taskUniId); err != nil {
		logger.Errorf("Failed to delete binding, taskUniId: %s error: %s", taskMark.taskUniId, err)
		return
	}
	logger.Infof("[TASK DELETE] Remove Task: %s detect. taskUniId: %s", taskMark.task.Kind, taskMark.taskUniId)
}

func (d *DefaultWatcher) watchReloadRequest() {
	watchChannel := common.DaemonReloadReqChannel()

	logger.Infof("\nDaemonTask [DefaultWatcher] reload-request watcher started\n - \t "+
		"interval: %s \n - \t subscribeKey: %s", d.config.watchTaskInterval, watchChannel)

	sub := d.redisClient.Subscribe(d.ctx, watchChannel)
	ch := sub.Channel()
	for {
		select {
		case <-d.ctx.Done():
			sub.Close()
			logger.Info("Daemon task scheduler reload-request subscribe stopped.")
			return
		case msg := <-ch:
			taskUniId := msg.Payload
			// Step1: 判断任务是否存在
			taskInfo, err := d.getDaemonTask(taskUniId)
			if err != nil {
				logger.Errorf(
					"Failed to get daemon task of taskUniId: %s, error: %s",
					taskUniId, err,
				)
				continue
			}
			if taskInfo == nil {
				logger.Errorf(
					"TaskUniId: %s not found in daemonTasks: %s, this reload will not effect, error: %s",
					taskUniId, common.DaemonTaskKey(), err,
				)
				continue
			}

			mark := watchTaskMark{taskUniId: taskUniId, task: *taskInfo}
			if payloadExist, err := d.redisClient.HExists(
				d.ctx, common.DaemonReloadReqPayloadHash(), taskUniId).Result(); payloadExist && err == nil {
				valueStr, err := d.redisClient.HGet(d.ctx, common.DaemonReloadReqPayloadHash(), taskUniId).Result()
				if err != nil {
					logger.Errorf(
						"TaskUniId: %s in %s, but get value from broker failed: %s",
						taskUniId, common.DaemonReloadReqPayloadHash(), err,
					)
					continue
				}
				mark.newPayload = []byte(valueStr)
				if _, err = d.redisClient.HDel(
					d.ctx, common.DaemonReloadReqPayloadHash(), taskUniId).Result(); err != nil {
					logger.Errorf(
						"failed to delete taskUniId: %s in hashKey: %s, error: %s",
						taskUniId, common.DaemonReloadReqPayloadHash(), err,
					)
				}
			}
			d.reassign(mark)
		}
	}
}

func (d *DefaultWatcher) reassign(mark watchTaskMark) {
	// Step1: 判断 Binding 是否存在
	workerId, err := GetBinding().GetBindingWorkerIdByTaskUniId(mark.taskUniId)
	if err != nil {
		logger.Errorf(
			"[reassign] Failed to obtained binding with taskUniId: %s, error: %s",
			mark.taskUniId, err,
		)
		return
	}

	if err = d.overrideAndUpdateTaskPayload(&mark); err != nil {
		logger.Errorf("Override and update taskPayload in daemonTaskQueue failed, error: %s", err)
		return
	}

	if workerId == "" {
		logger.Warnf(
			"TaskUniId: %s exist in the task queue, "+
				"but not in the binding queue, this task will be added normally", mark.taskUniId,
		)
		// Binding 不存在 -> 直接添加
		d.handleAddTask(mark)
	} else {
		// Binding 存在 -> 保留 WorkerId 关系
		if err = GetBinding().addReloadExecuteRequest(mark.taskUniId, mark.task, workerId); err != nil {
			logger.Errorf("Failed to send reload singal to worker queue, error: %s", err)
			return
		}
	}

	logger.Infof("Reload taskUniId: %s successfully", mark.taskUniId)
}

func (d *DefaultWatcher) overrideAndUpdateTaskPayload(mark *watchTaskMark) error {
	originData, err := jsonx.Marshal(mark.task)
	if err != nil {
		return err
	}

	if len(mark.newPayload) != 0 {
		logger.Infof(
			"[OverridePayload] find new payload, override.\nNEW: %s\nOLD: %s\n",
			mark.newPayload, mark.task.Payload,
		)
		mergePayload, err := d.mergeMapping(mark.task.Payload, mark.newPayload)
		if err != nil {
			return err
		}
		mark.task.Payload = mergePayload
		if ComputeTaskUniId(mark.task) != mark.taskUniId {
			return fmt.Errorf("[OverridePayload] taskUniId: %s is inconsistent after update, "+
				"the dimension field of this task.payload cannot be modified", mark.taskUniId)
		}
	}
	newData, err := jsonx.Marshal(mark.task)
	if err != nil {
		return err
	}

	pipe := d.redisClient.Pipeline()
	pipe.SRem(d.ctx, common.DaemonTaskKey(), originData)
	pipe.SAdd(d.ctx, common.DaemonTaskKey(), newData)
	_, err = pipe.Exec(d.ctx)
	if err != nil {
		return err
	}

	return err
}

func (d *DefaultWatcher) mergeMapping(origin []byte, target []byte) ([]byte, error) {
	// Merge two map
	var originMapping map[string]any
	var targetMapping map[string]any

	if err := jsonx.Unmarshal(origin, &originMapping); err != nil {
		return nil, err
	}

	if err := jsonx.Unmarshal(target, &targetMapping); err != nil {
		return nil, err
	}

	maps.Copy(originMapping, targetMapping)
	merge, err := jsonx.Marshal(originMapping)
	if err != nil {
		return nil, err
	}

	return merge, nil
}

// getDaemonTask obtained the daemon task
func (d *DefaultWatcher) getDaemonTask(taskUniId string) (*task.SerializerTask, error) {
	members, err := d.redisClient.SMembers(context.Background(), common.DaemonTaskKey()).Result()
	if err != nil {
		return nil, err
	}

	for _, item := range members {
		var t task.SerializerTask
		if err = jsonx.Unmarshal([]byte(item), &t); err != nil {
			logger.Errorf(
				"Failed to unmarshal data to SerializerTask, data: %s, error: %s",
				item, err,
			)
			continue
		}
		if ComputeTaskUniId(t) == taskUniId {
			return &t, nil
		}
	}

	return nil, nil
}

func NewDefaultWatcher(ctx context.Context) Watcher {
	options := DefaultWatcherOptions{
		watchWorkerInterval: config.SchedulerDaemonTaskWorkerWatcherInterval,
		watchTaskInterval:   config.SchedulerDaemonTaskTaskWatcherInterval,
	}

	return &DefaultWatcher{
		ctx:         ctx,
		redisClient: rdb.GetRDB().Client(),
		config:      options,
	}
}
