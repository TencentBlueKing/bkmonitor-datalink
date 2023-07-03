// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"context"
	"math/rand"
	"time"
)

// TimeoutOrContextDone return time or true when context done
func TimeoutOrContextDone(ctx context.Context, ch <-chan time.Time) (time.Time, bool) {
	select {
	case t := <-ch:
		return t, false
	case <-ctx.Done():
		return time.Time{}, true
	}
}

// RandInt 返回一个 [startIndex, endIndex) 之间的时间, 支持时间间隔
func RandInt(startIndex, endIndex time.Duration, intervals ...time.Duration) time.Duration {
	var (
		interval time.Duration
		delta    int64
	)
	if len(intervals) > 0 {
		interval = intervals[0]
	}
	if interval <= 0 {
		interval = 1
	}

	index := startIndex
	deltaTime := endIndex - startIndex
	if interval > (deltaTime) {
		delta = int64(deltaTime)
	} else {
		delta = int64(deltaTime / interval)
	}

	if delta == 0 {
		return index
	}

	if delta < 0 {
		// wrong, 仅仅为规避错误
		delta = -delta
		index = endIndex
	}
	return index + time.Duration(rand.Int63n(delta))
}
