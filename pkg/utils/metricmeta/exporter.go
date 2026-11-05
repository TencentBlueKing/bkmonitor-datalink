// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metricmeta

import (
	"context"
	"encoding/json"
	"maps"
	"sync"
	"time"
)

// ExportFunc 返回 nil 表示整批成功。实现必须响应 ctx；保留 body 时须自行复制。
type ExportFunc func(ctx context.Context, body []byte) error

// Exporter 串行发送待发快照，确认后登记 Bloom，失败时恢复未确认项。
type Exporter struct {
	store    *Store
	export   func(context.Context, []Entry) error
	sending  chan struct{} // 串行化发送与配置重载，等待发送时可取消。
	ticker   *time.Ticker
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	stopOnce sync.Once
	stopErr  error
}

// NewExporter 将每个批次编码为 JSON 数组后调用 export。调用 Start 后才启动后台发送。
func NewExporter(config Config, export ExportFunc) (*Exporter, error) {
	if export == nil {
		return nil, ErrUnconfigured
	}
	return NewBatchExporter(config, func(ctx context.Context, entries []Entry) error {
		rows := make([]wireEntry, len(entries))
		for i, entry := range entries {
			rows[i] = entry.wire()
		}
		body, err := json.Marshal(rows)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return export(ctx, body)
	})
}

// NewBatchExporter 创建按批发送元信息的实例。回调同步执行，须响应 ctx 并只读使用 entries。
// 切片仅在回调期间有效。如需异步保留，须先复制。返回 nil 表示确认整批。
func NewBatchExporter(config Config, export func(ctx context.Context, entries []Entry) error) (*Exporter, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	if export == nil {
		return nil, ErrUnconfigured
	}
	ctx, cancel := context.WithCancel(context.Background())
	config.Limits.ByDataID = maps.Clone(config.Limits.ByDataID)
	return &Exporter{store: newStore(config), export: export, sending: make(chan struct{}, 1), ctx: ctx, cancel: cancel}, nil
}

// Store 返回可并发观察元信息的存储实例。
func (e *Exporter) Store() *Store { return e.store }

// Reload 应用完整配置，保留待发值、已发送版本和当前窗口计数。
func (e *Exporter) Reload(config Config) error {
	if err := config.validate(); err != nil {
		return err
	}
	config.Limits.ByDataID = maps.Clone(config.Limits.ByDataID)
	// 与发送快照串行，返回时旧快照不会再送出已禁止来源。
	e.sending <- struct{}{}
	defer func() { <-e.sending }()
	s := e.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if e.ticker != nil && config.Exporter.Interval != s.config.Exporter.Interval {
		e.ticker.Reset(config.Exporter.Interval)
	}
	s.config = config
	for _, limit := range config.Limits.ByDataID {
		if limit != 0 {
			continue
		}
		for k := range s.pending {
			if n, ok := config.Limits.ByDataID[k.DataID]; ok && n == 0 {
				delete(s.pending, k)
				s.drop("blacklist")
			}
		}
		break
	}
	return nil
}

// Start 幂等启动后台发送，关闭后的实例不可重启。
func (e *Exporter) Start() error {
	e.store.mu.Lock()
	defer e.store.mu.Unlock()
	if e.store.closed {
		return ErrClosed
	}
	if e.ticker != nil {
		return nil
	}
	e.ticker = time.NewTicker(e.store.config.Exporter.Interval)
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer e.ticker.Stop()
		for {
			select {
			case <-e.ticker.C:
				_ = e.flush(e.ctx, false)
			case <-e.ctx.Done():
				return
			}
		}
	}()
	return nil
}

// Flush 与后台发送串行，适合使用方主动触发发送或验收。
func (e *Exporter) Flush(ctx context.Context) error {
	return e.flush(ctx, false)
}

func (e *Exporter) flush(parent context.Context, final bool) error {
	start := time.Now()
	defer func() { e.store.observeFlush(time.Since(start)) }()
	e.store.mu.Lock()
	timeout := e.store.config.Exporter.Timeout
	e.store.mu.Unlock()
	ctx, cancel := context.WithDeadline(parent, start.Add(timeout))
	defer cancel()
	select {
	case e.sending <- struct{}{}:
		defer func() { <-e.sending }()
	case <-ctx.Done():
		return ctx.Err()
	}
	e.store.mu.Lock()
	closed, batchSize := e.store.closed, e.store.config.Exporter.BatchSize
	e.store.mu.Unlock()
	if closed && !final {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current := e.store.take()
	if len(current.items) == 0 {
		return nil
	}
	defer e.store.restore(current.items)
	batch := make([]Entry, 0, min(batchSize, len(current.items)))
	send := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := e.export(ctx, batch); err != nil {
			e.store.mu.Lock()
			e.store.stats.Failures++
			e.store.mu.Unlock()
			return err
		}
		for _, entry := range batch {
			e.store.ack(entry.Key, current.items[entry.Key], current.generation)
			delete(current.items, entry.Key)
		}
		batch = batch[:0]
		return nil
	}
	for key, item := range current.items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.store.suppressed(item) {
			delete(current.items, key)
			continue
		}
		batch = append(batch, Entry{Key: key, Value: item.Value})
		if len(batch) == batchSize {
			if err := send(); err != nil {
				return err
			}
		}
	}
	return send()
}

// Stop 拒收新值、取消后台发送，并以独立期限发送最后一轮。
func (e *Exporter) Stop() error { return e.stop(true) }

// Discard 关闭已撤销的目标，不再向该目标刷新待发数据。
func (e *Exporter) Discard() { _ = e.stop(false) }

func (e *Exporter) stop(final bool) error {
	e.stopOnce.Do(func() {
		e.store.seal()
		e.cancel()
		e.wg.Wait()
		if final {
			e.stopErr = e.flush(context.Background(), true)
		}
		e.sending <- struct{}{}
		defer func() { <-e.sending }()
		e.store.release()
	})
	return e.stopErr
}
