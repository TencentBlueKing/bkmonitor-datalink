// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tenant

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/user"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
)

const (
	DefaultTenantId           = "system"
	TenantListRefreshInterval = 5 * time.Minute // 缓存刷新间隔，可根据需要调整
)

var (
	tenantList           []user.ListTenantData
	tenantListRWMutex    sync.RWMutex
	lastTenantListUpdate time.Time
)

// GetTenantIdByBkBizId 根据 bkBizId 获取租户 ID
func GetTenantIdByBkBizId(bkBizId int) (string, error) {
	if !cfg.EnableMultiTenantMode {
		return DefaultTenantId, nil
	}

	// 当业务 id 为负数是，业务 ID 取正等于space.id
	// 当业务 id 为正数是，空间类型为 bkcc, space.space_id 等于业务 id
	spaces := make([]space.Space, 0)
	db := mysql.GetDBSession().DB
	if bkBizId < 0 {
		db.Where("id = ?", -bkBizId).Find(&spaces)
	} else {
		db.Where("space_id = ?", bkBizId).Where("space_type_id = ?", "bkcc").Find(&spaces)
	}
	if len(spaces) == 0 {
		return DefaultTenantId, nil
	}
	if len(spaces) > 1 {
		return DefaultTenantId, errors.New("multiple spaces found")
	}
	return spaces[0].BkTenantId, nil
}

// GetTenantIdBySpaceUID 根据 spaceUID 获取租户 ID
func GetTenantIdBySpaceUID(spaceUID string) (string, error) {
	if !cfg.EnableMultiTenantMode {
		return DefaultTenantId, nil
	}

	// 分割 spaceUID，格式为 space_type_id__space_id
	splits := strings.Split(spaceUID, "__")
	if len(splits) != 2 {
		return "", fmt.Errorf("invalid space uid: %s", spaceUID)
	}

	spaceTypeId := splits[0]
	spaceId := splits[1]

	db := mysql.GetDBSession().DB
	spaces := make([]space.Space, 0)
	db.Where("space_type_id = ?", spaceTypeId).Where("space_id = ?", spaceId).Find(&spaces)
	if len(spaces) == 0 {
		return DefaultTenantId, nil
	}
	if len(spaces) > 1 {
		return DefaultTenantId, errors.New("multiple spaces found")
	}
	return spaces[0].BkTenantId, nil
}

// GetTenantList
func GetTenantList() ([]user.ListTenantData, error) {
	now := time.Now()
	tenantListRWMutex.RLock()
	// check cache expired
	if len(tenantList) > 0 && now.Sub(lastTenantListUpdate) < TenantListRefreshInterval {
		defer tenantListRWMutex.RUnlock()
		return tenantList, nil
	}
	tenantListRWMutex.RUnlock()

	tenantListRWMutex.Lock()
	defer tenantListRWMutex.Unlock()

	// double check, prevent duplicate refresh
	if len(tenantList) > 0 && time.Since(lastTenantListUpdate) < TenantListRefreshInterval {
		return tenantList, nil
	}

	if !cfg.EnableMultiTenantMode {
		// single tenant mode
		tenantList = []user.ListTenantData{
			{
				Id:   DefaultTenantId,
				Name: "System",
			},
		}
		lastTenantListUpdate = time.Now()
		return tenantList, nil
	} else {
		// multi tenant mode
		userApi, _ := api.GetUserApi(DefaultTenantId)

		// query tenant list
		var result user.ListTenantResp
		_, err := userApi.ListTenant().SetResult(&result).Request()
		if err != nil {
			return nil, err
		}

		tenantList = result.Data
		lastTenantListUpdate = time.Now()
		return tenantList, nil
	}
}
