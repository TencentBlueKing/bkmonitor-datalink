// MIT License

// Copyright (c) 2021~2022 腾讯蓝鲸

// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:

// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.

// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package cmdbcache

import (
	"context"
	"time"

	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"
	"golang.org/x/time/rate"
)

// OptRateLimitBodyProvider creates a new RateLimitBodyProvider with default rate limiter.
func OptRateLimitBodyProvider(qps float64, burst int, timeout int) *RateLimitBodyProvider {
	return NewRateLimitBodyProvider(qps, burst, timeout)
}

// RateLimitBodyProvider is a rate limiter for body provider.
type RateLimitBodyProvider struct {
	timeout time.Duration
	limiter *rate.Limiter
}

// NewRateLimitBodyProvider creates a new RateLimitBodyProvider with rate limiter.
func NewRateLimitBodyProvider(qps float64, burst int, timeout int) *RateLimitBodyProvider {
	return &RateLimitBodyProvider{
		timeout: time.Duration(timeout) * time.Second,
		limiter: rate.NewLimiter(rate.Limit(qps), burst),
	}
}

// ApplyToClient will add to the operation operations.
func (r *RateLimitBodyProvider) ApplyToClient(cli define.BkApiClient) error {
	return cli.AddOperationOptions(r)
}

// ApplyToOperation will set the body provider.
func (r *RateLimitBodyProvider) ApplyToOperation(op define.Operation) error {
	op.SetBodyProvider(r)
	return nil
}

// ProvideBody method provides the request body, and returns the content length.
func (r *RateLimitBodyProvider) ProvideBody(operation define.Operation, data interface{}) error {
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

	return nil
}
