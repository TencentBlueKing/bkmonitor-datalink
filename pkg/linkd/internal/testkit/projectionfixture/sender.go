// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package projectionfixture 仅供可靠任务存储测试模拟外部写入延迟、失败和确认。
// 生产装配使用 kaccompat，不能用此协议模拟证明真实 KAC 兼容写入。
package projectionfixture

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"linkd/internal/projection"
)

// Sender 将测试快照发给测试自身的 httptest 接收器。
type Sender struct {
	endpoint, token string
	client          *http.Client
	transport       *http.Transport
}

// New 为存储契约测试建立独立连接池。
func New(endpoint, token string) (*Sender, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	return &Sender{endpoint: endpoint, token: token, transport: tr, client: &http.Client{Transport: tr, Timeout: 10 * time.Second}}, nil
}

// Close 关闭测试连接。
func (s *Sender) Close() { s.transport.CloseIdleConnections() }

// Send 只模拟有界确认，连接不来自生产目标配置。
func (s *Sender) Send(ctx context.Context, _ projection.Destination, q projection.Request) (projection.Receipt, error) {
	raw, err := json.Marshal(q)
	if err != nil {
		return projection.Receipt{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(raw))
	if err != nil {
		return projection.Receipt{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Internal-Token", "Bearer "+s.token)
	res, err := s.client.Do(req)
	if err != nil {
		return projection.Receipt{}, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		return projection.Receipt{}, projection.Failure{Code: "remote_unavailable", Retryable: res.StatusCode >= 500}
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if err != nil {
		return projection.Receipt{}, err
	}
	var ack projection.Receipt
	if json.Unmarshal(data, &ack) != nil {
		return ack, projection.ErrInvalidReceipt
	}
	visible := ack.SearchVisible
	ack.SearchVisible = true
	if ack.ValidateFor(q) != nil {
		return ack, projection.ErrInvalidReceipt
	}
	if !visible {
		return ack, projection.Failure{Code: "visibility_pending", Retryable: true}
	}
	return ack, nil
}
