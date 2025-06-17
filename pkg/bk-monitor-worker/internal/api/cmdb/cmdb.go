// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdb

import (
	"github.com/TencentBlueKing/bk-apigateway-sdks/core/bkapi"
	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
)

// Client for cmdb
type Client struct {
	define.BkApiClient

	// 是否使用api网关
	useApiGateway bool
}

// New cmdb client
func New(configProvider define.ClientConfigProvider, opts ...define.BkApiClientOption) (*Client, error) {
	client, err := bkapi.NewBkApiClient("cmdb", configProvider, opts...)
	if err != nil {
		return nil, err
	}
	return &Client{BkApiClient: client, useApiGateway: cfg.BkApiCmdbApiGatewayUrl != ""}, nil
}

// SearchCloudArea for cmdb resource search_cloud_area
// 查询云区域信息
func (c *Client) SearchCloudArea(opts ...define.OperationOption) define.Operation {
	var path string
	if c.useApiGateway {
		path = "/api/v3/findmany/cloudarea"
	} else {
		path = "search_cloud_area"
	}

	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "search_cloud_area",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// ListBizHostsTopo for cmdb resource list_biz_hosts_topo
// 查询业务主机及关联拓扑
func (c *Client) ListBizHostsTopo(opts ...define.OperationOption) define.Operation {
	/*
		@params
		bk_biz_id | int | 业务id
		host_property_filter ｜ [map] | 查询条件
		fields | [string] | 查询字段
	*/
	var path string
	if c.useApiGateway {
		path = "/api/v3/hosts/app/{bk_biz_id}/list_hosts_topo"
	} else {
		path = "list_biz_hosts_topo"
	}

	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "list_biz_hosts_topo",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// ListHostsWithoutBiz for cmdb resource list_hosts_without_biz
// 跨业务主机查询
func (c *Client) ListHostsWithoutBiz(opts ...define.OperationOption) define.Operation {
	/*
		@params
		bk_biz_id | int | 业务id
		host_property_filter ｜ [map] | 查询条件
		fields | [string] | 查询字段
	*/
	var path string
	if c.useApiGateway {
		path = "/api/v3/hosts/list_hosts_without_app"
	} else {
		path = "list_hosts_without_biz"
	}

	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "list_hosts_without_biz",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// FindHostBizRelation for cmdb resource list_hosts_without_biz
// 查询主机业务关系信息
func (c *Client) FindHostBizRelation(opts ...define.OperationOption) define.Operation {
	/*
		@params
		bk_host_id | [int] | 主机id列表
	*/
	var path string
	if c.useApiGateway {
		path = "/api/v3/hosts/modules/read"
	} else {
		path = "find_host_biz_relations"
	}

	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "find_host_biz_relations",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// SearchBusiness for cmdb resource search_business
// 查询业务信息
func (c *Client) SearchBusiness(opts ...define.OperationOption) define.Operation {
	var path string
	if c.useApiGateway {
		path = "/api/v3/biz/search/0"
	} else {
		path = "search_business"
	}
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "search_business",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// SearchBizInstTopo for cmdb resource search_biz_inst_topo
// 查询业务拓扑
func (c *Client) SearchBizInstTopo(opts ...define.OperationOption) define.Operation {
	var path string
	if c.useApiGateway {
		path = "/api/v3/find/topoinst/biz/{bk_biz_id}"
	} else {
		path = "search_biz_inst_topo"
	}

	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "search_biz_inst_topo",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// GetBizInternalModule for cmdb resource get_biz_internal_module
// 查询业务内部模块
func (c *Client) GetBizInternalModule(opts ...define.OperationOption) define.Operation {
	var path string
	var method string
	if c.useApiGateway {
		path = "/api/v3/topo/internal/0/{bk_biz_id}"
		method = "GET"
	} else {
		path = "get_biz_internal_module"
		method = "POST"
	}

	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "get_biz_internal_module",
		Method: method,
		Path:   path,
	}, opts...)
}

// SearchObjectAttribute for cmdb resource search_object_attribute
// 查询对象属性
func (c *Client) SearchObjectAttribute(opts ...define.OperationOption) define.Operation {
	var path string
	if c.useApiGateway {
		path = "/api/v3/find/objectattr"
	} else {
		path = "search_object_attribute"
	}
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "search_object_attribute",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// ResourceWatch for cmdb resource resource_watch
// 资源变更订阅
func (c *Client) ResourceWatch(opts ...define.OperationOption) define.Operation {
	var path string
	if c.useApiGateway {
		path = "/api/v3/event/watch/resource/{bk_resource}"
	} else {
		path = "resource_watch"
	}
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "resource_watch",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// SearchModule for cmdb resource search_module
// 查询模块信息
func (c *Client) SearchModule(opts ...define.OperationOption) define.Operation {
	var path string
	if c.useApiGateway {
		path = "/api/v3/module/search/0/{bk_biz_id}/{bk_set_id}"
	} else {
		path = "search_module"
	}
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "search_module",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// SearchSet for cmdb resource search_set
// 查询集群信息
func (c *Client) SearchSet(opts ...define.OperationOption) define.Operation {
	var path string
	if c.useApiGateway {
		path = "/api/v3/set/search/0/{bk_biz_id}"
	} else {
		path = "search_set"
	}

	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "search_set",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// ListServiceInstanceDetail for cmdb resource list_service_instance_detail
// 查询服务实例详情
func (c *Client) ListServiceInstanceDetail(opts ...define.OperationOption) define.Operation {
	var path string
	if c.useApiGateway {
		path = "/api/v3/findmany/proc/service_instance/details"
	} else {
		path = "list_service_instance_detail"
	}
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "list_service_instance_detail",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// SearchDynamicGroup for cmdb resource search_dynamic_group
// 查询动态分组
func (c *Client) SearchDynamicGroup(opts ...define.OperationOption) define.Operation {
	var path string
	if c.useApiGateway {
		path = "/api/v3/dynamicgroup/search/{bk_biz_id}"
	} else {
		path = "search_dynamic_group"
	}
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "search_dynamic_group",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// ExecuteDynamicGroup for cmdb resource execute_dynamic_group
// 执行动态分组
func (c *Client) ExecuteDynamicGroup(opts ...define.OperationOption) define.Operation {
	var path string
	if c.useApiGateway {
		path = "/api/v3/dynamicgroup/data/{bk_biz_id}/{id}"
	} else {
		path = "execute_dynamic_group"
	}
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "execute_dynamic_group",
		Method: "POST",
		Path:   path,
	}, opts...)
}
