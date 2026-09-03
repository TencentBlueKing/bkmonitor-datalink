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
	"linkd/internal/lifecycle"
	"linkd/internal/store"
)

type EventProcessor interface {
	ProcessEvent(ctx context.Context, bkTenantID, eventID string) (lifecycle.ProcessResult, error)
}

type EventReader interface {
	GetEvent(ctx context.Context, bkTenantID, eventID string) (store.StoredEvent, error)
}

type Mailbox interface {
	Peek(ctx context.Context, mailboxID string) (string, error)
	AckHead(ctx context.Context, mailboxID, eventID string) error
}

type Logger interface {
	InfoContext(ctx context.Context, message string, args ...any)
	WarnContext(ctx context.Context, message string, args ...any)
}

// Handler 按 Signal 指向的 Mailbox 获取跨进程 lease，并有界连续处理队首 Event。
type Handler struct {
	eventReader EventReader
	mailbox     Mailbox
	processor   EventProcessor
	locker      Locker
	config      Config
	logger      Logger
	observer    Observer
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

func (h *Handler) Handle(ctx context.Context, message consume.Message) consume.Outcome {
	signal, err := DecodeSignal(message.Body)
	if err != nil {
		h.logger.WarnContext(ctx, "discard invalid lifecycle mailbox signal", "message_id", message.ID, "reason_code", "invalid_signal")
		return consume.Discard(fmt.Errorf("invalid lifecycle mailbox signal: %w", err))
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
		return consume.Retry(err, 0)
	}
	h.observer.LeaseOperation(ctx, "acquire", "succeeded")

	workCtx, cancelWork := context.WithCancel(ctx)
	renewCtx, cancelRenew := context.WithCancel(ctx)
	renewDone := make(chan error, 1)
	go h.renewLoop(renewCtx, cancelWork, lease, renewDone)

	processed, lastResult, complete, blockErr, processErr := h.drain(workCtx, signal)
	cancelRenew()
	renewErr := <-renewDone
	cancelWork()

	releaseCtx, releaseCancel := context.WithTimeout(context.Background(), h.config.ReleaseTimeout)
	releaseErr := h.locker.Release(releaseCtx, lease)
	releaseCancel()
	if releaseErr != nil {
		h.observer.LeaseOperation(ctx, "release", "failed")
		h.logger.WarnContext(ctx, "release lifecycle mailbox lease failed", "mailbox_id", signal.MailboxID, "reason_code", "lease_release_failed")
	}
	if releaseErr == nil {
		h.observer.LeaseOperation(ctx, "release", "succeeded")
	}
	if renewErr != nil {
		return consume.Retry(renewErr, 0)
	}
	if blockErr != nil {
		return consume.Block(blockErr)
	}
	if processErr != nil {
		return consume.Retry(processErr, 0)
	}
	if complete {
		eventSourceID := ""
		if processed > 0 {
			eventSourceID = signal.EventSourceID
		}
		h.observer.MailboxDrained(ctx, eventSourceID, "completed", processed)
		h.logger.InfoContext(ctx, "lifecycle mailbox drained", "mailbox_id", signal.MailboxID, "processed", processed,
			"event_id", lastResult.EventID, "alert_id", lastResult.AlertID, "outcome", lastResult.Outcome)
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
			h.observer.MailboxOperation(ctx, "", "peek", "failed")
			return processed, last, false, nil, peekErr
		}
		h.observer.MailboxOperation(ctx, "", "peek", "succeeded")
		if eventID == "" {
			return processed, last, true, nil, nil
		}
		stored, readErr := h.eventReader.GetEvent(ctx, signal.BKTenantID, eventID)
		if readErr != nil {
			return processed, last, false, nil, fmt.Errorf("read mailbox event %q: %w", eventID, readErr)
		}
		if stored.Event.EventSourceID != signal.EventSourceID || stored.Event.Fingerprint != signal.Fingerprint {
			return processed, last, false, fmt.Errorf("mailbox %q contains event %q with mismatched identity", signal.MailboxID, eventID), nil
		}
		result, processErr := h.processor.ProcessEvent(ctx, signal.BKTenantID, eventID)
		h.observer.EventProcessed(ctx, stored.Event.EventSourceID, stored.Event.Action, result, processErr)
		if processErr != nil {
			h.observer.MailboxOperation(ctx, stored.Event.EventSourceID, "process", "failed")
			return processed, last, false, nil, processErr
		}
		h.observer.MailboxOperation(ctx, stored.Event.EventSourceID, "process", "succeeded")
		if ackErr := h.mailbox.AckHead(ctx, signal.MailboxID, eventID); ackErr != nil {
			h.observer.MailboxOperation(ctx, stored.Event.EventSourceID, "ack", "failed")
			return processed, last, false, nil, ackErr
		}
		h.observer.MailboxOperation(ctx, stored.Event.EventSourceID, "ack", "succeeded")
		processed++
		last = result
	}
	return processed, last, false, nil, nil
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
