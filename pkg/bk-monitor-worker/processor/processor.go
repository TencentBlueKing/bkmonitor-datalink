// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processor

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/broker"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	t "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/errors"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/timex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type RetryDelayFunc func(n int, e error, t *t.Task) time.Duration

type Processor struct {
	Broker broker.Broker
	Clock  timex.Clock

	Handler     Handler
	BaseCtxFn   func() context.Context
	QueueConfig map[string]int

	// orderedQueues is set only in strict-priority mode.
	OrderedQueues []string

	RetryDelayFunc RetryDelayFunc
	IsFailureFunc  func(error) bool

	ErrHandler ErrorHandler
	// sema is a counting semaphore to ensure the number of active workers
	// does not exceed the limit.
	Sema chan struct{}

	ShutdownTimeout time.Duration

	// done operate
	Done chan struct{}
	Once sync.Once

	// quit operate
	Quit chan struct{}

	// abort operate
	Abort chan struct{}
}

type ProcessorParams struct {
	Broker          broker.Broker
	BaseCtxFn       func() context.Context
	RetryDelayFunc  RetryDelayFunc
	IsFailureFunc   func(error) bool
	Concurrency     int
	Queues          map[string]int
	StrictPriority  bool
	ErrHandler      ErrorHandler
	ShutdownTimeout time.Duration
}

// NewProcessor constructs a new processor.
func NewProcessor(params ProcessorParams) *Processor {
	queues := normalizeQueues(params.Queues)
	orderedQueues := []string(nil)
	if params.StrictPriority {
		orderedQueues = sortByPriority(queues)
	}
	return &Processor{
		Broker:          params.Broker,
		BaseCtxFn:       params.BaseCtxFn,
		Clock:           timex.NewTimeClock(),
		QueueConfig:     queues,
		OrderedQueues:   orderedQueues,
		RetryDelayFunc:  params.RetryDelayFunc,
		IsFailureFunc:   params.IsFailureFunc,
		Sema:            make(chan struct{}, params.Concurrency),
		Done:            make(chan struct{}),
		Quit:            make(chan struct{}),
		Abort:           make(chan struct{}),
		ErrHandler:      params.ErrHandler,
		Handler:         HandlerFunc(func(ctx context.Context, t *t.Task) error { return fmt.Errorf("handler not set") }),
		ShutdownTimeout: params.ShutdownTimeout,
	}
}

// Stop Note: stops only the "processor" goroutine, does not stop workers.
// It's safe to call this method multiple times.
func (p *Processor) Stop() {
	p.Once.Do(func() {
		logger.Info("Processor shutting down...")
		// Unblock if processor is waiting for sema token.
		close(p.Quit)
		// Signal the processor goroutine to stop processing tasks
		// from the queue.
		p.Done <- struct{}{}
	})
}

// Shutdown shutdown processor
func (p *Processor) Shutdown() {
	p.Stop()

	time.AfterFunc(p.ShutdownTimeout, func() { close(p.Abort) })

	logger.Info("Waiting for all workers to finish...")
	// 直到所有都释放
	for i := 0; i < cap(p.Sema); i++ {
		p.Sema <- struct{}{}
	}
	logger.Info("All workers have finished")
}

func (p *Processor) Start(wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-p.Done:
				logger.Debug("Processor done")
				return
			default:
				p.Exec()
			}
		}
	}()
}

// Exec pop task from queue and process it
func (p *Processor) Exec() {
	select {
	case <-p.Quit:
		logger.Debugf("Processor quit")
		return
	case p.Sema <- struct{}{}: // acquire token
		qnames := p.Queues()
		msg, leaseExpirationTime, err := p.Broker.Dequeue(qnames...)
		logger.Debugf("Dequeue result: %v, %v, %v", msg, leaseExpirationTime, err)
		switch {
		case errors.Is(err, errors.ErrNoProcessableTask):
			// logger.Info("All queues are empty")
			time.Sleep(time.Second)
			<-p.Sema // release token
			return
		case err != nil:
			logger.Errorf("Could not dequeue task: %v", err)
			<-p.Sema // release token
			return
		}

		lease := common.NewLease(leaseExpirationTime)
		deadline := p.ComputeDeadline(msg)
		go func() {
			defer func() {
				<-p.Sema // release token
			}()

			ctx, cancel := t.AddTaskMetadata2Context(p.BaseCtxFn(), msg, deadline)
			defer func() {
				cancel()
			}()

			// check context before starting a worker goroutine.
			select {
			case <-ctx.Done():
				logger.Warnf("task context canceled for task id=%s, deadline=%s", msg.ID, deadline)
				// already canceled (e.g. deadline exceeded).
				p.HandleFailedMessage(ctx, lease, msg, ctx.Err())
				return
			default:
			}

			resCh := make(chan error, 1)
			beginTime := time.Now()
			go func() {
				// task run count
				metrics.RunTaskTotal(msg.Kind)
				task := t.NewTask(
					msg.Kind,
					msg.Payload,
				)
				resCh <- p.Perform(ctx, task)
			}()

			select {
			case <-p.Abort:
				// time is up, push the message back to queue and quit this worker goroutine.
				logger.Debugf("Quitting worker. task id=%s", msg.ID)
				p.Requeue(lease, msg)
				return
			case <-lease.Done():
				logger.Debugf("Lease expired for task id=%s", msg.ID)
				metrics.RunTaskFailureTotal(msg.Kind)
				cancel()
				p.HandleFailedMessage(ctx, lease, msg, errors.New("task lease expired"))
				return
			case <-ctx.Done():
				logger.Debugf("task context canceled for task id=%s", msg.ID)
				metrics.RunTaskFailureTotal(msg.Kind)
				p.HandleFailedMessage(ctx, lease, msg, ctx.Err())
				return
			case resErr := <-resCh:
				if resErr != nil {
					logger.Debugf("task error for task id=%s, error: %v", msg.ID, resErr)
					metrics.RunTaskFailureTotal(msg.Kind)
					p.HandleFailedMessage(ctx, lease, msg, resErr)
					return
				}
				metrics.RunTaskDurationSeconds(msg.Kind, beginTime)
				metrics.RunTaskSuccessTotal(msg.Kind)
				p.HandleSucceededMessage(lease, msg)
			}
		}()
	}
}

// Requeue enqueue for retry task
func (p *Processor) Requeue(l *common.Lease, msg *t.TaskMessage) {
	if !l.IsValid() {
		// If lease is not valid, do not write to redis; Let recoverer take care of it.
		return
	}
	ctx, _ := context.WithDeadline(context.Background(), l.Deadline())
	err := p.Broker.Requeue(ctx, msg)
	if err != nil {
		logger.Errorf("Could not push task id=%s back to queue: %v", msg.ID, err)
	} else {
		logger.Infof("Pushed task id=%s back to queue", msg.ID)
	}
}

// HandleSucceededMessage succeeded task handler
func (p *Processor) HandleSucceededMessage(l *common.Lease, msg *t.TaskMessage) {
	if msg.Retention > 0 {
		p.MarkAsComplete(l, msg)
	} else {
		p.MarkAsDone(l, msg)
	}
}

// MarkAsComplete make a complated flag for task
func (p *Processor) MarkAsComplete(l *common.Lease, msg *t.TaskMessage) {
	if !l.IsValid() {
		// If lease is not valid, do not write to redis; Let recoverer take care of it.
		return
	}
	ctx, _ := context.WithDeadline(context.Background(), l.Deadline())
	err := p.Broker.MarkAsComplete(ctx, msg)
	if err != nil {
		errMsg := fmt.Sprintf("Could not move task id=%s type=%q from %q to %q:  %+v",
			msg.ID, msg.Kind, common.ActiveKey(msg.Queue), common.CompletedKey(msg.Queue), err)
		logger.Warnf("mark task completed error, %s", errMsg)
	}
}

// MarkAsDone make a done flag for task
func (p *Processor) MarkAsDone(l *common.Lease, msg *t.TaskMessage) {
	if !l.IsValid() {
		// If lease is not valid, do not write to redis; Let recoverer take care of it.
		return
	}
	ctx, _ := context.WithDeadline(context.Background(), l.Deadline())
	err := p.Broker.Done(ctx, msg)
	if err != nil {
		errMsg := fmt.Sprintf(
			"Could not remove task id=%s type=%q from %q err: %+v",
			msg.ID, msg.Kind, common.ActiveKey(msg.Queue), err,
		)
		logger.Warnf("mark task done error, %s", errMsg)
	}
}

// HandleFailedMessage failed task handler
func (p *Processor) HandleFailedMessage(ctx context.Context, l *common.Lease, msg *t.TaskMessage, err error) {
	if p.ErrHandler != nil {
		p.ErrHandler.HandleError(ctx, t.NewTask(msg.Kind, msg.Payload), err)
	}
	if !p.IsFailureFunc(err) {
		// retry the task
		p.Retry(l, msg, err, false)
		return
	}
	skipRetryErr := errors.New("skip retry for the task")
	if msg.Retried >= msg.Retry || errors.Is(err, skipRetryErr) {
		logger.Warnf("Retry exhausted for task id=%s", msg.ID)
		p.Archive(l, msg, err)
	} else {
		logger.Warnf("Task failed and retry for task id=%s, error: %v", msg.ID, err)
		p.Retry(l, msg, err, true)
	}
}

// Retry retry task with a intervals
func (p *Processor) Retry(l *common.Lease, msg *t.TaskMessage, e error, isFailure bool) {
	ctx, _ := context.WithDeadline(context.Background(), l.Deadline())
	d := p.RetryDelayFunc(msg.Retried, e, t.NewTask(msg.Kind, msg.Payload))
	retryAt := time.Now().Add(d)
	err := p.Broker.Retry(ctx, msg, retryAt, e.Error(), isFailure)
	if err != nil {
		errMsg := fmt.Sprintf(
			"Could not move task id=%s from %q to %q",
			msg.ID, common.ActiveKey(msg.Queue), common.RetryKey(msg.Queue),
		)
		logger.Warnf("retry task error, %s", errMsg)
	}
}

// Archive archive the task
func (p *Processor) Archive(l *common.Lease, msg *t.TaskMessage, e error) {
	ctx, _ := context.WithDeadline(context.Background(), l.Deadline())
	err := p.Broker.Archive(ctx, msg, e.Error())
	if err != nil {
		errMsg := fmt.Sprintf(
			"Could not move task id=%s from %q to %q",
			msg.ID, common.ActiveKey(msg.Queue), common.ArchivedKey(msg.Queue),
		)
		logger.Warnf("archive task error, %s", errMsg)
	}
}

// queues returns a list of queues to query.
func (p *Processor) Queues() []string {
	// 如果仅有一个，则
	if len(p.QueueConfig) == 1 {
		for qname := range p.QueueConfig {
			return []string{qname}
		}
	}
	// 如果顺序队列不为空，则返回
	if p.OrderedQueues != nil {
		return p.OrderedQueues
	}
	var names []string
	for qname, priority := range p.QueueConfig {
		for i := 0; i < priority; i++ {
			names = append(names, qname)
		}
	}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	r.Shuffle(len(names), func(i, j int) { names[i], names[j] = names[j], names[i] })
	return uniq(names, len(p.QueueConfig))
}

// Perform exec a task handle
func (p *Processor) Perform(ctx context.Context, task *t.Task) (err error) {
	defer func() {
		if x := recover(); x != nil {
			logger.Errorf("recovering from panic. See the stack trace below for details:\n%s", string(debug.Stack()))
			_, file, line, ok := runtime.Caller(1) // skip the first frame (panic itself)
			if ok && strings.Contains(file, "runtime/") {
				// The panic came from the runtime, most likely due to incorrect
				// map/slice usage. The parent frame should have the real trigger.
				_, file, line, ok = runtime.Caller(2)
			}

			// Include the file and line number info in the error, if runtime.Caller returned ok.
			if ok {
				err = fmt.Errorf("panic [%s:%d]: %v", file, line, x)
			} else {
				err = fmt.Errorf("panic: %v", x)
			}
		}
	}()
	return p.Handler.ProcessTask(ctx, task)
}

// uniq dedupes elements and returns a slice of unique names of length l.
// Order of the output slice is based on the input list.
func uniq(names []string, l int) []string {
	var res []string
	seen := make(map[string]struct{})
	for _, s := range names {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			res = append(res, s)
		}
		if len(res) == l {
			break
		}
	}
	return res
}

// sortByPriority returns a list of sorted queue
func sortByPriority(qcfg map[string]int) []string {
	var queues []*queue
	for qname, n := range qcfg {
		queues = append(queues, &queue{qname, n})
	}
	sort.Sort(sort.Reverse(byPriority(queues)))
	var res []string
	for _, q := range queues {
		res = append(res, q.name)
	}
	return res
}

type queue struct {
	name     string
	priority int
}

type byPriority []*queue

func (x byPriority) Len() int { return len(x) }

func (x byPriority) Less(i, j int) bool { return x[i].priority < x[j].priority }

func (x byPriority) Swap(i, j int) { x[i], x[j] = x[j], x[i] }

// normalizeQueues get queues by their greatest common divisor
func normalizeQueues(queues map[string]int) map[string]int {
	var xs []int
	for _, x := range queues {
		xs = append(xs, x)
	}
	d := gcd(xs...)
	res := make(map[string]int)
	for q, x := range queues {
		res[q] = x / d
	}
	return res
}

// greatest common divisor
func gcd(xs ...int) int {
	fn := func(x, y int) int {
		for y > 0 {
			x, y = y, x%y
		}
		return x
	}
	res := xs[0]
	for i := 0; i < len(xs); i++ {
		res = fn(xs[i], res)
		if res == 1 {
			return 1
		}
	}
	return res
}

// computeDeadline returns the given task's deadline,
func (p *Processor) ComputeDeadline(msg *t.TaskMessage) time.Time {
	if msg.Timeout == 0 && msg.Deadline == 0 {
		logger.Errorf("internal error: both timeout and deadline are not set for the task message: %s", msg.ID)
		return p.Clock.Now().Add(common.DefaultTimeout)
	}
	if msg.Timeout != 0 && msg.Deadline != 0 {
		deadlineUnix := math.Min(float64(p.Clock.Now().Unix()+msg.Timeout), float64(msg.Deadline))
		return time.Unix(int64(deadlineUnix), 0)
	}
	if msg.Timeout != 0 {
		return p.Clock.Now().Add(time.Duration(msg.Timeout) * time.Second)
	}
	return time.Unix(msg.Deadline, 0)
}
