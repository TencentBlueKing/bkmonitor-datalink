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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestWriteBatch(t *testing.T, transport Transport, modify func(*WriteBatchConfig)) *WriteBatchTransport {
	t.Helper()
	r, err := New(transport, mustStaticRouter(t), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	cfg := WriteBatchConfig{MaxOperations: 32, MaxBytes: 4 << 20, Wait: 20 * time.Millisecond, MaxConcurrentBatches: 2, MaxCalls: 32, Timeout: time.Second}
	if modify != nil {
		modify(&cfg)
	}
	_, b, err := r.EnableWriteBatch(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	return b
}

func TestWriteBatchAlreadyQueuedCallsFillBatch(t *testing.T) {
	for _, wait := range []time.Duration{0, 2 * time.Millisecond, 100 * time.Millisecond} {
		t.Run(wait.String(), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			requests := 0
			b := &WriteBatchTransport{
				ctx: ctx, cancel: cancel,
				config: WriteBatchConfig{MaxOperations: 100, MaxBytes: 4 << 20, Wait: wait, Timeout: time.Second},
				queue:  make(chan *batchCall, 100), slots: make(chan struct{}, 100), workers: make(chan struct{}, 1),
				next: transportFunc(func(req *http.Request) (*http.Response, error) {
					requests++
					data, _ := io.ReadAll(req.Body)
					n := len(bytes.Split(bytes.TrimSpace(data), []byte{'\n'})) / 2
					items := make([]map[string]any, n)
					for i := range items {
						items[i] = map[string]any{"create": map[string]any{"status": 201}}
					}
					encoded, _ := json.Marshal(map[string]any{"items": items})
					return batchHTTPResponse(200, encoded), nil
				}),
			}
			// 预先就绪且期限已过，模拟执行槽位曾经占满后的队列；不得主动等待新项。
			calls := make([]*batchCall, 100)
			for i := range calls {
				calls[i] = &batchCall{ctx: ctx, queued: time.Now().Add(-time.Second), reply: make(chan batchReply, 1), operations: []batchOperation{{Action: "create", Metadata: map[string]any{"_index": "logs", "_id": fmt.Sprint(i)}, Source: json.RawMessage(`{}`)}}}
				b.slots <- struct{}{}
				b.queue <- calls[i]
			}
			b.wg.Add(1)
			go b.run(b.queue, wait)
			t.Cleanup(b.Close)
			for _, c := range calls {
				select {
				case result := <-c.reply:
					if result.err != nil {
						t.Fatal(result.err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("queued calls did not finish")
				}
			}
			b.Close()
			if requests != 1 {
				t.Fatalf("ready operations split into %d requests, want 1", requests)
			}
		})
	}
}

func TestWriteBatchDeadlineAndIndependentReads(t *testing.T) {
	b := newTestWriteBatch(t, transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/_mget" {
			return batchHTTPResponse(200, []byte(`{"docs":[{"found":true}]}`)), nil
		}
		return batchHTTPResponse(200, []byte(`{"items":[{"create":{"status":201}}]}`)), nil
	}), func(c *WriteBatchConfig) {
		c.MaxOperations = 100
		c.Wait = 100 * time.Millisecond
		c.MaxConcurrentBatches = 32
	})
	readCtx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
	defer cancel()
	read, _ := http.NewRequestWithContext(readCtx, http.MethodGet, "/events/_doc/a?realtime=true", nil)
	response, err := b.Perform(read)
	if err != nil {
		t.Fatalf("realtime read waited for write deadline: %v", err)
	}
	_ = response.Body.Close()
	write, _ := http.NewRequestWithContext(t.Context(), http.MethodPut, "/alerts/_create/a?refresh=false", strings.NewReader(`{}`))
	started := time.Now()
	response, err = b.Perform(write)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if time.Since(started) < 100*time.Millisecond {
		t.Fatal("partial write batch sent before deadline")
	}
}

func TestWriteBatchMixedOperationsPreserveResponses(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	b := newTestWriteBatch(t, transportFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		if req.URL.Path != "/_bulk" || req.URL.Query().Get("refresh") != "false" {
			return nil, fmt.Errorf("unexpected path %s", req.URL)
		}
		data, _ := io.ReadAll(req.Body)
		lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
		var items []map[string]any
		for i := 0; i < len(lines); i += 2 {
			var header map[string]map[string]any
			if err := json.Unmarshal(lines[i], &header); err != nil {
				return nil, err
			}
			for action, meta := range header {
				status := 201
				if action == "index" {
					status = 200
					if meta["if_seq_no"] != float64(7) {
						return nil, fmt.Errorf("missing OCC: %v", meta)
					}
				}
				if meta["_id"] == "conflict" {
					status = 409
				}
				items = append(items, map[string]any{action: map[string]any{"_index": meta["_index"], "_id": meta["_id"], "_seq_no": 8, "_primary_term": 1, "status": status}})
			}
		}
		encoded, _ := json.Marshal(map[string]any{"items": items})
		return batchHTTPResponse(200, encoded), nil
	}), nil)
	paths := []string{"/events/_doc/a?refresh=false&if_seq_no=7&if_primary_term=1", "/alerts/_doc/b?refresh=false&if_seq_no=7&if_primary_term=1", "/alerts/_create/conflict?refresh=false&require_alias=true", "/_bulk?refresh=false&require_alias=true"}
	var wg sync.WaitGroup
	for i, path := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			method := http.MethodPut
			body := `{}`
			if i == 3 {
				method = http.MethodPost
				body = "{\"create\":{\"_index\":\"logs\",\"_id\":\"log\"}}\n{}\n"
			}
			req, _ := http.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
			resp, err := b.Perform(req)
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = resp.Body.Close() }()
			want := 200
			if i == 2 {
				want = 409
			}
			if resp.StatusCode != want {
				t.Errorf("status=%d want %d", resp.StatusCode, want)
			}
		}()
	}
	wg.Wait()
	if requests != 1 {
		t.Fatalf("physical writes=%d want 1", requests)
	}
}

func TestWriteBatchRealtimeReads(t *testing.T) {
	b := newTestWriteBatch(t, transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/_mget" || req.URL.Query().Get("realtime") != "true" {
			return nil, fmt.Errorf("unexpected read %s", req.URL)
		}
		var body struct {
			Docs []map[string]string `json:"docs"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			return nil, err
		}
		docs := make([]map[string]any, len(body.Docs))
		for i, doc := range body.Docs {
			docs[i] = map[string]any{"_index": doc["_index"], "_id": doc["_id"], "found": doc["_id"] != "missing"}
		}
		data, _ := json.Marshal(map[string]any{"docs": docs})
		return batchHTTPResponse(200, data), nil
	}), nil)
	var wg sync.WaitGroup
	for _, id := range []string{"present", "missing"} {
		wg.Go(func() {
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/events/_doc/"+id+"?realtime=true", nil)
			resp, err := b.Perform(req)
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = resp.Body.Close() }()
			want := 200
			if id == "missing" {
				want = 404
			}
			if resp.StatusCode != want {
				t.Errorf("status=%d", resp.StatusCode)
			}
		})
	}
	wg.Wait()
}

func TestWriteBatchCancellationDoesNotCancelPeers(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	b := newTestWriteBatch(t, transportFunc(func(req *http.Request) (*http.Response, error) {
		close(started)
		select {
		case <-release:
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
		return batchHTTPResponse(200, []byte(`{"items":[{"create":{"status":201}},{"create":{"status":201}}]}`)), nil
	}), nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	results := make(chan error, 2)
	for _, ctx := range []context.Context{ctx, t.Context()} {
		go func() {
			req, _ := http.NewRequestWithContext(ctx, http.MethodPut, "/alerts/_create/a?refresh=false", strings.NewReader(`{}`))
			resp, err := b.Perform(req)
			if resp != nil {
				_ = resp.Body.Close()
			}
			results <- err
		}()
	}
	<-started
	cancel()
	close(release)
	err1, err2 := <-results, <-results
	if (err1 == nil) == (err2 == nil) {
		t.Fatalf("expected one canceled call: %v / %v", err1, err2)
	}
}

func TestWriteBatchCloseUnblocksQueuedCalls(t *testing.T) {
	b := newTestWriteBatch(t, transportFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	}), func(c *WriteBatchConfig) { c.MaxCalls = 1; c.Wait = 100 * time.Millisecond })
	done := make(chan error, 2)
	for range 2 {
		go func() {
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodPut, "/alerts/_create/a?refresh=false", strings.NewReader(`{}`))
			resp, err := b.Perform(req)
			if resp != nil {
				_ = resp.Body.Close()
			}
			done <- err
		}()
	}
	b.Close()
	for range 2 {
		select {
		case err := <-done:
			if err == nil {
				t.Error("unexpected success")
			}
		case <-time.After(time.Second):
			t.Fatal("close blocked caller")
		}
	}
}

func TestWriteBatchSplitsByCountAndBytes(t *testing.T) {
	for _, large := range []bool{false, true} {
		t.Run(fmt.Sprint(large), func(t *testing.T) {
			var sizes []int
			b := newTestWriteBatch(t, transportFunc(func(req *http.Request) (*http.Response, error) {
				data, _ := io.ReadAll(req.Body)
				lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
				n := len(lines) / 2
				sizes = append(sizes, n)
				items := make([]map[string]any, n)
				for i := range items {
					items[i] = map[string]any{"create": map[string]any{"status": 201}}
				}
				encoded, _ := json.Marshal(map[string]any{"items": items})
				return batchHTTPResponse(200, encoded), nil
			}), func(c *WriteBatchConfig) { c.MaxOperations = 2; c.MaxBytes = 1 << 20 })
			body := ""
			for i := range 5 {
				source := `{}`
				if large {
					source = `{"x":"` + strings.Repeat("x", 600000) + `"}`
				}
				body += fmt.Sprintf("{\"create\":{\"_index\":\"logs\",\"_id\":\"%d\"}}\n%s\n", i, source)
			}
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "/_bulk?refresh=false", strings.NewReader(body))
			resp, err := b.Perform(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			want := 3
			if large {
				want = 5
			}
			if len(sizes) != want {
				t.Fatalf("batches=%v", sizes)
			}
		})
	}
}

func TestWriteBatchPartialTransportFailurePreservesCompletedSlice(t *testing.T) {
	calls := 0
	b := newTestWriteBatch(t, transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 2 {
			return nil, io.ErrUnexpectedEOF
		}
		return batchHTTPResponse(200, []byte(`{"items":[{"create":{"status":201}}]}`)), nil
	}), func(c *WriteBatchConfig) { c.MaxOperations = 1 })
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "/_bulk?refresh=false", strings.NewReader("{\"create\":{\"_index\":\"logs\",\"_id\":\"1\"}}\n{}\n{\"create\":{\"_index\":\"logs\",\"_id\":\"2\"}}\n{}\n"))
	resp, err := b.Perform(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var response struct {
		Items []struct {
			Create struct {
				Status int `json:"status"`
			} `json:"create"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 2 || response.Items[0].Create.Status != 201 || response.Items[1].Create.Status != 503 || calls != 2 {
		t.Fatalf("response=%+v calls=%d", response, calls)
	}
}

func TestWriteBatchCanceledQueuedCallIsNotSent(t *testing.T) {
	b := newTestWriteBatch(t, transportFunc(func(*http.Request) (*http.Response, error) {
		t.Error("canceled operation was sent")
		return nil, io.ErrUnexpectedEOF
	}), func(c *WriteBatchConfig) { c.Wait = 100 * time.Millisecond })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, "/alerts/_create/a?refresh=false", strings.NewReader(`{}`))
	resp, err := b.Perform(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected cancellation")
	}
	b.Close()
}

func TestWriteBatchMalformedResponseFailsCaller(t *testing.T) {
	for _, body := range []string{`{"items":[]}`, `{"items":[{"delete":{"status":200}}]}`, `not-json`} {
		t.Run(body, func(t *testing.T) {
			b := newTestWriteBatch(t, transportFunc(func(*http.Request) (*http.Response, error) { return batchHTTPResponse(200, []byte(body)), nil }), nil)
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodPut, "/alerts/_create/a?refresh=false", strings.NewReader(`{}`))
			resp, err := b.Perform(req)
			if resp != nil {
				_ = resp.Body.Close()
			}
			if err == nil {
				t.Fatal("accepted malformed response")
			}
		})
	}
}
