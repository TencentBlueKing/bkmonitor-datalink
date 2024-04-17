// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bcsclustermanager

import (
	"github.com/TencentBlueKing/bk-apigateway-sdks/core/bkapi"
	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"
)

// Client for bcs cluster manager
type Client struct {
	define.BkApiClient
}

// New bcs_cluster_manager client
func New(configProvider define.ClientConfigProvider, opts ...define.BkApiClientOption) (*Client, error) {
	client, err := bkapi.NewBkApiClient("bcs-cluster-manager", configProvider, opts...)
	if err != nil {
		return nil, err
	}

	return &Client{BkApiClient: client}, nil
}

// FetchClusters for bcs cluster manager resource fetch clusters
// 从bcs-cluster-manager获取集群列表
func (c *Client) FetchClusters(opts ...define.OperationOption) define.Operation {
	/*
		@params
		cluster_id | string | 集群ID
		businessID | string | 业务id
		engineType | string | 集群类型
	*/
	return c.BkApiClient.NewOperation(bkapi.OperationConfig{
		Name:   "fetch cluster",
		Method: "GET",
		Path:   "cluster",
	}, opts...)
}
