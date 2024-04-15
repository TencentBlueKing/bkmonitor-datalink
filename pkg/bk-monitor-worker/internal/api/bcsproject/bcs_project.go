// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bcsproject

import (
	"github.com/TencentBlueKing/bk-apigateway-sdks/core/bkapi"
	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"
)

// Client for bcs project
type Client struct {
	define.BkApiClient
}

// New bcs_project client
func New(configProvider define.ClientConfigProvider, opts ...define.BkApiClientOption) (*Client, error) {
	client, err := bkapi.NewBkApiClient("bcs-project", configProvider, opts...)
	if err != nil {
		return nil, err
	}

	return &Client{BkApiClient: client}, nil
}

// GetProjects for bcs project resource get_projects
// 从bcs-project查询项目信息
func (c *Client) GetProjects(opts ...define.OperationOption) define.Operation {
	/*
		@params
		limit 	| int 	 | 每页限制
		offset  | int 	 | 数据偏移量
		kind 	| string | 项目类型
	*/
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "get projects",
		Method: "GET",
		Path:   "projects",
	}, opts...)
}
