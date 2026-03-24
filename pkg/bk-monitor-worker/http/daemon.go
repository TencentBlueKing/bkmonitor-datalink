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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
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

	daemonTaskBytes, err := getDaemonTask(params.UniId)
	if err != nil {
		BadReqResponse(c, "get daemonTask failed, error: %v", err)
		return
	}
	if daemonTaskBytes == nil {
		BadReqResponse(c, "taskUniId: [%s] not found!", params.UniId)
		return
	}

	broker := rdb.GetRDB()
	// 检查是否已经存在于重载队列中
	exist, err := broker.Client().SIsMember(context.Background(), common.DaemonReloadReqChannel(), params.UniId).Result()
	if err != nil {
		BadReqResponse(c, "found: %s if in queue failed, error: %s", params.UniId, err)
		return
	}
	if exist {
		Response(
			c,
			&gin.H{"data": fmt.Sprintf(
				"task: %s already in %s queue", params.UniId, common.DaemonReloadReqChannel())},
		)
		return
	}

	// 推送重载请求到调度队队列中、并将此次 payload 更新存储在 hash 结构中等待消费
	pipe := broker.Client().Pipeline()
	pipe.Publish(context.Background(), common.DaemonReloadReqChannel(), params.UniId)
	if len(params.Payload) > 0 {
		payloadData, err := jsonx.Marshal(params.Payload)
		if err != nil {
			BadReqResponse(c, "failed to parse payload to bytes, error: %s", err)
			return
		}
		pipe.HSetNX(context.Background(), common.DaemonReloadReqPayloadHash(), params.UniId, payloadData)
	}
	if _, err = pipe.Exec(context.Background()); err != nil {
		BadReqResponse(c, "execute publish reload signal to broker failed, error: %v", err)
		return
	}

	Response(c, &gin.H{"data": fmt.Sprintf("send %s to channel: %s", params.UniId, common.DaemonReloadReqChannel())})
}

func getDaemonTask(taskUniId string) ([]byte, error) {
	tasks, err := rdb.GetRDB().Client().SMembers(context.Background(), common.DaemonTaskKey()).Result()
	if err != nil {
		return nil, err
	}

	for _, i := range tasks {
		var item task.SerializerTask
		if err = jsonx.Unmarshal([]byte(i), &item); err != nil {
			return nil, err
		}
		itemTaskUniId := daemon.ComputeTaskUniId(item)
		if itemTaskUniId == "" {
			logger.Errorf("failed to compute taskUniId, value: %v", item)
			continue
		}
		if itemTaskUniId == taskUniId {
			return []byte(i), nil
		}
	}
	return nil, nil
}
