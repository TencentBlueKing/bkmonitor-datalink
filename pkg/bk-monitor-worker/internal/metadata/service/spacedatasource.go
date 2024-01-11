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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/pkg/errors"
)

// SpaceDataSourceSvc space datasource service
type SpaceDataSourceSvc struct {
	*space.SpaceDataSource
}

func NewSpaceDataSourceSvc(obj *space.SpaceDataSource) SpaceDataSourceSvc {
	return SpaceDataSourceSvc{
		SpaceDataSource: obj,
	}
}

// BulkCreateRecords 批量创建记录
func (SpaceDataSourceSvc) BulkCreateRecords(spaceType string, SpaceDataIdMap map[string][]uint, fromAuthorization bool) ([]string, error) {
	// NOTE: 当共享集群项目时，返回的数据中，共享集群即使这个项目下的专用集群，也是这个项目的共享集群, 因此，分开创建，避免相同的数据写入
	var changedSpaceIdList []string
	db := mysql.GetDBSession().DB
	for spaceId, dataids := range SpaceDataIdMap {
		var sdsList []space.SpaceDataSource
		if len(dataids) != 0 {
			if err := space.NewSpaceDataSourceQuerySet(db).Select(space.SpaceDataSourceDBSchema.BkDataId).SpaceTypeIdEq(spaceType).SpaceIdEq(spaceId).BkDataIdIn(dataids...).All(&sdsList); err != nil {
				return nil, errors.Wrapf(err, "query SpaceDataSource with space_type [%s] space_id [%s] data_id [%v] failed", spaceType, spaceId, dataids)
			}
		}
		dataidSet := mapset.NewSet[uint](dataids...)
		existDataidSet := mapset.NewSet[uint]()
		for _, sds := range sdsList {
			existDataidSet.Add(sds.BkDataId)
		}
		diffSet := dataidSet.Difference(existDataidSet)
		var changed bool
		for _, dataid := range diffSet.ToSlice() {
			sds := space.SpaceDataSource{
				SpaceTypeId:       spaceType,
				SpaceId:           spaceId,
				BkDataId:          dataid,
				FromAuthorization: fromAuthorization,
			}
			if err := sds.Create(db); err != nil {
				logger.Errorf("create SpaceDataSource with space_type_id [%s] space_id [%s] bk_data_id [%v] from_authorization [%v] failed, %v", spaceType, spaceId, dataid, fromAuthorization, err)
				continue
			}
			changed = true
		}
		if changed {
			changedSpaceIdList = append(changedSpaceIdList, spaceId)
		}
	}
	return changedSpaceIdList, nil
}
