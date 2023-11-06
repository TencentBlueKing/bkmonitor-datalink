// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ratelimiter

import (
	"math"

	"k8s.io/client-go/util/flowcontrol"
)

const (
	TypeTokenBucket = "token_bucket"
	TypeNoop        = "noop"
)

// Config 限流器配置项
type Config struct {
	// Type of rate limiter
	Type string `config:"type" mapstructure:"type"`

	// The bucket is initially filled with 'burst' tokens, and refills at a rate of 'qps'
	Qps float32 `config:"qps" mapstructure:"qps"`

	// The maximum number of tokens in the bucket is capped at 'burst'
	Burst int `config:"burst" mapstructure:"burst"`
}

// RateLimiter 限流器接口定义
type RateLimiter interface {
	// Type return type of ratelimiter
	Type() string

	// TryAccept returns true if a token is taken immediately. Otherwise,
	// it returns false.
	TryAccept() bool

	// Stop stops the rate limiter, subsequent calls to CanAccept will return false
	Stop()

	// QPS returns QPS of this rate limiter
	QPS() float32
}

// New 根据配置生成限流器
func New(c Config) RateLimiter {
	switch c.Type {
	case TypeTokenBucket:
		return newTokenBucketRateLimiter(c.Qps, c.Burst)
	default:
		return newNoopRateLimiter()
	}
}

// newNoopRateLimiter 返回空限流器实现
func newNoopRateLimiter() RateLimiter {
	return noopRateLimiter{}
}

type noopRateLimiter struct{}

// Type 实现 RateLimiter Type 方法
func (noopRateLimiter) Type() string {
	return TypeNoop
}

// Stop 实现 RateLimiter Stop 方法
func (noopRateLimiter) Stop() {}

// TryAccept 实现 RateLimiter TryAccept 方法
func (noopRateLimiter) TryAccept() bool {
	return true
}

// QPS 实现 RateLimiter QPS 方法
func (noopRateLimiter) QPS() float32 {
	return 0
}

// newTokenBucketRateLimiter 存在三种情况
// 1）qps == 0: 没有 qps 限制
// 2）qps < 0: 拒绝所有请求
// 3）qps > 0: 令牌桶限流
func newTokenBucketRateLimiter(qps float32, burst int) RateLimiter {
	limiter := &tokenBucketRateLimiter{}
	if qps == 0 {
		limiter.unlimited = true
		return limiter
	}
	if qps < 0 {
		limiter.rejected = true
		return limiter
	}
	if burst < int(qps) {
		burst = int(qps) + 1
	}
	return &tokenBucketRateLimiter{
		limiter: flowcontrol.NewTokenBucketRateLimiter(qps, burst),
	}
}

type tokenBucketRateLimiter struct {
	unlimited bool
	rejected  bool
	limiter   flowcontrol.RateLimiter
}

// Type 实现 RateLimiter Type 方法
func (rl *tokenBucketRateLimiter) Type() string {
	return TypeTokenBucket
}

// Stop 实现 RateLimiter Stop 方法
func (rl *tokenBucketRateLimiter) Stop() {
	if rl.rejected || rl.unlimited {
		return
	}
	rl.limiter.Stop()
}

// TryAccept 实现 RateLimiter TryAccept 方法
func (rl *tokenBucketRateLimiter) TryAccept() bool {
	if rl.unlimited {
		return true
	}
	if rl.rejected {
		return false
	}
	return rl.limiter.TryAccept()
}

// QPS 实现 RateLimiter QPS 方法
func (rl *tokenBucketRateLimiter) QPS() float32 {
	if rl.unlimited {
		return math.MaxFloat32
	}
	if rl.rejected {
		return 0
	}
	return rl.limiter.QPS()
}
