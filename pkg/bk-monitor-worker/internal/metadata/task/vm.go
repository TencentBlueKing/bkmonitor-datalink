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
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/dependentredis"
	t "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// RefreshQueryVMSpaceList 刷新查询 vm 的空间列表
func RefreshQueryVMSpaceList(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("Runtime panic caught: %v", err)
		}
	}()

	logger.Infof("start refresh query vm space list")
	// 获取空间列表
	spaceUidList := config.QueryVMSpaceUidList
	if len(spaceUidList) == 0 {
		logger.Warnf("no space_uid from QUERY_VM_SPACE_UID_LIST")
		return nil
	}
	// 推送到 redis
	client, err := dependentredis.GetInstance(ctx)
	if err != nil {
		return errors.Wrapf(err, "get redis client error, %v", err)
	}
	// 推送空间数据到 redis，用于创建时，推送失败或者没有推送的场景
	var uidList []interface{}
	for _, uid := range spaceUidList {
		uidList = append(uidList, uid)
	}
	if err := client.SAdd(config.QueryVMSpaceUidListKey, uidList...); err != nil {
		return err
	}
	// 进行 publish
	currTime := map[string]interface{}{"time": time.Now().Unix()}
	currTimeJson, err := jsonx.MarshalString(currTime)
	if err != nil {
		return err
	}
	if err := client.Publish(config.QueryVMSpaceUidChannelKey, currTimeJson); err != nil {
		return errors.Wrapf(err, "publish time [%s] to [%s] failed, %v", currTimeJson, config.QueryVMSpaceUidChannelKey, err)
	}
	logger.Infof("refresh query vm space list success")
	return nil
}
