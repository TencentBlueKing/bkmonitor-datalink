// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package actiondelivery

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Destination 是从全局部署插件解析出的连接信息，仅在执行时保留于内存。
type Destination struct {
	// Endpoint 是完整动作受理接口 URL，不跟随重定向，不允许 userinfo/query/fragment。
	Endpoint string
	// InternalToken 是接收端内部认证凭据；不写入任务、错误或日志。
	InternalToken string
}

// Sender 负责一次有界外部调用；重试策略属于持久任务，不能在 HTTP 内隐藏无限重试。
type Sender interface {
	Send(context.Context, Destination, Request) (Receipt, error)
}

// HTTPSender 拥有独立连接池和十秒请求上限；最多四个同主机连接，不接受全局可变 client。
type HTTPSender struct {
	client    *http.Client
	transport *http.Transport
}

// NewHTTPSender 创建独立且禁止重定向的 HTTP 投递器。
func NewHTTPSender() (*HTTPSender, error) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, ErrInvalid
	}
	tr := base.Clone()
	tr.MaxConnsPerHost = 4
	tr.MaxIdleConnsPerHost = 4
	tr.MaxIdleConns = 16
	return &HTTPSender{transport: tr, client: &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Close 关闭空闲连接；在调用者停止使用 Sender 后调用。
func (s *HTTPSender) Close() { s.transport.CloseIdleConnections() }

// Send 只在持久受理身份、原请求摘要和可见性满足契约时返回，不回显远端 body 或凭据。
func (s *HTTPSender) Send(ctx context.Context, d Destination, q Request) (Receipt, error) {
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	if q.Validate() != nil {
		return Receipt{}, ErrInvalid
	}
	parsed, err := url.Parse(d.Endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || d.InternalToken == "" || len(d.InternalToken) > 16<<10 || strings.ContainsAny(d.InternalToken, string([]byte{13, 10})) {
		return Receipt{}, Failure{"target_unavailable", false}
	}
	body, err := json.Marshal(q)
	if err != nil || len(body) > MaxRequestBytes {
		return Receipt{}, ErrInvalid
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.Endpoint, bytes.NewReader(body))
	if err != nil {
		return Receipt{}, Failure{"target_unavailable", false}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Internal-Token", "Bearer "+d.InternalToken)
	req.Header.Set("X-Bk-Tenant-Id", q.TenantID)
	response, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Receipt{}, ctx.Err()
		}
		return Receipt{}, Failure{"transport_failed", true}
	}
	defer func() { _ = response.Body.Close() }()
	switch {
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		return Receipt{}, Failure{"remote_unauthorized", false}
	case response.StatusCode == http.StatusConflict:
		return Receipt{}, Failure{"identity_conflict", false}
	case response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500:
		return Receipt{}, Failure{"remote_unavailable", true}
	case response.StatusCode != http.StatusOK:
		return Receipt{}, Failure{"remote_rejected", false}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<10+1))
	if err != nil {
		if ctx.Err() != nil {
			return Receipt{}, ctx.Err()
		}
		return Receipt{}, Failure{"transport_failed", true}
	}
	if len(raw) > 64<<10 {
		return Receipt{}, Failure{"response_too_large", false}
	}
	return DecodeReceipt(raw, q)
}
