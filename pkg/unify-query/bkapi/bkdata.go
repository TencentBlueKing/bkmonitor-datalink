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
	"encoding/json"
	"fmt"
	"sync"

	"github.com/spf13/viper"
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

const ()

type BkDataAPI struct {
	bkAPI *BkAPI

	uriPath string

	authConfig map[string]string
}

func GetBkDataAPI() *BkDataAPI {
	onceBkDataAPI.Do(func() {
		bkAPI := GetBkAPI()
		defaultBkDataAPI = &BkDataAPI{
			bkAPI:   bkAPI,
			uriPath: viper.GetString(BkDataUriPathConfigPath),
			authConfig: map[string]string{
				BkDataDataTokenKey:            viper.GetString(BkDataTokenConfigPath),
				BkDataAuthenticationMethodKey: viper.GetString(BkDataAuthenticationMethodConfigPath),
				BkUserNameKey:                 AdminUserName,
				BkAppCodeKey:                  bkAPI.GetCode(),
			},
		}
	})
	return defaultBkDataAPI
}

func (i *BkDataAPI) GetDataAuth() map[string]string {
	return i.authConfig
}

func (i *BkDataAPI) Headers(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		headers = make(map[string]string)
	}

	auth, _ := json.Marshal(i.authConfig)
	headers[BkDataAuthorization] = string(auth)
	return i.bkAPI.Headers(headers)
}

func (i *BkDataAPI) url(path string) string {
	url := i.bkAPI.Url(i.uriPath)
	if path != "" {
		url = fmt.Sprintf("%s/%s", url, path)
	}
	return url
}

func (i *BkDataAPI) QueryAsyncUrl() string {
	return i.url(QueryAsync)
}

func (i *BkDataAPI) QuerySyncUrl() string {
	return i.url(QuerySync)
}

func (i *BkDataAPI) QueryEsUrl() string {
	return fmt.Sprintf("%s/es", i.QuerySyncUrl())
}
