// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pluginruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// HTTPClient 同步调用 Python 执行端，不入 Redis 队列、不轮询结果。
type HTTPClient struct {
	client           *http.Client
	endpoint         string
	token            string
	maxResponseBytes int64
}

// HTTPError 保留执行端的非成功状态，不保存响应正文或凭据。
// 它不代表插件临时失败，也不授权调用方自动重试本次执行。
type HTTPError struct {
	// StatusCode 是执行端实际返回的 HTTP 状态，不含插件异常内容。
	StatusCode int
}

// Error 返回可安全记录的执行端状态。
func (e *HTTPError) Error() string {
	return fmt.Sprintf("python runtime returned HTTP %d", e.StatusCode)
}

// NewHTTPClient 配置同步执行地址、内部认证和响应字节预算；超时由 client 与调用 Context 控制。
// 独立复制 client 并禁止重定向，防止执行 POST 和凭据隐式重发；不修改调用方的客户端。
func NewHTTPClient(client *http.Client, endpoint, token string, maxResponseBytes int64) (*HTTPClient, error) {
	if client == nil || maxResponseBytes <= 0 {
		return nil, fmt.Errorf("HTTP client and positive response budget are required")
	}
	executionClient := *client
	executionClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HTTPClient{client: &executionClient, endpoint: endpoint, token: token, maxResponseBytes: maxResponseBytes}, nil
}

// Execute 等待本次 HTTP 执行结果；网络错误不自动重试，避免不确定结果下重复执行插件。
func (c *HTTPClient) Execute(ctx context.Context, request Request) (Result, error) {
	request.Version = 1
	data, err := json.Marshal(request)
	if err != nil {
		return Result{}, fmt.Errorf("encode execution request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if err != nil {
		return Result{}, fmt.Errorf("create execution request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("call Python runtime: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return Result{}, &HTTPError{StatusCode: response.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, c.maxResponseBytes+1))
	if err != nil {
		return Result{}, fmt.Errorf("read execution result: %w", err)
	}
	if int64(len(body)) > c.maxResponseBytes {
		return Result{}, fmt.Errorf("execution response exceeds byte budget")
	}
	var result Result
	if err = json.Unmarshal(body, &result); err != nil {
		return Result{}, fmt.Errorf("decode execution result: %w", err)
	}
	if result.Failure != nil {
		return result, result.Failure
	}
	return result, nil
}
