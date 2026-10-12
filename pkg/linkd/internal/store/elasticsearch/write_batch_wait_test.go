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
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"linkd/internal/store"
)

func TestBatchCallWaitBoundaries(t *testing.T) {
	start := time.Now()
	for _, tc := range []struct {
		name                              string
		collect, ready, execute, returned int
		batch, queue                      int
	}{
		{"queued cancellation", 0, 0, 0, 5, 0, 5},
		{"collecting cancellation", 2, 0, 0, 5, 3, 2},
		{"worker cancellation", 2, 6, 0, 9, 4, 5},
		{"execution is excluded", 2, 6, 9, 20, 4, 5},
		{"future worker after cancellation", 2, 6, 9, 4, 2, 2},
		{"late joiner waits only remainder", 1, 3, 3, 10, 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var timing batchCallTiming
			at := func(n int) time.Time { return start.Add(time.Duration(n) * time.Millisecond) }
			timing.enqueue(start)
			if tc.collect > 0 {
				timing.collect(at(tc.collect))
			}
			if tc.ready > 0 {
				timing.ready(at(tc.ready))
			}
			if tc.execute > 0 {
				timing.execute(at(tc.execute))
			}
			batch, queue := timing.waits(at(tc.returned))
			if batch != time.Duration(tc.batch)*time.Millisecond || queue != time.Duration(tc.queue)*time.Millisecond {
				t.Fatalf("batch=%v queue=%v", batch, queue)
			}
		})
	}
}

func TestBatchWaitObserverRunsOnLogicalCallerAndDoesNotMultiplySplitBulk(t *testing.T) {
	requests := 0
	b := newTestWriteBatch(t, transportFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		// 同一逻辑 Bulk 被拆成多个物理请求，仍只上报一次逻辑等待。
		return batchHTTPResponse(200, []byte(`{"items":[{"create":{"status":201}}]}`)), nil
	}), func(c *WriteBatchConfig) { c.MaxOperations = 1; c.Wait = 0 })
	calls := map[store.WaitKind]int{}
	ctx := store.WithWaitObserver(t.Context(), func(kind store.WaitKind, d time.Duration) {
		calls[kind]++
		if d <= 0 {
			t.Error("nonpositive wait")
		}
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/_bulk?refresh=false", strings.NewReader("{\"create\":{\"_index\":\"logs\",\"_id\":\"a\"}}\n{}\n{\"create\":{\"_index\":\"logs\",\"_id\":\"b\"}}\n{}\n"))
	resp, err := b.Perform(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if requests != 2 || calls[store.WaitBatch] != 1 || calls[store.WaitStorageQueue] != 2 {
		t.Fatalf("requests=%d waits=%v", requests, calls)
	}
}

func TestBatchWaitCancellationDoesNotReportBackgroundCompletion(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	b := newTestWriteBatch(t, transportFunc(func(*http.Request) (*http.Response, error) {
		close(started)
		<-release
		return batchHTTPResponse(200, []byte(`{"items":[{"create":{"status":201}}]}`)), nil
	}), func(c *WriteBatchConfig) { c.MaxOperations = 1; c.Wait = 0 })
	var mu sync.Mutex
	observations := 0
	ctx, cancel := context.WithCancel(store.WithWaitObserver(t.Context(), func(store.WaitKind, time.Duration) { mu.Lock(); defer mu.Unlock(); observations++ }))
	done := make(chan error, 1)
	go func() {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPut, "/logs/_create/a?refresh=false", strings.NewReader(`{}`))
		resp, err := b.Perform(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		done <- err
	}()
	<-started
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	mu.Lock()
	before := observations
	mu.Unlock()
	close(release)
	b.Close()
	mu.Lock()
	defer mu.Unlock()
	if observations != before || before == 0 {
		t.Fatalf("background observer leaked: before=%d after=%d", before, observations)
	}
}
