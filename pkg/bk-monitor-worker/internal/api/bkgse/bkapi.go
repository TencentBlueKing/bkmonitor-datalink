// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bkgse

import (
	"github.com/TencentBlueKing/bk-apigateway-sdks/core/bkapi"
	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"
)

// VERSION for resource definitions
const VERSION = "2023092718195302"

// Client for bkapi bk_gse
type Client struct {
	define.BkApiClient
	UseApiGateway bool
}

// New bk_gse client
func New(useApiGateway bool, configProvider define.ClientConfigProvider, opts ...define.BkApiClientOption) (*Client, error) {
	client, err := bkapi.NewBkApiClient("bk-gse", configProvider, opts...)
	if err != nil {
		return nil, err
	}

	return &Client{BkApiClient: client, UseApiGateway: useApiGateway}, nil
}

// AddRoute for bkapi resource add_route
// 注册数据路由
func (c *Client) AddRoute(opts ...define.OperationOption) define.Operation {
	path := "/api/v2/data/add_route"
	if !c.UseApiGateway {
		path = "config_add_route"
	}
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "add_route",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// AddStreamto for bkapi resource add_streamto
// 添加数据入库的消息队列，第三方服务配置
func (c *Client) AddStreamto(opts ...define.OperationOption) define.Operation {
	path := "/api/v2/data/add_streamto"
	if !c.UseApiGateway {
		path = "config_add_streamto"
	}
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "add_streamto",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// DeleteRoute for bkapi resource delete_route
// 删除路由
func (c *Client) DeleteRoute(opts ...define.OperationOption) define.Operation {
	path := "/api/v2/data/delete_route"
	if !c.UseApiGateway {
		path = "config_delete_route"
	}
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "delete_route",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// DeleteStreamto for bkapi resource delete_streamto
// 删除数据路由入库配置
func (c *Client) DeleteStreamto(opts ...define.OperationOption) define.Operation {
	path := "/api/v2/data/delete_streamto"
	if !c.UseApiGateway {
		path = "config_delete_streamto"
	}
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "delete_streamto",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// GetProcStatus for bkapi resource get_proc_status
// 查询进程状态信息
func (c *Client) GetProcStatus(opts ...define.OperationOption) define.Operation {
	path := "/api/v2/data/get_proc_status"
	if !c.UseApiGateway {
		path = "get_proc_status"
	}
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "get_proc_status",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// ListAgentState for bkapi resource list_agent_state
// 查询Agent状态列表信息
func (c *Client) ListAgentState(opts ...define.OperationOption) define.Operation {
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "list_agent_state",
		Method: "POST",
		Path:   "/api/v2/cluster/list_agent_state",
	}, opts...)
}

// QueryRoute for bkapi resource query_route
// 查询数据路由配置信息
func (c *Client) QueryRoute(opts ...define.OperationOption) define.Operation {
	path := "/api/v2/data/query_route"
	if !c.UseApiGateway {
		path = "config_query_route"
	}
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "query_route",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// QueryStreamto for bkapi resource query_streamto
// 查询数据入库消息队列或第三方平台的配置
func (c *Client) QueryStreamto(opts ...define.OperationOption) define.Operation {
	path := "/api/v2/data/query_streamto"
	if !c.UseApiGateway {
		path = "config_query_streamto"
	}
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "query_streamto",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// UpdateRoute for bkapi resource update_route
// 更新路由配置
func (c *Client) UpdateRoute(opts ...define.OperationOption) define.Operation {
	path := "/api/v2/data/update_route"
	if !c.UseApiGateway {
		path = "config_update_route"
	}
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "update_route",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// UpdateStreamto for bkapi resource update_streamto
// 更新数据路由入库配置
func (c *Client) UpdateStreamto(opts ...define.OperationOption) define.Operation {
	path := "/api/v2/data/update_streamto"
	if !c.UseApiGateway {
		path = "config_update_streamto"
	}
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "update_streamto",
		Method: "POST",
		Path:   path,
	}, opts...)
}
