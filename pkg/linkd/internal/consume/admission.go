// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consume

import (
	"context"
	"sync/atomic"
	"time"
)

type admissionKey struct{}

// WithAdmission 为任务注入独立的领取闸门；关闭不影响已领取消息的有界排空。
func WithAdmission(ctx context.Context, allowed *atomic.Bool) context.Context {
	return context.WithValue(ctx, admissionKey{}, allowed)
}

// WaitForAdmission 阻止失联任务继续领取，恢复授权后唤醒；取消始终优先退出。
func WaitForAdmission(ctx context.Context) error {
	gate, _ := ctx.Value(admissionKey{}).(*atomic.Bool)
	for gate != nil && !gate.Load() {
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return ctx.Err()
}

type partitionObserverKey struct{}

// WithPartitionObserver 将消费端的实际分区分配上报到任务状态，不参与业务身份。
func WithPartitionObserver(ctx context.Context, observe func([]string)) context.Context {
	return context.WithValue(ctx, partitionObserverKey{}, observe)
}

// ReportPartitions 上报完整分区快照，调用方不得在返回后修改传入切片。
func ReportPartitions(ctx context.Context, partitions []string) {
	if f, ok := ctx.Value(partitionObserverKey{}).(func([]string)); ok {
		f(partitions)
	}
}

// PartitionObserver 返回上下文注入的纯状态回调，Session 不需要保存 Context。
func PartitionObserver(ctx context.Context) func([]string) {
	f, _ := ctx.Value(partitionObserverKey{}).(func([]string))
	return f
}
