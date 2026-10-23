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
	"sync"
	"time"
)

// ExportFunc 返回 nil 表示整批成功。实现必须响应 ctx；保留 body 时须自行复制。
type ExportFunc func(ctx context.Context, body []byte) error

type Exporter struct {
	store       *Store
	export      ExportFunc
	config      Config
	flushMu     sync.Mutex
	lifecycleMu sync.Mutex
	started     bool
	closed      bool
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	stopOnce    sync.Once
	stopErr     error
	lastStart   time.Time
}

func NewExporter(config Config, export ExportFunc) (*Exporter, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	if export == nil {
		return nil, ErrUnconfigured
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Exporter{store: newStore(config), config: config, export: export, ctx: ctx, cancel: cancel}, nil
}

func (e *Exporter) Store() *Store { return e.store }

func (e *Exporter) Start() error {
	e.lifecycleMu.Lock()
	defer e.lifecycleMu.Unlock()
	if e.closed {
		return ErrClosed
	}
	if e.started {
		return nil
	}
	e.started = true
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		ticker := time.NewTicker(e.config.FlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
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

func waitContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Exporter) flush(parent context.Context, final bool) error {
	start := time.Now()
	defer func() { e.store.observeFlush(time.Since(start)) }()
	ctx, cancel := context.WithTimeout(parent, e.config.ExportTimeout)
	defer cancel()
	e.flushMu.Lock()
	defer e.flushMu.Unlock()
	e.lifecycleMu.Lock()
	closed := e.closed
	e.lifecycleMu.Unlock()
	if closed && !final {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current := e.store.take(final)
	if len(current.items) == 0 {
		return nil
	}
	defer e.store.restore(current.items)
	batch := make([]Key, 0, min(e.config.ExportBatchSize, len(current.items)))
	body := make([]byte, 1, min(e.config.ExportBatchBytes, 16*1024))
	body[0] = '['
	remaining := e.store.remainingVersions()
	send := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := waitContext(ctx, time.Until(e.lastStart.Add(e.config.ExportMinInterval))); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		e.lastStart = time.Now()
		if err := e.export(ctx, append(body, ']')); err != nil {
			e.store.mu.Lock()
			e.store.stats.Failures++
			e.store.mu.Unlock()
			return err
		}
		for _, key := range batch {
			e.store.ack(key, current.items[key], current.generation)
			delete(current.items, key)
		}
		batch, body = batch[:0], body[:1]
		remaining = e.store.remainingVersions()
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
		if remaining == 0 {
			return nil
		}
		encoded, err := (Entry{key, item.Value}).MarshalJSON()
		if err != nil {
			return err
		}
		if len(encoded)+2 > e.config.ExportBatchBytes {
			e.store.mu.Lock()
			e.store.drop("batch_bytes")
			e.store.mu.Unlock()
			delete(current.items, key)
			continue
		}
		comma := 0
		if len(batch) > 0 {
			comma = 1
		}
		if len(batch) == e.config.ExportBatchSize || len(batch) == remaining || len(body)+comma+len(encoded)+1 > e.config.ExportBatchBytes {
			if err := send(); err != nil {
				return err
			}
			if remaining == 0 {
				return nil
			}
		}
		if len(batch) > 0 {
			body = append(body, ',')
		}
		body = append(body, encoded...)
		batch = append(batch, key)
	}
	return send()
}

func (e *Exporter) Stop() error {
	e.stopOnce.Do(func() {
		e.lifecycleMu.Lock()
		e.closed = true
		e.store.seal()
		e.cancel()
		e.lifecycleMu.Unlock()
		e.wg.Wait()
		e.stopErr = e.flush(context.Background(), true)
		e.store.release()
	})
	return e.stopErr
}
