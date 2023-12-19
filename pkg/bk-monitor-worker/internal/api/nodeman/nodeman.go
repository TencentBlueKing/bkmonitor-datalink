// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodeman

import (
	"github.com/TencentBlueKing/bk-apigateway-sdks/core/bkapi"
	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"
)

// Client for cmdb
type Client struct {
	define.BkApiClient
}

// New nodeman client
func New(configProvider define.ClientConfigProvider, opts ...define.BkApiClientOption) (*Client, error) {
	client, err := bkapi.NewBkApiClient("node_man", configProvider, opts...)
	if err != nil {
		return nil, err
	}

	return &Client{BkApiClient: client}, nil
}

// PluginInfo for nodeman resource search_cloud_area
// 查询插件信息
func (c *Client) PluginInfo(opts ...define.OperationOption) define.Operation {
	/*
		@params
		name	| string | 插件名 | required
		version | string | 版本号
	*/
	path := "plugin_info"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "search_cloud_area",
		Method: "GET",
		Path:   path,
	}, opts...)
}

// GetProxiesByBiz for nodeman resource get_proxies_by_biz
// 通过业务查询业务所使用的所有云区域下的ProxyIP
func (c *Client) GetProxiesByBiz(opts ...define.OperationOption) define.Operation {
	/*
		@params
		bk_biz_id	| int | 业务ID | required
	*/
	path := "api/host/biz_proxies/"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "get_proxies_by_biz",
		Method: "GET",
		Path:   path,
	}, opts...)
}

// UpdateSubscription for nodeman resource subscription_update
func (c *Client) UpdateSubscription(opts ...define.OperationOption) define.Operation {
	/*
		@params
		subscription_id	| int | 采集配置订阅id | required
		scope	| map{bk_biz_id: int | 业务ID, node_type: string| 采集对象类型| required | TOPO/INSTANCE/SERVICE_TEMPLATE/SET_TEMPLATE, nodes | [map{}] | 节点列表 | required} | 事件订阅监听的范围 | required
		steps	| [string] | 触发的动作 | required
		run_immediately	| bool | 是否立即触发
	*/
	path := "subscription_update/"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "subscription_update",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// CreateSubscription for nodeman resource subscription_create
func (c *Client) CreateSubscription(opts ...define.OperationOption) define.Operation {
	/*
		@params
		scope	| map{bk_biz_id: int | 业务ID, object_type: string | 采集目标类型| SERVICE/HOST, node_type: string| 采集对象类型 | required | TOPO/INSTANCE/SERVICE_TEMPLATE/SET_TEMPLATE, nodes | [map{}] | 节点列表 | required} | 事件订阅监听的范围 | required
		steps	| [string] | 触发的动作 | required
		target_hosts	| [string] | 远程采集机器 |
		run_immediately	| bool | 是否立即触发
	*/
	path := "subscription_create/"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "subscription_create",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// RunSubscription for nodeman resource subscription_run
func (c *Client) RunSubscription(opts ...define.OperationOption) define.Operation {
	/*
		@params
		subscription_id	| int | 采集配置订阅id | required
		scope	| map{node_type: string| 采集对象类型| required | TOPO/INSTANCE/SERVICE_TEMPLATE/SET_TEMPLATE, nodes | [map{}] | 节点列表 | required} | 事件订阅监听的范围
		actions	| [string] | 触发的动作
	*/
	path := "subscription_run/"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "subscription_run",
		Method: "POST",
		Path:   path,
	}, opts...)
}
