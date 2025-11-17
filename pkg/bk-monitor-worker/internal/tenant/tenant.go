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
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

const (
	DefaultTenantId           = "system"
	TenantListRefreshInterval = 5 * time.Minute // 缓存刷新间隔，可根据需要调整
)

var (
	tenantList           []ListTenantData
	tenantListRWMutex    sync.RWMutex
	lastTenantListUpdate time.Time

	tenantAdminUserCache sync.Map
)

// sendRequestToUserApi send request to user api
func sendRequestToUserApi(tenantId string, method string, path string, urlParams map[string]string, response any) error {
	// build url
	baseUrl := fmt.Sprintf("%s/api/bk-user/prod/", cfg.BkApiUrl)
	url := fmt.Sprintf("%s%s", baseUrl, path)

	// create http client
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return err
	}

	// set url params
	if len(urlParams) > 0 {
		q := req.URL.Query()
		for k, v := range urlParams {
			q.Set(k, v)
		}
		req.URL.RawQuery = q.Encode()
	}

	// set tenant header
	req.Header.Set("X-Bk-Tenant-Id", tenantId)

	// set bkapi authorization header
	authStr, err := jsonx.Marshal(map[string]string{"bk_username": "admin", "bk_app_code": cfg.BkApiAppCode, "bk_app_secret": cfg.BkApiAppSecret})
	if err != nil {
		return err
	}
	req.Header.Set("X-Bkapi-Authorization", string(authStr))

	// send request
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// unmarshal response body to response
	return jsonx.Unmarshal(body, response)
}

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
func GetTenantList() ([]ListTenantData, error) {
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
		tenantList = []ListTenantData{
			{
				Id:   DefaultTenantId,
				Name: "System",
			},
		}
		lastTenantListUpdate = time.Now()
		return tenantList, nil
	} else {
		// multi tenant mode
		var result ListTenantResp
		err := sendRequestToUserApi(DefaultTenantId, http.MethodGet, "api/v3/open/tenants/", nil, &result)
		if err != nil {
			return nil, fmt.Errorf("failed to get tenant list, err: %v", err)
		}

		// handle api result error
		if !result.Result {
			return nil, fmt.Errorf("failed to get tenant list, code: %d, message: %s", result.Code, result.Message)
		}

		tenantList = result.Data
		lastTenantListUpdate = time.Now()
		return tenantList, nil
	}
}

// GetTenantAdminUser get tenant admin user
func GetTenantAdminUser(tenantId string) (string, error) {
	// single tenant mode use default admin user
	if !cfg.EnableMultiTenantMode {
		return "admin", nil
	}

	// check cache
	if val, ok := tenantAdminUserCache.Load(tenantId); ok {
		return val.(string), nil
	}

	// multi tenant mode use bk-user api to get admin virtual user
	var result BatchLookupVirtualUserResp
	urlParams := map[string]string{
		"lookup_field": "login_name",
		"lookups":      "bk_admin",
	}
	err := sendRequestToUserApi(tenantId, http.MethodGet, "api/v3/open/tenant/virtual-users/-/lookup/", urlParams, &result)
	if err != nil {
		return "", fmt.Errorf("failed to get tenant admin user, tenantId: %s, err: %v", tenantId, err)
	}

	// handle api result error
	if !result.Result {
		return "", fmt.Errorf("failed to get tenant admin user, tenantId: %s, code: %d, message: %s", tenantId, result.Code, result.Message)
	}

	// handle api empty result error
	if len(result.Data) == 0 {
		return "", fmt.Errorf("tenant admin user not found, tenantId: %s", tenantId)
	}

	// cache the admin user
	tenantAdminUserCache.Store(tenantId, result.Data[0].BkUsername)

	return result.Data[0].BkUsername, nil
}
