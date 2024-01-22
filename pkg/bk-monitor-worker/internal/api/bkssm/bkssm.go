// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bkssm

import (
	"github.com/TencentBlueKing/bk-apigateway-sdks/core/bkapi"
	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
)

// Client for bkssm
type Client struct {
	define.BkApiClient
}

// New bkssm client
func New(configProvider define.ClientConfigProvider, opts ...define.BkApiClientOption) (*Client, error) {
	client, err := bkapi.NewBkApiClient("bkssm", configProvider, opts...)
	if err != nil {
		return nil, err
	}

	return &Client{BkApiClient: client}, nil
}

// GetAccessToken for bkssm resource get_access_token
func (c *Client) GetAccessToken(opts ...define.OperationOption) define.Operation {
	path := "access-tokens"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "get_access_token",
		Method: "POST",
		Path:   path,
	}, opts...).SetBody(map[string]interface{}{
		"app_code":    cfg.BkApiAppCode,
		"app_secret":  cfg.BkApiAppSecret,
		"env_name":    "prod",
		"id_provider": "client",
		"grant_type":  "client_credentials",
	})
}
