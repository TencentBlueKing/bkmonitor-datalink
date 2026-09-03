// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consume

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

// Runtime 编排一个 Session 的拉取、并发处理、重试和确认生命周期。
// Runtime 实例只能调用一次 Run。
type Runtime struct {
	config   Config
	session  Session
	handler  Handler
	labels   RuntimeLabels
	observer Observer
	observed bool
	running  atomic.Bool
}

// New 创建消息消费运行时。构造只保存依赖，完整配置校验在 Run 前完成。
func New(config Config, session Session, handler Handler, options ...RuntimeOption) *Runtime {
	runtime := &Runtime{
		config:   config,
		session:  session,
		handler:  handler,
		observer: noopObserver{},
	}
	for _, option := range options {
		if option != nil {
			option(runtime)
		}
	}
	return runtime
}

type receiveResult struct {
	deliveries []Delivery
	err        error
}

type workerJob struct {
	entry *trackedDelivery
}

type workerResult struct {
	entry   *trackedDelivery
	outcome Outcome
}

type settleResult struct {
	batch *settleBatch
	err   error
}

// Run 持续消费直到 ctx 取消、Session 拉取失败或关闭完成。正常取消会先在
// ShutdownDrainTimeout 内处理已接管消息并提交可安全确认的进度。
func (r *Runtime) Run(ctx context.Context) error {
	startedAt := time.Now()
	if ctx == nil {
		return fmt.Errorf("run message consumption runtime: context is nil")
	}
	if !r.running.CompareAndSwap(false, true) {
		return fmt.Errorf("run message consumption runtime: runtime can only run once")
	}
	if err := r.config.Validate(); err != nil {
		return fmt.Errorf("run message consumption runtime: validate config: %w", err)
	}
	if r.session == nil {
		return fmt.Errorf("run message consumption runtime: session is nil")
	}
	if r.handler == nil {
		return fmt.Errorf("run message consumption runtime: handler is nil")
	}
	if validator, ok := r.session.(RuntimeValidator); ok {
		if err := validator.ValidateRuntime(r.config); err != nil {
			return fmt.Errorf("run message consumption runtime: validate session: %w", err)
		}
	}
	capabilities := r.session.Capabilities()
	if capabilities.Settlement != SettlementIndividual && capabilities.Settlement != SettlementCumulative {
		return fmt.Errorf("run message consumption runtime: unsupported settlement mode: %d", capabilities.Settlement)
	}

	receiveCtx, cancelReceive := context.WithCancel(context.Background())
	workCtx, cancelWork := context.WithCancel(context.Background())
	defer cancelReceive()
	defer cancelWork()

	receiveRequests := make(chan ReceiveLimits, 1)
	receiveResults := make(chan receiveResult, 1)
	workerJobs := make(chan workerJob, r.config.MaxInflightMessages)
	workerResults := make(chan workerResult, r.config.MaxInflightMessages)
	settleRequests := make(chan *settleBatch, 1)
	settleResults := make(chan settleResult, 1)

	var background sync.WaitGroup
	background.Add(1)
	go r.receiveLoop(receiveCtx, receiveRequests, receiveResults, &background)
	for range r.config.WorkerCount {
		background.Add(1)
		go r.workerLoop(workCtx, workerJobs, workerResults, &background)
	}
	background.Add(1)
	go r.settleLoop(workCtx, settleRequests, settleResults, &background)

	state := runtimeState{
		runtime:         r,
		capabilities:    capabilities,
		receiveRequests: receiveRequests,
		workerJobs:      workerJobs,
		settleRequests:  settleRequests,
		lanes:           make(map[string]*laneState),
		activeOrderKeys: make(map[string]bool),
		waitingByKey:    make(map[string][]*trackedDelivery),
	}

	runErr := state.loop(ctx, receiveResults, workerResults, settleResults, cancelReceive, cancelWork)
	close(receiveRequests)
	close(workerJobs)
	close(settleRequests)
	cancelReceive()
	cancelWork()
	backgroundDone := make(chan struct{})
	go func() {
		background.Wait()
		close(backgroundDone)
	}()
	select {
	case <-backgroundDone:
	case <-time.After(r.config.SessionCloseTimeout):
		runErr = errors.Join(runErr, fmt.Errorf("stop message consumption workers: %w", context.DeadlineExceeded))
	}

	closeCtx, closeCancel := context.WithTimeout(context.Background(), r.config.SessionCloseTimeout)
	defer closeCancel()
	closeErr := r.session.Close(closeCtx)
	if closeErr != nil && !errors.Is(closeErr, ErrSessionClosed) {
		closeErr = fmt.Errorf("close message consumption session: %w", closeErr)
	} else {
		closeErr = nil
	}
	result := errors.Join(runErr, closeErr)
	r.observer.ShutdownFinished(
		context.Background(),
		result == nil,
		time.Since(startedAt),
		state.inflightMessages,
	)
	return result
}

func (r *Runtime) receiveLoop(
	ctx context.Context,
	requests <-chan ReceiveLimits,
	results chan<- receiveResult,
	waitGroup *sync.WaitGroup,
) {
	defer waitGroup.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case limits, ok := <-requests:
			if !ok {
				return
			}
			deliveries, err := r.session.Receive(ctx, limits)
			select {
			case results <- receiveResult{deliveries: deliveries, err: err}:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (r *Runtime) workerLoop(
	ctx context.Context,
	jobs <-chan workerJob,
	results chan<- workerResult,
	waitGroup *sync.WaitGroup,
) {
	defer waitGroup.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-jobs:
			if !ok {
				return
			}
			outcome := r.handle(ctx, job.entry.delivery.Message)
			select {
			case results <- workerResult{entry: job.entry, outcome: outcome}:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (r *Runtime) handle(parent context.Context, message Message) (outcome Outcome) {
	ctx, cancel := context.WithTimeout(parent, r.config.ProcessTimeout)
	defer cancel()
	startedAt := time.Now()
	r.observer.HandlerStarted(ctx, message)
	defer func() {
		if recovered := recover(); recovered != nil {
			outcome = Block(fmt.Errorf("handler panic: %v\n%s", recovered, debug.Stack()))
		}
		r.observer.HandlerFinished(ctx, outcome.Kind, time.Since(startedAt))
	}()

	outcome = r.handler.Handle(ctx, message)
	if err := outcome.validate(); err != nil {
		return Block(fmt.Errorf("handler returned invalid outcome: %w", err))
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) &&
		(outcome.Kind == OutcomeComplete || outcome.Kind == OutcomeDiscard) {
		return Retry(context.DeadlineExceeded, 0)
	}
	return outcome
}

func (r *Runtime) settleLoop(
	ctx context.Context,
	requests <-chan *settleBatch,
	results chan<- settleResult,
	waitGroup *sync.WaitGroup,
) {
	defer waitGroup.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case batch, ok := <-requests:
			if !ok {
				return
			}
			err := r.session.Confirm(ctx, batch.receipts)
			select {
			case results <- settleResult{batch: batch, err: err}:
			case <-ctx.Done():
				return
			}
		}
	}
}

type runtimeState struct {
	runtime      *Runtime
	capabilities Capabilities

	receiveRequests chan<- ReceiveLimits
	workerJobs      chan<- workerJob
	settleRequests  chan<- *settleBatch

	lanes           map[string]*laneState
	activeOrderKeys map[string]bool
	waitingByKey    map[string][]*trackedDelivery
	retries         retryQueue
	retrySeq        uint64
	settleQueue     []*settleBatch

	receiving        bool
	settling         bool
	shuttingDown     bool
	workCount        int
	inflightMessages int
	inflightBytes    int
	receiveNotBefore time.Time
	shutdownDeadline time.Time
	fatalErr         error
}

func (s *runtimeState) loop(
	ctx context.Context,
	receiveResults <-chan receiveResult,
	workerResults <-chan workerResult,
	settleResults <-chan settleResult,
	cancelReceive context.CancelFunc,
	cancelWork context.CancelFunc,
) error {
	ctxDone := ctx.Done()
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	for {
		now := time.Now()
		s.dispatchReadyRetries(now)
		s.tryReceive(now)
		s.trySettle(now)
		s.runtime.observer.Snapshot(ctx, s.snapshot(now))

		if s.shuttingDown && s.workCount == 0 && !s.settling && len(s.settleQueue) == 0 {
			return s.fatalErr
		}
		if s.shuttingDown && !now.Before(s.shutdownDeadline) {
			cancelWork()
			return errors.Join(s.fatalErr, fmt.Errorf("drain message consumption runtime: %w", context.DeadlineExceeded))
		}

		wake := s.nextWake(now)
		var timerC <-chan time.Time
		if !wake.IsZero() {
			delay := time.Until(wake)
			if delay < 0 {
				delay = 0
			}
			timer.Reset(delay)
			timerC = timer.C
		}

		select {
		case <-ctxDone:
			if !s.shuttingDown {
				s.startShutdown(cancelReceive, time.Now())
			}
			ctxDone = nil
		case result := <-receiveResults:
			s.receiving = false
			s.handleReceiveResult(result, cancelReceive)
		case result := <-workerResults:
			s.handleWorkerResult(result, time.Now(), cancelReceive)
		case result := <-settleResults:
			s.handleSettleResult(result, time.Now())
		case <-timerC:
		}
		if timerC != nil && !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}
}

func (s *runtimeState) tryReceive(now time.Time) {
	if s.shuttingDown || s.receiving || now.Before(s.receiveNotBefore) {
		return
	}
	messageCapacity := s.runtime.config.MaxInflightMessages - s.inflightMessages
	byteCapacity := s.runtime.config.MaxInflightBytes - s.inflightBytes
	if messageCapacity <= 0 || byteCapacity <= 0 {
		return
	}

	maxLaneInflight := 0
	for _, lane := range s.lanes {
		if lane.inflight > maxLaneInflight {
			maxLaneInflight = lane.inflight
		}
	}
	laneCapacity := s.runtime.config.MaxInflightPerLane - maxLaneInflight
	if laneCapacity <= 0 {
		return
	}

	limits := ReceiveLimits{
		MaxMessages: min(s.runtime.config.MaxBatchMessages, messageCapacity, laneCapacity),
		MaxBytes:    min(s.runtime.config.MaxBatchBytes, byteCapacity),
	}
	select {
	case s.receiveRequests <- limits:
		s.receiving = true
	default:
	}
}

func (s *runtimeState) handleReceiveResult(result receiveResult, cancelReceive context.CancelFunc) {
	if result.err != nil {
		if s.shuttingDown && errors.Is(result.err, context.Canceled) {
			return
		}
		s.fatalErr = fmt.Errorf("receive messages: %w", result.err)
		s.startShutdown(cancelReceive, time.Now())
		return
	}
	if len(result.deliveries) == 0 {
		s.receiveNotBefore = time.Now().Add(s.runtime.config.EmptyReceiveBackoff)
		return
	}

	limitsError := s.validateReceived(result.deliveries)
	if limitsError != nil {
		s.fatalErr = limitsError
		s.startShutdown(cancelReceive, time.Now())
		return
	}
	for index := range result.deliveries {
		delivery := cloneDelivery(result.deliveries[index])
		s.runtime.observer.DeliveryReceived(context.Background(), DeliveryObservation{
			Lane: delivery.Meta.Lane, Bytes: len(delivery.Message.Body), Redelivered: delivery.Meta.Redeliver,
		})
		entry := &trackedDelivery{
			delivery: delivery,
			attempt:  max(1, delivery.Meta.Attempt),
			firstAt:  time.Now(),
		}
		lane := s.lane(delivery.Meta.Lane)
		lane.inflight++
		if s.capabilities.Settlement == SettlementCumulative {
			lane.queue = append(lane.queue, entry)
		}
		s.inflightMessages++
		s.inflightBytes += len(delivery.Message.Body)
		if lane.blocked {
			continue
		}
		s.dispatch(entry)
	}
}

func (s *runtimeState) validateReceived(deliveries []Delivery) error {
	if len(deliveries) > s.runtime.config.MaxBatchMessages ||
		len(deliveries) > s.runtime.config.MaxInflightMessages-s.inflightMessages {
		return fmt.Errorf("%w: received %d messages", ErrReceiveLimitExceeded, len(deliveries))
	}
	batchBytes := 0
	perLane := make(map[string]int)
	for index, delivery := range deliveries {
		if !delivery.Receipt.Valid() {
			return fmt.Errorf("%w: delivery[%d] has invalid receipt", ErrInvalidDelivery, index)
		}
		if delivery.Message.ID == "" {
			return fmt.Errorf("%w: delivery[%d] has empty message id", ErrInvalidDelivery, index)
		}
		if delivery.Message.TenantID == "" {
			return fmt.Errorf("%w: delivery[%d] has empty tenant id", ErrInvalidDelivery, index)
		}
		if delivery.Meta.Lane == "" {
			return fmt.Errorf("%w: delivery[%d] has empty lane", ErrInvalidDelivery, index)
		}
		batchBytes += len(delivery.Message.Body)
		perLane[delivery.Meta.Lane]++
	}
	if batchBytes > s.runtime.config.MaxBatchBytes ||
		batchBytes > s.runtime.config.MaxInflightBytes-s.inflightBytes {
		return fmt.Errorf("%w: received %d payload bytes", ErrReceiveLimitExceeded, batchBytes)
	}
	for laneName, count := range perLane {
		if count+s.lane(laneName).inflight > s.runtime.config.MaxInflightPerLane {
			return fmt.Errorf("%w: lane %q received %d messages", ErrReceiveLimitExceeded, laneName, count)
		}
	}
	return nil
}

func cloneDelivery(delivery Delivery) Delivery {
	delivery.Message.Body = append([]byte(nil), delivery.Message.Body...)
	if delivery.Message.Headers != nil {
		headers := make(map[string][]byte, len(delivery.Message.Headers))
		for key, value := range delivery.Message.Headers {
			headers[key] = append([]byte(nil), value...)
		}
		delivery.Message.Headers = headers
	}
	return delivery
}

func (s *runtimeState) dispatch(entry *trackedDelivery) {
	key := entry.delivery.Message.OrderKey
	if key != "" && s.activeOrderKeys[key] {
		s.waitingByKey[key] = append(s.waitingByKey[key], entry)
		return
	}
	if key != "" {
		s.activeOrderKeys[key] = true
	}
	s.submit(entry)
}

func (s *runtimeState) submit(entry *trackedDelivery) {
	entry.processing = true
	s.workCount++
	s.workerJobs <- workerJob{entry: entry}
}

func (s *runtimeState) handleWorkerResult(
	result workerResult,
	now time.Time,
	cancelReceive context.CancelFunc,
) {
	entry := result.entry
	if !entry.processing {
		return
	}
	entry.processing = false
	s.workCount--

	outcome := result.outcome
	if err := outcome.validate(); err != nil {
		outcome = Block(fmt.Errorf("handler returned invalid outcome: %w", err))
	}
	switch outcome.Kind {
	case OutcomeComplete, OutcomeDiscard:
		entry.terminal = true
		s.releaseOrderKey(entry)
		s.queueSettlement(entry)
	case OutcomeRetry:
		if s.shuttingDown {
			return
		}
		s.scheduleRetry(entry, outcome, now, cancelReceive)
	case OutcomeBlock:
		s.failLane(entry.delivery.Meta.Lane, outcome.Err, now, cancelReceive)
	case OutcomeDefer:
		if s.shuttingDown {
			return
		}
		s.scheduleDefer(entry, outcome, now, cancelReceive)
	}
}

func (s *runtimeState) scheduleDefer(
	entry *trackedDelivery,
	outcome Outcome,
	now time.Time,
	cancelReceive context.CancelFunc,
) {
	if len(s.retries) >= s.runtime.config.MaxRetryMessages {
		s.failLane(entry.delivery.Meta.Lane, fmt.Errorf("deferred message capacity exhausted"), now, cancelReceive)
		return
	}
	s.retrySeq++
	s.retries.add(&retryItem{entry: entry, next: now.Add(outcome.RetryAfter), seq: s.retrySeq})
}

func (s *runtimeState) scheduleRetry(
	entry *trackedDelivery,
	outcome Outcome,
	now time.Time,
	cancelReceive context.CancelFunc,
) {
	if entry.attempt >= s.runtime.config.RetryMaxAttempts || len(s.retries) >= s.runtime.config.MaxRetryMessages {
		s.failLane(
			entry.delivery.Meta.Lane,
			fmt.Errorf("retry attempts exhausted after %d attempts: %w", entry.attempt, outcome.Err),
			now,
			cancelReceive,
		)
		return
	}
	delay := outcome.RetryAfter
	if delay == 0 {
		delay = s.retryBackoff(entry.attempt)
	}
	if now.Sub(entry.firstAt)+delay > s.runtime.config.RetryMaxElapsed {
		s.failLane(
			entry.delivery.Meta.Lane,
			fmt.Errorf("retry elapsed budget exhausted after %s: %w", now.Sub(entry.firstAt), outcome.Err),
			now,
			cancelReceive,
		)
		return
	}
	entry.attempt++
	entry.delivery.Meta.Attempt = entry.attempt
	s.retrySeq++
	s.retries.add(&retryItem{entry: entry, next: now.Add(delay), seq: s.retrySeq})
	s.runtime.observer.RetryScheduled(context.Background())
}

func (s *runtimeState) retryBackoff(attempt int) time.Duration {
	delay := s.runtime.config.RetryBackoffMin
	for range max(0, attempt-1) {
		if delay >= s.runtime.config.RetryBackoffMax/2 {
			delay = s.runtime.config.RetryBackoffMax
			break
		}
		delay *= 2
	}
	if delay > s.runtime.config.RetryBackoffMax {
		delay = s.runtime.config.RetryBackoffMax
	}
	// 仅用于打散同一时刻的本地重试，不参与任何业务身份计算。
	//nolint:gosec // G404: 重试 jitter 不需要密码学安全随机数。
	jitter := 0.8 + rand.Float64()*0.4
	return time.Duration(float64(delay) * jitter)
}

func (s *runtimeState) dispatchReadyRetries(now time.Time) {
	if s.shuttingDown {
		return
	}
	for {
		item := s.retries.takeReady(now)
		if item == nil {
			return
		}
		if s.lane(item.entry.delivery.Meta.Lane).blocked {
			// blocked lane 只能出现在 Runtime 已开始失败退出的所有权代际。条目必须放回堆，
			// 否则 inflight 计数仍然保留而重试条目永久丢失。
			s.retries.add(item)
			return
		}
		s.submit(item.entry)
	}
}

func (s *runtimeState) releaseOrderKey(entry *trackedDelivery) {
	key := entry.delivery.Message.OrderKey
	if key == "" {
		return
	}
	waiting := s.waitingByKey[key]
	if len(waiting) == 0 {
		delete(s.activeOrderKeys, key)
		delete(s.waitingByKey, key)
		return
	}
	next := waiting[0]
	if s.lane(next.delivery.Meta.Lane).blocked {
		return
	}
	if len(waiting) == 1 {
		delete(s.waitingByKey, key)
	} else {
		s.waitingByKey[key] = waiting[1:]
	}
	s.submit(next)
}

func (s *runtimeState) failLane(
	laneName string,
	cause error,
	now time.Time,
	cancelReceive context.CancelFunc,
) {
	if cause == nil {
		cause = errors.New("handler blocked lane without an error")
	}
	pauseErr := s.pauseLane(laneName)
	s.fatalErr = errors.Join(
		s.fatalErr,
		fmt.Errorf("block message lane %q: %w", laneName, cause),
		pauseErr,
	)
	s.startShutdown(cancelReceive, now)
}

func (s *runtimeState) pauseLane(laneName string) error {
	lane := s.lane(laneName)
	if lane.blocked {
		return nil
	}
	lane.blocked = true
	s.runtime.observer.FlowTransition(context.Background(), "pause")
	controller, ok := s.runtime.session.(LanePauser)
	if !ok || !s.capabilities.CanPauseLane {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.runtime.config.ProcessTimeout)
	defer cancel()
	if err := controller.Pause(ctx, laneName); err != nil {
		return fmt.Errorf("pause message lane %q: %w", laneName, err)
	}
	return nil
}

func (s *runtimeState) queueSettlement(entry *trackedDelivery) {
	lane := s.lane(entry.delivery.Meta.Lane)
	if s.capabilities.Settlement == SettlementIndividual {
		s.settleQueue = append(s.settleQueue, &settleBatch{
			lane:     entry.delivery.Meta.Lane,
			entries:  []*trackedDelivery{entry},
			receipts: []Receipt{entry.delivery.Receipt},
		})
		return
	}
	if lane.settling {
		return
	}
	s.queueCumulativePrefix(entry.delivery.Meta.Lane, lane)
}

func (s *runtimeState) queueCumulativePrefix(laneName string, lane *laneState) {
	count := 0
	for count < len(lane.queue) && lane.queue[count].terminal {
		count++
	}
	if count == 0 {
		return
	}
	entries := append([]*trackedDelivery(nil), lane.queue[:count]...)
	lane.queue = lane.queue[count:]
	receipts := make([]Receipt, len(entries))
	for index, entry := range entries {
		receipts[index] = entry.delivery.Receipt
	}
	lane.settling = true
	s.settleQueue = append(s.settleQueue, &settleBatch{
		lane:     laneName,
		entries:  entries,
		receipts: receipts,
	})
}

func (s *runtimeState) trySettle(now time.Time) {
	if s.settling || len(s.settleQueue) == 0 || now.Before(s.settleQueue[0].nextAttempt) {
		return
	}
	select {
	case s.settleRequests <- s.settleQueue[0]:
		s.settling = true
		s.settleQueue[0].startedAt = now
	default:
	}
}

func (s *runtimeState) handleSettleResult(result settleResult, now time.Time) {
	if !s.settling || len(s.settleQueue) == 0 || s.settleQueue[0] != result.batch {
		return
	}
	s.settling = false
	duration := time.Duration(0)
	if !result.batch.startedAt.IsZero() {
		duration = now.Sub(result.batch.startedAt)
	}
	s.runtime.observer.SettlementFinished(context.Background(), SettlementObservation{
		Mode: s.capabilities.Settlement, Lane: result.batch.lane, Messages: len(result.batch.entries),
		Succeeded: result.err == nil, Duration: duration,
	})
	if result.err != nil {
		result.batch.nextAttempt = now.Add(s.runtime.config.ConfirmRetryBackoff)
		result.batch.startedAt = time.Time{}
		return
	}
	s.settleQueue = s.settleQueue[1:]
	for _, entry := range result.batch.entries {
		s.inflightMessages--
		s.inflightBytes -= len(entry.delivery.Message.Body)
		s.lane(entry.delivery.Meta.Lane).inflight--
	}
	if s.capabilities.Settlement == SettlementCumulative {
		lane := s.lane(result.batch.lane)
		lane.settling = false
		s.queueCumulativePrefix(result.batch.lane, lane)
	}
}

func (s *runtimeState) startShutdown(cancelReceive context.CancelFunc, now time.Time) {
	if s.shuttingDown {
		return
	}
	s.shuttingDown = true
	s.shutdownDeadline = now.Add(s.runtime.config.ShutdownDrainTimeout)
	s.retries = nil
	cancelReceive()
}

func (s *runtimeState) nextWake(now time.Time) time.Time {
	var wake time.Time
	if s.runtime.observed {
		wake = now.Add(time.Second)
	}
	if !s.receiveNotBefore.IsZero() && now.Before(s.receiveNotBefore) {
		wake = s.receiveNotBefore
	}
	if next, ok := s.retries.nextTime(); ok && (wake.IsZero() || next.Before(wake)) {
		wake = next
	}
	if len(s.settleQueue) > 0 {
		next := s.settleQueue[0].nextAttempt
		if next.After(now) && (wake.IsZero() || next.Before(wake)) {
			wake = next
		}
	}
	if s.shuttingDown && (wake.IsZero() || s.shutdownDeadline.Before(wake)) {
		wake = s.shutdownDeadline
	}
	return wake
}

func (s *runtimeState) lane(name string) *laneState {
	lane := s.lanes[name]
	if lane == nil {
		lane = &laneState{}
		s.lanes[name] = lane
	}
	return lane
}

func (s *runtimeState) snapshot(now time.Time) RuntimeSnapshot {
	snapshot := RuntimeSnapshot{
		InflightMessages: s.inflightMessages,
		InflightBytes:    s.inflightBytes,
		RetryItems:       len(s.retries),
	}
	if len(s.retries) != 0 {
		oldest := s.retries[0].entry.firstAt
		for _, item := range s.retries[1:] {
			if item.entry.firstAt.Before(oldest) {
				oldest = item.entry.firstAt
			}
		}
		snapshot.RetryOldestAge = max(now.Sub(oldest), 0)
	}
	for laneName, lane := range s.lanes {
		laneBytes := 0
		for _, entry := range lane.queue {
			laneBytes += len(entry.delivery.Message.Body)
		}
		snapshot.Lanes = append(snapshot.Lanes, LaneSnapshot{
			Lane: laneName, InflightMessages: lane.inflight, InflightBytes: laneBytes,
			Paused: lane.blocked, Owned: true,
		})
		for _, entry := range lane.queue {
			if !entry.terminal {
				continue
			}
			snapshot.SettlementGap++
			age := max(now.Sub(entry.firstAt), 0)
			if age > snapshot.SettlementGapOldestAge {
				snapshot.SettlementGapOldestAge = age
			}
		}
	}
	return snapshot
}
