// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

const (
	defaultHTTPTimeout        = 15 * time.Second
	maxHTTPConnectionsPerHost = 1024
	maxHTTPIdleConnections    = 4096
)

// HTTPTransportConfig 描述 Elasticsearch HTTP 节点和认证方式。
type HTTPTransportConfig struct {
	Addresses     []string
	APIKey        string
	BasicUsername string
	BasicPassword string
	Timeout       time.Duration
	// MaxConnectionsPerHost 同时限制单节点总连接和可保留的 idle connection 数量。
	MaxConnectionsPerHost int
}

// HTTPTransport 把 Repository 的相对请求轮询发送到一个或多个 Elasticsearch origin。
type HTTPTransport struct {
	baseURLs      []*url.URL
	client        *http.Client
	transport     *http.Transport
	apiKey        string
	basicUsername string
	basicPassword string
	next          atomic.Uint64
}

// NewHTTPTransport 创建可关闭空闲连接的 Elasticsearch HTTP Transport。
func NewHTTPTransport(config HTTPTransportConfig) (*HTTPTransport, error) {
	if len(config.Addresses) == 0 {
		return nil, fmt.Errorf("create elasticsearch HTTP transport: addresses must not be empty")
	}
	if config.APIKey != "" && (config.BasicUsername != "" || config.BasicPassword != "") {
		return nil, fmt.Errorf("create elasticsearch HTTP transport: API key and basic auth are mutually exclusive")
	}
	baseURLs := make([]*url.URL, 0, len(config.Addresses))
	for index, address := range config.Addresses {
		parsed, err := url.Parse(address)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return nil, fmt.Errorf("create elasticsearch HTTP transport: addresses[%d] is invalid", index)
		}
		baseURLs = append(baseURLs, parsed)
	}
	if config.Timeout == 0 {
		config.Timeout = defaultHTTPTimeout
	}
	if config.Timeout < time.Second {
		return nil, fmt.Errorf("create elasticsearch HTTP transport: timeout must be at least one second")
	}
	if config.MaxConnectionsPerHost < 1 || config.MaxConnectionsPerHost > maxHTTPConnectionsPerHost {
		return nil, fmt.Errorf(
			"create elasticsearch HTTP transport: max connections per host must be between 1 and %d",
			maxHTTPConnectionsPerHost,
		)
	}
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("create elasticsearch HTTP transport: default HTTP transport is unavailable")
	}
	transport := defaultTransport.Clone()
	transport.MaxConnsPerHost = config.MaxConnectionsPerHost
	transport.MaxIdleConnsPerHost = config.MaxConnectionsPerHost
	transport.MaxIdleConns = min(config.MaxConnectionsPerHost*len(baseURLs), maxHTTPIdleConnections)
	return &HTTPTransport{
		baseURLs:      baseURLs,
		client:        &http.Client{Transport: transport, Timeout: config.Timeout},
		transport:     transport,
		apiKey:        config.APIKey,
		basicUsername: config.BasicUsername,
		basicPassword: config.BasicPassword,
	}, nil
}

// Perform 实现 Repository Transport，并保留原请求 Context、header、path 和 query。
func (t *HTTPTransport) Perform(request *http.Request) (*http.Response, error) {
	if request == nil {
		return nil, fmt.Errorf("perform elasticsearch HTTP request: request must not be nil")
	}
	index := t.next.Add(1) - 1
	baseURL := t.baseURLs[index%uint64(len(t.baseURLs))]
	resolved := *baseURL
	resolved.Path = strings.TrimSuffix(baseURL.Path, "/") + request.URL.Path
	resolved.RawQuery = request.URL.RawQuery
	cloned := request.Clone(request.Context())
	cloned.URL = &resolved
	if t.apiKey != "" {
		cloned.Header.Set("Authorization", "ApiKey "+t.apiKey)
	} else if t.basicUsername != "" || t.basicPassword != "" {
		cloned.SetBasicAuth(t.basicUsername, t.basicPassword)
	}
	return t.client.Do(cloned)
}

// Close 释放 HTTP keep-alive 空闲连接。
func (t *HTTPTransport) Close() {
	t.transport.CloseIdleConnections()
}

var _ Transport = (*HTTPTransport)(nil)
