// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tracing

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const (
	queueSpans    = 2048
	batchSpans    = 512
	batchInterval = 2 * time.Second
)

// boundedProcessor queues ended spans and exports them in batches from one
// goroutine. Its own rather than the SDK's batcher, which drops a span its
// full queue cannot take without saying so: here the drop and a batch the
// collector refused are each counted, and a Slot never waits for either.
type boundedProcessor struct {
	exporter sdktrace.SpanExporter
	counters Counters
	// failed hears a refused batch; nil hears nothing.
	failed  func(error)
	queue   chan sdktrace.ReadOnlySpan
	flush   chan chan struct{}
	stop    chan struct{}
	done    chan struct{}
	stopped atomic.Bool
	once    sync.Once
}

func newBoundedProcessor(exporter sdktrace.SpanExporter, counters Counters, failed func(error)) *boundedProcessor {
	processor := &boundedProcessor{exporter: exporter, counters: counters, failed: failed, queue: make(chan sdktrace.ReadOnlySpan, queueSpans),
		flush: make(chan chan struct{}), stop: make(chan struct{}), done: make(chan struct{})}
	go processor.run()
	return processor
}

func (processor *boundedProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}

func (processor *boundedProcessor) OnEnd(span sdktrace.ReadOnlySpan) {
	if processor.stopped.Load() || !span.SpanContext().IsSampled() {
		return
	}
	select {
	case processor.queue <- span:
	default:
		processor.counters.TraceSpansDropped(DropQueueFull, 1)
	}
}

func (processor *boundedProcessor) run() {
	defer close(processor.done)
	ticker := time.NewTicker(batchInterval)
	defer ticker.Stop()
	batch := make([]sdktrace.ReadOnlySpan, 0, batchSpans)
	send := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), exportTimeout)
		if err := processor.exporter.ExportSpans(ctx, batch); err != nil {
			processor.counters.TraceSpansDropped(DropExportFailed, len(batch))
			if processor.failed != nil {
				processor.failed(err)
			}
		}
		cancel()
		batch = batch[:0]
	}
	drain := func() {
		for {
			select {
			case span := <-processor.queue:
				batch = append(batch, span)
				if len(batch) == batchSpans {
					send()
				}
			default:
				send()
				return
			}
		}
	}
	for {
		select {
		case span := <-processor.queue:
			batch = append(batch, span)
			if len(batch) == batchSpans {
				send()
			}
		case <-ticker.C:
			send()
		case ack := <-processor.flush:
			drain()
			close(ack)
		case <-processor.stop:
			drain()
			return
		}
	}
}

// ForceFlush sends everything queued, within ctx.
func (processor *boundedProcessor) ForceFlush(ctx context.Context) error {
	ack := make(chan struct{})
	select {
	case processor.flush <- ack:
	case <-processor.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-ack:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Shutdown sends what is queued and stops, within ctx; spans ending after it
// are not exported.
func (processor *boundedProcessor) Shutdown(ctx context.Context) error {
	processor.once.Do(func() {
		processor.stopped.Store(true)
		close(processor.stop)
	})
	select {
	case <-processor.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return processor.exporter.Shutdown(ctx)
}
