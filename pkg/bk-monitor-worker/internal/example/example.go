// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package example

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type UserInfo struct {
	UserID int
}

func HandleExampleTask(ctx context.Context, t *task.Task) error {
	logger.Info("example func trigger")
	var p UserInfo
	if err := jsonx.Unmarshal(t.Payload, &p); err != nil {
		return fmt.Errorf("json.Unmarshal failed: %v", err)
	}

	// 逻辑处理start...
	logger.Printf("print user info: user_id=%d", p.UserID)
	return errors.New("this is a test")
}

func PeriodicHandleExampleTask(ctx context.Context, t *task.Task) error {
	logger.Info("periodic example func trigger")

	time.Sleep(2 * time.Minute)
	logger.Printf("periodic end")
	return nil
}

func PeriodicHandleExampleTask1(ctx context.Context, t *task.Task) error {
	logger.Info("periodic1 example func trigger")

	time.Sleep(2 * time.Minute)
	logger.Printf("periodic1 end")
	return nil
}
