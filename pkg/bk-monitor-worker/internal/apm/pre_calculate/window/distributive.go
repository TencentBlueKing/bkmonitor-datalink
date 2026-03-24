// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package window

import (
	"context"
	"runtime"
	"strconv"
	"sync"
	"time"

	xxhash "github.com/cespare/xxhash/v2"
	"go.uber.org/zap"
	"golang.org/x/sync/semaphore"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/runtimex"
	monitorLogger "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// DistributiveWindowOptions all configs
type DistributiveWindowOptions struct {
	subWindowSize               int
	watchExpiredInterval        time.Duration
	concurrentProcessCount      int
	concurrentExpirationMaximum int
	mappingMaxSpanCount         int
}

type DistributiveWindowOption func(*DistributiveWindowOptions)

// DistributiveWindowSubSize The number of sub-windows, each of which maintains its own data.
func DistributiveWindowSubSize(maxSize int) DistributiveWindowOption {
	return func(options *DistributiveWindowOptions) {
		options.subWindowSize = maxSize
	}
}

// DistributiveWindowWatchExpiredInterval unit: ms. The duration of check expiration trace in window.
// If value is too small, the concurrent performance may be affected
func DistributiveWindowWatchExpiredInterval(interval time.Duration) DistributiveWindowOption {
	return func(options *DistributiveWindowOptions) {
		options.watchExpiredInterval = interval
	}
}

// DistributiveWindowConcurrentProcessCount The maximum concurrency.
// For example, concurrentProcessCount is set to 10 and subWindowSize is set to 5,
// then each sub-window can have a maximum of 10 traces running at the same time,
// and a total of 5 * 10 can be processed at the same time.
func DistributiveWindowConcurrentProcessCount(c int) DistributiveWindowOption {
	return func(options *DistributiveWindowOptions) {
		if c <= 0 {
			options.concurrentProcessCount = runtime.NumCPU() * 2
		} else {
			options.concurrentProcessCount = c
		}
	}
}

// DistributiveWindowConcurrentExpirationMaximum Maximum number of concurrent expirations
func DistributiveWindowConcurrentExpirationMaximum(c int) DistributiveWindowOption {
	return func(options *DistributiveWindowOptions) {
		options.concurrentExpirationMaximum = c
	}
}

// DistributiveWindowMappingMaxSpanCount maximum number of span in sub-window
func DistributiveWindowMappingMaxSpanCount(c int) DistributiveWindowOption {
	return func(options *DistributiveWindowOptions) {
		options.mappingMaxSpanCount = c
	}
}

// DistributiveWindow Parent-child window implementation classes.
// where each independent child window maintains its own data, when a span is to be added to the parent window,
// it is added to a child window through hash. Therefore, the child window is the downstream node of the parent window.
// At the same time, the parent window also acts as an observer
// and periodically traverses the child window to check whether there is an expired trace.
// Parent-Window(observer) -----> Child-Window(trigger)
//
//	                                 ^
//	                Via Hash         |
//	Span   ---------------------------
type DistributiveWindow struct {
	dataId string
	config DistributiveWindowOptions

	subWindows      map[int]*distributiveSubWindow
	observers       map[observer]struct{}
	saveRequestChan chan<- storage.SaveRequest

	ctx    context.Context
	logger monitorLogger.Logger
}

func NewDistributiveWindow(dataId string, ctx context.Context, processor Processor, saveReqChan chan<- storage.SaveRequest, specificOptions ...DistributiveWindowOption) Operator {
	specificConfig := &DistributiveWindowOptions{}
	for _, setter := range specificOptions {
		setter(specificConfig)
	}

	window := &DistributiveWindow{
		dataId:          dataId,
		config:          *specificConfig,
		observers:       make(map[observer]struct{}, specificConfig.subWindowSize),
		ctx:             ctx,
		saveRequestChan: saveReqChan,
		logger: monitorLogger.With(
			zap.String("location", "window"),
			zap.String("dataId", dataId),
		),
	}

	// Register sub-windows Event
	subWindowMapping := make(map[int]*distributiveSubWindow, specificConfig.subWindowSize)
	for i := 0; i < specificConfig.subWindowSize; i++ {
		w := newDistributiveSubWindow(
			dataId, ctx, i, processor, window.saveRequestChan,
			specificConfig.concurrentExpirationMaximum, specificConfig.mappingMaxSpanCount,
		)
		subWindowMapping[i] = w
		window.register(w)
	}

	window.logger.Infof("create %d sub-windows", len(subWindowMapping))
	window.subWindows = subWindowMapping
	return window
}

const maxInt = int(^uint(0) >> 1)

func (w *DistributiveWindow) locate(uni string) *distributiveSubWindow {
	// Based on the maximum value of int,
	// avoid uint > maximum value of int to calculate the negative number of subscript.
	hashValue := int(xxhash.Sum64([]byte(uni)) & uint64(maxInt))
	return w.subWindows[hashValue%w.config.subWindowSize]
}

func (w *DistributiveWindow) Start(spanChan <-chan []StandardSpan, errorReceiveChan chan<- error, runtimeOpts ...RuntimeConfigOption) {
	for ob := range w.observers {
		ob.assembleRuntimeConfig(runtimeOpts...)
		for i := 0; i < w.config.concurrentProcessCount; i++ {
			go ob.handleNotify(errorReceiveChan)
		}
	}

	go w.startWatch(errorReceiveChan)
	w.logger.Infof(
		"DataId: %s created with %d sub-window, %d concurrentProcessCount",
		w.dataId, len(w.observers), w.config.concurrentProcessCount,
	)

	go w.Handle(spanChan, errorReceiveChan)
}

func (w *DistributiveWindow) GetWindowsLength() int {
	res := 0

	for _, subWindow := range w.subWindows {
		res += len(subWindow.eventChan)
	}

	return res
}

func (w *DistributiveWindow) RecordTraceAndSpanCountMetric() {
	for subId := range w.subWindows {
		traceC, spanC := w.getSubWindowMetrics(subId)
		metrics.RecordApmPreCalcWindowTraceTotal(w.dataId, subId, traceC)
		metrics.RecordApmPreCalcWindowSpanTotal(w.dataId, subId, spanC)
	}
}

func (w *DistributiveWindow) getSubWindowMetrics(subId int) (int, int) {
	subWindow := w.subWindows[subId]

	traceCount := 0
	spanCount := 0

	if subWindow == nil {
		return traceCount, spanCount
	}
	subWindow.m.Range(func(key, value any) bool {
		traceCount++
		v := value.(CollectTrace)
		spanCount += v.Graph.Length()
		return true
	})

	return traceCount, spanCount
}

func (w *DistributiveWindow) Handle(spanChan <-chan []StandardSpan, errorReceiveChan chan<- error) {
	defer runtimex.HandleCrashToChan(errorReceiveChan)

	w.logger.Infof("DistributiveWindow handle started.")
loop:
	for {
		select {
		case m := <-spanChan:
			start := time.Now()
			for _, span := range m {
				subWindow := w.locate(span.TraceId)
				subWindow.add(span)
			}
			metrics.RecordApmPreCalcLocateSpanDuration(w.dataId, start)
		case <-w.ctx.Done():
			w.logger.Infof("Handle span stopped.")
			// clear data
			for _, subWindow := range w.subWindows {
				subWindow.m = &sync.Map{}
				close(subWindow.eventChan)
				subWindow.processor = Processor{}
			}
			w.subWindows = make(map[int]*distributiveSubWindow)

			break loop
		}
	}
}

func (w *DistributiveWindow) register(o observer) {
	w.observers[o] = struct{}{}
}

func (w *DistributiveWindow) deRegister(o observer) {
	delete(w.observers, o)
}

func (w *DistributiveWindow) startWatch(errorReceiveChan chan<- error) {
	defer runtimex.HandleCrashToChan(errorReceiveChan)

	// todo addMetrics: 监听器方法耗时
	tick := time.NewTicker(w.config.watchExpiredInterval)
	w.logger.Infof("DistributiveWindow watching started. interval: %dms", w.config.watchExpiredInterval.Milliseconds())
	for {
		select {
		case <-w.ctx.Done():
			tick.Stop()
			w.logger.Info("trigger watch stopped.")
			return
		case <-tick.C:
			for ob := range w.observers {
				ob.detectNotify()
			}
		}
	}
}

type distributiveSubWindow struct {
	id              int
	dataId          string
	runtimeStrategy ConfigBaseRuntimeStrategies

	eventChan            chan Event
	processor            Processor
	writeSaveRequestChan chan<- storage.SaveRequest

	m     *sync.Map
	mLock sync.Mutex

	ctx    context.Context
	logger monitorLogger.Logger

	sem *semaphore.Weighted
}

func newDistributiveSubWindow(
	dataId string, ctx context.Context, index int, processor Processor, saveReqChan chan<- storage.SaveRequest,
	concurrentMaximum int, mappingMaxSpanCount int,
) *distributiveSubWindow {
	subWindow := &distributiveSubWindow{
		id:                   index,
		dataId:               dataId,
		eventChan:            make(chan Event, concurrentMaximum),
		writeSaveRequestChan: saveReqChan,
		processor:            processor,
		m:                    &sync.Map{},
		ctx:                  ctx,
		logger: monitorLogger.With(
			zap.String("location", "window"),
			zap.String("dataId", dataId),
			zap.String("sub-window-id", strconv.Itoa(index)),
		),
		sem: semaphore.NewWeighted(int64(mappingMaxSpanCount)),
	}
	subWindow.logger.Infof(
		"DataId: %s Create SubWindow[%d] -> eventChanSize: %d semaphoreSize: %d",
		dataId, index, concurrentMaximum, mappingMaxSpanCount,
	)
	return subWindow
}

func (d *distributiveSubWindow) assembleRuntimeConfig(runtimeOpt ...RuntimeConfigOption) {
	config := RuntimeConfig{}
	for _, setter := range runtimeOpt {
		setter(&config)
	}
	d.runtimeStrategy = *NewRuntimeStrategies(
		config,
		[]ReentrantRuntimeStrategy{ReentrantLogRecord, ReentrantLimitMaxCount, RefreshUpdateTime},
		[]ReentrantRuntimeStrategy{PredicateLimitMaxDuration, PredicateNoDataDuration},
	)
}

func (d *distributiveSubWindow) detectNotify() {
	// todo 检查自己是否有过期的traceId
	expiredKeys := make([]string, 0)

	d.m.Range(func(key, value any) bool {
		v := value.(CollectTrace)
		if d.runtimeStrategy.predicate(v.Runtime, v) {
			expiredKeys = append(expiredKeys, key.(string))
		}
		return true
	})

	if len(expiredKeys) > 0 {
		metrics.RecordApmPreCalcExpiredKeyTotal(d.dataId, d.id, len(expiredKeys))
		for _, k := range expiredKeys {
			// 防止在执行前 addWindow 方法已经把值取了出来导致值还存放着上次窗口的数据
			d.mLock.Lock()
			v, exists := d.m.LoadAndDelete(k)
			d.mLock.Unlock()
			if !exists {
				d.logger.Errorf("An expired key[%s] was detected but does not exist in the mapping", k)
				continue
			}
			trace := v.(CollectTrace)
			// gc
			trace.Runtime = nil
			d.eventChan <- Event{CollectTrace: trace, ReleaseCount: int64(trace.Graph.Length())}
		}
	}
}

func (d *distributiveSubWindow) handleNotify(errorReceiveChan chan<- error) {
	defer runtimex.HandleCrashToChan(errorReceiveChan)
loop:
	for {
		select {
		case e := <-d.eventChan:
			start := time.Now()
			d.processor.PreProcess(d.writeSaveRequestChan, e)
			metrics.RecordApmPreCalcProcessEventDuration(d.dataId, d.id, start)
			d.sem.Release(e.ReleaseCount)
		case <-d.ctx.Done():
			break loop
		}
	}
}

func (d *distributiveSubWindow) add(span StandardSpan) {
	if err := d.sem.Acquire(d.ctx, 1); err != nil {
		d.logger.Errorf("DataId: %s subWindow[%d] acquire semphore failed, error: %s, skip span", d.dataId, d.id, err)
		return
	}

	d.mLock.Lock()
	value, exist := d.m.Load(span.TraceId)
	if !exist {
		graph := NewDiGraph()
		graph.AddNode(Node{StandardSpan: span})
		rt := d.runtimeStrategy.handleNew()
		d.m.Store(span.TraceId, CollectTrace{
			TraceId: span.TraceId,
			Graph:   graph,

			Runtime: rt,
		})
	} else {
		collect := value.(CollectTrace)
		graph := collect.Graph
		graph.AddNode(Node{StandardSpan: span})
		collect.Graph = graph

		d.runtimeStrategy.handleExist(collect.Runtime, collect)
		d.m.Store(span.TraceId, collect)
	}
	d.mLock.Unlock()
}
