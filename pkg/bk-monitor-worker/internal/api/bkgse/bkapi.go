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
	useApiGateway bool
}

// New bk_gse client
func New(useApiGateway bool, configProvider define.ClientConfigProvider, opts ...define.BkApiClientOption) (*Client, error) {
	client, err := bkapi.NewBkApiClient("bk-gse", configProvider, opts...)
	if err != nil {
		return nil, err
	}

	return &Client{BkApiClient: client, useApiGateway: useApiGateway}, nil
}

// AddRoute for bkapi resource add_route
// 注册数据路由
func (c *Client) AddRoute(opts ...define.OperationOption) define.Operation {
	/*
		@params
		metadata | map{plat_name: string | 路由所属的平台 | required, label: map | 可选信息, channel_id: int | 路由ID} | 所属平台的源信息 | required
		operation | map{operator_name: string | API调用者 | required} | 操作人配置 | required
		route | map{name: string | 路由名称 |required ,stream_to: map{stream_to_id: int | 数据接收端配置ID | required, kafka: map | Kafka存储信息, redis: map | Redis存储信息, pulsar: map | Pulsar存储信息} | required, filter_name_and: [string] | 与条件, filter_name_or: [string] | 或条件} | 路由入库配置
		stream_filters | [int] | 过滤规则配置
	*/
	path := "/api/v2/data/add_route"
	if !c.useApiGateway {
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
	/*
		@params
		metadata | map{plat_name: string | 路由所属的平台 | required, label: map | 可选信息} | 所属平台的源信息 | required
		operation | map{operator_name: string | API调用者 | required} | 操作人配置 | required
		stream_to | map{name: string | 接收端名称 | required, report_mode: string | 接收端类型 | kafka/redis/pulsar/file,  data_log: string | 文件路径, kafka: map | Kafka存储信息, redis: map | Redis存储信息, pulsar: map | Pulsar存储信息} | 接收端详细配置
	*/
	path := "/api/v2/data/add_streamto"
	if !c.useApiGateway {
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
	/*
		@params
		condition | map{channel_id: int | 路由ID | required, plat_name: string | 路由所属的平台 | required, label: map | 可选信息} | 条件信息 | required
		operation | map{operator_name: string | API调用者 | required, method: string | 指定删除方式 | all/specification | required} | 操作配置 | required
		specification | map{route: [string] | 路由名称列表, stream_filters: [string] | 过滤条件名称列表} | 接收端详细配置
	*/
	path := "/api/v2/data/delete_route"
	if !c.useApiGateway {
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
	/*
		@params
		condition | map{stream_to: int | 接收端配置的ID | required, plat_name: string | 路由所属的平台 | required} | 条件信息 | required
		operation | map{operator_name: string | API调用者 | required} | 操作人配置 | required
	*/
	path := "/api/v2/data/delete_streamto"
	if !c.useApiGateway {
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
	/*
		@params
		namespace | string | 命名空间
		hosts | map{ip: string| IP地址, bk_cloud_id | int | 云区域ID} | 主机列表
		agent_id_list | [string] | Agent ID列表
		meta | map{namespace: string | 命名空间, name: string | 进程名, labels: map{proc_name: string | 进程名称}} | 元信息
	*/
	path := "/api/v2/data/get_proc_status"
	if !c.useApiGateway {
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
	/*
		@params
		agent_id_list | [string] | agent ID列表
	*/
	path := "/api/v2/data/query_route"
	if !c.useApiGateway {
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
	/*
		@params
		condition | map{stream_to_id: int | 接收端配置的ID, plat_name: string | 接收端配置所属的平台 | required, label: map | 可选信息} | 条件信息 | required
		operation | map{operator_name: string | API调用者 | required, method: string | 指定删除方式 | all/specification | required} | 操作配置 | required

	*/
	path := "/api/v2/data/query_streamto"
	if !c.useApiGateway {
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
	/*
		@params
		condition | map{channel_id: int | 路由ID | required, plat_name: string | 路由所属的平台 | required, label: map | 可选信息} | 条件信息 | required
		operation | map{name: string | 路由名称 | required, stream_to: map{stream_to_id: int | 数据接收端配置信息 | required, kafka: map | Kafka存储信息, redis: map | Redis存储信息, pulsar: map | Pulsar存储信息} | required, filter_name_and: [string] | 与条件, filter_name_or: [string] | 或条件} | 路由入库配置
		specification | map{route: [string] | 路由名称列表, stream_filters: [string] | 过滤条件名称列表} | 路由信息 | required

	*/
	path := "/api/v2/data/update_route"
	if !c.useApiGateway {
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
	/*
		@params
		condition | map{stream_to_id: int | 接收端配置的ID | required, plat_name: string | 接收端配置所属的平台 | required} | 条件信息 | required
		operation | map{operator_name: string | API调用者 | required} | 操作人配置 | required
		stream_to | map{name: string | 接收端名称 | required, report_mode: string | 接收端类型 | kafka/redis/pulsar/file,  data_log: string | 文件路径, kafka: map | Kafka存储信息, redis: map | Redis存储信息, pulsar: map | Pulsar存储信息} | 接收端详细配置
	*/
	path := "/api/v2/data/update_streamto"
	if !c.useApiGateway {
		path = "config_update_streamto"
	}
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "update_streamto",
		Method: "POST",
		Path:   path,
	}, opts...)
}
