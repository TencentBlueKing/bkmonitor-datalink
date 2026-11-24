// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package store

import (
	"context"
	"time"
)

// WaitKind 区分主动合批收集与存储容量/调度排队，均不包含实际请求执行。
type WaitKind string

const (
	// WaitBatch 是请求进入收集窗口后等待同伴请求的时间。
	WaitBatch WaitKind = "batch_wait"
	// WaitStorageQueue 是调用容量、收集队列及执行槽位的等待。
	WaitStorageQueue WaitKind = "storage_queue"
)

type waitObserverKey struct{}

// WithWaitObserver 为当前串行调用链注入等待观察器。存储必须在调用者 goroutine
// 返回前同步上报，不从异步批次 goroutine 调用，避免污染其他 Event 的分段。
func WithWaitObserver(ctx context.Context, observer func(WaitKind, time.Duration)) context.Context {
	return context.WithValue(ctx, waitObserverKey{}, observer)
}

// ObserveWait 上报当前逻辑调用实际经历的等待；未注入观察器时不做处理。
func ObserveWait(ctx context.Context, kind WaitKind, duration time.Duration) {
	if duration <= 0 {
		return
	}
	if observer, ok := ctx.Value(waitObserverKey{}).(func(WaitKind, time.Duration)); ok && observer != nil {
		observer(kind, duration)
	}
}
