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

	"github.com/gin-gonic/gin"

	rdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/broker/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/service/scheduler/daemon"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

type DaemonTaskReloadParam struct {
	UniId   string         `json:"task_uni_id"`
	Payload map[string]any `json:"payload"`
}

// ReloadDaemonTask 重载常驻任务 将 task_uni_id 放入重载队列中，由调度器负责监听并处理
func ReloadDaemonTask(c *gin.Context) {
	params := new(DaemonTaskReloadParam)
	if err := BindJSON(c, params); err != nil {
		BadReqResponse(c, "parse params error: %v", err)
		return
	}

	taskInstance, _, err := getDaemonTaskBytes(params.UniId)
	if err != nil {
		BadReqResponse(c, "get daemonTask failed, error: %v", err)
		return
	}
	if taskInstance == nil {
		BadReqResponse(c, "taskUniId: [%s] not found!", params.UniId)
		return
	}

	if err = updateDaemonTask(daemon.ComputeTaskUniId(*taskInstance), params.Payload); err != nil {
		BadReqResponse(c, "failed to update daemonTask, error: %s", err)
		return
	}

	Response(c, &gin.H{"data": fmt.Sprintf("send %s to channel: %s", params.UniId, common.DaemonReloadReqChannel())})
	return
}

func getDaemonTaskBytes(taskUniId string) (*task.SerializerTask, []byte, error) {
	tasks, err := rdb.GetRDB().Client().SMembers(context.Background(), common.DaemonTaskKey()).Result()
	if err != nil {
		return nil, nil, err
	}

	for _, i := range tasks {
		var item task.SerializerTask
		if err = jsonx.Unmarshal([]byte(i), &item); err != nil {
			return nil, nil, err
		}
		itemTaskUniId := daemon.ComputeTaskUniId(item)
		if itemTaskUniId == taskUniId {
			return &item, []byte(i), nil
		}
	}
	return nil, nil, nil
}

// isDaemonTaskExist 检查常驻任务是否已存在 (不确保运行正常 只是检查是否已被创建)
func isDaemonTaskExist(taskUniId string) bool {
	t, _, e := getDaemonTaskBytes(taskUniId)
	if e != nil {
		return false
	}
	return t != nil
}

func updateDaemonTask(taskUniId string, payload map[string]any) error {
	broker := rdb.GetRDB()
	// 检查是否已经存在于重载队列中
	exist, err := broker.Client().SIsMember(context.Background(), common.DaemonReloadReqChannel(), taskUniId).Result()
	if err != nil {
		return fmt.Errorf("found: %s if in queue failed, error: %s", taskUniId, err)
	}
	if exist {
		return fmt.Errorf(fmt.Sprintf("task: %s already in %s queue", taskUniId, common.DaemonReloadReqChannel()))
	}

	// 推送重载请求到调度队队列中、并将此次 payload 更新存储在 hash 结构中等待消费
	pipe := broker.Client().Pipeline()
	pipe.Publish(context.Background(), common.DaemonReloadReqChannel(), taskUniId)
	if len(payload) > 0 {
		payloadData, err := jsonx.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to parse payload to bytes, error: %s", err)
		}
		pipe.HSetNX(context.Background(), common.DaemonReloadReqPayloadHash(), taskUniId, payloadData)
	}
	if _, err = pipe.Exec(context.Background()); err != nil {
		return fmt.Errorf("execute publish reload signal to broker failed, error: %v", err)
	}
	return nil
}
