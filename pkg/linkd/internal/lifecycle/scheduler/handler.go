// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"linkd/internal/consume"
	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/processingerror"
	"linkd/internal/store"
)

type EventProcessor interface {
	ProcessEvent(ctx context.Context, stored store.StoredEvent) (lifecycle.ProcessResult, error)
}

type EventReader interface {
	GetEvent(ctx context.Context, bkTenantID, eventID string) (store.StoredEvent, error)
}

type Mailbox interface {
	Peek(ctx context.Context, mailboxID string) (string, error)
	AckHead(ctx context.Context, mailboxID, eventID string) error
}

type Logger interface {
	DebugContext(ctx context.Context, message string, args ...any)
	WarnContext(ctx context.Context, message string, args ...any)
}

// Handler 按 Signal 指向的 Mailbox 获取跨进程 lease，并有界连续处理队首 Event。
type Handler struct {
	expectedSource string
	eventReader    EventReader
	mailbox        Mailbox
	processor      EventProcessor
	locker         Locker
	config         Config
	logger         Logger
	observer       Observer
}

func NewHandler(
	eventReader EventReader,
	mailbox Mailbox,
	processor EventProcessor,
	locker Locker,
	config Config,
	logger Logger,
	observers ...Observer,
) (*Handler, error) {
	for name, dependency := range map[string]any{
		"event_reader": eventReader, "mailbox": mailbox, "processor": processor, "locker": locker, "logger": logger,
	} {
		if dependency == nil {
			return nil, fmt.Errorf("create lifecycle scheduler: %s must not be nil", name)
		}
	}
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("create lifecycle scheduler: %w", err)
	}
	observer := Observer(noopObserver{})
	if len(observers) > 0 && observers[0] != nil {
		observer = observers[0]
	}
	return &Handler{eventReader: eventReader, mailbox: mailbox, processor: processor, locker: locker, config: config, logger: logger, observer: observer}, nil
}

func (h *Handler) Handle(ctx context.Context, message consume.Message) (outcome consume.Outcome) {
	defer func() {
		if recover() != nil {
			h.logger.WarnContext(ctx, "lifecycle scheduler operation panicked", "module", "lifecycle", "operation", "schedule", "error", processingerror.CapturePanic(), "message_id", message.ID, "reason_code", "scheduler_panic")
			// Event 处理的 panic 在消息边界终结；队列/租约协议异常不能直接丢弃唤醒。
			outcome = consume.Defer(h.config.LockRetryDelay)
		}
	}()
	signal, err := DecodeSignal(message.Body)
	if err != nil {
		h.logger.WarnContext(ctx, "discard invalid lifecycle mailbox signal", "module", "lifecycle", "operation", "decode_signal", "error", err, "message_id", message.ID, "reason_code", "invalid_signal")
		return consume.Discard(fmt.Errorf("invalid lifecycle mailbox signal: %w", err))
	}
	if h.expectedSource != "" && signal.EventSourceID != h.expectedSource {
		return consume.Discard(fmt.Errorf("signal belongs to another event source"))
	}
	if message.ID != signal.MessageID || message.TenantID != signal.BKTenantID || message.OrderKey != signal.MailboxID {
		return consume.Discard(fmt.Errorf("lifecycle mailbox signal transport metadata does not match payload"))
	}

	lease, err := h.locker.Acquire(ctx, signal.MailboxID)
	if errors.Is(err, ErrLockBusy) {
		h.observer.LeaseOperation(ctx, "acquire", "busy")
		return consume.Defer(h.config.LockRetryDelay)
	}
	if err != nil {
		h.observer.LeaseOperation(ctx, "acquire", "failed")
		h.logger.WarnContext(ctx, "acquire lifecycle mailbox lease failed", "module", "lifecycle", "operation", "lease_acquire", "error", err, "bk_tenant_id", signal.BKTenantID, "event_source_id", signal.EventSourceID, "mailbox_id", signal.MailboxID)
		return consume.Defer(h.config.LockRetryDelay)
	}
	h.observer.LeaseOperation(ctx, "acquire", "succeeded")

	workCtx, cancelWork := context.WithCancel(ctx)
	renewCtx, cancelRenew := context.WithCancel(ctx)
	renewDone := make(chan error, 1)
	go func() {
		_, err := processingerror.Guard(func() (struct{}, error) { h.renewLoop(renewCtx, cancelWork, lease, renewDone); return struct{}{}, nil })
		if err != nil {
			cancelWork()
			renewDone <- err
		}
	}()
	cleaned := false
	cleanup := func() error {
		if cleaned {
			return nil
		}
		cleaned = true
		cancelRenew()
		renewErr := <-renewDone
		cancelWork()
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), h.config.ReleaseTimeout)
		defer releaseCancel()
		_, releaseErr := processingerror.Guard(func() (struct{}, error) { return struct{}{}, h.locker.Release(releaseCtx, lease) })
		if releaseErr != nil {
			h.observer.LeaseOperation(ctx, "release", "failed")
			h.logger.WarnContext(ctx, "release lifecycle mailbox lease failed", "module", "lifecycle", "operation", "lease_release", "error", releaseErr, "mailbox_id", signal.MailboxID, "reason_code", "lease_release_failed")
		} else {
			h.observer.LeaseOperation(ctx, "release", "succeeded")
		}
		return renewErr
	}
	// 包括队列适配器 panic 在内的所有出口都停止续租并释放 owner。
	defer func() { _ = cleanup() }()
	processed, lastResult, complete, blockErr, processErr := h.drain(workCtx, signal)
	renewErr := cleanup()
	if renewErr != nil {
		h.logger.WarnContext(ctx, "renew lifecycle mailbox lease failed", "module", "lifecycle", "operation", "lease_renew", "error", renewErr, "bk_tenant_id", signal.BKTenantID, "event_source_id", signal.EventSourceID, "mailbox_id", signal.MailboxID)
		return consume.Defer(h.config.LockRetryDelay)
	}
	if blockErr != nil {
		return consume.Defer(h.config.LockRetryDelay)
	}
	if processErr != nil {
		// 普通 Event 失败已在 drain 内终结；这里保留队列确认与所有权协议的唤醒。
		return consume.Defer(h.config.LockRetryDelay)
	}
	if complete {
		eventSourceID := ""
		if processed > 0 {
			eventSourceID = signal.EventSourceID
		}
		h.observer.MailboxDrained(ctx, eventSourceID, "completed", processed)
		h.logger.DebugContext(ctx, "lifecycle mailbox drained", "module", "lifecycle", "operation", "mailbox_drain", "mailbox_id", signal.MailboxID, "processed", processed,
			"event_id", lastResult.EventID, "alert_ids", lastResult.AlertIDs, "outcome", lastResult.Outcome)
		return consume.Complete()
	}
	eventSourceID := ""
	if processed > 0 {
		eventSourceID = signal.EventSourceID
	}
	h.observer.MailboxDrained(ctx, eventSourceID, "deferred", processed)
	return consume.Defer(0)
}

func (h *Handler) drain(
	ctx context.Context,
	signal Signal,
) (processed int, last lifecycle.ProcessResult, complete bool, blockErr, err error) {
	for processed < h.config.MaxDrainEvents {
		eventID, peekErr := h.mailbox.Peek(ctx, signal.MailboxID)
		if peekErr != nil {
			h.logger.WarnContext(ctx, "read lifecycle mailbox head failed", "module", "lifecycle", "operation", "mailbox_peek", "error", peekErr, "bk_tenant_id", signal.BKTenantID, "event_source_id", signal.EventSourceID, "mailbox_id", signal.MailboxID)
			h.observer.MailboxOperation(ctx, "", "peek", "failed")
			return processed, last, false, nil, peekErr
		}
		h.observer.MailboxOperation(ctx, "", "peek", "succeeded")
		if eventID == "" {
			return processed, last, true, nil, nil
		}
		stored, result, attempts, stage, processErr := h.processMailboxEvent(ctx, signal, eventID)
		if processingPending(processErr) {
			h.logger.WarnContext(ctx, "lifecycle event waiting for dependency", "module", "lifecycle", "operation", stage, "error", processErr, "bk_tenant_id", signal.BKTenantID, "event_source_id", signal.EventSourceID, "event_id", eventID, "mailbox_id", signal.MailboxID)
			return processed, last, false, processErr, nil
		}
		if ctx.Err() != nil {
			return processed, last, false, nil, ctx.Err()
		}
		h.observer.EventProcessed(ctx, signal.EventSourceID, eventAction(stored.Event.Evaluations), result, processErr)
		if processErr != nil {
			reason := "processing_failed"
			if errors.Is(processErr, processingerror.ErrPanic) {
				reason = "handler_panic"
			} else if processingerror.Retryable(processErr) && attempts >= processingerror.MaxAttempts {
				reason = "retry_exhausted"
			}
			h.logger.WarnContext(ctx, "discard lifecycle event", "module", "lifecycle", "operation", stage, "bk_tenant_id", signal.BKTenantID, "event_source_id", signal.EventSourceID,
				"event_id", eventID, "mailbox_id", signal.MailboxID, "reason_code", reason, "attempts", attempts,
				"stage", stage, "error", processErr)
			h.recordFailure(ctx, signal.BKTenantID, eventID, reason)
			h.observer.MailboxOperation(ctx, signal.EventSourceID, "process", "discarded")
		} else {
			h.observer.MailboxOperation(ctx, signal.EventSourceID, "process", "succeeded")
		}
		if ctx.Err() != nil {
			return processed, last, false, nil, ctx.Err()
		}
		if ackErr := h.mailbox.AckHead(ctx, signal.MailboxID, eventID); ackErr != nil {
			h.logger.WarnContext(ctx, "acknowledge lifecycle mailbox head failed", "module", "lifecycle", "operation", "mailbox_ack", "error", ackErr, "bk_tenant_id", signal.BKTenantID, "event_source_id", signal.EventSourceID, "event_id", eventID, "mailbox_id", signal.MailboxID)
			h.observer.MailboxOperation(ctx, stored.Event.EventSourceID, "ack", "failed")
			return processed, last, false, nil, ackErr
		}
		h.observer.MailboxOperation(ctx, stored.Event.EventSourceID, "ack", "succeeded")
		processed++
		last = result
	}
	return processed, last, false, nil, nil
}

// processMailboxEvent 每次重读 Event 持久检查点；普通临时错误最多三次，控制面/索引等待直接让出调度。
func (h *Handler) processMailboxEvent(ctx context.Context, signal Signal, id string) (stored store.StoredEvent, result lifecycle.ProcessResult, attempts int, stage string, err error) {
	for attempts = 1; attempts <= processingerror.MaxAttempts; attempts++ {
		stage = "event_read"
		stored, err = processingerror.Guard(func() (store.StoredEvent, error) {
			return readLifecycleEvent(ctx, h.eventReader, signal.BKTenantID, id)
		})
		if err == nil {
			stage = "event_identity"
			if stored.Event.EventSourceID != signal.EventSourceID || stored.Event.Fingerprint != signal.Fingerprint {
				err = fmt.Errorf("mailbox event identity mismatch")
			} else {
				stage = "event_process"
				result, err = processingerror.Guard(func() (lifecycle.ProcessResult, error) { return h.processor.ProcessEvent(ctx, stored) })
			}
		}
		if err == nil || processingPending(err) || ctx.Err() != nil || !processingerror.Retryable(err) || attempts == processingerror.MaxAttempts {
			return
		}
		timer := time.NewTimer(time.Duration(attempts) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
	return
}

// 控制面传输故障与索引准备均保留 Mailbox 头并让出 lease，不能按普通三次错误丢弃 Event。
func processingPending(err error) bool {
	var pending interface{ PolicyPreparationPending() bool }
	return errors.Is(err, processingerror.ErrControlPlaneUnavailable) || (errors.As(err, &pending) && pending.PolicyPreparationPending())
}

// recordFailure 尽力保存明确的终态；诊断存储失败不能再次卡住该 Event。
// 失败事件直接终结，终态重投不会重复处理。
func (h *Handler) recordFailure(ctx context.Context, tenant, id, reason string) {
	writer, ok := h.eventReader.(interface {
		CompareAndSetEventResult(context.Context, string, string, store.VersionToken, store.EventResult) (store.StoredEvent, error)
	})
	if !ok {
		return
	}
	_, err := processingerror.Guard(func() (store.StoredEvent, error) {
		for range processingerror.MaxAttempts {
			current, err := h.eventReader.GetEvent(ctx, tenant, id)
			if err != nil || current.Processing.State.Terminal() {
				return current, err
			}
			next, err := writer.CompareAndSetEventResult(ctx, tenant, id, current.Version, store.EventResult{State: domain.EventProcessStateRejected, Outcome: "processing_failed", ReasonCode: reason, ProcessedAt: time.Now().UTC()})
			if !errors.Is(err, store.ErrVersionConflict) {
				return next, err
			}
		}
		return store.StoredEvent{}, store.ErrVersionConflict
	})
	if err != nil {
		h.logger.WarnContext(ctx, "persist discarded event diagnostic failed", "module", "lifecycle", "operation", "persist_rejection", "bk_tenant_id", tenant, "event_id", id, "reason_code", "diagnostic_write_failed", "error", err)
	}
}

func readLifecycleEvent(ctx context.Context, reader EventReader, bkTenantID, eventID string) (store.StoredEvent, error) {
	// 快照更新的首次读取保留来源原文，确保丰富提交的完整文档预算仍然有效。
	if _, ok := reader.(store.LifecycleEventSnapshotStore); ok {
		return reader.GetEvent(ctx, bkTenantID, eventID)
	}
	if lifecycleStore, ok := reader.(store.LifecycleEventStore); ok {
		return lifecycleStore.GetLifecycleEvent(ctx, bkTenantID, eventID)
	}
	return reader.GetEvent(ctx, bkTenantID, eventID)
}

func (h *Handler) renewLoop(ctx context.Context, cancelWork context.CancelFunc, lease Lease, done chan<- error) {
	ticker := time.NewTicker(h.config.RenewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			done <- nil
			return
		case <-ticker.C:
			if err := h.locker.Renew(ctx, lease); err != nil {
				h.observer.LeaseOperation(ctx, "renew", "failed")
				cancelWork()
				done <- err
				return
			}
			h.observer.LeaseOperation(ctx, "renew", "succeeded")
		}
	}
}

var _ consume.Handler = (*Handler)(nil)

// BindSource 在开始消费前固定来源作用域，不允许运行中修改。
func (h *Handler) BindSource(id string) { h.expectedSource = id }

// eventAction 为事件级指标提供有界动作标签，多种动作共存时使用 mixed。
func eventAction(evaluations []domain.EventEvaluation) domain.EventAction {
	if len(evaluations) == 0 {
		return "unknown"
	}
	action := evaluations[0].Action
	for _, evaluation := range evaluations[1:] {
		if evaluation.Action != action {
			return "mixed"
		}
	}
	return action
}
