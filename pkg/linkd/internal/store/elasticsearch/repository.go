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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"linkd/internal/store"
)

const (
	defaultMaxReadTargets   = 128
	defaultMaxDocumentBytes = 1 << 20
	defaultMaxRequestBytes  = 16 << 20
	defaultMaxResponseBytes = 64 << 20
	defaultPITKeepAlive     = time.Minute
	maxIdentityBytes        = 1024
)

var (
	_ store.Repository = (*Repository)(nil)
	// ErrResponseTooLarge 允许控制面在单次扫描超过响应硬上限时缩小拉取批次后继续处理。
	ErrResponseTooLarge = errors.New("elasticsearch response is too large")
	maxDateNanos        = time.Unix(math.MaxInt64/int64(time.Second), math.MaxInt64%int64(time.Second)).UTC()
)

// Transport 与 Elasticsearch 官方 Go client 的低层 Perform 方法兼容。
// 调用方可按实际集群 major version 选择官方 client，而 Repository 不固定客户端 major。
type Transport interface {
	Perform(request *http.Request) (*http.Response, error)
}

// Config 定义 Elasticsearch Repository 的硬资源边界。
type Config struct {
	// MaxReadTargets 限制一次跨索引查询展开的目标数量。
	MaxReadTargets int
	// MaxDocumentBytes 限制单个领域文档编码后的大小。
	MaxDocumentBytes int
	// MaxRequestBytes 限制单次 JSON 或 NDJSON 请求体大小。
	MaxRequestBytes int
	// MaxResponseBytes 限制读取 Elasticsearch 响应体的大小。
	MaxResponseBytes int64
	// PITKeepAlive 控制分页 PIT 每次请求后的保活时间。
	PITKeepAlive time.Duration
}

// DefaultConfig 返回适合首版实现的有界默认值。
func DefaultConfig() Config {
	return Config{
		MaxReadTargets:   defaultMaxReadTargets,
		MaxDocumentBytes: defaultMaxDocumentBytes,
		MaxRequestBytes:  defaultMaxRequestBytes,
		MaxResponseBytes: defaultMaxResponseBytes,
		PITKeepAlive:     defaultPITKeepAlive,
	}
}

// Repository 使用 Elasticsearch 原生 create、search 和 seq_no/primary_term CAS 实现存储端口。
type Repository struct {
	transport Transport
	router    Router
	config    Config
}

// New 创建 Event 与 AlertLog Elasticsearch Repository。
func New(transport Transport, router Router, config Config) (*Repository, error) {
	if transport == nil {
		return nil, fmt.Errorf("%w: elasticsearch transport must not be nil", store.ErrInvalidArgument)
	}
	if router == nil {
		return nil, fmt.Errorf("%w: elasticsearch router must not be nil", store.ErrInvalidArgument)
	}
	defaults := DefaultConfig()
	if config.MaxReadTargets == 0 {
		config.MaxReadTargets = defaults.MaxReadTargets
	}
	if config.MaxDocumentBytes == 0 {
		config.MaxDocumentBytes = defaults.MaxDocumentBytes
	}
	if config.MaxRequestBytes == 0 {
		config.MaxRequestBytes = defaults.MaxRequestBytes
	}
	if config.MaxResponseBytes == 0 {
		config.MaxResponseBytes = defaults.MaxResponseBytes
	}
	if config.PITKeepAlive == 0 {
		config.PITKeepAlive = defaults.PITKeepAlive
	}
	if config.MaxReadTargets < 1 || config.MaxDocumentBytes < 1 || config.MaxRequestBytes < 1 ||
		config.MaxResponseBytes < 1 || config.PITKeepAlive < time.Second {
		return nil, fmt.Errorf("%w: elasticsearch resource limits must be positive", store.ErrInvalidArgument)
	}
	if config.MaxRequestBytes < config.MaxDocumentBytes {
		return nil, fmt.Errorf(
			"%w: elasticsearch max request bytes must cover max document bytes",
			store.ErrInvalidArgument,
		)
	}
	return &Repository{transport: transport, router: router, config: config}, nil
}

type responseError struct {
	StatusCode int
	Type       string
	Reason     string
}

func (e *responseError) Error() string {
	if e.Type == "" && e.Reason == "" {
		return fmt.Sprintf("elasticsearch response status %d", e.StatusCode)
	}
	return fmt.Sprintf("elasticsearch response status %d: %s: %s", e.StatusCode, e.Type, e.Reason)
}

func (r *Repository) performJSON(
	ctx context.Context,
	method, path string,
	query url.Values,
	body []byte,
	result any,
) error {
	return r.performContent(ctx, method, path, query, body, "application/json", result)
}

func (r *Repository) performNDJSON(
	ctx context.Context,
	method, path string,
	query url.Values,
	body []byte,
	result any,
) error {
	return r.performContent(ctx, method, path, query, body, "application/x-ndjson", result)
}

func (r *Repository) performContent(
	ctx context.Context,
	method, path string,
	query url.Values,
	body []byte,
	contentType string,
	result any,
) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if len(body) > r.config.MaxRequestBytes {
		return fmt.Errorf("elasticsearch request exceeds %d bytes", r.config.MaxRequestBytes)
	}
	requestURL := path
	if len(query) != 0 {
		requestURL += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build elasticsearch request: %w", err)
	}
	if len(body) != 0 {
		request.Header.Set("Content-Type", contentType)
	}
	request.Header.Set("Accept", "application/json")
	response, err := r.transport.Perform(request)
	if err != nil {
		return fmt.Errorf("perform elasticsearch request: %w", err)
	}
	if response == nil || response.Body == nil {
		return fmt.Errorf("elasticsearch transport returned an empty response")
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, r.config.MaxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read elasticsearch response: %w", err)
	}
	if int64(len(data)) > r.config.MaxResponseBytes {
		return fmt.Errorf("%w: exceeds %d bytes", ErrResponseTooLarge, r.config.MaxResponseBytes)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return decodeResponseError(response.StatusCode, data)
	}
	if result == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(result); err != nil {
		return fmt.Errorf("decode elasticsearch response: %w", err)
	}
	return nil
}

func decodeResponseError(statusCode int, data []byte) error {
	var envelope struct {
		Error struct {
			Type      string `json:"type"`
			Reason    string `json:"reason"`
			RootCause []struct {
				Type   string `json:"type"`
				Reason string `json:"reason"`
			} `json:"root_cause"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return &responseError{StatusCode: statusCode}
	}
	errorType := envelope.Error.Type
	reason := envelope.Error.Reason
	if len(envelope.Error.RootCause) != 0 {
		if errorType == "" {
			errorType = envelope.Error.RootCause[0].Type
		}
		if reason == "" {
			reason = envelope.Error.RootCause[0].Reason
		}
	}
	return &responseError{StatusCode: statusCode, Type: errorType, Reason: reason}
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: context must not be nil", store.ErrInvalidArgument)
	}
	return ctx.Err()
}

func joinTargets(targets []string) string {
	return strings.Join(targets, ",")
}

func validateIdentity(bkTenantID, idName, id string) error {
	if bkTenantID == "" {
		return fmt.Errorf("%w: bk_tenant_id must not be empty", store.ErrInvalidArgument)
	}
	if id == "" {
		return fmt.Errorf("%w: %s must not be empty", store.ErrInvalidArgument, idName)
	}
	if len(bkTenantID) > maxIdentityBytes || len(id) > maxIdentityBytes {
		return fmt.Errorf(
			"%w: elasticsearch identity fields must not exceed %d bytes",
			store.ErrInvalidArgument,
			maxIdentityBytes,
		)
	}
	return nil
}

func normalizeBatchIDs(bkTenantID, idName string, ids []string) ([]string, error) {
	if bkTenantID == "" {
		return nil, fmt.Errorf("%w: bk_tenant_id must not be empty", store.ErrInvalidArgument)
	}
	if len(ids) == 0 || len(ids) > store.MaxBatchSize {
		return nil, fmt.Errorf(
			"%w: %s batch size must be between 1 and %d",
			store.ErrInvalidArgument,
			idName,
			store.MaxBatchSize,
		)
	}
	seen := make(map[string]struct{}, len(ids))
	normalized := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			return nil, fmt.Errorf("%w: %s must not be empty", store.ErrInvalidArgument, idName)
		}
		if len(bkTenantID) > maxIdentityBytes || len(id) > maxIdentityBytes {
			return nil, fmt.Errorf(
				"%w: elasticsearch identity fields must not exceed %d bytes",
				store.ErrInvalidArgument,
				maxIdentityBytes,
			)
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	return normalized, nil
}

func validateDateNanos(name string, value time.Time) error {
	value = value.Round(0).UTC()
	if value.Before(time.Unix(0, 0).UTC()) || value.After(maxDateNanos) {
		return fmt.Errorf(
			"%w: %s is outside elasticsearch date_nanos range",
			store.ErrInvalidArgument,
			name,
		)
	}
	return nil
}

func asResponseError(err error) (*responseError, bool) {
	var response *responseError
	return response, errors.As(err, &response)
}
