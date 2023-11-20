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
	path := "/v3/databus/tasks/"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "start_databus_cleans",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// CreateDataStorages for bkdata resource create_data_storages
// 创建数据入库
func (c *Client) CreateDataStorages(opts ...define.OperationOption) define.Operation {
	path := "/v3/databus/data_storages/"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "create_data_storages",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// AccessDeployPlan for bkdata resource access_deploy_plan
// 提交接入部署计划(数据源接入)
func (c *Client) AccessDeployPlan(opts ...define.OperationOption) define.Operation {
	path := "/v3/access/deploy_plan/"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "access_deploy_plan",
		Method: "POST",
		Path:   path,
	}, opts...)
}
