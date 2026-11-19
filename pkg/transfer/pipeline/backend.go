// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pipeline

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/monitor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// BackendWithCutterAdapter : 需要进行指标拆分的backend的适配器
type BackendWithCutterAdapter struct {
	define.Backend
	*define.ProcessorMonitor
	*ProcessorTimeObserver
	transformer etl.TransformFn
}

// NewBackendWithCutterAdapter
func NewBackendWithCutterAdapter(ctx context.Context, b define.Backend) define.Backend {
	pipe := config.PipelineConfigFromContext(ctx)
	shipper := config.ShipperConfigFromContext(ctx)
	option := utils.NewMapHelper(pipe.Option)
	transformer := etl.TransformAsIs
	if option.GetOrDefault(config.PipelineConfigOptAllowDynamicMetricsAsFloat, true).(bool) {
		transformer = etl.TransformNilFloat64
	}
	return &BackendWithCutterAdapter{
		Backend:               b,
		ProcessorMonitor:      NewBackendProcessorMonitor(pipe, shipper),
		ProcessorTimeObserver: NewProcessorTimeObserver(pipe),
		transformer:           transformer,
	}
}

// Push :
func (b *BackendWithCutterAdapter) Push(d define.Payload, killChan chan<- error) {
	var (
		record = new(define.ETLRecord)
		err    = d.To(record)
	)

	if err != nil {
		logging.Warnf("%v payload %v to record failed: %v", b, d, err)
		b.CounterFails.Add(1)
		return
	}

	metrics := record.Metrics
	// 指标拆分
	for key, value := range metrics {
		metricsValue, err := b.transformer(value)
		if err != nil {
			logging.Warnf("%+v conv %v type: %T to float64 failed in %+v because of error: %v", b, key, value, d, err)
			continue
		}
		record.Metrics = map[string]interface{}{
			define.MetricValueFieldName: metricsValue,
		}
		record.Dimensions[define.MetricKeyFieldName] = key

		p, err := define.DerivePayload(d, record)
		if err != nil {
			logging.Warnf("%v derivePayload payload %v failed: %v", b, d, err)
			continue
		}
		b.Backend.Push(p, killChan)
	}
}

// BulkHandler
type BulkHandler interface {
	SetManager(manager BulkManager)
	Handle(ctx context.Context, payload define.Payload, killChan chan<- error) (result interface{}, at time.Time, ok bool)
	Flush(ctx context.Context, results []interface{}) (int, error)
	SetETLRecordFields(f *define.ETLRecordFields)
	Close() error
}

// BulkManager
type BulkManager interface {
	define.Stringer
}

// Bulk defaults
var (
	BulkDefaultBufferSize           = 2000
	BulkDefaultFlushInterval        = 1 * time.Second
	BulkDefaultFlushRetries         = 3
	BulkDefaultConcurrency    int64 = 25
	BulkDefaultMaxConcurrency int64 = 10000
)

// BulkGlobalConcurrencySemaphore
var BulkGlobalConcurrencySemaphore = utils.NewWeightedSemaphore(BulkDefaultMaxConcurrency)

var BulkGlobalPushSemaphore = utils.NewWeightedSemaphore(BulkDefaultMaxConcurrency)

// BaseBulkHandler
type BaseBulkHandler struct {
	BulkManager
}

// String
func (h *BaseBulkHandler) String() string {
	if h.BulkManager != nil {
		return h.BulkManager.String()
	}
	return fmt.Sprintf("%T[%p]", h, h)
}

// SetManager
func (h *BaseBulkHandler) SetManager(manager BulkManager) {
	h.BulkManager = manager
}

// BulkBackendAdapter
type BulkBackendAdapter struct {
	*define.BaseBackend
	*define.ProcessorMonitor
	*ProcessorTimeObserver
	handler             BulkHandler
	concurrency         utils.Semaphore
	context             context.Context
	cancelFunc          context.CancelFunc
	waitGroup           sync.WaitGroup
	pushContext         context.Context
	pushCancelFunc      context.CancelFunc
	pushWaitGroup       sync.WaitGroup
	bufferUsageObserver prometheus.Observer
	flushTimeObserver   *monitor.TimeObserver
	pool                sync.Pool
	bufferSize          int
	flushInterval       time.Duration
	flushRetries        int
	pushOnce            sync.Once
	resultChan          chan interface{}
	buffer              []interface{}
	pushSem             utils.Semaphore
}

func getBufferSizeAndFlushInterval(ctx context.Context, name string) (int, time.Duration) {
	bufferSize := BulkDefaultBufferSize
	flushInterval := BulkDefaultFlushInterval
	mqConfig := config.MQConfigFromContext(ctx)
	if mqConfig == nil {
		return bufferSize, flushInterval
	}

	if mqConfig.BatchSize != 0 {
		bufferSize = mqConfig.BatchSize
	}
	if mqConfig.FlushInterval != "" {
		interval, err := time.ParseDuration(mqConfig.FlushInterval)
		if err == nil {
			flushInterval = interval
		}
	}

	logging.Debugf("backend:%s use bufferSize:%d and flushInterval:%s", name, bufferSize, flushInterval)
	return bufferSize, flushInterval
}

// NewBulkBackendDefaultAdapter
func NewBulkBackendDefaultAdapter(ctx context.Context, name string, handler BulkHandler, maxQps int) *BulkBackendAdapter {
	bufferSize, flushInterval := getBufferSizeAndFlushInterval(ctx, name)
	return NewBulkBackendAdapter(ctx, name, handler, bufferSize, flushInterval, BulkDefaultFlushRetries)
}

// NewBulkBackendAdapter
func NewBulkBackendAdapter(ctx context.Context, name string, handler BulkHandler, bufferSize int, flushInterval time.Duration, flushRetries int) *BulkBackendAdapter {
	pipelineConfig := config.PipelineConfigFromContext(ctx)
	ctx, cancelFunc := context.WithCancel(ctx)

	concurrency := BulkDefaultConcurrency
	n := pipelineConfig.MQConfig.BulkConcurrency
	if n > 0 {
		concurrency = n
	}

	adapter := &BulkBackendAdapter{
		bufferUsageObserver: MonitorBulkBackendBufferUsage.With(prometheus.Labels{
			"name":    name,
			"id":      strconv.Itoa(pipelineConfig.DataID),
			"cluster": define.ConfClusterID,
		}),
		flushTimeObserver: monitor.NewTimeObserver(MonitorBulkBackendSendDuration.With(prometheus.Labels{
			"name":    name,
			"id":      strconv.Itoa(pipelineConfig.DataID),
			"cluster": define.ConfClusterID,
		})),
		concurrency: utils.NewChainingSemaphore(
			BulkGlobalConcurrencySemaphore, utils.NewWeightedSemaphore(concurrency),
		),
		BaseBackend:           define.NewBaseBackend(name),
		ProcessorMonitor:      NewBackendProcessorMonitor(config.PipelineConfigFromContext(ctx), config.ShipperConfigFromContext(ctx)),
		ProcessorTimeObserver: NewProcessorTimeObserver(config.PipelineConfigFromContext(ctx)),
		handler:               handler,
		context:               ctx,
		cancelFunc:            cancelFunc,
		pushContext:           ctx,
		pushCancelFunc:        cancelFunc,
		bufferSize:            bufferSize,
		flushInterval:         flushInterval,
		flushRetries:          flushRetries,
		resultChan:            make(chan interface{}, define.CoreNum()),
		pool:                  sync.Pool{New: func() interface{} { return make([]interface{}, 0, bufferSize) }},
		buffer:                make([]interface{}, 0, bufferSize),
		pushSem: utils.NewChainingSemaphore(
			BulkGlobalPushSemaphore, utils.NewWeightedSemaphore(concurrency),
		),
	}
	handler.SetManager(adapter)
	return adapter
}

func (b *BulkBackendAdapter) isEmpty() bool {
	return len(b.buffer) == 0
}

func (b *BulkBackendAdapter) isFull() bool {
	return len(b.buffer) == cap(b.buffer)
}

func (b *BulkBackendAdapter) add(result interface{}) {
	b.buffer = append(b.buffer, result)
	if b.isFull() {
		b.flush()
	}
}

func (b *BulkBackendAdapter) SetETLRecordFields(f *define.ETLRecordFields) {
	if b.handler != nil {
		b.handler.SetETLRecordFields(f)
	}
}

func (b *BulkBackendAdapter) flushWithRetries(buffer []interface{}) int {
	ctx := b.context
	flushRetries := b.flushRetries
	interval := b.flushInterval / time.Duration(flushRetries)
	for i := 0; i <= flushRetries; i++ {
		n, err := b.handler.Flush(ctx, buffer)
		if err == nil {
			logging.Debugf("backend %v flushed %d results", b, n)
			return n
		}

		if i < flushRetries {
			logging.Errorf("backend %v retry after %v because of error %v", b, interval, err)
			_, done := utils.TimeoutOrContextDone(ctx, time.After(interval))
			if done {
				logging.Warnf("backend %v abort because of context done", b)
				break
			}
		} else {
			logging.Errorf("backend %v flush %d results error %v", b, n, err)
		}
	}

	return 0
}

func (b *BulkBackendAdapter) flush() {
	if b.isEmpty() {
		return
	}

	buffer := b.buffer
	b.buffer = b.pool.Get().([]interface{})

	err := b.concurrency.Acquire(b.context, 1)
	if err != nil {
		logging.Warnf("%v abort flush because context has done", b)
		return
	}

	b.waitGroup.Add(1)
	go func(buffer []interface{}) {
		size := float64(len(buffer))
		defer func() {
			b.pool.Put(buffer[:0])
			b.waitGroup.Done()
			b.concurrency.Release(1)
		}()
		defer utils.RecoverError(func(e error) {
			logging.Errorf("backend %v flush %.0f results panic %+v", b, size, e)
		})
		observerRecord := b.flushTimeObserver.Start()
		n := b.flushWithRetries(buffer)
		observerRecord.Finish()
		flushed := float64(n)
		if flushed > size {
			flushed = size
		}
		b.CounterSuccesses.Add(flushed)
		b.CounterFails.Add(size - flushed)
		b.bufferUsageObserver.Observe(size / float64(b.bufferSize))
	}(buffer)
}

func (b *BulkBackendAdapter) cleanUp() {
	for result := range b.resultChan {
		b.add(result)
	}
	b.flush()
}

func (b *BulkBackendAdapter) run(ctx context.Context) {
	b.waitGroup.Add(1)
	go func() {
		defer utils.RecoverError(func(e error) {
			logging.Errorf("push %v backend error %+v", b, e)
		})
		defer b.waitGroup.Done()
		logging.Infof("backend %v running", b)

		ticker := time.NewTicker(b.flushInterval)
	loop:
		for {
			select {
			case result, ok := <-b.resultChan:
				if !ok {
					break loop
				}
				b.add(result)
			case <-ticker.C:
				b.flush()
			case <-ctx.Done():
				break loop
			}
		}
		logging.Infof("backend %v stopping", b)
		b.cleanUp()
		ticker.Stop()
		logging.Infof("backend %v cleaned", b)
	}()
}

// Close : close backend, should call Wait() function to wait
func (b *BulkBackendAdapter) Close() error {
	logging.Infof("backend %v closing", b)

	// 停止 push 数据
	b.pushCancelFunc()
	// 等待所有 push goroutine 退出
	b.pushWaitGroup.Wait()
	// 停止输入 保证不会有 send closed channel 情况发生
	close(b.resultChan)
	// 停止协程
	b.waitGroup.Wait()
	// 停止上下文
	b.cancelFunc()

	return b.handler.Close()
}

// Push : can not call Push after called Close()
func (b *BulkBackendAdapter) Push(d define.Payload, killChan chan<- error) {
	b.pushOnce.Do(func() {
		ctx, cancel := context.WithCancel(b.context)
		b.pushContext = ctx
		b.pushCancelFunc = cancel
		b.run(ctx)
	})

	// pushCancelFunc 之后不再新起 goroutine
	select {
	case <-b.pushContext.Done():
		return
	default:
	}

	if err := b.pushSem.Acquire(b.context, 1); err != nil {
		logging.Errorf("backend %v failed to acquire semaphore, err: %v", b, err)
		return
	}

	b.pushWaitGroup.Add(1)
	go func() {
		defer b.pushSem.Release(1)
		defer b.pushWaitGroup.Done()
		result, at, ok := b.handler.Handle(b.context, d, killChan)
		if !ok {
			b.CounterFails.Inc()
			return
		}

		t := d.GetTime()
		b.ObserveRecvDelta(t.Sub(at).Seconds())
		b.ObserveProcessElapsed(time.Since(t).Seconds())

		select {
		case b.resultChan <- result:
			logging.Debugf("backend %v pushed payload %v to buffer", b, d)
		case <-b.pushContext.Done():
			return
		}
	}()
}
