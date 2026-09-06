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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"testing/synctest"
)

type readBatchResult struct {
	status int
	data   []byte
	err    error
}

func submitBatchRead(t *testing.T, b *WriteBatchTransport, ctx context.Context, index, id string) <-chan readBatchResult {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/"+index+"/_doc/"+id+"?realtime=true", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan readBatchResult, 1)
	go func() {
		defer close(done)
		response, err := b.Perform(req)
		result := readBatchResult{err: err}
		if response != nil {
			result.status = response.StatusCode
			result.data, result.err = io.ReadAll(response.Body)
			_ = response.Body.Close()
		}
		done <- result
	}()
	return done
}

func echoBatchRead(req *http.Request) (*http.Response, error) {
	if req.URL.Path != "/_mget" || req.URL.Query().Get("realtime") != "true" {
		return nil, fmt.Errorf("unexpected realtime batch URL %s", req.URL)
	}
	var input struct {
		Docs []map[string]any `json:"docs"`
	}
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		return nil, err
	}
	for i, doc := range input.Docs {
		doc["found"] = true
		doc["_seq_no"] = i
		if doc["_id"] == "missing" {
			doc["found"] = false
		}
		if doc["_id"] == "failed" {
			doc["status"] = 429
			doc["error"] = map[string]string{"type": "es_rejected_execution_exception"}
		}
	}
	data, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	return batchHTTPResponse(200, data), nil
}

func TestReadBatchWaitBoundary(t *testing.T) {
	for _, wait := range []time.Duration{-time.Nanosecond, 0, 10 * time.Millisecond, 10*time.Millisecond + time.Nanosecond} {
		t.Run(wait.String(), func(t *testing.T) {
			r, err := New(transportFunc(echoBatchRead), mustStaticRouter(t), DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			_, b, err := r.EnableWriteBatch(WriteBatchConfig{MaxOperations: 32, MaxBytes: 1 << 20, ReadWait: wait, MaxCalls: 32, MaxConcurrentBatches: 2, Timeout: time.Second}, nil)
			if b != nil {
				b.Close()
			}
			if (err == nil) != (wait >= 0 && wait <= 10*time.Millisecond) {
				t.Fatalf("read wait=%s err=%v", wait, err)
			}
		})
	}
}

func TestReadBatchRejectsRepositoryRequestLimit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxRequestBytes = 1 << 20
	r, err := New(transportFunc(func(*http.Request) (*http.Response, error) {
		t.Error("oversized read was sent")
		return nil, errors.New("unexpected request")
	}), mustStaticRouter(t), cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, b, err := r.EnableWriteBatch(WriteBatchConfig{MaxOperations: 32, MaxBytes: 1 << 20, ReadWait: 10 * time.Millisecond, MaxCalls: 1, MaxConcurrentBatches: 1, Timeout: time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	done := submitBatchRead(t, b, t.Context(), "events", strings.Repeat("x", 1<<20))
	if r := <-done; r.err == nil || !strings.Contains(r.err.Error(), "exceeds size limit") {
		t.Fatalf("read limit: %v", r.err)
	}
	if len(b.slots) != 0 {
		t.Fatal("rejected read retained its call slot")
	}
}

func TestReadBatchDeadlineDiagnostic(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := &diagnosticObserver{phases: map[string]int{}, triggers: map[string]int{}}
		r, err := New(transportFunc(echoBatchRead), mustStaticRouter(t), DefaultConfig())
		if err != nil {
			t.Fatal(err)
		}
		_, b, err := r.EnableWriteBatch(WriteBatchConfig{MaxOperations: 32, MaxBytes: 1 << 20, ReadWait: 10 * time.Millisecond, MaxCalls: 32, MaxConcurrentBatches: 1, Timeout: time.Second}, o)
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		done := submitBatchRead(t, b, t.Context(), "events", "a")
		if result := <-done; result.err != nil {
			t.Fatal(result.err)
		}
		b.Close()
		if o.triggers["read/deadline"] != 1 || o.triggers["read/ready"] != 0 {
			t.Fatalf("read triggers: %v", o.triggers)
		}
		for _, phase := range []string{"collect", "worker_slot", "operation_queue", "encode", "response_body", "response_decode"} {
			if o.phases["read/"+phase] != 1 {
				t.Fatalf("read phase %s: %v", phase, o.phases)
			}
		}
	})
}

func TestReadBatchDeadlineCollectsStaggeredCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		sent := make(chan time.Duration, 8)
		b := newTestWriteBatch(t, transportFunc(func(req *http.Request) (*http.Response, error) {
			sent <- time.Since(start)
			return echoBatchRead(req)
		}), func(c *WriteBatchConfig) { c.ReadWait = 10 * time.Millisecond; c.Wait = 100 * time.Millisecond })
		defer b.Close()
		first := submitBatchRead(t, b, t.Context(), "events", "a")
		synctest.Wait()
		time.Sleep(4 * time.Millisecond)
		second := submitBatchRead(t, b, t.Context(), "events", "b")
		synctest.Wait()
		time.Sleep(5 * time.Millisecond)
		synctest.Wait()
		if len(sent) != 0 {
			t.Fatal("partial read batch sent before its deadline")
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		if len(sent) != 1 {
			t.Fatalf("read requests=%d, want 1", len(sent))
		}
		if elapsed := <-sent; elapsed != 10*time.Millisecond {
			t.Fatalf("read deadline reset or waited for writer: %v", elapsed)
		}
		for _, done := range []<-chan readBatchResult{first, second} {
			if r := <-done; r.err != nil || r.status != 200 {
				t.Fatalf("read result: %+v", r)
			}
		}
	})
}

func TestReadBatchWindowProducesEffectiveBatchSize(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		counts := make(chan int, 64)
		b := newTestWriteBatch(t, transportFunc(func(req *http.Request) (*http.Response, error) {
			data, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			var input struct {
				Docs []json.RawMessage `json:"docs"`
			}
			if err := json.Unmarshal(data, &input); err != nil {
				return nil, err
			}
			counts <- len(input.Docs)
			req.Body = io.NopCloser(strings.NewReader(string(data)))
			return echoBatchRead(req)
		}), func(c *WriteBatchConfig) { c.ReadWait = 10 * time.Millisecond })
		defer b.Close()
		var replies []<-chan readBatchResult
		for i := range 40 {
			replies = append(replies, submitBatchRead(t, b, t.Context(), "events", fmt.Sprint(i)))
			synctest.Wait()
			time.Sleep(500 * time.Microsecond)
		}
		time.Sleep(10 * time.Millisecond)
		synctest.Wait()
		batches, total := len(counts), 0
		if batches < 2 || batches > 3 {
			t.Fatalf("40 staggered reads made %d requests, want 2..3", batches)
		}
		for range batches {
			total += <-counts
		}
		if total != 40 {
			t.Fatalf("batched operations=%d", total)
		}
		for _, reply := range replies {
			if r := <-reply; r.err != nil || r.status != 200 {
				t.Fatalf("read result: %v", r.err)
			}
		}
	})
}

func TestReadBatchFullFlushPreservesPositionsAndItemErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		sent := make(chan time.Duration, 8)
		b := newTestWriteBatch(t, transportFunc(func(req *http.Request) (*http.Response, error) {
			sent <- time.Since(start)
			return echoBatchRead(req)
		}), func(c *WriteBatchConfig) { c.MaxOperations = 5; c.ReadWait = 10 * time.Millisecond })
		defer b.Close()
		inputs := [][2]string{{"tenant-a", "same"}, {"tenant-b", "same"}, {"tenant-a", "same"}, {"tenant-a", "missing"}, {"tenant-b", "failed"}}
		var results []<-chan readBatchResult
		for i, input := range inputs {
			if i > 0 {
				time.Sleep(time.Millisecond)
			}
			results = append(results, submitBatchRead(t, b, t.Context(), input[0], input[1]))
			synctest.Wait()
		}
		if len(sent) != 1 {
			t.Fatalf("read requests=%d, want 1", len(sent))
		}
		if elapsed := <-sent; elapsed != 4*time.Millisecond {
			t.Fatalf("full batch did not send immediately: %v", elapsed)
		}
		for i, done := range results {
			r := <-done
			want := 200
			if i == 3 {
				want = 404
			}
			if i == 4 {
				want = 429
			}
			if r.err != nil || r.status != want {
				t.Fatalf("item %d: %+v", i, r)
			}
			var item struct {
				Index    string `json:"_index"`
				ID       string `json:"_id"`
				Sequence int    `json:"_seq_no"`
			}
			if err := json.Unmarshal(r.data, &item); err != nil {
				t.Fatal(err)
			}
			if item.Index != inputs[i][0] || item.ID != inputs[i][1] || item.Sequence != i {
				t.Fatalf("item position %d: %+v", i, item)
			}
		}
	})
}

func TestReadBatchBytesTriggerBeforeDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		sizes := make(chan int, 8)
		b := newTestWriteBatch(t, transportFunc(func(req *http.Request) (*http.Response, error) {
			if time.Since(start) != 0 {
				t.Error("byte limit waited for deadline")
			}
			data, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			sizes <- len(data)
			req.Body = io.NopCloser(strings.NewReader(string(data)))
			return echoBatchRead(req)
		}), func(c *WriteBatchConfig) { c.MaxBytes = 1 << 20; c.ReadWait = 10 * time.Millisecond })
		defer b.Close()
		id := strings.Repeat("a", (1<<20)/2)
		first := submitBatchRead(t, b, t.Context(), "events", id)
		synctest.Wait()
		second := submitBatchRead(t, b, t.Context(), "events", id+"b")
		synctest.Wait()
		if len(sizes) != 2 {
			t.Fatalf("oversized combined read was not split: %v", sizes)
		}
		for range 2 {
			n := <-sizes
			if n > 1<<20 {
				t.Fatalf("physical read exceeds byte limit: %d", n)
			}
		}
		for _, done := range []<-chan readBatchResult{first, second} {
			if r := <-done; r.err != nil || r.status != 200 {
				t.Fatalf("read result: %v", r.err)
			}
		}
	})
}

func TestReadBatchQueuedCancellationIsNotSent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var sent atomic.Int64
		b := newTestWriteBatch(t, transportFunc(func(req *http.Request) (*http.Response, error) {
			sent.Add(1)
			data, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			if strings.Contains(string(data), "cancelled") {
				t.Error("queued cancellation was sent")
			}
			req.Body = io.NopCloser(strings.NewReader(string(data)))
			return echoBatchRead(req)
		}), func(c *WriteBatchConfig) { c.ReadWait = 10 * time.Millisecond })
		defer b.Close()
		ctx, cancel := context.WithCancel(t.Context())
		first := submitBatchRead(t, b, ctx, "events", "cancelled")
		synctest.Wait()
		cancel()
		second := submitBatchRead(t, b, t.Context(), "events", "live")
		synctest.Wait()
		if sent.Load() != 0 {
			t.Fatal("partial batch sent early")
		}
		time.Sleep(10 * time.Millisecond)
		synctest.Wait()
		if r := <-first; !errors.Is(r.err, context.Canceled) {
			t.Fatalf("cancelled read: %v", r.err)
		}
		if r := <-second; r.err != nil || r.status != 200 {
			t.Fatalf("live read: %+v", r)
		}
		if sent.Load() != 1 {
			t.Fatalf("requests=%d", sent.Load())
		}
	})
}

func TestReadBatchInflightCancellationDoesNotCancelPeers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started, release := make(chan context.Context, 1), make(chan struct{})
		b := newTestWriteBatch(t, transportFunc(func(req *http.Request) (*http.Response, error) {
			started <- req.Context()
			<-release
			return echoBatchRead(req)
		}), func(c *WriteBatchConfig) { c.MaxOperations = 2; c.ReadWait = 10 * time.Millisecond })
		defer b.Close()
		ctx, cancel := context.WithCancel(t.Context())
		first := submitBatchRead(t, b, ctx, "events", "cancelled")
		second := submitBatchRead(t, b, t.Context(), "events", "live")
		physical := <-started
		cancel()
		synctest.Wait()
		if physical.Err() != nil {
			t.Fatal("caller cancellation cancelled its peer's physical batch")
		}
		close(release)
		synctest.Wait()
		if r := <-first; !errors.Is(r.err, context.Canceled) {
			t.Fatalf("cancelled read: %v", r.err)
		}
		if r := <-second; r.err != nil || r.status != 200 {
			t.Fatalf("live read: %+v", r)
		}
	})
}

func TestReadBatchCloseReleasesQueuedAndInflightCalls(t *testing.T) {
	for _, inflight := range []bool{false, true} {
		t.Run(fmt.Sprint(inflight), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var requests atomic.Int64
				b := newTestWriteBatch(t, transportFunc(func(req *http.Request) (*http.Response, error) {
					requests.Add(1)
					<-req.Context().Done()
					return nil, req.Context().Err()
				}), func(c *WriteBatchConfig) {
					c.MaxOperations = 2
					c.MaxCalls = 3
					c.MaxConcurrentBatches = 1
					c.ReadWait = 10 * time.Millisecond
				})
				defer b.Close()
				done := []<-chan readBatchResult{submitBatchRead(t, b, t.Context(), "events", "a")}
				synctest.Wait()
				if inflight {
					for _, id := range []string{"b", "c", "d"} {
						done = append(done, submitBatchRead(t, b, t.Context(), "events", id))
						synctest.Wait()
					}
					if len(b.slots) != 3 || requests.Load() != 1 {
						t.Fatalf("unbounded calls or worker use: %d/%d", len(b.slots), requests.Load())
					}
				}
				b.Close()
				synctest.Wait()
				for _, result := range done {
					if r := <-result; !errors.Is(r.err, context.Canceled) {
						t.Fatalf("close result: %v", r.err)
					}
				}
				if len(b.slots) != 0 || len(b.workers) != 0 {
					t.Fatal("close leaked calls or workers")
				}
				if !inflight && requests.Load() != 0 {
					t.Fatal("close sent queued reads")
				}
			})
		})
	}
}
