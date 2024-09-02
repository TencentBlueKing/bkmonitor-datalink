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
	AdminUserName      = "admin"
	BkApiAuthorization = "X-Bkapi-Authorization"
)

type BkApi struct {
	address string

	code   string
	secret string
}

var (
	onceBkApi    sync.Once
	defaultBkApi *BkApi
)

func GetBkApi() *BkApi {
	onceBkApi.Do(func() {
		defaultBkApi = &BkApi{
			address: viper.GetString(BkApiAddressConfigPath),
			code:    viper.GetString(BkApiCodeConfigPath),
			secret:  viper.GetString(BkApiSecretConfigPath),
		}
	})

	return defaultBkApi
}

func (i *BkApi) GetCode() string {
	return i.code
}

func (i *BkApi) Headers(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		headers = make(map[string]string)
	}
	headers[BkApiAuthorization] = fmt.Sprintf(
		`{"bk_username": "%s", "bk_app_code": "%s", "bk_app_secret": "%s"}`,
		AdminUserName, i.code, i.secret,
	)
	return headers
}

func (i *BkApi) Url(path string) string {
	url := i.address
	if path != "" {
		url = fmt.Sprintf("%s/%s", i.address, path)
	}
	return url
}
