// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package task

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	task "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
)

const (
	taskKind = "async:add"
)

// UserInfo test data
type UserInfo struct {
	UserID int
}

// NewEmailDeliveryTask
func NewAddTask(userID int) (*task.Task, error) {
	payload, err := json.Marshal(UserInfo{UserID: userID})
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	return task.NewTask(taskKind, payload), nil
}

// HandleTask
func HandleTask(ctx context.Context, t *task.Task) error {
	var p UserInfo
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return fmt.Errorf("json.Unmarshal failed: %v", err)
	}
	//逻辑处理start...
	log.Printf("print user info: user_id=%d", p.UserID)
	return nil
}
