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
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
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

	logger.Infof("start sync bkcc space name task")
	cmdbApi, err := api.GetCmdbApi()
	if err != nil {
		return err
	}
	var bizResp cmdb.SearchBusinessResp
	if _, err := cmdbApi.SearchBusiness().SetResult(&bizResp).Request(); err != nil {
		return err
	}
	bizIdNameMap := make(map[string]string)
	var bizIdList []string
	for _, info := range bizResp.Data.Info {
		// 过滤出bkcc业务
		if info.BkBizId > 0 {
			bizIdStr := strconv.Itoa(info.BkBizId)
			bizIdNameMap[bizIdStr] = info.BkBizName
			bizIdList = append(bizIdList, bizIdStr)
		}
	}

	db := mysql.GetDBSession().DB
	var spaceList []space.Space
	if err := space.NewSpaceQuerySet(db).SpaceTypeIdEq(models.SpaceTypeBKCC).All(&spaceList); err != nil {
		return err
	}
	for _, sp := range spaceList {
		oldName := sp.SpaceName
		name, ok := bizIdNameMap[sp.SpaceId]
		// 不存在则跳过
		if !ok {
			continue
		}
		// 名称变动，需要更新
		if name != "" && name != oldName {
			sp.SpaceName = name
			err := sp.Update(db, space.SpaceDBSchema.SpaceName)
			if err != nil {
				logger.Errorf("update bkcc space name [%s] to [%s] failed, %v", oldName, sp.SpaceName)
				continue
			}
			logger.Infof("update bkcc space name [%s] to [%s]", oldName, sp.SpaceName)
		}
	}
	logger.Info("refresh bkcc space name successfully")
	return nil
}
