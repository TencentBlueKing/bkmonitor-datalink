// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cleaner

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"linkd/internal/config"
	"linkd/internal/consume"
	"linkd/internal/domain"
	"linkd/internal/store"
)

type EventBatchWriter interface {
	CreateEvents(ctx context.Context, events []domain.Event) ([]store.CreateEventItemResult, error)
}

type MailboxWriter interface {
	// EnqueueBatch 只执行成功前缀；首个失败后的项不得产生副作用并必须返回错误。
	// 返回值与输入逐项对应；顶层错误表示本次调用结果未知。
	EnqueueBatch(ctx context.Context, events []domain.Event) ([]MailboxEnqueueResult, error)
}

type MailboxEnqueueResult struct {
	Signaled bool
	Err      error
}

type allowReceiveGate struct{}

func (allowReceiveGate) Check(context.Context) ReceiveDecision { return ReceiveDecision{Allowed: true} }

type Logger interface {
	InfoContext(ctx context.Context, message string, args ...any)
	WarnContext(ctx context.Context, message string, args ...any)
}

type cleanerEntry struct {
	delivery          consume.Delivery
	processing        bool
	processAttempt    int
	processFirstRetry time.Time
	processRetryAt    time.Time
	readyAt           time.Time
	event             domain.Event
	discard           error
	stored            bool
	mailbox           bool
	skipMailbox       bool
	terminal          bool
}

type cleanerLane struct {
	name       string
	entries    []*cleanerEntry
	batching   bool
	paused     bool
	retryAt    time.Time
	attempt    int
	firstRetry time.Time
	revoking   bool
}

type processJob struct{ entry *cleanerEntry }

type processResult struct {
	entry  *cleanerEntry
	result ProcessResult
	err    error
}

type receiveResult struct {
	deliveries []consume.Delivery
	err        error
}

type laneBatchResult struct {
	lane    string
	settled int
	err     error
	blocked error
}

// Runtime 实现消息队列无关的 n→*→n Cleaner：并发纯计算，按 lane 连续批量执行副作用与确认。
type Runtime struct {
	config      config.CleanerRuntimeConfig
	session     consume.Session
	processor   Processor
	events      EventBatchWriter
	mailboxes   MailboxWriter
	receiveGate ReceiveGate
	logger      Logger
	observer    consume.Observer
	running     atomic.Bool
}

func NewRuntime(cfg config.CleanerRuntimeConfig, session consume.Session, processor Processor,
	events EventBatchWriter, mailboxes MailboxWriter, receiveGate ReceiveGate, logger Logger,
	observers ...consume.Observer,
) (*Runtime, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("create cleaner runtime: %w", err)
	}
	for name, dependency := range map[string]any{
		"session": session, "processor": processor, "event_writer": events, "mailbox_writer": mailboxes,
		"receive_gate": receiveGate, "logger": logger,
	} {
		if dependency == nil {
			return nil, fmt.Errorf("create cleaner runtime: %s must not be nil", name)
		}
	}
	var observer consume.Observer
	if len(observers) > 0 {
		observer = observers[0]
	}
	return &Runtime{
		config: cfg, session: session, processor: processor, events: events, mailboxes: mailboxes,
		receiveGate: receiveGate, logger: logger, observer: observer,
	}, nil
}

func (r *Runtime) Run(ctx context.Context) (runErr error) {
	if ctx == nil {
		return fmt.Errorf("run cleaner runtime: context must not be nil")
	}
	if !r.running.CompareAndSwap(false, true) {
		return fmt.Errorf("run cleaner runtime: runtime can only run once")
	}
	if r.observer != nil {
		r.observer.FlowTransition(ctx, "start")
		defer r.observer.FlowTransition(context.Background(), "stop")
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := r.session.Close(closeCtx); err != nil && !errors.Is(err, consume.ErrSessionClosed) {
			if ctx.Err() != nil && errors.Is(err, context.DeadlineExceeded) {
				r.logger.WarnContext(context.Background(), "cleaner session close timed out after shutdown")
				return
			}
			runErr = errors.Join(runErr, fmt.Errorf("close cleaner session: %w", err))
		}
	}()

	receiveRequests := make(chan consume.ReceiveLimits, 1)
	receiveResults := make(chan receiveResult, 1)
	jobs := make(chan processJob, r.config.MaxInflightMessages)
	processed := make(chan processResult, r.config.MaxInflightMessages)
	batches := make(chan laneBatchResult, r.config.MaxConcurrentBatches)
	batchSlots := make(chan struct{}, r.config.MaxConcurrentBatches)
	var batchWG sync.WaitGroup
	workCtx, cancelWork := context.WithCancel(context.Background())
	defer cancelWork()
	var background sync.WaitGroup
	background.Add(1)
	go r.receiveLoop(workCtx, receiveRequests, receiveResults, &background)
	for range r.config.WorkerCount {
		background.Add(1)
		go r.workerLoop(workCtx, jobs, processed, &background)
	}

	lanes := make(map[string]*cleanerLane)
	inflightMessages, inflightBytes := 0, 0
	receiving := false
	flowPaused := false
	shuttingDown := false
	shutdownDeadline := time.Time{}
	var fatalErr error
	var ownershipEvents <-chan consume.OwnershipEvent
	if ownership, ok := r.session.(consume.OwnershipSession); ok {
		ownershipEvents = ownership.OwnershipEvents()
	}
	var pendingRevoke *consume.OwnershipEvent
	var revokeDeadline time.Time
	runDone := ctx.Done()
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	for {
		now := time.Now()
		if r.observer != nil {
			snapshot := consume.RuntimeSnapshot{InflightMessages: inflightMessages, InflightBytes: inflightBytes}
			for _, lane := range lanes {
				laneBytes := 0
				for _, entry := range lane.entries {
					laneBytes += len(entry.delivery.Message.Body)
				}
				snapshot.Lanes = append(snapshot.Lanes, consume.LaneSnapshot{
					Lane: lane.name, InflightMessages: len(lane.entries), InflightBytes: laneBytes,
					Paused: lane.paused, Owned: !lane.revoking,
				})
			}
			r.observer.Snapshot(ctx, snapshot)
		}
		if pendingRevoke != nil {
			drained := true
			for _, laneName := range pendingRevoke.Lanes {
				if lane := lanes[laneName]; lane != nil && (len(lane.entries) != 0 || lane.batching) {
					drained = false
					break
				}
			}
			if drained {
				pendingRevoke.Complete()
				for _, laneName := range pendingRevoke.Lanes {
					delete(lanes, laneName)
				}
				pendingRevoke = nil
			} else if !now.Before(revokeDeadline) {
				pendingRevoke.Complete()
				pendingRevoke = nil
				fatalErr = errors.Join(fatalErr, fmt.Errorf("drain revoked cleaner lanes: %w", context.DeadlineExceeded))
				shuttingDown = true
				shutdownDeadline = now
			}
		}
		for _, lane := range lanes {
			for _, entry := range lane.entries {
				if entry.processing || entry.processRetryAt.IsZero() || now.Before(entry.processRetryAt) {
					continue
				}
				entry.processing = true
				entry.processRetryAt = time.Time{}
				jobs <- processJob{entry: entry}
			}
		}
		for _, lane := range lanes {
			r.maybeStartBatch(workCtx, lane, now, shuttingDown || lane.revoking, batchSlots, batches, &batchWG)
		}
		globalLaneBackpressure := false
		if _, ok := r.session.(consume.LaneController); !ok {
			for _, lane := range lanes {
				if len(lane.entries) >= r.config.MaxInflightPerLane {
					globalLaneBackpressure = true
					break
				}
			}
		}
		var backpressureWake time.Time
		if !shuttingDown && !globalLaneBackpressure && !receiving && inflightMessages < r.config.MaxInflightMessages && inflightBytes < r.config.MaxInflightBytes {
			decision := r.receiveGate.Check(workCtx)
			flowController, canPauseFlow := r.session.(consume.FlowController)
			if !decision.Allowed && !canPauseFlow {
				backpressureWake = decision.RecheckAt
			} else {
				if !decision.Allowed && !flowPaused {
					if err := flowController.PauseFlow(workCtx); err != nil {
						fatalErr = errors.Join(fatalErr, fmt.Errorf("pause cleaner flow for signal backpressure: %w", err))
						shuttingDown = true
						shutdownDeadline = now.Add(time.Duration(r.config.ShutdownDrainTimeoutSeconds) * time.Second)
					} else {
						flowPaused = true
					}
				}
				if decision.Allowed && flowPaused {
					if err := flowController.ResumeFlow(workCtx); err != nil {
						fatalErr = errors.Join(fatalErr, fmt.Errorf("resume cleaner flow after signal backpressure: %w", err))
						shuttingDown = true
						shutdownDeadline = now.Add(time.Duration(r.config.ShutdownDrainTimeoutSeconds) * time.Second)
					} else {
						flowPaused = false
					}
				}
				if shuttingDown {
					continue
				}
				limits := consume.ReceiveLimits{
					MaxMessages: min(r.config.MaxBatchMessages, r.config.MaxInflightMessages-inflightMessages),
					MaxBytes:    min(r.config.MaxBatchBytes, r.config.MaxInflightBytes-inflightBytes),
				}
				select {
				case receiveRequests <- limits:
					receiving = true
				default:
				}
			}
		}
		if shuttingDown && inflightMessages == 0 {
			cancelWork()
			break
		}
		if shuttingDown && !now.Before(shutdownDeadline) {
			fatalErr = errors.Join(fatalErr, fmt.Errorf("drain cleaner runtime: %w", context.DeadlineExceeded))
			cancelWork()
			break
		}

		wake := r.nextWake(lanes, now, shuttingDown, len(batchSlots) < cap(batchSlots))
		// 不再借过期组批期限空转后，退出和撤销所有权的截止时间必须独立唤醒。
		if shuttingDown && (wake.IsZero() || shutdownDeadline.Before(wake)) {
			wake = shutdownDeadline
		}
		if pendingRevoke != nil && (wake.IsZero() || revokeDeadline.Before(wake)) {
			wake = revokeDeadline
		}
		if !backpressureWake.IsZero() && (wake.IsZero() || backpressureWake.Before(wake)) {
			wake = backpressureWake
		}
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
		case <-runDone:
			runDone = nil
			if !shuttingDown {
				shuttingDown = true
				shutdownDeadline = time.Now().Add(time.Duration(r.config.ShutdownDrainTimeoutSeconds) * time.Second)
			}
		case result := <-receiveResults:
			receiving = false
			if result.err != nil {
				if !shuttingDown && !errors.Is(result.err, context.Canceled) {
					fatalErr = fmt.Errorf("receive cleaner messages: %w", result.err)
					shuttingDown = true
					shutdownDeadline = time.Now().Add(time.Duration(r.config.ShutdownDrainTimeoutSeconds) * time.Second)
				}
				break
			}
			for _, delivery := range result.deliveries {
				if !delivery.Receipt.Valid() || delivery.Message.ID == "" || delivery.Meta.Lane == "" {
					fatalErr = fmt.Errorf("%w: cleaner delivery is missing receipt, message id or lane", consume.ErrInvalidDelivery)
					shuttingDown = true
					shutdownDeadline = time.Now().Add(time.Duration(r.config.ShutdownDrainTimeoutSeconds) * time.Second)
					break
				}
				entry := &cleanerEntry{delivery: delivery, processing: true}
				if r.observer != nil {
					r.observer.DeliveryReceived(ctx, consume.DeliveryObservation{
						Lane: delivery.Meta.Lane, Bytes: len(delivery.Message.Body), Redelivered: delivery.Meta.Redeliver,
					})
				}
				lane := lanes[delivery.Meta.Lane]
				if lane == nil {
					lane = &cleanerLane{name: delivery.Meta.Lane}
					lanes[lane.name] = lane
				}
				lane.entries = append(lane.entries, entry)
				inflightMessages++
				inflightBytes += len(delivery.Message.Body)
				jobs <- processJob{entry: entry}
				if !lane.paused && len(lane.entries) >= r.config.MaxInflightPerLane {
					if controller, ok := r.session.(consume.LaneController); ok {
						if err := controller.Pause(ctx, lane.name); err != nil {
							fatalErr = errors.Join(fatalErr, err)
							shuttingDown = true
						} else {
							lane.paused = true
							if r.observer != nil {
								r.observer.FlowTransition(ctx, "pause")
							}
						}
					}
				}
			}
			if ownership, ok := r.session.(consume.OwnershipSession); ok && len(result.deliveries) > 0 {
				ownership.AllowOwnershipChanges()
			}
		case result := <-processed:
			result.entry.processing = false
			if result.err != nil {
				r.scheduleProcessRetry(result.entry, result.err, time.Now(), &fatalErr, &shuttingDown, &shutdownDeadline)
			} else {
				result.entry.readyAt = time.Now()
				result.entry.event = result.result.Event
				result.entry.discard = result.result.DiscardErr
				result.entry.processAttempt = 0
				result.entry.processFirstRetry = time.Time{}
				result.entry.processRetryAt = time.Time{}
			}
		case result := <-batches:
			lane := lanes[result.lane]
			if lane == nil {
				break
			}
			lane.batching = false
			if result.settled > 0 {
				for _, entry := range lane.entries[:result.settled] {
					inflightMessages--
					inflightBytes -= len(entry.delivery.Message.Body)
				}
				lane.entries = lane.entries[result.settled:]
				lane.attempt, lane.firstRetry, lane.retryAt = 0, time.Time{}, time.Time{}
				if lane.paused && len(lane.entries) <= r.config.ResumeInflightPerLane {
					if controller, ok := r.session.(consume.LaneController); ok {
						if err := controller.Resume(ctx, lane.name); err != nil {
							fatalErr = errors.Join(fatalErr, err)
							shuttingDown = true
						} else {
							lane.paused = false
							if r.observer != nil {
								r.observer.FlowTransition(ctx, "resume")
							}
						}
					}
				}
			}
			if result.blocked != nil {
				fatalErr = errors.Join(fatalErr, result.blocked)
				shuttingDown = true
				shutdownDeadline = time.Now().Add(time.Duration(r.config.ShutdownDrainTimeoutSeconds) * time.Second)
			} else if result.err != nil {
				r.scheduleLaneRetry(lane, result.err, time.Now(), &fatalErr, &shuttingDown, &shutdownDeadline)
			}
		case event := <-ownershipEvents:
			if r.observer != nil {
				r.observer.OwnershipChanged(ctx, consume.OwnershipObservation{Kind: event.Kind, Lanes: event.Lanes})
			}
			switch event.Kind {
			case consume.OwnershipAssigned:
				event.Complete()
			case consume.OwnershipRevoked:
				if pendingRevoke != nil {
					event.Complete()
					fatalErr = errors.Join(fatalErr, fmt.Errorf("overlapping cleaner ownership revoke"))
					shuttingDown = true
					shutdownDeadline = time.Now()
					break
				}
				for _, laneName := range event.Lanes {
					if lane := lanes[laneName]; lane != nil {
						lane.revoking = true
					}
				}
				pendingRevoke = &event
				revokeDeadline = time.Now().Add(time.Duration(r.config.ShutdownDrainTimeoutSeconds) * time.Second)
			case consume.OwnershipLost:
				event.Complete()
				fatalErr = errors.Join(fatalErr, fmt.Errorf("cleaner lane ownership lost"))
				shuttingDown = true
				shutdownDeadline = time.Now()
			}
		case <-timerC:
		}
		if timerC != nil && !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}
	if pendingRevoke != nil {
		pendingRevoke.Complete()
	}
	close(receiveRequests)
	close(jobs)
	cancelWork()
	batchWG.Wait()
	background.Wait()
	if r.observer != nil {
		r.observer.ShutdownFinished(context.Background(), fatalErr == nil, 0, inflightMessages)
	}
	return fatalErr
}

func (r *Runtime) receiveLoop(ctx context.Context, requests <-chan consume.ReceiveLimits, results chan<- receiveResult, wg *sync.WaitGroup) {
	defer wg.Done()
	for request := range requests {
		deliveries, err := r.session.Receive(ctx, request)
		if err == nil {
			bytes := 0
			for _, delivery := range deliveries {
				bytes += len(delivery.Message.Body)
			}
			if len(deliveries) > request.MaxMessages || bytes > request.MaxBytes {
				err = fmt.Errorf("cleaner receive: %w: received %d messages/%d bytes, limit %d/%d",
					consume.ErrReceiveLimitExceeded, len(deliveries), bytes, request.MaxMessages, request.MaxBytes)
				deliveries = nil
			}
		}
		if err == nil && len(deliveries) == 0 {
			select {
			case <-time.After(20 * time.Millisecond):
			case <-ctx.Done():
				return
			}
		}
		select {
		case results <- receiveResult{deliveries: deliveries, err: err}:
		case <-ctx.Done():
			return
		}
	}
}

func (r *Runtime) workerLoop(ctx context.Context, jobs <-chan processJob, results chan<- processResult, wg *sync.WaitGroup) {
	defer wg.Done()
	for job := range jobs {
		workCtx, cancel := context.WithTimeout(ctx, time.Duration(r.config.ProcessTimeoutSeconds)*time.Second)
		startedAt := time.Now()
		if r.observer != nil {
			r.observer.HandlerStarted(workCtx, job.entry.delivery.Message)
		}
		result, err := r.processor.Process(workCtx, job.entry.delivery.Message)
		if r.observer != nil {
			outcome := consume.OutcomeComplete
			stepOutcome := "succeeded"
			if err != nil {
				outcome = consume.OutcomeRetry
				stepOutcome = "failed"
			} else if result.DiscardErr != nil {
				outcome = consume.OutcomeDiscard
				stepOutcome = "rejected"
			}
			r.observer.HandlerFinished(workCtx, outcome, time.Since(startedAt))
			r.observer.StepFinished(workCtx, consume.StepObservation{
				Step: "transform", Outcome: stepOutcome, Items: 1, Duration: time.Since(startedAt),
			})
		}
		cancel()
		select {
		case results <- processResult{entry: job.entry, result: result, err: err}:
		case <-ctx.Done():
			return
		}
	}
}

func (r *Runtime) maybeStartBatch(ctx context.Context, lane *cleanerLane, now time.Time, force bool,
	slots chan struct{}, results chan<- laneBatchResult, wg *sync.WaitGroup) {
	if lane.batching || len(lane.entries) == 0 || (!lane.retryAt.IsZero() && now.Before(lane.retryAt)) {
		return
	}
	count, bytes := 0, 0
	for _, entry := range lane.entries {
		if entry.readyAt.IsZero() {
			break
		}
		if count > 0 && (count >= r.config.MaxBatchMessages || bytes+len(entry.delivery.Message.Body) > r.config.MaxBatchBytes) {
			break
		}
		count++
		bytes += len(entry.delivery.Message.Body)
	}
	if count == 0 {
		return
	}
	oldest := lane.entries[0].readyAt
	if !force && count < r.config.MaxBatchMessages && bytes < r.config.MaxBatchBytes && now.Sub(oldest) < time.Duration(r.config.BatchWaitMilliseconds)*time.Millisecond {
		return
	}
	select {
	case slots <- struct{}{}:
	default:
		return
	}
	lane.batching = true
	entries := append([]*cleanerEntry(nil), lane.entries[:count]...)
	wg.Add(1)
	go func() {
		defer wg.Done()
		result := r.runLaneBatch(ctx, lane.name, entries)
		// 必须先释放槽位再通知事件循环。否则循环可能消费完成通知后仍看到
		// 槽位已满，进入等待，而稍后的槽位释放不再产生任何唤醒事件。
		<-slots
		select {
		case results <- result:
		case <-ctx.Done():
		}
	}()
}

func (r *Runtime) runLaneBatch(ctx context.Context, lane string, entries []*cleanerEntry) laneBatchResult {
	toStore := make([]domain.Event, 0, len(entries))
	storeEntries := make([]*cleanerEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.discard == nil && !entry.stored {
			toStore = append(toStore, entry.event)
			storeEntries = append(storeEntries, entry)
		}
	}
	if len(toStore) > 0 {
		startedAt := time.Now()
		items, err := createNormalizedEvents(ctx, r.events, toStore)
		if err != nil {
			r.observeStep(ctx, "event_store", "failed", len(toStore), time.Since(startedAt))
			return laneBatchResult{lane: lane, err: err}
		}
		if len(items) != len(storeEntries) {
			r.observeStep(ctx, "event_store", "failed", len(toStore), time.Since(startedAt))
			return laneBatchResult{lane: lane, blocked: fmt.Errorf("event batch writer returned %d items for %d events", len(items), len(storeEntries))}
		}
		var firstItemErr error
		outcomes := map[string]int{}
		for index, item := range items {
			entry := storeEntries[index]
			if item.Err == nil {
				entry.event = item.Result.Event
				entry.stored = true
				entry.skipMailbox = item.Result.Processing.State.Terminal()
				if item.Result.Created {
					outcomes["created"]++
				} else {
					outcomes["replayed"]++
				}
				continue
			}
			if errors.Is(item.Err, store.ErrIdentityConflict) || errors.Is(item.Err, store.ErrInvalidArgument) {
				entry.discard = item.Err
				outcomes["rejected"]++
				continue
			}
			outcomes["failed"]++
			if firstItemErr == nil {
				firstItemErr = item.Err
			}
		}
		for outcome, count := range outcomes {
			r.observeStep(ctx, "event_store", outcome, count, time.Since(startedAt))
		}
		if firstItemErr != nil {
			return r.finishLanePrefix(ctx, lane, entries, firstItemErr)
		}
	}
	return r.finishLanePrefix(ctx, lane, entries, nil)
}

func createNormalizedEvents(ctx context.Context, writer EventBatchWriter, events []domain.Event) ([]store.CreateEventItemResult, error) {
	if normalized, ok := writer.(store.NormalizedEventBatchStore); ok {
		return normalized.CreateNormalizedEvents(ctx, events)
	}
	return writer.CreateEvents(ctx, events)
}

// finishLanePrefix 只把已经持久化或确定性丢弃的连续队首推进到原消息确认。
// unprocessed Event 必须先入 Mailbox；Repository 返回的终态重投直接跳过 Mailbox。
// 后项暂时失败不回滚前项，也绝不能让更后的 Event 越过缺口先产生副作用。
func (r *Runtime) finishLanePrefix(ctx context.Context, lane string, entries []*cleanerEntry, pendingErr error) laneBatchResult {
	if err, blocked := r.enqueueLanePrefix(ctx, entries); blocked != nil {
		return laneBatchResult{lane: lane, blocked: blocked}
	} else if err != nil {
		pendingErr = err
	}
	settled := 0
	for _, entry := range entries {
		if entry.discard != nil {
			entry.terminal = true
			settled++
			continue
		}
		if !entry.stored {
			break
		}
		if entry.skipMailbox && !entry.mailbox {
			r.observeStep(ctx, "mailbox_enqueue", "skipped_terminal", 1, 0)
			entry.mailbox = true
		}
		if !entry.mailbox {
			break
		}
		entry.terminal = true
		settled++
	}
	if settled == 0 {
		if pendingErr != nil {
			return laneBatchResult{lane: lane, err: pendingErr}
		}
		return laneBatchResult{lane: lane, err: fmt.Errorf("lane batch made no progress")}
	}
	receipts := make([]consume.Receipt, settled)
	for index := range settled {
		receipts[index] = entries[index].delivery.Receipt
	}
	settleStartedAt := time.Now()
	if err := r.session.Confirm(ctx, receipts); err != nil {
		if r.observer != nil {
			r.observer.SettlementFinished(ctx, consume.SettlementObservation{
				Mode: r.session.Capabilities().Settlement, Lane: lane, Messages: settled,
				Succeeded: false, Duration: time.Since(settleStartedAt),
			})
		}
		return laneBatchResult{lane: lane, err: err}
	}
	if r.observer != nil {
		r.observer.SettlementFinished(ctx, consume.SettlementObservation{
			Mode: r.session.Capabilities().Settlement, Lane: lane, Messages: settled,
			Succeeded: true, Duration: time.Since(settleStartedAt),
		})
	}
	return laneBatchResult{lane: lane, settled: settled, err: pendingErr}
}

// enqueueLanePrefix 只提交已落库前缀中尚未入队的项，保留旧的原消息连续确认边界。
func (r *Runtime) enqueueLanePrefix(ctx context.Context, entries []*cleanerEntry) (error, error) {
	var events []domain.Event
	var targets []*cleanerEntry
	for _, entry := range entries {
		if entry.discard != nil {
			continue
		}
		if !entry.stored {
			break
		}
		if !entry.mailbox && !entry.skipMailbox {
			events = append(events, entry.event)
			targets = append(targets, entry)
		}
	}
	if len(events) == 0 {
		return nil, nil
	}
	started := time.Now()
	results, err := r.mailboxes.EnqueueBatch(ctx, events)
	elapsed := time.Since(started)
	if err != nil {
		r.observeStep(ctx, "mailbox_enqueue", "failed", len(events), elapsed)
		return err, nil
	}
	if len(results) != len(events) {
		return nil, fmt.Errorf("mailbox writer returned %d items, want %d", len(results), len(events))
	}
	added, emitted := 0, 0
	var firstErr error
	for i, result := range results {
		if result.Err != nil {
			if firstErr == nil {
				firstErr = result.Err
			}
			continue
		}
		if firstErr != nil {
			return nil, fmt.Errorf("mailbox writer succeeded after a failed prefix")
		}
		targets[i].mailbox = true
		added++
		if result.Signaled {
			emitted++
		}
	}
	if added > 0 {
		r.observeStep(ctx, "mailbox_enqueue", "added", added, elapsed)
	}
	if added < len(events) {
		r.observeStep(ctx, "mailbox_enqueue", "failed", len(events)-added, elapsed)
	}
	if emitted > 0 {
		r.observeStep(ctx, "mailbox_signal", "emitted", emitted, elapsed)
	}
	if added > emitted {
		r.observeStep(ctx, "mailbox_signal", "coalesced", added-emitted, elapsed)
	}
	return firstErr, nil
}

func (r *Runtime) observeStep(ctx context.Context, step, outcome string, items int, duration time.Duration) {
	if r.observer == nil || items <= 0 {
		return
	}
	r.observer.StepFinished(ctx, consume.StepObservation{
		Step: step, Outcome: outcome, Items: items, Duration: duration,
	})
}

func (r *Runtime) scheduleLaneRetry(lane *cleanerLane, cause error, now time.Time, fatalErr *error,
	shuttingDown *bool, shutdownDeadline *time.Time) {
	if lane.firstRetry.IsZero() {
		lane.firstRetry = now
	}
	if lane.attempt >= r.config.RetryMaxAttempts || now.Sub(lane.firstRetry) >= time.Duration(r.config.RetryMaxElapsedSeconds)*time.Second {
		*fatalErr = errors.Join(*fatalErr, fmt.Errorf("cleaner lane %q retry exhausted: %w", lane.name, cause))
		*shuttingDown = true
		*shutdownDeadline = now.Add(time.Duration(r.config.ShutdownDrainTimeoutSeconds) * time.Second)
		return
	}
	lane.attempt++
	delay := 100 * time.Millisecond
	for range lane.attempt - 1 {
		delay = min(delay*2, 5*time.Second)
	}
	//nolint:gosec // 重试 jitter 不承担安全或身份用途，不需要密码学随机数。
	delay = time.Duration(float64(delay) * (0.8 + rand.Float64()*0.4))
	lane.retryAt = now.Add(delay)
	if r.observer != nil {
		r.observer.RetryScheduled(context.Background())
	}
}

func (r *Runtime) scheduleProcessRetry(entry *cleanerEntry, cause error, now time.Time, fatalErr *error,
	shuttingDown *bool, shutdownDeadline *time.Time) {
	if entry.processFirstRetry.IsZero() {
		entry.processFirstRetry = now
	}
	if entry.processAttempt >= r.config.RetryMaxAttempts ||
		now.Sub(entry.processFirstRetry) >= time.Duration(r.config.RetryMaxElapsedSeconds)*time.Second {
		*fatalErr = errors.Join(*fatalErr, fmt.Errorf("cleaner message %q retry exhausted: %w", entry.delivery.Message.ID, cause))
		*shuttingDown = true
		*shutdownDeadline = now.Add(time.Duration(r.config.ShutdownDrainTimeoutSeconds) * time.Second)
		return
	}
	entry.processAttempt++
	delay := 100 * time.Millisecond
	for range entry.processAttempt - 1 {
		delay = min(delay*2, 5*time.Second)
	}
	//nolint:gosec // 重试 jitter 不承担安全或身份用途，不需要密码学随机数。
	delay = time.Duration(float64(delay) * (0.8 + rand.Float64()*0.4))
	entry.processRetryAt = now.Add(delay)
	if r.observer != nil {
		r.observer.RetryScheduled(context.Background())
	}
}

// nextWake 只返回尚未到期且到期后可推进工作的期限。已就绪的工作已由本轮
// 调度尝试过；执行中或等待槽位的批次应由完成通知唤醒，不能用旧期限忙等。
func (r *Runtime) nextWake(lanes map[string]*cleanerLane, now time.Time, force, canBatch bool) time.Time {
	var wake time.Time
	consider := func(candidate time.Time) {
		if candidate.After(now) && (wake.IsZero() || candidate.Before(wake)) {
			wake = candidate
		}
	}
	for _, lane := range lanes {
		for _, entry := range lane.entries {
			if !entry.processing {
				consider(entry.processRetryAt)
			}
		}
		if lane.batching || !canBatch {
			continue
		}
		if !lane.retryAt.IsZero() {
			consider(lane.retryAt)
		} else if !force && !lane.revoking && len(lane.entries) > 0 && !lane.entries[0].readyAt.IsZero() {
			consider(lane.entries[0].readyAt.Add(time.Duration(r.config.BatchWaitMilliseconds) * time.Millisecond))
		}
	}
	return wake
}
