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
	MaxOperations int
	MaxBytes      int
	Wait          time.Duration
	// ReadWait 限制独立实时读取队列的收集时间；0 表示仅合并已就绪项。
	ReadWait             time.Duration
	MaxConcurrentBatches int
	MaxCalls             int
	Timeout              time.Duration
}

// BatchObserver 观察物理批次，逻辑 Repository 指标继续由调用方记录。
type BatchObserver interface {
	BatchFinished(context.Context, string, int, int, time.Duration, time.Duration, int)
	BatchPhase(context.Context, string, string, time.Duration)
	BatchTriggered(context.Context, string, string)
}

type batchOperation struct {
	Action   string
	Target   string
	ID       string
	Metadata map[string]any
	Source   json.RawMessage
	// sourceExcludes 只用于 realtime _mget 投影，并随每个 doc 独立编码。
	sourceExcludes []string
	// encodedRead 复用点读元信息编码；收集预算和物理请求使用同一份字节。
	encodedRead []byte
	// encodedWrite 在物理切批时生成并在发送时复用，避免重复编码和复制文档。
	encodedWrite []byte
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
	next            Transport
	config          WriteBatchConfig
	maxRequestBytes int
	observer        BatchObserver
	ctx             context.Context
	cancel          context.CancelFunc
	queue           chan *batchCall
	readQueue       chan *batchCall
	slots           chan struct{}
	workers         chan struct{}
	wg              sync.WaitGroup
	once            sync.Once
	admission       sync.Mutex
}

// EnableWriteBatch 返回使用独立合批 transport 的 Repository，不修改原 Repository。
// 调用方必须先 Close 合批器，再关闭底层连接池。
func (r *Repository) EnableWriteBatch(cfg WriteBatchConfig, observer BatchObserver) (*Repository, *WriteBatchTransport, error) {
	if cfg.MaxOperations < 1 || cfg.MaxOperations > 1000 || cfg.MaxBytes < 1<<20 || cfg.MaxBytes > r.config.MaxRequestBytes || cfg.Wait < 0 || cfg.Wait > 256*time.Millisecond || cfg.ReadWait < 0 || cfg.ReadWait > 10*time.Millisecond || cfg.MaxConcurrentBatches < 1 || cfg.MaxConcurrentBatches > 32 || cfg.MaxCalls < 1 || cfg.Timeout <= 0 {
		return nil, nil, fmt.Errorf("invalid lifecycle write batch resource limits")
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := &WriteBatchTransport{next: r.transport, config: cfg, maxRequestBytes: r.config.MaxRequestBytes, observer: observer, ctx: ctx, cancel: cancel, queue: make(chan *batchCall, cfg.MaxCalls), slots: make(chan struct{}, cfg.MaxCalls), workers: make(chan struct{}, cfg.MaxConcurrentBatches)}
	b.readQueue = make(chan *batchCall, cfg.MaxCalls)
	b.wg.Add(2)
	go b.run(b.queue, cfg.Wait)
	// realtime 读复用满批/字节/期限触发规则，但独立收集，不能等待写侧的长聚合期限。
	go b.run(b.readQueue, cfg.ReadWait)
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
	query := req.URL.Query()
	read := req.Method == http.MethodGet && len(parts) == 3 && parts[1] == "_doc" && query.Get("realtime") == "true"
	indexWrite := req.Method == http.MethodPut && len(parts) == 3 && (parts[1] == "_create" || parts[1] == "_doc")
	updateWrite := req.Method == http.MethodPost && len(parts) == 3 && parts[1] == "_update"
	write := (indexWrite || updateWrite) && query.Get("refresh") == "false"
	bulk := req.Method == http.MethodPost && req.URL.Path == "/_bulk" && query.Get("refresh") == "false"
	if !read && !write && !bulk {
		return b.next.Perform(req)
	}
	admitted := time.Now()
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
	b.phase(batchKind(read), "admission", time.Since(admitted))
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
		} else if updateWrite {
			action = "update"
		}
		metadata := map[string]any{"_index": parts[0], "_id": parts[2]}
		for _, key := range []string{"if_seq_no", "if_primary_term"} {
			if value := query.Get(key); value != "" {
				n, err := strconv.ParseInt(value, 10, 64)
				if err != nil {
					<-b.slots
					return nil, err
				}
				metadata[key] = n
			}
		}
		if query.Get("require_alias") == "true" {
			metadata["require_alias"] = true
		}
		operation := batchOperation{Action: action, Target: parts[0], ID: parts[2], Metadata: metadata, Source: c.body}
		if excludes := query.Get("_source_excludes"); read && excludes != "" {
			for _, value := range strings.Split(excludes, ",") {
				if value = strings.TrimSpace(value); value != "" {
					operation.sourceExcludes = append(operation.sourceExcludes, value)
				}
			}
		}
		c.operations = []batchOperation{operation}
		if read {
			encoded, err := encodeBatchOperation(c.operations[0], true)
			if err != nil {
				<-b.slots
				return nil, err
			}
			if len(encoded)+len(`{"docs":[]}`) > b.maxRequestBytes {
				<-b.slots
				return nil, fmt.Errorf("realtime batch request exceeds size limit")
			}
			c.operations[0].encodedRead = encoded
			// GET 没有正文，读侧以编码后的 mget 元信息计入收集预算。
			c.body = encoded
		}
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
			if query.Get("require_alias") == "true" {
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

func batchCollectBytes(c *batchCall) int {
	if c.read {
		return len(c.body) + 1 // 每项预留一个逗号，首项在外层扣除。
	}
	return len(c.body)
}

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
		kind := batchKind(first.read)
		reason := "deadline"
		count := len(first.operations)
		size := batchCollectBytes(first)
		if first.read {
			size += len(`{"docs":[]}`) - 1
		}
		timer := time.NewTimer(max(0, wait-time.Since(first.queued)))
	collect:
		for count < b.config.MaxOperations && size < b.config.MaxBytes {
			// 先非阻塞地合入已经就绪的操作。等待期限只限制等待新项，不能让已到期
			// timer 与非空队列随机竞争，否则执行槽位拥堵时会退化为大量微小批次。
			select {
			case c := <-queue:
				calls = append(calls, c)
				count += len(c.operations)
				size += batchCollectBytes(c)
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
				size += batchCollectBytes(c)
			}
		}
		timer.Stop()
		if count >= b.config.MaxOperations {
			reason = "operations"
		} else if size >= b.config.MaxBytes {
			reason = "bytes"
		} else if wait == 0 {
			reason = "ready"
		}
		b.phase(kind, "collect", time.Since(first.queued))
		if b.observer != nil {
			b.observer.BatchTriggered(b.ctx, kind, reason)
		}
		slotStarted := time.Now()
		select {
		case b.workers <- struct{}{}:
		case <-b.ctx.Done():
			for _, c := range calls {
				b.finish(c, batchReply{err: b.ctx.Err()})
			}
			return
		}
		b.phase(kind, "worker_slot", time.Since(slotStarted))
		b.wg.Add(1)
		go func() { defer b.wg.Done(); defer func() { <-b.workers }(); b.execute(calls) }()
	}
}

type operationRef struct {
	call      *batchCall
	position  int
	operation batchOperation
}

// batchItem 保留原始子响应及已解析的单请求视图，避免分发时再次解码同一结果。
type batchItem struct {
	raw    json.RawMessage
	data   json.RawMessage
	status int
}

func (b *WriteBatchTransport) execute(calls []*batchCall) {
	for _, read := range []bool{true, false} {
		var refs []operationRef
		results := map[*batchCall][]batchItem{}
		errors := map[*batchCall]error{}
		for _, c := range calls {
			if c.read != read {
				continue
			}
			results[c] = make([]batchItem, len(c.operations))
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
			if read {
				size = len(`{"docs":[]}`)
			}
			for n < len(refs) && n < b.config.MaxOperations {
				encoded, err := encodeBatchOperation(refs[n].operation, read)
				if err != nil {
					errors[refs[n].call] = err
				}
				if !read {
					refs[n].operation.encodedWrite = encoded
				}
				operationBytes := len(encoded)
				if read && n > 0 {
					operationBytes++
				}
				if n > 0 && size+operationBytes > b.config.MaxBytes {
					break
				}
				size += operationBytes
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
				// raw 来自已验证的 JSON 响应或本地编码的错误项；直接拼接，
				// 保留逐项原始字段，不再 Marshal RawMessage 重扫每个子对象。
				b.finish(c, batchReply{status: http.StatusOK, data: bulkItemResponse(items)})
				continue
			}
			if len(items) != 1 || len(items[0].data) == 0 {
				b.finish(c, batchReply{err: fmt.Errorf("missing batch item")})
				continue
			}
			b.finish(c, batchReply{status: items[0].status, data: items[0].data})
		}
	}
}

func bulkItemResponse(items []batchItem) []byte {
	size := len(`{"items":[]}`) + max(0, len(items)-1)
	for _, item := range items {
		size += len(item.raw)
	}
	data := make([]byte, 0, size)
	data = append(data, `{"items":[`...)
	for i, item := range items {
		if i > 0 {
			data = append(data, ',')
		}
		data = append(data, item.raw...)
	}
	return append(data, ']', '}')
}

func encodeBatchOperation(op batchOperation, read bool) ([]byte, error) {
	if read {
		if op.encodedRead != nil {
			return op.encodedRead, nil
		}
		if len(op.sourceExcludes) == 0 {
			return json.Marshal(map[string]string{"_index": op.Target, "_id": op.ID})
		}
		return json.Marshal(map[string]any{
			"_index": op.Target,
			"_id":    op.ID,
			"_source": map[string]any{
				"excludes": op.sourceExcludes,
			},
		})
	}
	if op.encodedWrite != nil {
		return op.encodedWrite, nil
	}
	header, err := json.Marshal(map[string]any{op.Action: op.Metadata})
	if err != nil {
		return nil, err
	}
	return append(append(append(header, '\n'), op.Source...), '\n'), nil
}

func (b *WriteBatchTransport) send(refs []operationRef, read bool, results map[*batchCall][]batchItem, errs map[*batchCall]error) {
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
	kind := batchKind(read)
	encodeStarted := time.Now()
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
	b.phase(kind, "encode", time.Since(encodeStarted))
	path := "/_bulk?refresh=false"
	if read {
		path = "/_mget?realtime=true"
	}
	ctx, cancel := context.WithTimeout(b.ctx, b.config.Timeout)
	defer cancel()
	started := time.Now()
	wait := started.Sub(refs[0].call.queued)
	// 按物理操作记录，重复身份仍各占一项；与旧首项等待指标不可混算。
	for _, ref := range refs {
		b.phase(kind, "operation_queue", started.Sub(ref.call.queued))
	}
	ctx, trace := b.traceRequest(ctx, kind)
	defer trace()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(body.Bytes()))
	var items []json.RawMessage
	var serverTook *int64
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
				bodyStarted := time.Now()
				data, e := io.ReadAll(io.LimitReader(response.Body, defaultMaxResponseBytes+1))
				_ = response.Body.Close()
				b.phase(kind, "response_body", time.Since(bodyStarted))
				err = e
				if err == nil && len(data) > defaultMaxResponseBytes {
					err = ErrResponseTooLarge
				}
				if err == nil && response.StatusCode >= 300 {
					err = fmt.Errorf("batch HTTP status %d", response.StatusCode)
				}
				if err == nil {
					decodeStarted := time.Now()
					var decoded struct {
						Items []json.RawMessage `json:"items"`
						Docs  []json.RawMessage `json:"docs"`
						Took  json.RawMessage   `json:"took"`
					}
					err = json.Unmarshal(data, &decoded)
					b.phase(kind, "response_decode", time.Since(decodeStarted))
					items = decoded.Items
					if read {
						items = decoded.Docs
					}
					if err == nil && len(items) != len(refs) {
						err = fmt.Errorf("batch response item count mismatch")
					}
					// took 缺失或非法不能改变业务结果，也不能补成零；仅 Bulk
					// 有该服务端计时。与相同请求的客户端执行时间配对采样。
					if err == nil && !read && len(decoded.Took) > 0 && string(decoded.Took) != "null" {
						var milliseconds int64
						if json.Unmarshal(decoded.Took, &milliseconds) == nil && milliseconds >= 0 && milliseconds <= int64((1<<63-1)/time.Millisecond) {
							serverTook = &milliseconds
						}
					}
				}
			}
		}
	}
	itemsStarted := time.Now()
	failed := 0
	for i, ref := range refs {
		if err != nil {
			if ref.call.bulk {
				// 已完成切片保留逐项成功；失败切片按结果未知交回原 Bulk 错误处理。
				data, _ := json.Marshal(map[string]any{ref.operation.Action: map[string]any{"status": 503, "error": map[string]string{"type": "batch_transport_error", "reason": err.Error()}}})
				results[ref.call][ref.position] = batchItem{raw: data}
			} else {
				errs[ref.call] = err
			}
			failed++
			continue
		}
		var item struct {
			Found  bool            `json:"found"`
			Status int             `json:"status"`
			Error  json.RawMessage `json:"error"`
		}
		mapped := batchItem{raw: items[i], data: items[i], status: http.StatusOK}
		if read {
			_ = json.Unmarshal(items[i], &item)
			if len(item.Error) > 0 {
				mapped.status = item.Status
				if mapped.status < 400 {
					mapped.status = http.StatusInternalServerError
				}
			} else if !item.Found {
				mapped.status = http.StatusNotFound
			}
		} else {
			var action map[string]json.RawMessage
			_ = json.Unmarshal(items[i], &action)
			if decodeErr := json.Unmarshal(action[ref.operation.Action], &item); decodeErr != nil || item.Status < 200 || item.Status > 599 {
				errs[ref.call] = fmt.Errorf("invalid bulk response action or status")
				failed++
				continue
			}
			mapped.data = action[ref.operation.Action]
			mapped.status = item.Status
		}
		if item.Status >= 400 || len(item.Error) > 0 {
			failed++
		}
		results[ref.call][ref.position] = mapped
	}
	finished := time.Now()
	b.phase(kind, "response_items", finished.Sub(itemsStarted))
	if serverTook != nil {
		b.phase(kind, "server_took", time.Duration(*serverTook)*time.Millisecond)
		b.phase(kind, "paired_execution", finished.Sub(started))
	}
	if b.observer != nil {
		b.observer.BatchFinished(ctx, kind, len(refs), body.Len(), wait, finished.Sub(started), failed)
	}
}

func batchHTTPResponse(status int, data []byte) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}
}
