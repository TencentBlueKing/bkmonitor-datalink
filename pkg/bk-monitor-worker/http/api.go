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
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	storeRedis "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/errors"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/timex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/worker"
)

type taskOptions struct {
	Retry     int    `json:"retry,omitempty"`
	Queue     string `json:"queue,omitempty"`
	Timeout   int    `json:"timeout,omitempty"`
	Deadline  string `json:"deadline,omitempty"`
	UniqueTTL int    `json:"unique_ttl,omitempty"`
}

type taskParams struct {
	Kind    string                 `binding:"required" json:"kind"`
	Payload map[string]interface{} `json:"payload"`
	Options taskOptions            `json:"options"`
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
	payload, err := json.Marshal(params.Payload)
	if err != nil {
		ServerErrResponse(c, "json marshal error: %v", err)
		return
	}
	// 如果是周期任务，则写入到 redis 中，周期性取任务写入到队列执行
	// 如果是异步任务，则直接写入到队列，然后执行任务
	kind := params.Kind
	metrics.RegisterTaskCount(kind)
	// 组装 task
	newedTask := &task.Task{
		Kind:    kind,
		Payload: payload,
		Options: composeOption(params.Options),
	}
	// 根据类型做判断
	if strings.HasPrefix(kind, AsyncTask) {
		if err := enqueueTask(newedTask); err != nil {
			ServerErrResponse(c, "enqueue task error, %v", err)
			return
		}
	} else if strings.HasPrefix(kind, PeriodicTask) {
		if err := pushTaskToRedis(c, newedTask); err != nil {
			ServerErrResponse(c, "push task to redis error, %v", err)
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
		timeoutOpt := IntToSecond(opt.Timeout)
		opts = append(opts, task.Timeout(timeoutOpt))
	}
	if opt.Deadline != "" {
		deadlineOpt, _ := timex.StringToTime(opt.Deadline)
		opts = append(opts, task.Deadline(deadlineOpt))
	}
	if opt.UniqueTTL != 0 {
		uniqueTTLOpt := IntToSecond(opt.UniqueTTL)
		opts = append(opts, task.Timeout(uniqueTTLOpt))
	}
	return opts
}

// 写入任务队列
func enqueueTask(t *task.Task) error {
	// new client
	client, err := worker.NewClient()
	if err != nil {
		return errors.New(fmt.Sprintf("new client error, %v", err))
	}
	defer client.Close()

	// 入队列
	if _, err = client.Enqueue(t); err != nil {
		return errors.New(fmt.Sprintf("enqueue task error, %v", err))
	}

	return nil
}

// 推送任务到 redis 中
func pushTaskToRedis(c *gin.Context, t *task.Task) error {
	r, err := storeRedis.GetInstance(c)
	if err != nil {
		return err
	}

	PeriodicTaskKey := storeRedis.GetPeriodicTaskKey()
	ChannelName := storeRedis.GetChannelName()

	// expiration set zero，means the key has no expiration time
	if err := r.HSet(PeriodicTaskKey, t.Kind, string(t.Payload)); err != nil {
		return err
	}

	// public msg
	if err := r.Publish(ChannelName, t.Kind); err != nil {
		return err
	}

	return nil
}
