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

// Fix: 包级 init 时 viper 还没解析 config,totalFlowBytes==0,
// 走到 TotalFlowBytes() 的 fallback 分支拿到 128MB/s 写死。
// 改为可变指针,PostParse 阶段重建可热更新。
var globalFlowLimiter = NewFlowLimiter("kafka:global", 128*1024*1024)

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

// SetTotalFlowBytes 供 PostParse / 测试使用的 setter
func SetTotalFlowBytes(n int) { totalFlowBytes = n }

// SetDataIdFlowBytes 供 PostParse / 测试使用的 setter
func SetDataIdFlowBytes(n int) { dataIdFlowBytes = n }

// RebuildGlobalFlowLimiter 重建全局流量限速器;PostParse 阶段调用以热更新
func RebuildGlobalFlowLimiter() {
	globalFlowLimiter = NewFlowLimiter("kafka:global", TotalFlowBytes())
}

// LimitRate 限制全局流量速率
func LimitRate(n int) {
	globalFlowLimiter.Consume(n)
}

type FlowLimiter struct {
	name    string
	limiter *rate.Limiter
}

func bytesRatio(b int) int {
	n := b / 1024
	if n <= 0 {
		n = 1
	}
	return n
}

// NewFlowLimiter 流控实现
func NewFlowLimiter(name string, bytesRate int) *FlowLimiter {
	r := bytesRatio(bytesRate)
	fr := &FlowLimiter{
		name:    name,
		limiter: rate.NewLimiter(rate.Limit(r), r),
	}
	return fr
}

// Consume 消耗 token
func (fr *FlowLimiter) Consume(n int) {
	now := time.Now()
	tokens := bytesRatio(n)
	time.Sleep(fr.limiter.ReserveN(now, tokens).DelayFrom(now))

	MonitorFlowBytes.WithLabelValues(fr.name).Add(float64(n))
	MonitorFlowBytesDistribution.WithLabelValues(fr.name).Observe(float64(n))
	MonitorFlowBytesConsumedDuration.WithLabelValues(fr.name).Observe(time.Since(now).Seconds())
}
