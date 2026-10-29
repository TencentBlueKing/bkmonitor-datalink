// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	profilingwindow "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/window"
)

// Processor applies Kafka messages and unknown-state transitions through Store.
type Processor struct {
	store   Store
	options Options
	sweeper *profilingwindow.Sweeper
}

// NewProcessor creates an event processor. Zero window and scan options use
// defaults; negative values are invalid.
func NewProcessor(store Store, options Options) (*Processor, error) {
	if store == nil || options.WindowMaxDuration < 0 || options.WindowDelay < 0 ||
		options.UnknownScanInterval < 0 || options.UnknownScanBatchSize < 0 {
		return nil, ErrInvalidOptions
	}
	if options.WindowMaxDuration == 0 {
		options.WindowMaxDuration = DefaultWindowMaxDuration
	}
	if options.WindowDelay == 0 {
		options.WindowDelay = DefaultWindowDelay
	}
	if options.UnknownScanInterval == 0 {
		options.UnknownScanInterval = 10 * time.Second
	}
	if options.UnknownScanBatchSize == 0 {
		options.UnknownScanBatchSize = 500
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Processor{
		store:   store,
		options: options,
		sweeper: profilingwindow.NewSweeper(
			store,
			options.UnknownScanInterval,
			options.UnknownScanBatchSize,
			options.Now,
			options.OnError,
		),
	}, nil
}

// HandleMessage decodes a message and applies its valid Events. If an array
// contains invalid items, valid Events are applied before the decode error is
// returned so callers can skip poison items without dropping the rest.
func (p *Processor) HandleMessage(ctx context.Context, payload []byte) error {
	return p.HandleMessages(ctx, [][]byte{payload})
}

// HandleMessages decodes and applies a batch of messages. Invalid events are
// skipped while valid events are persisted, then reported to the caller.
func (p *Processor) HandleMessages(ctx context.Context, payloads [][]byte) error {
	started := time.Now()
	events, messageErr, err := decodeEvents(payloads)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return messageErr
	}

	nowMS := p.options.Now().UnixMilli()
	policy := WindowPolicy{MaxDuration: p.options.WindowMaxDuration, Delay: p.options.WindowDelay}
	if batchStore, ok := p.store.(BatchStore); ok {
		err := batchStore.ApplyEvents(ctx, events, nowMS, policy)
		if err == nil {
			logger.Debugf("[profiling] Event batch applied event_count=%d cost=%s", len(events), time.Since(started))
			return messageErr
		}
		if !errors.Is(err, ErrInvalidEvent) {
			return err
		}
		// Isolate a storage-level poison event so the rest of the batch can persist.
		if messageErr == nil {
			messageErr = err
		}
	}

	for _, event := range events {
		if err := p.store.ApplyEvent(ctx, event, nowMS, policy); err != nil {
			if !errors.Is(err, ErrInvalidEvent) {
				return err
			}
			if messageErr == nil {
				messageErr = err
			}
			continue
		}
	}
	logger.Debugf("[profiling] Event batch applied event_count=%d cost=%s", len(events), time.Since(started))
	return messageErr
}

func decodeEvents(payloads [][]byte) ([]Event, error, error) {
	var events []Event
	var messageErr error
	for _, payload := range payloads {
		decoded, decodeErr := DecodeMessage(payload)
		logger.Debugf("[profiling] decoded Event message event_count=%d payload_bytes=%d", len(decoded), len(payload))
		if decodeErr != nil {
			if !errors.Is(decodeErr, ErrInvalidEvent) {
				return nil, nil, decodeErr
			}
			if messageErr == nil {
				messageErr = decodeErr
			}
		}
		for _, event := range decoded {
			if err := ValidateEvent(event); err != nil {
				if !errors.Is(err, ErrInvalidEvent) {
					return nil, nil, err
				}
				if messageErr == nil {
					messageErr = err
				}
				continue
			}
			events = append(events, event)
		}
	}
	return events, messageErr, nil
}

// RunUnknownSweeper scans once at startup, then marks running hosts with
// expired windows as unknown at each interval. Transition delay is roughly the
// scan interval plus the time needed to process earlier batches.
func (p *Processor) RunUnknownSweeper(ctx context.Context) error {
	return p.sweeper.Run(ctx)
}

// SweepUnknownNow immediately scans all expired windows. The periodic scanner
// and local mock share this entry point so tests can advance a virtual clock.
func (p *Processor) SweepUnknownNow(ctx context.Context) error {
	return p.sweeper.SweepNow(ctx)
}

// ValidateEvent checks the fields required for a normalized active profiling Event.
func ValidateEvent(event Event) error {
	if event.Name != EventStarted && event.Name != EventFinished {
		return fmt.Errorf("%w: event_name %q", ErrUnsupportedEvent, event.Name)
	}
	if event.TenantID == "" || event.BizID <= 0 || event.TaskID < 0 || event.ScheduledAt <= 0 ||
		event.HostID <= 0 || event.Timestamp <= 0 || event.ScheduleType == "" {
		return ErrInvalidEvent
	}
	if err := validateConfigObject(event.Content.Config); err != nil {
		return err
	}
	for _, target := range event.Content.TargetHosts {
		if target.HostID <= 0 {
			return ErrInvalidEvent
		}
	}
	if event.Name == EventStarted && (event.Status != "" && event.Status != StatusRunning || event.Content.StartedAt <= 0) {
		return ErrInvalidEvent
	}
	if event.Name == EventFinished && (!isTerminalStatus(event.Status) || event.Content.StartedAt <= 0 ||
		event.Content.FinishedAt <= 0 || event.Content.ExecutionDurationMS < 0) {
		return ErrInvalidEvent
	}
	return nil
}
