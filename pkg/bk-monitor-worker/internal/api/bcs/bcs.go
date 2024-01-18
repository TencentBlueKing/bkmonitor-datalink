// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bcs

import (
	"github.com/TencentBlueKing/bk-apigateway-sdks/core/bkapi"
	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"
)

// Client for bcs
type Client struct {
	define.BkApiClient
}

// New bcs client
func New(configProvider define.ClientConfigProvider, opts ...define.BkApiClientOption) (*Client, error) {
	client, err := bkapi.NewBkApiClient("bcs-api", configProvider, opts...)
	if err != nil {
		return nil, err
	}

	return &Client{BkApiClient: client}, nil
}

// FetchSharedClusterNamespaces for bcs resource fetch_shared_cluster_namespaces
// 获取项目使用的共享集群的命名空间数据
func (c *Client) FetchSharedClusterNamespaces(opts ...define.OperationOption) define.Operation {
	/*
		@params
		project_code | string | 集群project_code
		cluster_id | string | 集群id
	*/
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "fetch_shared_cluster_namespaces",
		Method: "GET",
		Path:   "/bcsproject/v1/projects/{project_code}/clusters/{cluster_id}/native/namespaces",
	}, opts...)
}
