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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
)

const (
	AdminUserName      = "admin"
	BkAPIAuthorization = "X-Bkapi-Authorization"

	BkUserNameKey = "bk_username"
	BkAppCodeKey  = "bk_app_code"
	BkSecretKey   = "bk_app_secret"
)

type BkAPI struct {
	address string

	authConfig map[string]string
}

var (
	onceBkAPI    sync.Once
	defaultBkAPI *BkAPI
)

func GetBkAPI() *BkAPI {
	onceBkAPI.Do(func() {
		defaultBkAPI = &BkAPI{
			address: viper.GetString(BkAPIAddressConfigPath),
			authConfig: map[string]string{
				BkAppCodeKey:  viper.GetString(BkAPICodeConfigPath),
				BkSecretKey:   viper.GetString(BkAPISecretConfigPath),
				BkUserNameKey: AdminUserName,
			},
		}
	})

	return defaultBkAPI
}

func (i *BkAPI) GetCode() string {
	return i.authConfig[BkAppCodeKey]
}

func (i *BkAPI) Headers(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		headers = make(map[string]string)
	}
	auth, _ := json.Marshal(i.authConfig)
	headers[BkAPIAuthorization] = string(auth)
	return headers
}

func (i *BkAPI) Url(path string) string {
	url := i.address
	if path != "" {
		url = fmt.Sprintf("%s/%s/", i.address, path)
	}
	return url
}
