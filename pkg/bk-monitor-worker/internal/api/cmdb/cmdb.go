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
)

// Client for cmdb
type Client struct {
	define.BkApiClient
}

// New bk_gse client
func New(configProvider define.ClientConfigProvider, opts ...define.BkApiClientOption) (*Client, error) {
	client, err := bkapi.NewBkApiClient("cmdb", configProvider, opts...)
	if err != nil {
		return nil, err
	}

	return &Client{BkApiClient: client}, nil
}

// SearchCloudArea for cmdb resource search_cloud_area
// 查询云区域信息
func (c *Client) SearchCloudArea(opts ...define.OperationOption) define.Operation {
	path := "search_cloud_area"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "search_cloud_area",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// ListBizHostsTopo for cmdb resource list_biz_hosts_topo
// 查询业务主机及关联拓扑
func (c *Client) ListBizHostsTopo(opts ...define.OperationOption) define.Operation {
	path := "list_biz_hosts_topo"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "list_biz_hosts_topo",
		Method: "POST",
		Path:   path,
	}, opts...)
}

// SearchBusiness for cmdb resource search_business
// 查询业务信息
func (c *Client) SearchBusiness(opts ...define.OperationOption) define.Operation {
	path := "search_business"
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "search_business",
		Method: "POST",
		Path:   path,
	}, opts...)
}
