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
	"strconv"
	"strings"
	"sync"
	"time"
)

// WriteBatchConfig 限制单个 Lifecycle runtime 的合批等待和资源使用。
type WriteBatchConfig struct {
	MaxOperations        int
	MaxBytes             int
	Wait                 time.Duration
	MaxConcurrentBatches int
	MaxCalls             int
	Timeout              time.Duration
}

// BatchObserver 观察物理批次，逻辑 Repository 指标继续由调用方记录。
type BatchObserver interface {
	BatchFinished(context.Context, string, int, int, time.Duration, time.Duration, int)
}

type batchOperation struct {
	Action   string
	Target   string
	ID       string
	Metadata map[string]any
	Source   json.RawMessage
}

type batchReply struct {
	status int
	data   []byte
	err    error
}

type batchCall struct {
	ctx        context.Context
	operations []batchOperation
	body       []byte
	read       bool
	bulk       bool
	queued     time.Time
	reply      chan batchReply
}

// WriteBatchTransport 聚合同一 Repository 中独立调用的已编码写入与实时读取。
// 只装配到 Lifecycle；调用仍同步等待逐项响应，因此原状态机的依赖顺序保持不变。
type WriteBatchTransport struct {
	next      Transport
	config    WriteBatchConfig
	observer  BatchObserver
	ctx       context.Context
	cancel    context.CancelFunc
	queue     chan *batchCall
	readQueue chan *batchCall
	slots     chan struct{}
	workers   chan struct{}
	wg        sync.WaitGroup
	once      sync.Once
	admission sync.Mutex
}

// EnableWriteBatch 返回使用独立合批 transport 的 Repository，不修改原 Repository。
// 调用方必须先 Close 合批器，再关闭底层连接池。
func (r *Repository) EnableWriteBatch(cfg WriteBatchConfig, observer BatchObserver) (*Repository, *WriteBatchTransport, error) {
	if cfg.MaxOperations < 1 || cfg.MaxOperations > 1000 || cfg.MaxBytes < 1<<20 || cfg.MaxBytes > r.config.MaxRequestBytes || cfg.Wait < 0 || cfg.Wait > 100*time.Millisecond || cfg.MaxConcurrentBatches < 1 || cfg.MaxConcurrentBatches > 32 || cfg.MaxCalls < 1 || cfg.Timeout <= 0 {
		return nil, nil, fmt.Errorf("invalid lifecycle write batch resource limits")
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := &WriteBatchTransport{next: r.transport, config: cfg, observer: observer, ctx: ctx, cancel: cancel, queue: make(chan *batchCall, cfg.MaxCalls), slots: make(chan struct{}, cfg.MaxCalls), workers: make(chan struct{}, cfg.MaxConcurrentBatches)}
	b.readQueue = make(chan *batchCall, cfg.MaxCalls)
	b.wg.Add(2)
	go b.run(b.queue, cfg.Wait)
	// realtime 校验只合并已经就绪的读，不占用写批次计数或等待写入的聚合期限。
	go b.run(b.readQueue, 0)
	copy := *r
	copy.transport = b
	return &copy, b, nil
}

// Close 取消待处理与在途批次，等待所有 worker 退出；可重复调用。
func (b *WriteBatchTransport) Close() {
	b.once.Do(func() { b.admission.Lock(); b.cancel(); b.admission.Unlock() })
	b.wg.Wait()
}

// Perform 将允许合批的内部请求分解为操作，其他请求直接执行。
func (b *WriteBatchTransport) Perform(req *http.Request) (*http.Response, error) {
	parts := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
	read := req.Method == http.MethodGet && len(parts) == 3 && parts[1] == "_doc" && req.URL.Query().Get("realtime") == "true"
	write := req.Method == http.MethodPut && len(parts) == 3 && (parts[1] == "_create" || parts[1] == "_doc") && req.URL.Query().Get("refresh") == "false"
	bulk := req.Method == http.MethodPost && req.URL.Path == "/_bulk" && req.URL.Query().Get("refresh") == "false"
	if !read && !write && !bulk {
		return b.next.Perform(req)
	}
	select {
	case b.slots <- struct{}{}:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	case <-b.ctx.Done():
		return nil, b.ctx.Err()
	}
	if err := b.ctx.Err(); err != nil {
		<-b.slots
		return nil, err
	}
	c := &batchCall{ctx: req.Context(), read: read, bulk: bulk, queued: time.Now(), reply: make(chan batchReply, 1)}
	if req.Body != nil {
		data, err := io.ReadAll(io.LimitReader(req.Body, defaultMaxRequestBytes+1))
		_ = req.Body.Close()
		if err != nil || len(data) > defaultMaxRequestBytes {
			<-b.slots
			return nil, fmt.Errorf("read batch request: %w", errOrLimit(err))
		}
		c.body = data
	}
	if read || write {
		action := "index"
		if write && parts[1] == "_create" {
			action = "create"
		}
		metadata := map[string]any{"_index": parts[0], "_id": parts[2]}
		for _, key := range []string{"if_seq_no", "if_primary_term"} {
			if value := req.URL.Query().Get(key); value != "" {
				n, err := strconv.ParseInt(value, 10, 64)
				if err != nil {
					<-b.slots
					return nil, err
				}
				metadata[key] = n
			}
		}
		if req.URL.Query().Get("require_alias") == "true" {
			metadata["require_alias"] = true
		}
		c.operations = []batchOperation{{Action: action, Target: parts[0], ID: parts[2], Metadata: metadata, Source: c.body}}
	} else {
		lines := bytes.Split(bytes.TrimSpace(c.body), []byte{'\n'})
		if len(lines)%2 != 0 {
			<-b.slots
			return nil, fmt.Errorf("invalid create bulk line count")
		}
		for i := 0; i < len(lines); i += 2 {
			var header map[string]map[string]any
			if err := json.Unmarshal(lines[i], &header); err != nil {
				<-b.slots
				return nil, err
			}
			meta, ok := header["create"]
			if !ok || len(header) != 1 {
				<-b.slots
				return nil, fmt.Errorf("unsupported lifecycle bulk action")
			}
			target, _ := meta["_index"].(string)
			id, _ := meta["_id"].(string)
			if req.URL.Query().Get("require_alias") == "true" {
				meta["require_alias"] = true
			}
			c.operations = append(c.operations, batchOperation{Action: "create", Target: target, ID: id, Metadata: meta, Source: lines[i+1]})
		}
	}
	b.admission.Lock()
	if err := b.ctx.Err(); err != nil {
		b.admission.Unlock()
		<-b.slots
		return nil, err
	}
	// 已取得调用 slot，queue 容量等于总 slot 数，因此此发送不会等待。
	if c.read {
		b.readQueue <- c
	} else {
		b.queue <- c
	}
	b.admission.Unlock()
	select {
	case reply := <-c.reply:
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		if reply.err != nil {
			return nil, reply.err
		}
		return batchHTTPResponse(reply.status, reply.data), nil
	case <-req.Context().Done():
		return nil, req.Context().Err()
	case <-b.ctx.Done():
		return nil, b.ctx.Err()
	}
}

func errOrLimit(err error) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("request exceeds size limit")
}

func (b *WriteBatchTransport) finish(c *batchCall, r batchReply) { c.reply <- r; <-b.slots }

func (b *WriteBatchTransport) run(queue <-chan *batchCall, wait time.Duration) {
	defer b.wg.Done()
	defer func() {
		for {
			select {
			case c := <-queue:
				b.finish(c, batchReply{err: b.ctx.Err()})
			default:
				return
			}
		}
	}()
	for {
		var first *batchCall
		select {
		case <-b.ctx.Done():
			return
		case first = <-queue:
		}
		calls := []*batchCall{first}
		count := len(first.operations)
		size := len(first.body)
		timer := time.NewTimer(max(0, wait-time.Since(first.queued)))
	collect:
		for count < b.config.MaxOperations && size < b.config.MaxBytes {
			// 先非阻塞地合入已经就绪的操作。等待期限只限制等待新项，不能让已到期
			// timer 与非空队列随机竞争，否则执行槽位拥堵时会退化为大量微小批次。
			select {
			case c := <-queue:
				calls = append(calls, c)
				count += len(c.operations)
				size += len(c.body)
				continue
			default:
			}
			select {
			case <-b.ctx.Done():
				timer.Stop()
				for _, c := range calls {
					b.finish(c, batchReply{err: b.ctx.Err()})
				}
				return
			case <-timer.C:
				break collect
			case c := <-queue:
				calls = append(calls, c)
				count += len(c.operations)
				size += len(c.body)
			}
		}
		timer.Stop()
		select {
		case b.workers <- struct{}{}:
		case <-b.ctx.Done():
			for _, c := range calls {
				b.finish(c, batchReply{err: b.ctx.Err()})
			}
			return
		}
		b.wg.Add(1)
		go func() { defer b.wg.Done(); defer func() { <-b.workers }(); b.execute(calls) }()
	}
}

type operationRef struct {
	call      *batchCall
	position  int
	operation batchOperation
}

func (b *WriteBatchTransport) execute(calls []*batchCall) {
	for _, read := range []bool{true, false} {
		var refs []operationRef
		results := map[*batchCall][]json.RawMessage{}
		errors := map[*batchCall]error{}
		for _, c := range calls {
			if c.read != read {
				continue
			}
			results[c] = make([]json.RawMessage, len(c.operations))
			if err := c.ctx.Err(); err != nil {
				errors[c] = err
				continue
			}
			for i, op := range c.operations {
				refs = append(refs, operationRef{c, i, op})
			}
		}
		for len(refs) > 0 {
			n := 0
			size := 0
			for n < len(refs) && n < b.config.MaxOperations {
				encoded, err := encodeBatchOperation(refs[n].operation, read)
				if err != nil {
					errors[refs[n].call] = err
				}
				if n > 0 && size+len(encoded) > b.config.MaxBytes {
					break
				}
				size += len(encoded)
				n++
			}
			b.send(refs[:n], read, results, errors)
			refs = refs[n:]
		}
		for c, items := range results {
			if err := errors[c]; err != nil {
				b.finish(c, batchReply{err: err})
				continue
			}
			if c.bulk {
				data, _ := json.Marshal(map[string]any{"items": items})
				b.finish(c, batchReply{status: http.StatusOK, data: data})
				continue
			}
			if len(items) != 1 || len(items[0]) == 0 {
				b.finish(c, batchReply{err: fmt.Errorf("missing batch item")})
				continue
			}
			data := items[0]
			status := http.StatusOK
			if read {
				var result struct {
					Found  bool            `json:"found"`
					Error  json.RawMessage `json:"error"`
					Status int             `json:"status"`
				}
				_ = json.Unmarshal(data, &result)
				if len(result.Error) > 0 {
					status = result.Status
					if status < 400 {
						status = 500
					}
				} else if !result.Found {
					status = 404
				}
			} else {
				var result map[string]json.RawMessage
				_ = json.Unmarshal(data, &result)
				data = result[c.operations[0].Action]
				var item struct {
					Status int `json:"status"`
				}
				_ = json.Unmarshal(data, &item)
				status = item.Status
			}
			b.finish(c, batchReply{status: status, data: data})
		}
	}
}

func encodeBatchOperation(op batchOperation, read bool) ([]byte, error) {
	if read {
		return json.Marshal(map[string]string{"_index": op.Target, "_id": op.ID})
	}
	header, err := json.Marshal(map[string]any{op.Action: op.Metadata})
	if err != nil {
		return nil, err
	}
	return append(append(append(header, '\n'), op.Source...), '\n'), nil
}

func (b *WriteBatchTransport) send(refs []operationRef, read bool, results map[*batchCall][]json.RawMessage, errs map[*batchCall]error) {
	// 前一个切片请求执行期间调用方可能取消；尚未发送的项不能继续写入。
	active := refs[:0]
	for _, ref := range refs {
		if err := ref.call.ctx.Err(); err != nil {
			errs[ref.call] = err
			continue
		}
		active = append(active, ref)
	}
	refs = active
	if len(refs) == 0 {
		return
	}
	var body bytes.Buffer
	if read {
		body.WriteString(`{"docs":[`)
	}
	for i, ref := range refs {
		encoded, err := encodeBatchOperation(ref.operation, read)
		if err != nil {
			for _, r := range refs {
				errs[r.call] = err
			}
			return
		}
		if read && i > 0 {
			body.WriteByte(',')
		}
		body.Write(encoded)
	}
	if read {
		body.WriteString(`]}`)
	}
	path := "/_bulk?refresh=false"
	kind := "write"
	if read {
		path = "/_mget?realtime=true"
		kind = "read"
	}
	ctx, cancel := context.WithTimeout(b.ctx, b.config.Timeout)
	defer cancel()
	started := time.Now()
	wait := started.Sub(refs[0].call.queued)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(body.Bytes()))
	var items []json.RawMessage
	if err == nil {
		req.Header.Set("Content-Type", "application/x-ndjson")
		if read {
			req.Header.Set("Content-Type", "application/json")
		}
		var response *http.Response
		response, err = b.next.Perform(req)
		if err == nil {
			if response == nil || response.Body == nil {
				err = fmt.Errorf("empty batch response")
			} else {
				data, e := io.ReadAll(io.LimitReader(response.Body, defaultMaxResponseBytes+1))
				_ = response.Body.Close()
				err = e
				if err == nil && len(data) > defaultMaxResponseBytes {
					err = ErrResponseTooLarge
				}
				if err == nil && response.StatusCode >= 300 {
					err = fmt.Errorf("batch HTTP status %d", response.StatusCode)
				}
				if err == nil {
					var decoded struct {
						Items []json.RawMessage `json:"items"`
						Docs  []json.RawMessage `json:"docs"`
					}
					err = json.Unmarshal(data, &decoded)
					items = decoded.Items
					if read {
						items = decoded.Docs
					}
					if err == nil && len(items) != len(refs) {
						err = fmt.Errorf("batch response item count mismatch")
					}
				}
			}
		}
	}
	failed := 0
	for i, ref := range refs {
		if err != nil {
			if ref.call.bulk {
				// 已完成切片保留逐项成功；失败切片按结果未知交回原 Bulk 错误处理。
				data, _ := json.Marshal(map[string]any{ref.operation.Action: map[string]any{"status": 503, "error": map[string]string{"type": "batch_transport_error", "reason": err.Error()}}})
				results[ref.call][ref.position] = data
			} else {
				errs[ref.call] = err
			}
			failed++
			continue
		}
		var item struct {
			Status int             `json:"status"`
			Error  json.RawMessage `json:"error"`
		}
		if read {
			_ = json.Unmarshal(items[i], &item)
		} else {
			var action map[string]json.RawMessage
			_ = json.Unmarshal(items[i], &action)
			if decodeErr := json.Unmarshal(action[ref.operation.Action], &item); decodeErr != nil || item.Status < 200 || item.Status > 599 {
				errs[ref.call] = fmt.Errorf("invalid bulk response action or status")
				failed++
				continue
			}
		}
		if item.Status >= 400 || len(item.Error) > 0 {
			failed++
		}
		results[ref.call][ref.position] = items[i]
	}
	if b.observer != nil {
		b.observer.BatchFinished(ctx, kind, len(refs), body.Len(), wait, time.Since(started), failed)
	}
}

func batchHTTPResponse(status int, data []byte) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}
}
