// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bcsclustermanager"

	"github.com/TencentBlueKing/bk-apigateway-sdks/core/bkapi"
	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bkgse"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

var gseApi *bkgse.Client
var bcsClusterManager *bcsclustermanager.Client
var cmdbApi *cmdb.Client

const (
	BkApiBaseUrlPath       = "bk_api.api_url"
	BkApiStagePath         = "bk_api.stage"
	BkApiAppCodePath       = "bk_api.app_code"
	BkApiAppSecretPath     = "bk_api.app_secret"
	BkApiUseApiGatewayPath = "bk_api.use_api_gateway"
)

// GetGseApi 获取GseApi客户端
func GetGseApi() (*bkgse.Client, error) {
	if gseApi != nil {
		return gseApi, nil
	}
	var config define.ClientConfigProvider
	useApiGateWay := viper.GetBool(BkApiUseApiGatewayPath)
	if useApiGateWay {
		config = bkapi.ClientConfig{
			BkApiUrlTmpl:  fmt.Sprintf("%s/api/{api_name}/", viper.GetString(BkApiBaseUrlPath)),
			Stage:         viper.GetString(BkApiStagePath),
			AppCode:       viper.GetString(BkApiAppCodePath),
			AppSecret:     viper.GetString(BkApiAppSecretPath),
			JsonMarshaler: jsonx.Marshal,
		}
	} else {
		config = bkapi.ClientConfig{
			Endpoint:            fmt.Sprintf("%s/api/c/compapi/v2/gse/", viper.GetString(BkApiBaseUrlPath)),
			Stage:               viper.GetString(BkApiStagePath),
			AppCode:             viper.GetString(BkApiAppCodePath),
			AppSecret:           viper.GetString(BkApiAppSecretPath),
			JsonMarshaler:       jsonx.Marshal,
			AuthorizationParams: map[string]string{"bk_username": "admin"},
		}
	}
	var err error
	gseApi, err = bkgse.New(useApiGateWay, config, bkapi.OptJsonResultProvider(), bkapi.OptJsonBodyProvider())
	if err != nil {
		return nil, err
	}
	return gseApi, nil
}

const (
	BkApiBcsApiGatewaySchemaPath = "bk_api.bcs_api_gateway_schema"
	BkApiBcsApiGatewayHostPath   = "bk_api.bcs_api_gateway_host"
	BkApiBcsApiGatewayPortPath   = "bk_api.bcs_api_gateway_port"
	BkApiBcsApiGatewayTokenPath  = "bk_api.bcs_api_gateway_token"
)

func GetBcsClusterManagerApi() (*bcsclustermanager.Client, error) {
	if bcsClusterManager != nil {
		return bcsClusterManager, nil
	}
	config := bkapi.ClientConfig{
		Endpoint:            fmt.Sprintf("%s://%s:%v/bcsapi/v4/clustermanager/v1/", viper.GetString(BkApiBcsApiGatewaySchemaPath), viper.GetString(BkApiBcsApiGatewayHostPath), viper.GetInt(BkApiBcsApiGatewayPortPath)),
		AuthorizationParams: map[string]string{"Authorization": fmt.Sprintf("Bearer %s", viper.GetString(BkApiBcsApiGatewayTokenPath))},
		JsonMarshaler:       jsonx.Marshal,
	}
	var err error
	bcsClusterManager, err = bcsclustermanager.New(true, config, bkapi.OptJsonResultProvider(), bkapi.OptJsonBodyProvider())
	if err != nil {
		return nil, err
	}
	return bcsClusterManager, nil
}

func GetCmdbApi() (*cmdb.Client, error) {
	if bcsClusterManager != nil {
		return cmdbApi, nil
	}
	config := bkapi.ClientConfig{
		Endpoint:            fmt.Sprintf("%s/api/c/compapi/v2/cc/", viper.GetString(BkApiBaseUrlPath)),
		AuthorizationParams: map[string]string{"bk_username": "admin", "bk_supplier_account": "0"},
		AppCode:             viper.GetString(BkApiAppCodePath),
		AppSecret:           viper.GetString(BkApiAppSecretPath),
		JsonMarshaler:       jsonx.Marshal,
	}

	var err error
	cmdbApi, err = cmdb.New(config, bkapi.OptJsonResultProvider(), bkapi.OptJsonBodyProvider())
	if err != nil {
		return nil, err
	}
	return cmdbApi, nil
}
