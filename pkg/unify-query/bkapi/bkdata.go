// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bkapi

import (
	"context"
	"fmt"
	"maps"
	"os"
	"strings"
	"sync"

	"golang.org/x/sync/singleflight"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
)

const (
	BkDataAuthorization = "X-Bkbase-Authorization"

	QuerySync  = "query_sync"
	QueryAsync = "query_async"

	BkDataAuthenticationMethodKey = "bkdata_authentication_method"
	BkDataDataTokenKey            = "bkdata_data_token"
)

var (
	onceBkDataAPI    sync.Once
	defaultBkDataAPI *BkDataAPI
)

type BkDataAPI struct {
	bkAPI        *BkAPI
	adminLogin   string
	userAPIURL   string
	adminUsers   sync.Map // successful lookups by tenant for this configured login
	adminLookups singleflight.Group

	baseAddress string // 如果配置了 bk_data.address，则直接使用此地址，不再通过 bk_api.address 组装
	uriPath     string

	authConfig map[string]string

	clusterMap map[string]string
}

func GetBkDataAPI() *BkDataAPI {
	onceBkDataAPI.Do(func() {
		settings := currentSettings.Load()
		bkAPI := getBkAPI(settings)
		defaultBkDataAPI = &BkDataAPI{
			bkAPI:       bkAPI,
			baseAddress: settings.dataAddress,
			uriPath:     settings.dataURI,
			authConfig: map[string]string{
				BkDataDataTokenKey:            settings.token,
				BkDataAuthenticationMethodKey: settings.authMethod,
				BkUserNameKey:                 AdminUserName,
				BkAppCodeKey:                  bkAPI.GetCode(),
			},
			clusterMap: settings.clusterMap,
		}
		// An initially empty method may become user on reload. An explicit token stays pinned.
		if settings.authMethod == "" || settings.authMethod == "user" {
			defaultBkDataAPI.adminLogin = strings.TrimSpace(os.Getenv("BK_ADMIN_USERNAME"))
			defaultBkDataAPI.userAPIURL = userAPIBaseURL(bkAPI.address, settings.dataURI)
		}
	})
	return defaultBkDataAPI
}

func (i *BkDataAPI) GetDataAuth() map[string]string {
	return i.dataAuth(currentSettings.Load())
}

func (i *BkDataAPI) dataAuth(settings *clientSettings) map[string]string {
	authConfig := make(map[string]string, len(i.authConfig))
	for k, v := range i.authConfig {
		authConfig[k] = v
	}
	if authConfig[BkDataDataTokenKey] == "" {
		authConfig[BkDataDataTokenKey] = settings.token
	}
	if authConfig[BkDataAuthenticationMethodKey] == "" {
		authConfig[BkDataAuthenticationMethodKey] = settings.authMethod
	}
	if authConfig[BkUserNameKey] == "" {
		authConfig[BkUserNameKey] = AdminUserName
	}
	if authConfig[BkAppCodeKey] == "" {
		authConfig[BkAppCodeKey] = settings.appCode
	}
	return authConfig
}

func (i *BkDataAPI) Headers(headers map[string]string) map[string]string {
	return i.headersForAuth(headers, i.GetDataAuth())
}

func (i *BkDataAPI) headersForAuth(headers, dataAuth map[string]string) map[string]string {
	if len(headers) == 0 {
		headers = make(map[string]string)
	}

	auth, _ := json.Marshal(dataAuth)
	headers[BkDataAuthorization] = string(auth)
	return i.bkAPI.headersForUser(headers, dataAuth[BkUserNameKey])
}

// QueryAuth builds the body and gateway headers together. Standalone storage clients
// historically send data authentication in the body and supply their own headers.
func QueryAuth(ctx context.Context, api *BkDataAPI, tenantID string, headers map[string]string) (map[string]string, map[string]string, error) {
	headers = maps.Clone(headers)
	if api == nil {
		return GetBkDataAPI().GetDataAuth(), headers, nil
	}
	settings := currentSettings.Load()
	auth := api.dataAuth(settings)
	if auth[BkDataAuthenticationMethodKey] == "user" && (settings.multiTenant || api.adminLogin != "") {
		if !settings.multiTenant {
			tenantID = "default"
		}
		username, err := api.tenantAdmin(ctx, tenantID)
		if err != nil {
			return nil, nil, err
		}
		auth[BkUserNameKey] = username
	}
	return auth, api.headersForAuth(headers, auth), nil
}

func (i *BkDataAPI) url(path string) string {
	var url string
	// 如果配置了 baseAddress，则直接使用，否则使用原来的组装方式
	if i.baseAddress != "" {
		url = i.baseAddress
	} else {
		url = i.bkAPI.Url(i.uriPath)
	}
	if path != "" {
		url = fmt.Sprintf("%s/%s/", url, path)
	}
	return url
}

func (i *BkDataAPI) QueryUrlForES(spaceUid string) string {
	return fmt.Sprintf("%s/es", i.QueryUrl(spaceUid))
}

func (i *BkDataAPI) QueryUrl(spaceUid string) string {
	p := make([]string, 0)
	if spaceUid != "" {
		if v, ok := i.clusterMap[spaceUid]; ok {
			if v != "" {
				p = append(p, v)
			}
		}
	}
	p = append(p, QuerySync)
	return i.url(strings.Join(p, "/"))
}
