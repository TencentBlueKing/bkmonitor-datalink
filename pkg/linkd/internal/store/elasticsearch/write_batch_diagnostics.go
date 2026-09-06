// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"context"
	"net/http/httptrace"
	"sync"
	"time"
)

func batchKind(read bool) string {
	if read {
		return "read"
	}
	return "write"
}

func (b *WriteBatchTransport) phase(kind, phase string, duration time.Duration) {
	if b.observer != nil {
		b.observer.BatchPhase(b.ctx, kind, phase, duration)
	}
}

// traceRequest 只记录完成的 HTTP 阶段；缺少回调不填零，避免将连接失败
// 误报为零延迟。连接获取包含建连，首字节包含网络、服务端及本机调度，
// 均不能直接解释为 ES 内部执行耗时。回调可能来自不同 goroutine。
func (b *WriteBatchTransport) traceRequest(ctx context.Context, kind string) (context.Context, func()) {
	var mu sync.Mutex
	var getting, connected, wrote time.Time
	durations := make(map[string]time.Duration)
	trace := &httptrace.ClientTrace{
		GetConn: func(string) {
			mu.Lock()
			getting = time.Now()
			connected = time.Time{}
			wrote = time.Time{}
			mu.Unlock()
		},
		GotConn: func(httptrace.GotConnInfo) {
			mu.Lock()
			connected = time.Now()
			if !getting.IsZero() {
				durations["connection"] += connected.Sub(getting)
			}
			mu.Unlock()
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			mu.Lock()
			if info.Err == nil {
				wrote = time.Now()
				if !connected.IsZero() {
					durations["request_write"] += wrote.Sub(connected)
				}
			}
			mu.Unlock()
		},
		GotFirstResponseByte: func() {
			mu.Lock()
			if !wrote.IsZero() {
				durations["first_byte"] += time.Since(wrote)
			}
			mu.Unlock()
		},
	}
	return httptrace.WithClientTrace(ctx, trace), func() {
		mu.Lock()
		defer mu.Unlock()
		for phase, duration := range durations {
			b.phase(kind, phase, duration)
		}
	}
}
