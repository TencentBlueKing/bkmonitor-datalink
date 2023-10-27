// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

func init() {
	define.RegisterScheduler("apple", func(ctx context.Context, name string) (define.Scheduler, error) {
		conf := config.FromContext(ctx)
		if conf == nil {
			return nil, define.ErrOperationForbidden
		}
		ctx, cancel := context.WithTimeout(ctx, conf.GetDuration(ConfSchedulerAppleLifeKey))
		utils.CheckError(eventbus.SubscribeAsync(eventbus.EvSysExit, cancel, false))

		return define.NewScheduler(ctx, "watch")
	})
}
