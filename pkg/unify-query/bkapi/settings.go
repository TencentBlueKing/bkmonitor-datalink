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
	"sync/atomic"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/eventbus"
)

const (
	// BKDATA 配置
	BkDataAddressConfigPath              = "bk_data.address"
	BkDataUriPathConfigPath              = "bk_data.uri_path"
	BkDataAuthenticationMethodConfigPath = "bk_data.authentication_method"
	BkDataTokenConfigPath                = "bk_data.token"
	BkDataClusterSpaceUidConfigPath      = "bk_data.cluster_space_uid"

	// BKAPI 配置
	BkAPIAddressConfigPath = "bk_api.address"
	BkAPICodeConfigPath    = "bk_api.code"
	BkAPISecretConfigPath  = "bk_api.secret"
)

type clientSettings struct {
	apiAddress, appCode, appSecret string
	dataAddress, dataURI           string
	token, authMethod              string
	multiTenant                    bool
	clusterMap                     map[string]string
}

var currentSettings atomic.Pointer[clientSettings]

func init() {
	currentSettings.Store(&clientSettings{})
	eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPostParse, LoadConfig)
}

// LoadConfig publishes the accepted settings together before config reload returns.
func LoadConfig() {
	clusters := make(map[string]string)
	for name, spaces := range viper.GetStringMapStringSlice(BkDataClusterSpaceUidConfigPath) {
		for _, space := range spaces {
			if name != "" && space != "" {
				clusters[space] = name
			}
		}
	}
	currentSettings.Store(&clientSettings{
		apiAddress:  viper.GetString(BkAPIAddressConfigPath),
		appCode:     viper.GetString(BkAPICodeConfigPath),
		appSecret:   viper.GetString(BkAPISecretConfigPath),
		dataAddress: viper.GetString(BkDataAddressConfigPath),
		dataURI:     viper.GetString(BkDataUriPathConfigPath),
		token:       viper.GetString(BkDataTokenConfigPath),
		authMethod:  viper.GetString(BkDataAuthenticationMethodConfigPath),
		multiTenant: viper.GetBool(config.MultiTenantModeConfigPath),
		clusterMap:  clusters,
	})
}
