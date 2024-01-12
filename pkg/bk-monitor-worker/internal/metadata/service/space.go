// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package service

import (
	"context"
	"fmt"
	"strconv"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mapx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// SpaceSvc space service
type SpaceSvc struct {
	*space.Space
}

func NewSpaceSvc(obj *space.Space) SpaceSvc {
	return SpaceSvc{
		Space: obj,
	}
}

// RefreshBkccSpaceName 刷新 bkcc 类型空间名称
func (s *SpaceSvc) RefreshBkccSpaceName() error {
	// 获取bkcc业务cmdb数据信息
	bizIdNameMap, err := s.getBkccBizIdNameMap()
	if err != nil {
		return errors.Wrap(err, "getBkccBizIdNameMap failed")
	}
	// 更新数据库中记录
	db := mysql.GetDBSession().DB
	var spaceList []space.Space
	if err := space.NewSpaceQuerySet(db).SpaceTypeIdEq(models.SpaceTypeBKCC).All(&spaceList); err != nil {
		return errors.Wrap(err, "query bkcc space failed")
	}
	for _, sp := range spaceList {
		oldName := sp.SpaceName
		name, ok := bizIdNameMap[sp.SpaceId]
		// 不存在则跳过
		if !ok {
			continue
		}
		// 名称变动，需要更新
		if name != oldName {
			sp.SpaceName = name
			err := sp.Update(db, space.SpaceDBSchema.SpaceName)
			if err != nil {
				logger.Errorf("update bkcc space name [%s] to [%s] failed, %v", oldName, sp.SpaceName)
				continue
			}
			logger.Infof("update bkcc space name [%s] to [%s]", oldName, sp.SpaceName)
		}
	}
	return nil
}

// RefreshBkccSpace 同步 bkcc 的业务，自动创建对应的空间
func (s *SpaceSvc) RefreshBkccSpace() error {
	// 获取bkcc业务cmdb数据信息
	bizIdNameMap, err := s.getBkccBizIdNameMap()
	if err != nil {
		return errors.Wrap(err, "getBkccBizIdNameMap failed")
	}
	spaceIdSet := mapset.NewSet(mapx.GetMapKeys(bizIdNameMap)...)

	// 过滤已经创建空间的业务
	db := mysql.GetDBSession().DB
	var spaceList []space.Space
	if err := space.NewSpaceQuerySet(db).Select(space.SpaceDBSchema.SpaceId).SpaceTypeIdEq(models.SpaceTypeBKCC).All(&spaceList); err != nil {
		return errors.Wrap(err, "query bkcc space failed")
	}
	existSpaceIdSet := mapset.NewSet[string]()
	for _, sp := range spaceList {
		existSpaceIdSet.Add(sp.SpaceId)
	}

	diffSet := spaceIdSet.Difference(existSpaceIdSet)
	if diffSet.Cardinality() == 0 {
		logger.Infof("bkcc space need not add")
		return nil
	}
	var createdSpaces []interface{}
	for _, bizId := range diffSet.ToSlice() {
		bizName := bizIdNameMap[bizId]
		sp := space.Space{
			SpaceTypeId: models.SpaceTypeBKCC,
			SpaceId:     bizId,
			SpaceName:   bizName,
		}
		if err := sp.Create(db); err != nil {
			logger.Errorf("create Space with space_type_id [%s] space_id [%s] space_name [%s] failed, %v", models.SpaceTypeBKCC, bizId, bizName, err)
			continue
		}
		createdSpaces = append(createdSpaces, fmt.Sprintf("%s__%s", models.SpaceTypeBKCC, bizId))
	}
	if len(createdSpaces) != 0 {
		// 追加业务空间到 vm 查询的白名单中, 并通知到 unifyquery
		rds := redis.GetInstance(context.Background())
		if err := rds.SAdd(models.QueryVmSpaceUidListKey, createdSpaces...); err != nil {
			logger.Errorf("reids SAdd [%v] to channel [%s] failed, %v", createdSpaces, models.QueryVmSpaceUidListKey, err)
		}
		msg, err := jsonx.MarshalString(createdSpaces)
		if err != nil {
			return errors.Wrapf(err, "marshal space_id list [%v] failed", createdSpaces)
		}
		if err := rds.Publish(models.QueryVmSpaceUidChannelKey, msg); err != nil {
			return errors.Wrapf(err, "publish [%v] to [%v] failed", msg, models.QueryVmSpaceUidChannelKey)
		}
	}

	return nil
}

// 获取bkcc业务cmdb数据信息
func (*SpaceSvc) getBkccBizIdNameMap() (map[string]string, error) {
	cmdbApi, err := api.GetCmdbApi()
	if err != nil {
		return nil, errors.Wrap(err, "get cmdb api failed")
	}
	var bizResp cmdb.SearchBusinessResp
	if _, err := cmdbApi.SearchBusiness().SetResult(&bizResp).Request(); err != nil {
		return nil, errors.Wrap(err, "search business failed")
	}
	bizIdNameMap := make(map[string]string)
	for _, info := range bizResp.Data.Info {
		// 过滤出bkcc业务
		if info.BkBizId > 0 {
			bizIdStr := strconv.Itoa(info.BkBizId)
			bizIdNameMap[bizIdStr] = info.BkBizName
		}
	}
	return bizIdNameMap, nil
}
