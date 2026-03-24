// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"

	rdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/broker/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/service/scheduler/daemon"
	storeRedis "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/errors"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/timex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/worker"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type taskOptions struct {
	Retry     int    `json:"retry,omitempty"`
	Queue     string `json:"queue,omitempty"`
	Timeout   int    `json:"timeout,omitempty"`
	Deadline  string `json:"deadline,omitempty"`
	UniqueTTL int    `json:"unique_ttl,omitempty"`
}

type taskParams struct {
	Kind    string         `binding:"required" json:"kind"`
	Payload map[string]any `json:"payload"`
	Options taskOptions    `json:"options"`
}

type daemonTaskItem struct {
	UniId   string                 `json:"uni_id"`
	Kind    string                 `json:"kind"`
	Payload map[string]any         `json:"payload"`
	Options task.Options           `json:"options"`
	Binding *daemonTaskBindingInfo `json:"binding"`
}

type daemonTaskBindingInfo struct {
	WorkerId       string `json:"worker_id"`
	WorkerIsNormal bool   `json:"worker_is_normal"`
}

type removeTaskParams struct {
	TaskType  string `json:"task_type"`
	TaskUniId string `json:"task_uni_id"`
}

type removeAllTaskParams struct {
	TaskType string `json:"task_type"`
}

// CreateTask create a delay task
func CreateTask(c *gin.Context) {
	// get data
	params := new(taskParams)
	if err := BindJSON(c, params); err != nil {
		BadReqResponse(c, "parse params error: %v", err)
		return
	}
	// compose task
	payload, err := jsonx.Marshal(params.Payload)
	if err != nil {
		ServerErrResponse(c, "json marshal error: %v", err)
		return
	}
	// 如果是周期任务，则写入到 redis 中，周期性取任务写入到队列执行
	// 如果是异步任务，则直接写入到队列，然后执行任务
	// 如果是常驻任务，则直接写入到常驻任务队列中即可
	kind := params.Kind
	// 组装 task
	newedTask := &task.Task{
		Kind:    kind,
		Payload: payload,
		Options: composeOption(params.Options),
	}
	// 根据类型做判断
	if strings.HasPrefix(kind, AsyncTask) {
		if err = enqueueAsyncTask(newedTask); err != nil {
			ServerErrResponse(c, "enqueue async task error, %v", err)
			return
		}
	} else if strings.HasPrefix(kind, PeriodicTask) {
		if err = pushPeriodicTaskToRedis(newedTask); err != nil {
			ServerErrResponse(c, "push task to redis error, %v", err)
			return
		}
	} else if strings.HasPrefix(kind, DaemonTask) {
		if err = enqueueDaemonTask(newedTask); err != nil {
			ServerErrResponse(c, "enqueue daemon task error error, %v", err)
			return
		}
	} else {
		BadReqResponse(c, "task kind: %s not support", kind)
		return
	}

	// success response
	Response(c, nil)
}

// 组装传递的 option， 如 retry、deadline 等
func composeOption(opt taskOptions) []task.Option {
	var opts []task.Option
	// 添加 option
	if opt.Retry != 0 {
		opts = append(opts, task.MaxRetry(opt.Retry))
	}
	if opt.Queue != "" {
		opts = append(opts, task.Queue(opt.Queue))
	}
	if opt.Timeout != 0 {
		timeoutOpt := timex.IntToSecond(opt.Timeout)
		opts = append(opts, task.Timeout(timeoutOpt))
	}
	if opt.Deadline != "" {
		deadlineOpt, _ := timex.StringToTime(opt.Deadline)
		opts = append(opts, task.Deadline(deadlineOpt))
	}
	if opt.UniqueTTL != 0 {
		uniqueTTLOpt := timex.IntToSecond(opt.UniqueTTL)
		opts = append(opts, task.Timeout(uniqueTTLOpt))
	}
	return opts
}

// 写入任务队列
func enqueueAsyncTask(t *task.Task) error {
	// new client
	client, err := worker.GetClient()
	if err != nil {
		return err
	}
	defer client.Close()

	// 入队列
	if _, err := client.Enqueue(t); err != nil {
		return errors.New(fmt.Sprintf("enqueue task error, %v", err))
	}

	return nil
}

// 推送任务到 redis 中
func pushPeriodicTaskToRedis(t *task.Task) error {
	r := storeRedis.GetStorageRedisInstance()

	// expiration set zero，means the key has no expiration time
	if err := r.HSet(storeRedis.StoragePeriodicTaskKey, t.Kind, string(t.Payload)); err != nil {
		return err
	}

	// public msg
	if err := r.Publish(storeRedis.StoragePeriodicTaskChannelKey, t.Kind); err != nil {
		return err
	}

	return nil
}

// 推送任务到 task队列中
func enqueueDaemonTask(t *task.Task) error {
	broker := rdb.GetRDB()

	serializerTask, err := task.NewSerializerTask(*t)
	if err != nil {
		return err
	}
	data, err := jsonx.Marshal(serializerTask)
	if err != nil {
		return err
	}

	return broker.Client().SAdd(context.Background(), common.DaemonTaskKey(), data).Err()
}

// RemoveAllTask 删除所有任务
func RemoveAllTask(c *gin.Context) {
	params := new(removeAllTaskParams)
	if err := BindJSON(c, params); err != nil {
		BadReqResponse(c, "parse params error: %v", err)
		return
	}

	switch params.TaskType {
	case DaemonTask:
		broker := rdb.GetRDB()
		_, err := broker.Client().Del(context.Background(), common.DaemonTaskKey()).Result()
		if err != nil {
			ServerErrResponse(c, fmt.Sprintf("failed to delete key: %s.", common.DaemonTaskKey()), err)
			return
		}
		Response(c, &gin.H{})
	default:
		ServerErrResponse(c, fmt.Sprintf("Task remove not support type: %s", params.TaskType))
	}
}

// RemoveTask 删除某个任务
func RemoveTask(c *gin.Context) {
	params := new(removeTaskParams)
	if err := BindJSON(c, params); err != nil {
		BadReqResponse(c, "parse params error: %v", err)
		return
	}

	switch params.TaskType {
	case DaemonTask:
		daemonTaskBytes, err := getDaemonTask(params.TaskUniId)
		if err != nil {
			BadReqResponse(c, "get daemonTask failed error: %v", err)
			return
		}
		if daemonTaskBytes != nil {
			rdb.GetRDB().Client().SRem(context.Background(), common.DaemonTaskKey(), daemonTaskBytes)
			Response(c, &gin.H{"data": params.TaskUniId})
			return
		}
		ServerErrResponse(c, fmt.Sprintf(
			"failed to remove TaskUniId: %s, not found in key: %s",
			params.TaskUniId, common.DaemonTaskKey()),
		)
		return
	default:
		ServerErrResponse(c, fmt.Sprintf("Task remove not support type: %s", params.TaskType))
	}
}

// ListTask 获取broker中的任务列表
func ListTask(c *gin.Context) {
	taskType := c.DefaultQuery("task_type", "empty")

	switch taskType {
	case DaemonTask:
		client := rdb.GetRDB()
		tasks, err := client.Client().SMembers(context.Background(), common.DaemonTaskKey()).Result()
		if err != nil {
			ServerErrResponse(c, fmt.Sprintf("failed to list task by key: %s.", common.DaemonTaskKey()), err)
			return
		}
		var res []daemonTaskItem
		for _, i := range tasks {
			var item task.SerializerTask
			if err = jsonx.Unmarshal([]byte(i), &item); err != nil {
				ServerErrResponse(c, fmt.Sprintf("failed to parse key: %v to Task on value: %s", common.DaemonTaskKey(), i), err)
				return
			}

			taskUinId := daemon.ComputeTaskUniId(item)
			// 如果计算 taskUniId 失败，直接跳过
			if taskUinId == "" {
				logger.Errorf("failed to compute taskUniId, value: %v", item)
				continue
			}

			var payload map[string]any
			if err = jsonx.Unmarshal(item.Payload, &payload); err != nil {
				ServerErrResponse(c, fmt.Sprintf("failed to parse payload, value: %s, error: %s", item.Payload, err), err)
				return
			}

			// 查询绑定信息
			workerId, err := daemon.GetBinding().GetBindingWorkerIdByTask(item)
			if err != nil {
				ServerErrResponse(c, fmt.Sprintf("failed to get worker for taskUnid: %s", taskUinId), err)
				return
			}
			taskRes := daemonTaskItem{
				UniId:   taskUinId,
				Kind:    item.Kind,
				Options: item.Options,
				Payload: payload,
			}
			var bindingInfo daemonTaskBindingInfo
			var alive bool
			if workerId != "" {
				bindingInfo.WorkerId = workerId
				// 获取 worker 是否存活
				alive, err = daemon.GetBinding().IsWorkerAlive(workerId, item.Options.Queue)
				if err != nil {
					ServerErrResponse(c, fmt.Sprintf("failed to get worker status for taskUnid: %s", taskUinId), err)
					return
				}
				bindingInfo.WorkerIsNormal = alive
				taskRes.Binding = &bindingInfo
			}
			res = append(res, taskRes)
		}

		Response(c, &gin.H{"data": res})
		return
	default:
		BadReqResponse(c, fmt.Sprintf("Task list not support type: %s", taskType))
	}
}

// SetLogLevel 动态设置日志级别
func SetLogLevel(c *gin.Context) {
	logLevel := c.Query("log_level")
	if logLevel == "" {
		BadReqResponse(c, "params:[log_level] is null")
		return
	}
	// NOTE: 管理员使用，忽略具体值的校验
	logger.SetLoggerLevel(logLevel)
	Response(c, &gin.H{})
}
