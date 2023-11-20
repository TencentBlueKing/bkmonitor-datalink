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

	"github.com/TencentBlueKing/bk-apigateway-sdks/core/bkapi"
	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"

	projConfig "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bkgse"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

var gseApi *bkgse.Client

// GetGseApi 获取GseApi客户端
func GetGseApi() (*bkgse.Client, error) {
	if gseApi != nil {
		return gseApi, nil
	}
	var config define.ClientConfigProvider
	if projConfig.BkApiEnabled {
		config = bkapi.ClientConfig{
			BkApiUrlTmpl:  fmt.Sprintf("%s/api/{api_name}/", projConfig.BkApiUrl),
			Stage:         projConfig.BkApiStage,
			AppCode:       projConfig.BkApiAppCode,
			AppSecret:     projConfig.BkApiAppSecret,
			JsonMarshaler: jsonx.Marshal,
		}
	} else {
		config = bkapi.ClientConfig{
			Endpoint:            fmt.Sprintf("%s/api/c/compapi/v2/gse/", projConfig.BkApiUrl),
			Stage:               projConfig.BkApiStage,
			AppCode:             projConfig.BkApiAppCode,
			AppSecret:           projConfig.BkApiAppSecret,
			JsonMarshaler:       jsonx.Marshal,
			AuthorizationParams: map[string]string{"bk_username": "admin"},
		}
	}

	gseApi, err := bkgse.New(projConfig.BkApiEnabled, config, bkapi.OptJsonResultProvider(), bkapi.OptJsonBodyProvider())
	if err != nil {
		return nil, err
	}
	return gseApi, nil
}
