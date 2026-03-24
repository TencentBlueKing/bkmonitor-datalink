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

	linq "github.com/ahmetb/go-linq/v3"
	redis "github.com/go-redis/redis/v8"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"

	rdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/broker/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type DefaultNumeratorOptions struct {
	checkInterval time.Duration
}

type DefaultNumerator struct {
	ctx context.Context

	config      DefaultNumeratorOptions
	redisClient redis.UniversalClient
}

func (d *DefaultNumerator) start() {
	ticker := time.NewTicker(d.config.checkInterval)
	logger.Infof("\nDaemonTask [DefaultNumerator] numerator started\n - \t interval: %s", d.config.checkInterval)

	for {
		select {
		case <-ticker.C:
			workers, err := d.listWorker()
			if err != nil {
				logger.Errorf("Numerator loop failed. error: %s", err)
				continue
			}
			taskUniIdMapping, err := d.listTask()
			if err != nil {
				logger.Errorf("Numerator loop failed. %s", err)
				continue
			}
			workerTaskMapping, taskWorkerMapping, err := d.listBindingMapping(workers, taskUniIdMapping)
			if err != nil {
				logger.Errorf("Numerator loop failed. %s", err)
				continue
			}
			invalidTaskUniIdBindings := d.checkWorkerCorrect(workers, workerTaskMapping, taskUniIdMapping)
			exceptAddTasks, invalidWorkerIdBindings := d.checkTaskCorrect(taskUniIdMapping, taskWorkerMapping, workers)

			linq.From(invalidTaskUniIdBindings).ForEach(func(i any) {
				if err = GetBinding().deleteBinding(i.(string)); err != nil {
					logger.Errorf("Numerator delete binding failed, error: %s", err)
				}
			})
			linq.From(exceptAddTasks).ForEach(func(i any) { GetBinding().addTask(i.(task.SerializerTask)) })
			linq.From(invalidWorkerIdBindings).ForEach(func(i any) {
				if err = GetBinding().deleteWorkerBinding(i.(string)); err != nil {
					logger.Errorf("Numerator delete worker binding failed, error: %s", err)
				}
			})

		case <-d.ctx.Done():
			logger.Info("Daemon task-scheduler numerator stopped.")
			ticker.Stop()
			return
		}
	}
}

func (d *DefaultNumerator) checkWorkerCorrect(
	workers []service.WorkerInfo,
	workerTaskMapping map[string][]string,
	taskUniIdMapping map[string]task.SerializerTask,
) []string {
	var res []string
	taskUniIds := maps.Keys(taskUniIdMapping)

	for _, worker := range workers {
		workerTaskUniIds, workerHasTask := workerTaskMapping[worker.Id]
		if !workerHasTask {
			logger.Infof("worker: %s idle", worker.Id)
		} else {
			invalidTaskUniIds := d.checkTasksValid(workerTaskUniIds, taskUniIds)
			if len(invalidTaskUniIds) != 0 {
				// 发现了关系中绑定了不存在的task 需要从binding中去除
				logger.Warnf("%d invalid tasks(%+v) have binding relation.", len(invalidTaskUniIds), invalidTaskUniIds)
				res = append(res, invalidTaskUniIds...)
			}
		}
	}
	return res
}

func (d *DefaultNumerator) checkTasksValid(workerTasks []string, taskUniIds []string) []string {
	var res []string
	for _, item := range workerTasks {
		if !slices.Contains(taskUniIds, item) {
			res = append(res, item)
		}
	}
	return res
}

func (d *DefaultNumerator) checkTaskCorrect(
	taskUniMapping map[string]task.SerializerTask,
	taskWorkerMapping map[string]string,
	workers []service.WorkerInfo,
) ([]task.SerializerTask, []string) {
	var invalidWorkerIds []string
	var expectAddTasks []task.SerializerTask
	var workerIds []string
	linq.From(workers).Select(func(i any) any { return i.(service.WorkerInfo).Id }).ToSlice(&workerIds)

	for taskUniId, taskInstance := range taskUniMapping {
		bindingWorkerStr, taskHasWorker := taskWorkerMapping[taskUniId]

		if !taskHasWorker {
			logger.Warnf(
				"Task that was not had binding worker was found: taskKin: %s(taskUniId: %s)",
				taskInstance.Kind, taskUniId,
			)
			expectAddTasks = append(expectAddTasks, taskInstance)
		} else {
			workerBinding, err := GetBinding().toWorkerBinding(bindingWorkerStr)
			if err != nil {
				logger.Errorf("failed to parse value to WorkerBinding: %s, error: %s", bindingWorkerStr, err)
				continue
			}
			if !slices.Contains(workerIds, workerBinding.WorkerId) {
				logger.Warnf("Binding(%s <----> %s(INVALID!)) found invalid worker", taskUniId, workerBinding.WorkerId)
				invalidWorkerIds = append(invalidWorkerIds, workerBinding.WorkerId)
			}
		}
	}

	return expectAddTasks, invalidWorkerIds
}

func (d *DefaultNumerator) listWorker() ([]service.WorkerInfo, error) {
	var res []service.WorkerInfo
	workerPrefix := fmt.Sprintf("%s*", common.WorkerKeyPrefix())
	workers, err := d.redisClient.Keys(d.ctx, workerPrefix).Result()
	if err != nil {
		return res, fmt.Errorf("failed to obtain all worker keys whose prefix is: %s. error: %s", workerPrefix, err)
	}

	for _, key := range workers {
		bytesData, _ := d.redisClient.Get(d.ctx, key).Bytes()
		var workerInfo service.WorkerInfo
		if err = jsonx.Unmarshal(bytesData, &workerInfo); err != nil {
			return res, fmt.Errorf(
				"failed to unmarshal value to WorkerInfo with key: %s(value: %s). error: %s",
				key, bytesData, err,
			)
		}

		res = append(res, workerInfo)
	}

	return res, nil
}

func (d *DefaultNumerator) listTask() (map[string]task.SerializerTask, error) {
	res := make(map[string]task.SerializerTask)
	tasks, err := d.redisClient.SMembers(d.ctx, common.DaemonTaskKey()).Result()
	if err != nil {
		return res, fmt.Errorf("failed to get list of tasks with key: %s, error: %s", common.DaemonTaskKey(), err)
	}

	for _, item := range tasks {
		var instance task.SerializerTask
		if err = jsonx.Unmarshal([]byte(item), &instance); err != nil {
			return res, fmt.Errorf("failed to unmarshal value(%s) to Task on %s. error: %s", item, common.DaemonTaskKey(), err)
		}
		res[ComputeTaskUniId(instance)] = instance
	}

	return res, nil
}

func (d *DefaultNumerator) listBindingMapping(workers []service.WorkerInfo, taskUniIdMapping map[string]task.SerializerTask) (map[string][]string, map[string]string, error) {
	workerTaskMapping := make(map[string][]string, len(workers))
	taskWorkerMapping := make(map[string]string, len(taskUniIdMapping))

	var workerIds []string
	for _, w := range workers {
		workerIds = append(workerIds, w.Id)
	}

	for _, workerId := range workerIds {
		workerTaskBindings, err := d.redisClient.HKeys(d.ctx, common.DaemonBindingWorker(workerId)).Result()
		if err != nil {
			return workerTaskMapping, taskWorkerMapping, fmt.Errorf(
				"failed to obtain current binding tasks with workerIdKey: %s, error: %s",
				common.DaemonBindingWorker(workerId), err,
			)
		}

		workerTaskMapping[workerId] = workerTaskBindings
	}

	for taskUniId := range taskUniIdMapping {

		exist, err := d.redisClient.HExists(d.ctx, common.DaemonBindingTask(), taskUniId).Result()
		if err != nil {
			return workerTaskMapping, taskWorkerMapping, fmt.Errorf(
				"failed to obtain current binding worker with taskUniId: %s, error: %s", taskUniId, err,
			)
		}
		if exist {
			workerId, err := d.redisClient.HGet(d.ctx, common.DaemonBindingTask(), taskUniId).Result()
			if err != nil {
				return workerTaskMapping, taskWorkerMapping, fmt.Errorf(
					"failed to obtain current workerId "+
						"with taskUnidId on field: %s of key: %s. error: %s",
					taskUniId, common.DaemonBindingTask(), err)
			}
			if workerId != "" {
				taskWorkerMapping[taskUniId] = workerId
			}
		}
	}

	return workerTaskMapping, taskWorkerMapping, nil
}

func NewDefaultNumerator(ctx context.Context) Numerator {
	opts := DefaultNumeratorOptions{
		checkInterval: config.SchedulerDaemonTaskNumeratorInterval,
	}
	return &DefaultNumerator{ctx: ctx, config: opts, redisClient: rdb.GetRDB().Client()}
}
