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

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
	t "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// RefreshBkccSpaceName 刷新 bkcc 类型空间名称
func RefreshBkccSpaceName(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("Runtime panic caught: %v\n", err)
		}
	}()
	logger.Info("start sync bkcc space name task")
	svc := service.NewSpaceSvc(nil)
	if err := svc.RefreshBkccSpaceName(); err != nil {
		return errors.Wrap(err, "refresh bkcc space name failed")
	}
	logger.Info("refresh bkcc space name successfully")
	return nil
}

// RefreshBcsProjectBiz 检测 bcs 项目绑定的业务的变化
func RefreshBcsProjectBiz(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("RefreshBcsProjectBiz Runtime panic caught: %v", err)
		}
	}()
	logger.Info("start check and update the bind biz of bcs project task")
	svc := service.NewSpaceSvc(nil)
	if err := svc.RefreshBcsProjectBiz(); err != nil {
		return errors.Wrap(err, "refresh bcs project biz failed")
	}
	logger.Info("refresh bcs project biz successfully")
	return nil
}
