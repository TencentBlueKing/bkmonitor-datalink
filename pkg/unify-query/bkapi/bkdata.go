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
	"fmt"
	"sync"

	"github.com/spf13/viper"
)

const (
	BkDataAuthorization = "X-Bkbase-Authorization"

	QuerySync  = "query_sync"
	QueryAsync = "query_async"
)

var (
	onceBkDataApi    sync.Once
	defaultBkDataApi *BkDataApi
)

type BkDataApi struct {
	bkApi *BkApi

	uriPath              string
	token                string
	authenticationMethod string
}

func GetBkDataApi() *BkDataApi {
	onceBkDataApi.Do(func() {
		defaultBkDataApi = &BkDataApi{
			bkApi:                GetBkApi(),
			token:                viper.GetString(BkDataTokenConfigPath),
			authenticationMethod: viper.GetString(BkDataAuthenticationMethodConfigPath),
			uriPath:              viper.GetString(BkDataUriPathConfigPath),
		}
	})
	return defaultBkDataApi
}

func (i *BkDataApi) Headers(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		headers = make(map[string]string)
	}
	headers[BkDataAuthorization] = fmt.Sprintf(
		`{"bkdata_authentication_method": "%s", "bkdata_data_token": "%s", "bk_username": "%s", "bk_app_code"}`,
		i.authenticationMethod, i.token, AdminUserName, i.bkApi.GetCode(),
	)
	return i.bkApi.Headers(headers)
}

func (i *BkDataApi) url(path string) string {
	url := i.bkApi.Url(i.uriPath)
	if path != "" {
		url = fmt.Sprintf("%s/%s", url, path)
	}
	return url
}

func (i *BkDataApi) QueryAsyncUrl() string {
	return i.url(QueryAsync)
}

func (i *BkDataApi) QuerySyncUrl() string {
	return i.url(QuerySync)
}

func (i *BkDataApi) QueryEsUrl() string {
	return fmt.Sprintf("%s/es", i.QuerySyncUrl())
}
