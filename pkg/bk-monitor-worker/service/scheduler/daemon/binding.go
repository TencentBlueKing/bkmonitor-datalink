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

	redis "github.com/go-redis/redis/v8"

	rdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/broker/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type TaskBinding struct {
	UniId string
	task.SerializerTask
}

type WorkerBinding struct {
	WorkerId string
}

type Binding struct {
	redisClient redis.UniversalClient
}

func (b *Binding) addTask(t task.SerializerTask) {
	workerId, err := computeWorkerId(t)
	if err != nil {
		logger.Errorf("handle add task failed. error: %s", err)
		return
	}
	taskUniId := ComputeTaskUniId(t)
	if taskUniId == "" {
		logger.Errorf("add task, task uni id is empty")
		return
	}

	if err = b.addBinding(
		TaskBinding{UniId: taskUniId, SerializerTask: t},
		WorkerBinding{WorkerId: workerId},
	); err != nil {
		logger.Errorf("add binding failed. %s", err)
		return
	}
}

func (b *Binding) addTaskWithUniId(taskUniId string, t task.SerializerTask) {
	workerId, err := computeWorkerId(t)
	if err != nil {
		logger.Errorf("handle add task failed. error: %s", err)
		return
	}
	if err = b.addBinding(
		TaskBinding{UniId: taskUniId, SerializerTask: t},
		WorkerBinding{WorkerId: workerId},
	); err != nil {
		logger.Errorf("add binding failed. %s", err)
		return
	}

	return
}

func (b *Binding) addReloadExecuteRequest(taskUniId string, t task.SerializerTask, workerId string) error {
	bind := TaskBinding{UniId: taskUniId, SerializerTask: t}
	workerBindingBytes, err := jsonx.Marshal(bind)
	if err != nil {
		return err
	}

	if err = b.redisClient.Publish(
		context.Background(), common.DaemonReloadExecQueue(workerId), workerBindingBytes).Err(); err != nil {
		return err
	}

	return nil
}

func (b *Binding) addBinding(taskBinding TaskBinding, workerBinding WorkerBinding) error {
	ctx := context.Background()

	existsWorkerInfoStr, err := b.getWorkerByTask(ctx, taskBinding.UniId)
	if err != nil {
		return fmt.Errorf(
			"error obtaining field: %s from hash: %s. error: %s",
			taskBinding.UniId, common.DaemonBindingTask(), err,
		)
	}
	if existsWorkerInfoStr != "" {
		logger.Warnf("Task: %s(except to binding worker: %s) already exists in the current binding(workerId: %s), is same task been submitted repeatedly?", taskBinding.UniId, workerBinding.WorkerId, existsWorkerInfoStr)
		return nil
	}

	workerBindingBytes, _ := jsonx.Marshal(workerBinding)
	if err = b.redisClient.HSet(ctx, common.DaemonBindingTask(), taskBinding.UniId, workerBindingBytes).Err(); err != nil {
		return fmt.Errorf("failed to add a task binding, error: %s", err)
	}

	taskBindingBytes, _ := jsonx.Marshal(taskBinding)
	if err = b.redisClient.HSet(
		ctx, common.DaemonBindingWorker(workerBinding.WorkerId),
		taskBinding.UniId, taskBindingBytes,
	).Err(); err != nil {
		return fmt.Errorf("failed to add a worker binding, error: %s", err)
	}
	logger.Infof(
		"[BINDING ADD] ADD BINDING: %s(taskUniId) <------> %s(workerId)",
		taskBinding.UniId, workerBinding.WorkerId,
	)
	return nil
}

func (b *Binding) deleteBinding(taskUniId string) error {
	ctx := context.Background()

	workerInfoStr, err := b.getWorkerByTask(ctx, taskUniId)
	if err != nil {
		return err
	}
	var workerBinding WorkerBinding
	if err = jsonx.Unmarshal([]byte(workerInfoStr), &workerBinding); err != nil {
		return fmt.Errorf(
			"failed to parse value to WokerInfo on taskUniId Binding: %s, value: %s. error: %s",
			taskUniId, workerInfoStr, err,
		)
	}

	if workerBinding.WorkerId == "" {
		return fmt.Errorf(
			"failed to delete binding from task binding because the binding(%s <----> ?) does not exist",
			taskUniId,
		)
	}

	if err = b.redisClient.HDel(ctx, common.DaemonBindingTask(), taskUniId).Err(); err != nil {
		return fmt.Errorf(
			"failed to delete field: %s from task binding: %s. error: %s",
			taskUniId, common.DaemonBindingTask(), err,
		)
	}

	if err = b.redisClient.HDel(ctx, common.DaemonBindingWorker(workerBinding.WorkerId), taskUniId).Err(); err != nil {
		return fmt.Errorf(
			"failed to delete worker binding(%s <----> %s) on field: %s, error: %s",
			taskUniId, workerBinding.WorkerId, common.DaemonBindingWorker(workerBinding.WorkerId), err,
		)
	}

	logger.Infof("[BINDING DELETE] delete binding (%s <----> %s)", taskUniId, workerBinding.WorkerId)
	return nil
}

func (b *Binding) deleteWorkerBinding(workerId string) error {
	ctx := context.Background()
	bindingTaskHash, err := b.listTasksByWorker(ctx, workerId)
	if err != nil {
		return fmt.Errorf("failed to delete the binding for worker: %s, because the list of all tasks for this worker cannot be obtained. error: %s", workerId, err)
	}
	var bindingTasks []*TaskBinding
	for taskUniId, taskBindingStr := range bindingTaskHash {
		taskBinding, err := b.toTaskBinding(taskBindingStr)
		if err != nil {
			logger.Errorf(
				"failed to parse taskBindingStr to TaskBinding, taskUniId: %s value: %s. error: %s",
				taskUniId, taskBindingStr, err,
			)
			continue
		}
		bindingTasks = append(bindingTasks, taskBinding)

		err = b.redisClient.HDel(ctx, common.DaemonBindingTask(), taskBinding.UniId).Err()
		if err != nil {
			logger.Errorf("failed to delete filed: %s of task binding key: %s", taskBinding.UniId, common.DaemonBindingTask())
			continue
		}
		logger.Infof("[BINDING DELETE] delete field: %s of hash: %s", taskBinding.UniId, common.DaemonBindingTask())
	}

	err = b.redisClient.Del(ctx, common.DaemonBindingWorker(workerId)).Err()
	if err != nil {
		return fmt.Errorf("failed to delete worker binding key: %s. error: %s", common.DaemonBindingWorker(workerId), err)
	}

	logger.Infof("[BINDING DELETE] delete worker binding key: %s", common.DaemonBindingWorker(workerId))
	for _, taskBinding := range bindingTasks {
		b.addTask(taskBinding.SerializerTask)
	}

	return nil
}

func (b *Binding) toWorkerBinding(workerStr string) (*WorkerBinding, error) {
	var res WorkerBinding
	if err := jsonx.Unmarshal([]byte(workerStr), &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (b *Binding) toTaskBinding(taskStr string) (*TaskBinding, error) {
	var res TaskBinding
	if err := jsonx.Unmarshal([]byte(taskStr), &res); err != nil {
		return nil, err
	}

	return &res, nil
}

func (b *Binding) listTasksByWorker(ctx context.Context, workerId string) (map[string]string, error) {
	tasks, err := b.redisClient.HGetAll(ctx, common.DaemonBindingWorker(workerId)).Result()
	if err != nil {
		return nil, err
	}
	return tasks, nil
}

func (b *Binding) getWorkerByTask(ctx context.Context, taskUniId string) (string, error) {
	exist, err := b.redisClient.HExists(ctx, common.DaemonBindingTask(), taskUniId).Result()
	if err != nil {
		return "", fmt.Errorf("error obtaining field: %s from %s", taskUniId, common.DaemonBindingTask())
	}
	if exist {
		existsWorkerInfoStr, err := b.redisClient.HGet(ctx, common.DaemonBindingTask(), taskUniId).Result()
		if err != nil {
			return "", fmt.Errorf("error obtaining field: %s from %s", taskUniId, common.DaemonBindingTask())
		}
		return existsWorkerInfoStr, nil
	}

	return "", nil
}

// GetBindingWorkerIdByTask Get the worker which bound to the task (maybe not running)
func (b *Binding) GetBindingWorkerIdByTask(t task.SerializerTask) (string, error) {
	taskUniId := ComputeTaskUniId(t)
	if taskUniId == "" {
		return "", fmt.Errorf("GetBindingWorkerIdByTask: taskUniId is empty")
	}
	return b.GetBindingWorkerIdByTaskUniId(taskUniId)
}

func (b *Binding) GetBindingWorkerIdByTaskUniId(taskUniId string) (string, error) {
	workerInfoStr, err := b.getWorkerByTask(context.Background(), taskUniId)
	if err != nil {
		return "", err
	}
	if workerInfoStr == "" {
		return "", nil
	}
	var workerBinding WorkerBinding
	if err := jsonx.Unmarshal([]byte(workerInfoStr), &workerBinding); err != nil {
		return "", err
	}

	return workerBinding.WorkerId, nil
}

// IsWorkerAlive Determine whether worker is alive or not.
func (b *Binding) IsWorkerAlive(workerId, queue string) (bool, error) {
	aliveKey := common.WorkerKey(queue, workerId)
	r, err := b.redisClient.Exists(context.Background(), aliveKey).Result()
	if err != nil {
		return false, err
	}

	return r == 1, nil
}

var (
	bindingOnce     sync.Once
	bindingInstance *Binding
)

func GetBinding() *Binding {
	bindingOnce.Do(func() {
		bindingInstance = &Binding{redisClient: rdb.GetRDB().Client()}
	})

	return bindingInstance
}
