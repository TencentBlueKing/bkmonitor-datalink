// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdbcache

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"
	"golang.org/x/time/rate"
)

// OptRateLimitResultProvider creates a new RateLimitResultProvider with default rate limiter.
func OptRateLimitResultProvider(qps float64, burst int, timeout int) *RateLimitResultProvider {
	return NewRateLimitResultProvider(qps, burst, timeout)
}

// RateLimitResultProvider is a rate limiter for result provider.
type RateLimitResultProvider struct {
	timeout time.Duration
	limiter *rate.Limiter
}

// NewRateLimitResultProvider creates a new RateLimitResultProvider with rate limiter.
func NewRateLimitResultProvider(qps float64, burst int, timeout int) *RateLimitResultProvider {
	return &RateLimitResultProvider{
		timeout: time.Duration(timeout) * time.Second,
		limiter: rate.NewLimiter(rate.Limit(qps), burst),
	}
}

// ApplyToClient will add to the operation operations.
func (r *RateLimitResultProvider) ApplyToClient(cli define.BkApiClient) error {
	return cli.AddOperationOptions(r)
}

// ApplyToOperation will set the result provider.
func (r *RateLimitResultProvider) ApplyToOperation(op define.Operation) error {
	op.SetResultProvider(r)
	return nil
}

// ProvideResult method provides the result from response.
func (r *RateLimitResultProvider) ProvideResult(response *http.Response, result interface{}) error {
	if r.limiter == nil {
		return nil
	}

	// 设置超时时间
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	// 等待限流
	if err := r.limiter.Wait(ctx); err != nil {
		return err
	}

	// for most unmarshal functions, a nil receiver is not expected.
	if result == nil {
		return nil
	}

	err := json.NewDecoder(response.Body).Decode(result)
	if err != nil {
		return define.ErrorWrapf(err, "failed to unmarshal response result")
	}

	return nil
}
