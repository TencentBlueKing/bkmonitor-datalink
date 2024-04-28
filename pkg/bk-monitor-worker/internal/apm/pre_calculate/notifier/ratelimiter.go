// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package notifier

import "k8s.io/client-go/util/flowcontrol"

type tokenBucketRateLimiter struct {
	unlimited bool
	rejected  bool
	limiter   flowcontrol.RateLimiter
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
