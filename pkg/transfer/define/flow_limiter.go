// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"time"

	"golang.org/x/time/rate"
)

var totalFlowBytes int

var dataIdFlowBytes int

var globalFlowLimiter = NewFlowLimiter("kafka:global", TotalFlowBytes())

// TotalFlowBytes 全局最大允许的流量速率
func TotalFlowBytes() int {
	if totalFlowBytes <= 0 {
		return 1024 * 1024 * 128 // 默认为 128MB/s => 1Gb/s
	}
	return totalFlowBytes
}

// DataIdFlowBytes dataid 最大允许的流量速率
func DataIdFlowBytes() int {
	if dataIdFlowBytes <= 0 {
		return 1024 * 1024 * 20 // 默认为 20MB/s => 160Mb/s
	}
	return dataIdFlowBytes
}

// LimitRate 限制全局流量速率
func LimitRate(n int) {
	globalFlowLimiter.Consume(n)
}

// FlowLimiter 流量限流器
type FlowLimiter struct {
	n       int
	name    string
	limiter *rate.Limiter
}

// bytesRatio 等比缩小 b，即 1KB 表示 1 个 token
// 最少保证有 1 个 token
func bytesRatio(b int) int {
	n := b / 1024
	if n <= 0 {
		n = 1
	}
	return n
}

// NewFlowLimiter 流控实现
func NewFlowLimiter(name string, bytesRate int) *FlowLimiter {
	n := bytesRatio(bytesRate)
	fr := &FlowLimiter{
		n:       n,
		name:    name,
		limiter: rate.NewLimiter(rate.Limit(n), n),
	}
	return fr
}

// Consume 消耗 token
func (fr *FlowLimiter) Consume(n int) {
	now := time.Now()
	tokens := bytesRatio(n)

	// 确保不能超过 limiter/burst 否则会触发无限等待
	if tokens > fr.n {
		tokens = fr.n
	}

	time.Sleep(fr.limiter.ReserveN(now, tokens).DelayFrom(now))

	MonitorFlowBytes.WithLabelValues(fr.name).Add(float64(n))
	MonitorFlowBytesDistribution.WithLabelValues(fr.name).Observe(float64(n))
	MonitorFlowBytesConsumedDuration.WithLabelValues(fr.name).Observe(time.Since(now).Seconds())
}
