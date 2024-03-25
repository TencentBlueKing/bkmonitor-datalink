// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bkdata

import (
	"github.com/TencentBlueKing/bk-apigateway-sdks/core/bkapi"
	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"
)

// Client for bkdata
type Client struct {
	define.BkApiClient
}

// New bkdata client
func New(configProvider define.ClientConfigProvider, opts ...define.BkApiClientOption) (*Client, error) {
	client, err := bkapi.NewBkApiClient("bkdata", configProvider, opts...)
	if err != nil {
		return nil, err
	}

	return &Client{BkApiClient: client}, nil
}

// GetKafkaInfo for bkdata resource get_kafka_info
// 查询计算平台使用的 kafka 信息
func (c *Client) GetKafkaInfo(opts ...define.OperationOption) define.Operation {
	/*
		@params
		tags | string | tag标识
	*/
	path := "/v3/databus/bkmonitor/"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "get_kafka_info",
		Method: "GET",
		Path:   path,
	}, opts...)
}

// CreateDataHub for bkdata resource create_data_hub
// 数据接入及存储
func (c *Client) CreateDataHub(opts ...define.OperationOption) define.Operation {
	/*
		@params
		common | map{bk_biz_id: int | 业务ID, maintainer: string | 数据管理员, bk_username: string | 操作人, data_scenario: string | 接入类型} | 公共配置
		raw_data | map{raw_data_name: string | 数据源英文名称, raw_data_alias: string | 数据源中文名称, sensitivity: string | 数据敏感度, data_encoding: string | 数据编码, data_region: string | 数据所属区域, description: string | 数据源描述, data_source_tags: [string] | 数据来源标签, tags: [string] | 数据标签, data_scenario: json | 数据定义} | 原始数据配置
		clean | json | 数据清洗
		storage | [] | 数据存储
	*/
	path := "/v3/datahub/hubs/"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "create_data_hub",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// DataBusCleans for bkdata resource data_bus_cleans
// 接入数据清洗
func (c *Client) DataBusCleans(opts ...define.OperationOption) define.Operation {
	/*
		@params
		raw_data_id | string | 数据接入源ID | required
		json_config | string | 数据清洗配置，json格式 | required
		pe_config | string | 清洗规则的pe配置 | required
		bk_biz_id | int | 业务ID | required
		clean_config_name | string | 清洗配置名称 | required
		result_table_name | string | 清洗配置输出的结果表英文标识 | required
		result_table_name_alias | string | 清洗配置输出的结果表别名 | required
		fields | [map{field_name: string | 字段英文标识 | required, field_type: string | 字段类型 | required, field_alias: string | 字段别名 | required, is_dimension: string | 是否为维度字段 | required, field_index: int | 字段顺序索引 | required}] | 输出字段列表 | required
		description | string | 清洗配置描述信息
		bk_username | string | 用户名 | required
	*/
	path := "/v3/databus/cleans/"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "data_bus_cleans",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// StartDatabusCleans for bkdata resource start_databus_cleans
// 启动清洗配置
func (c *Client) StartDatabusCleans(opts ...define.OperationOption) define.Operation {
	/*
		@params
		result_table_id | string | 清洗结果表名称 | required
		storages | [string] | 分发任务的存储列表
		bk_username | string | 用户名

	*/
	path := "/v3/databus/tasks/"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "start_databus_cleans",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// StopDatabusCleans for bkdata resource stop_databus_cleans
// 停止清洗配置
func (c *Client) StopDatabusCleans(opts ...define.OperationOption) define.Operation {
	/*
		@params
		result_table_id | string | 清洗结果表名称 | required
		storages | [string] | 分发任务的存储列表
		bk_username | string | 用户名
	*/
	path := "/v3/databus/tasks/{result_table_id}/"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "stop_databus_cleans",
		Method: "DELETE",
		Path:   path,
	}, opts...)
}

// GetDatabusCleans for bkdata resource get_databus_cleans
// 停止清洗配置
func (c *Client) GetDatabusCleans(opts ...define.OperationOption) define.Operation {
	/*
		@params
		raw_data_id | string | 数据接入源ID | required
		bk_username | string | 用户名
	*/
	path := "/v3/databus/cleans/"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "get_databus_cleans",
		Method: "GET",
		Path:   path,
	}, opts...)
}

// CreateDataStorages for bkdata resource create_data_storages
// 创建数据入库
func (c *Client) CreateDataStorages(opts ...define.OperationOption) define.Operation {
	/*
		@params
		raw_data_id | string | 数据接入源ID | required
		data_type | string | 数据源类型 | required
		result_table_name | string | 清洗配置输出的结果表英文标识 | required
		result_table_name_alias | string | 业务ID | 清洗配置输出的结果表别名
		fields | [map{field_name: string | 字段英文标识 | required, field_type: string | 字段类型 | required, field_alias: string | 字段别名 | required, is_dimension: string | 是否为维度字段 | required, field_index: int | 字段顺序索引 | required}] | 输出字段列表 | required
		storage_type | string | 存储类型 | required
		storage_cluster | string | 存储集群 | required
		expires | string | 过期时间 | required
		config | map{schemaless: bool | schemaless} | config
	*/
	path := "/v3/databus/data_storages/"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "create_data_storages",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// UpdateDatabusCleans for bkdata resource update_databus_cleans
// 更新数据清洗
func (c *Client) UpdateDatabusCleans(opts ...define.OperationOption) define.Operation {
	/*
		@params
		processing_id | string | 清洗配置ID | required
		raw_data_id | string | 数据接入源ID | required
		json_config | string | 数据清洗配置，json格式 | required
		pe_config | string |清洗规则的pe配置
		bk_biz_id | int | 业务ID | required
		clean_config_name | string | 清洗配置名称 | required
		result_table_name | string | 清洗配置输出的结果表英文标识 | required
		result_table_name_alias | string | 清洗配置输出的结果表别名 | required
		fields | [map{field_name: string | 字段英文标识 | required, field_type: string | 字段类型 | required, field_alias: string | 字段别名 | required, is_dimension: string | 是否为维度字段 | required, field_index: int | 字段顺序索引 | required}] | 输出字段列表 | required
		description | string | 清洗配置描述信息
		bk_username | string | 用户名
	*/
	path := "/v3/databus/cleans/{processing_id}/"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "update_databus_cleans",
		Method: "PUT",
		Path:   path,
	}, opts...)
}

// AccessDeployPlan for bkdata resource access_deploy_plan
// 提交接入部署计划(数据源接入)
func (c *Client) AccessDeployPlan(opts ...define.OperationOption) define.Operation {
	/*
		@params
		data_scenario | string | 接入场景 | required
		bk_biz_id | int | 业务ID | required
		access_raw_data | map{raw_data_name: string | 数据源英文名称 | required, raw_data_alias: string | 数据源中文名称 | require, maintainer: string | 数据维护者 | required, data_source | string | 数据接入方式 | required, data_encoding: string | 数据编码 | require, sensitivity: string | 数据敏感度 | require, description: string | 数据源描述 , tags: [string] | 数据标签, data_source_tags: [string] | 数据源标签} | 接入源数据信息 | required
		access_conf_info | map {collection_model: map{collection_type: string | 接入方式 | required, start_at: int | 开始接入时位置, period: string | 采集周期 | required} | 数据采集接入方式配置 | required, resource: map{master: string | kafka的broker地址 | required, group: string | 消费者组 | required, topic: string | 消费topic | required, tasks: string | 最大并发度 | required, use_sasl: bool | 是否加密 | required, security_protocol: string | 安全协议, sasl_mechanism: string | SASL机制, user: string | 用户名, password: string | 密码} | 接入对象资源 | required} | 接入配置信息 | required
		description | string | 接入数据备注
		bk_username | string | 用户名
	*/
	path := "/v3/access/deploy_plan/"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "access_deploy_plan",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// AuthProjectsDataCheck for bkdata resource auth_projects_data_check
// 检查项目是否有结果表权限
func (c *Client) AuthProjectsDataCheck(opts ...define.OperationOption) define.Operation {
	/*
		@params
		project_id | int | 计算平台项目 | required
		result_table_id | string | 结果表名称 | required
		action_id | string | 动作方式
	*/
	path := "/v3/auth/projects/{project_id}/data/check/"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "auth_projects_data_check",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// AuthResultTable for bkdata resource auth_result_table
// 授权接口(管理员接口): 给项目加表权限
func (c *Client) AuthResultTable(opts ...define.OperationOption) define.Operation {
	/*
		@params
		project_id | int | 计算平台项目 | required
		object_id | string | 结果表名称 | required
		bk_biz_id | int | 业务ID | required
	*/
	path := "/v3/auth/projects/{project_id}/data/add/"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "auth_result_table",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// UpdateDataFlowNode for bkdata resource update_data_flow_node
// 更新DataFlow节点
func (c *Client) UpdateDataFlowNode(opts ...define.OperationOption) define.Operation {
	/*
		@params
		node_id	| int | DataFlow的节点ID	| required
		flow_id	| int | DataFlow的ID	| required
		from_links	| [map] | 与上游节点的连线信息	| required
		node_type	| string | 节点类型	| required
		config	| map | 节点配置	| required
		frontend_info	| map | DataFlow画布上的位置信息	| required

	*/
	path := "/v3/dataflow/flow/flows/{flow_id}/nodes/{node_id}"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "update_data_flow_node",
		Method: "PUT",
		Path:   path,
	}, opts...)
}

// AddDataFlowNode for bkdata resource add_data_flow_node
// 添加DataFlow节点
func (c *Client) AddDataFlowNode(opts ...define.OperationOption) define.Operation {
	/*
		@params
		flow_id	| int | DataFlow的ID	| required
		from_links	| [map] | 与上游节点的连线信息	| required
		node_type	| string | 节点类型	| required
		config	| map | 节点配置	| required
		frontend_info	| map | DataFlow画布上的位置信息	| required

	*/
	path := "/v3/dataflow/flow/flows/{flow_id}/nodes/"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "add_data_flow_node",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// GetLatestDeployDataFlow for bkdata resource get_latest_deploy_data_flow
// 获取DataFlow的最近部署信息
func (c *Client) GetLatestDeployDataFlow(opts ...define.OperationOption) define.Operation {
	/*
		@params
		flow_id	| int 	 | DataFlow的ID	| required
	*/
	path := "/v3/dataflow/flow/flows/{flow_id}/latest_deploy_data/"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "get_latest_deploy_data_flow",
		Method: "GET",
		Path:   path,
	}, opts...)
}

// GetDataFlow for bkdata resource get_data_flow
// 获取DataFlow信息
func (c *Client) GetDataFlow(opts ...define.OperationOption) define.Operation {
	/*
		@params
		flow_id	| int 	 | DataFlow的ID	| required
	*/
	path := "/v3/dataflow/flow/flows/{flow_id}"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "get_data_flow",
		Method: "GET",
		Path:   path,
	}, opts...)
}

// GetDataFlowGraph for bkdata resource get_data_flow_graph
// 获取DataFlow里的画布信息，即画布中的节点信息
func (c *Client) GetDataFlowGraph(opts ...define.OperationOption) define.Operation {
	/*
		@params
		flow_id	| int 	 | DataFlow的ID	| required
	*/
	path := "/v3/dataflow/flow/flows/{flow_id}/graph"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "get_data_flow_graph",
		Method: "GET",
		Path:   path,
	}, opts...)
}

// GetDataFlowList for bkdata resource get_data_flow_list
// 获取DataFlow列表信息
func (c *Client) GetDataFlowList(opts ...define.OperationOption) define.Operation {
	/*
		@params
		project_id	| int 	 | 计算平台的项目ID	| required
	*/
	path := "/v3/dataflow/flow/flows"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "get_data_flow_list",
		Method: "GET",
		Path:   path,
	}, opts...)
}

// CreateDataFlow for bkdata resource create_data_flow
// 创建DataFlow
func (c *Client) CreateDataFlow(opts ...define.OperationOption) define.Operation {
	/*
		@params
		project_id	| int 	 | 计算平台的项目ID	| required
		flow_name 	| string | DataFlow名称 	  	| required
		nodes 		| [map]	 | 节点
	*/
	path := "/v3/dataflow/flow/flows/"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "create_data_flow",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// StopDataFlow for bkdata resource stop_data_flow
// 停止DataFlow
func (c *Client) StopDataFlow(opts ...define.OperationOption) define.Operation {
	/*
		@params
		flow_id | int | DataFlow的ID | required
	*/
	path := "/v3/dataflow/flow/flows/{flow_id}/stop/"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "stop_data_flow",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// StartDataFlow for bkdata resource start_data_flow
// 启动DataFlow
func (c *Client) StartDataFlow(opts ...define.OperationOption) define.Operation {
	/*
		@params
		flow_id | int | DataFlow的ID | required
		consuming_mode | string | 数据处理模式
		cluster_group | string | 计算集群组
	*/
	path := "/v3/dataflow/flow/flows/{flow_id}/start/"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "start_data_flow",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// RestartDataFlow for bkdata resource restart_data_flow
// 重启DataFlow
func (c *Client) RestartDataFlow(opts ...define.OperationOption) define.Operation {
	/*
		@params
		flow_id | int | DataFlow的ID | required
		consuming_mode | string | 数据处理模式
		cluster_group | string | 计算集群组
	*/
	path := "/v3/dataflow/flow/flows/{flow_id}/restart/"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "restart_data_flow",
		Method: "POST",
		Path:   path,
	}, opts...)
}
