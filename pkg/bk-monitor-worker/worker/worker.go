// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/broker"
	rdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/broker/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	t "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Worker represents  worker process
type Worker struct {
	// broker
	broker broker.Broker
	// waitgroup
	wg sync.WaitGroup
	// goroutines
	forwarder *processor.Forwarder
	processor *processor.Processor
}

// WorkerConfig config info
type WorkerConfig struct {
	Concurrency              int
	BaseContext              func() context.Context
	RetryDelayFunc           processor.RetryDelayFunc
	IsFailure                func(error) bool
	Queues                   map[string]int
	StrictPriority           bool
	ErrorHandler             processor.ErrorHandler
	ShutdownTimeout          time.Duration
	HealthCheckFunc          func(error)
	HealthCheckInterval      time.Duration
	DelayedTaskCheckInterval time.Duration
}

// DefaultRetryDelayFunc default retry time
// NOTE: retry time from fab
func DefaultRetryDelayFunc(n int, e error, t *task.Task) time.Duration {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	s := int(math.Pow(float64(n), 4)) + 15 + (r.Intn(30) * (n + 1))
	return time.Duration(s) * time.Second
}

// NewWorker new a worker
func NewWorker(cfg WorkerConfig) (*Worker, error) {
	baseCtxFn := cfg.BaseContext
	if baseCtxFn == nil {
		baseCtxFn = context.Background
	}

	n := cfg.Concurrency
	if n < 1 {
		n = runtime.NumCPU()
	}

	delayFunc := cfg.RetryDelayFunc
	if delayFunc == nil {
		delayFunc = DefaultRetryDelayFunc
	}

	isFailureFunc := cfg.IsFailure
	if isFailureFunc == nil {
		isFailureFunc = func(err error) bool { return err != nil }
	}
	// 组装队列
	queues := make(map[string]int)
	for qname, p := range cfg.Queues {
		if err := common.ValidateQueueName(qname); err != nil {
			continue
		}
		if p > 0 {
			queues[qname] = p
		}
	}
	if len(queues) == 0 {
		queues = map[string]int{common.DefaultQueueName: 1}
	}
	var qnames []string
	for q := range queues {
		qnames = append(qnames, q)
	}
	// 等待时间
	shutdownTimeout := cfg.ShutdownTimeout
	if shutdownTimeout == 0 {
		shutdownTimeout = common.DefaultShutdownTimeout
	}
	// TODO: health check
	healthcheckInterval := cfg.HealthCheckInterval
	if healthcheckInterval == 0 {
		healthcheckInterval = common.DefaultHealthCheckInterval
	}

	rdb := rdb.GetRDB()

	delayedTaskCheckInterval := cfg.DelayedTaskCheckInterval
	if delayedTaskCheckInterval == 0 {
		delayedTaskCheckInterval = common.DefaultDelayedTaskCheckInterval
	}

	forwarder := processor.NewForwarder(processor.ForwarderParams{
		Broker:   rdb,
		Queues:   qnames,
		Interval: delayedTaskCheckInterval,
	})
	processor := processor.NewProcessor(processor.ProcessorParams{
		Broker:          rdb,
		RetryDelayFunc:  delayFunc,
		BaseCtxFn:       baseCtxFn,
		IsFailureFunc:   isFailureFunc,
		Concurrency:     n,
		Queues:          queues,
		StrictPriority:  cfg.StrictPriority,
		ErrHandler:      cfg.ErrorHandler,
		ShutdownTimeout: shutdownTimeout,
	})
	return &Worker{
		broker:    rdb,
		forwarder: forwarder,
		processor: processor,
	}, nil
}

// wait signal to shutdown
func (w *Worker) waitForSignals() {
	logger.Info("Send signal to stop processing new tasks")

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGTSTP)
	for {
		sig := <-sigs
		// 走停止流程
		if sig == syscall.SIGTSTP {
			w.Stop()
			continue
		}
		break
	}
}

// Run run task
func (w *Worker) Run(handler processor.Handler) error {
	if err := w.Start(handler); err != nil {
		return err
	}
	return nil
}

// Start
func (w *Worker) Start(handler processor.Handler) error {
	if handler == nil {
		return fmt.Errorf("server cannot run with nil handler")
	}
	w.processor.Handler = handler

	logger.Info("Starting processing")
	w.forwarder.Start(&w.wg)
	w.processor.Start(&w.wg)
	return nil
}

// Shutdown shutdow a work
func (w *Worker) Shutdown() {
	logger.Info("Starting graceful shutdown")
	w.forwarder.Shutdown()
	w.processor.Shutdown()
	w.wg.Wait()

	w.broker.Close()
	logger.Info("Exiting")
}

// Stop
func (w *Worker) Stop() {
	logger.Info("Stopping processor")
	w.processor.Stop()
	logger.Info("Processor stopped")
}

// WorkerMux 类似 net/httpHandler
type WorkerMux struct {
	mu sync.RWMutex
	m  map[string]muxEntry
}

// 任务 handle 的匹配
type muxEntry struct {
	h       processor.Handler
	pattern string
}

// NewServeMux allocates and returns a new ServeMux.
func NewServeMux() *WorkerMux {
	return new(WorkerMux)
}

// ProcessTask
func (mux *WorkerMux) ProcessTask(ctx context.Context, task *t.Task) error {
	h, _ := mux.Handler(task)
	return h.ProcessTask(ctx, task)
}

// Handler returns the registed task handler
func (mux *WorkerMux) Handler(t *t.Task) (h processor.Handler, pattern string) {
	mux.mu.RLock()
	defer mux.mu.RUnlock()

	h, pattern = mux.match(t.Kind)
	if h == nil {
		h, pattern = NotFoundHandler(), ""
	}
	return h, pattern
}

// Find a handler on a handler map given a kind string.
func (mux *WorkerMux) match(kind string) (h processor.Handler, pattern string) {
	// match
	v, ok := mux.m[kind]
	if ok {
		return v.h, v.pattern
	}

	return nil, ""
}

// Handle registers the handler for the given pattern.
// If a handler already exists for pattern, Handle panics.
func (mux *WorkerMux) Handle(pattern string, handler processor.Handler) {
	mux.mu.Lock()
	defer mux.mu.Unlock()

	if strings.TrimSpace(pattern) == "" {
		panic("invalid pattern")
	}
	if handler == nil {
		panic("nil handler")
	}
	// allow not same
	if _, exist := mux.m[pattern]; exist {
		panic("multiple registrations for " + pattern)
	}

	if mux.m == nil {
		mux.m = make(map[string]muxEntry)
	}
	e := muxEntry{h: handler, pattern: pattern}
	mux.m[pattern] = e
}

// HandleFunc registers the handler function for the given pattern.
func (mux *WorkerMux) HandleFunc(pattern string, handler func(context.Context, *t.Task) error) {
	// check handler
	if handler == nil {
		panic("not found handler")
	}
	mux.Handle(pattern, processor.HandlerFunc(handler))
}

// NotFound not found task returns
func NotFound(ctx context.Context, task *t.Task) error {
	return fmt.Errorf("handler not found for task %q", task.Kind)
}

// NotFoundHandler returns a not found task handler
func NotFoundHandler() processor.Handler { return processor.HandlerFunc(NotFound) }
