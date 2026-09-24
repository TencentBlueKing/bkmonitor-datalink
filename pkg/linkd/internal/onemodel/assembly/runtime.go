// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package assembly 为 OneModel 消费方装配独立、有界的只读连接。
package assembly

import (
	"fmt"
	"time"

	"linkd/internal/config"
	"linkd/internal/onemodel"
	elasticsearchstore "linkd/internal/store/elasticsearch"
)

// Open 创建 Client 及其传输；调用方必须关闭传输。构造过程不连接外部服务。
func Open(resource *config.OneModelResource, maxConnections int, timeout time.Duration) (*onemodel.Client, *elasticsearchstore.HTTPTransport, error) {
	transport, err := NewTransport(resource, maxConnections, timeout)
	if err != nil {
		return nil, nil, err
	}
	client, err := onemodel.NewClient(onemodel.ClientConfig{Transport: transport, IndexPrefix: resource.IndexPrefix})
	if err != nil {
		transport.Close()
		return nil, nil, err
	}
	return client, transport, nil
}

// NewTransport 为每个消费方创建有界且支持取消的 ES HTTP 连接池。
func NewTransport(resource *config.OneModelResource, maxConnections int, timeout time.Duration) (*elasticsearchstore.HTTPTransport, error) {
	if resource == nil {
		return nil, fmt.Errorf("resources.onemodel is required")
	}
	cfg := elasticsearchstore.HTTPTransportConfig{Addresses: append([]string(nil), resource.Addresses...), APIKey: resource.APIKey, Timeout: timeout, MaxConnectionsPerHost: maxConnections}
	if resource.BasicAuth != nil {
		cfg.BasicUsername = resource.BasicAuth.Username
		cfg.BasicPassword = resource.BasicAuth.Password
	}
	return elasticsearchstore.NewHTTPTransport(cfg)
}
