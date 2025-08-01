// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package common

import (
	"time"

	"golang.org/x/time/rate"
)

// bytesRatio 等比缩小 b，即 1KB 表示 1 个 token
// 最少保证有 1 个 token
func bytesRatio(b int) int {
	n := b / 1024
	if n <= 0 {
		n = 1
	}
	return n
}

type FlowLimiter struct {
	n        int
	consumed int
	limiter  *rate.Limiter
}

func NewFlowLimiter(bytesRate int) *FlowLimiter {
	n := bytesRatio(bytesRate)
	fl := &FlowLimiter{
		n:       n,
		limiter: rate.NewLimiter(rate.Limit(n), n),
	}
	return fl
}

func (fl *FlowLimiter) Consume(n int) {
	now := time.Now()
	fl.consumed += n
	tokens := bytesRatio(n)

	// 确保不能超过 limiter/burst 否则会触发无限等待
	if tokens > fl.n {
		tokens = fl.n
	}

	time.Sleep(fl.limiter.ReserveN(now, tokens).DelayFrom(now))
}
