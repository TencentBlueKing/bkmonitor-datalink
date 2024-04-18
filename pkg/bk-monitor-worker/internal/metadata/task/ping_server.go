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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
	t "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// RefreshPingServer2Nodeman 刷新Ping Server配置至节点管理，下发ip列表到proxy机器
func RefreshPingServer2Nodeman(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("RefreshPingServer2Nodeman Runtime panic caught: %v", err)
		}
	}()
	logger.Infof("start refresh ping server to nodeman")
	svc := service.NewPingServerSubscriptionConfigSvc(nil)
	err := svc.RefreshPingConf("bkmonitorproxy")
	if err != nil {
		logger.Errorf("refresh ping config for bkmonitorproxy failed, %v", err)
	}
	err = svc.RefreshPingConf("bk-collector")
	if err != nil {
		logger.Errorf("refresh ping config for bk-collector failed, %v", err)
	}
	logger.Infof("refresh ping server to nodeman finished")
	return nil
}
