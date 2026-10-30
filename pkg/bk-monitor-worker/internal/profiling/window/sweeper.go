// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package window

import (
	"context"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// ExpiryStore persists host status changes when deadlines expire.
type ExpiryStore interface {
	MarkUnknown(context.Context, int64, int) (int, error)
}

// Sweeper schedules expiration scans for persisted host windows.
type Sweeper struct {
	store     ExpiryStore
	interval  time.Duration
	batchSize int
	now       func() time.Time
	onError   func(error)
}

// NewSweeper creates a scheduler for persisted host-window deadlines.
func NewSweeper(
	store ExpiryStore,
	interval time.Duration,
	batchSize int,
	now func() time.Time,
	onError func(error),
) *Sweeper {
	return &Sweeper{
		store:     store,
		interval:  interval,
		batchSize: batchSize,
		now:       now,
		onError:   onError,
	}
}

// Run scans immediately on startup, then repeats at the configured interval.
func (s *Sweeper) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	logger.Infof("[profiling] host window lifecycle started interval=%s batch_size=%d", s.interval, s.batchSize)
	defer logger.Infof("[profiling] host window lifecycle stopped")
	for {
		if err := s.SweepNow(ctx); err != nil && ctx.Err() == nil {
			s.reportError(fmt.Errorf("scan expired host windows: %w", err))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// SweepNow drains all due-window batches until no more host states change.
func (s *Sweeper) SweepNow(ctx context.Context) error {
	totalChanged := 0
	for {
		changed, err := s.store.MarkUnknown(ctx, s.now().UnixMilli(), s.batchSize)
		if err != nil {
			return err
		}
		if changed == 0 {
			if totalChanged > 0 {
				logger.Infof("[profiling] host window scan complete expired=%d", totalChanged)
			} else {
				logger.Debugf("[profiling] host window scan complete expired=0")
			}
			return nil
		}
		totalChanged += changed
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

func (s *Sweeper) reportError(err error) {
	if s.onError != nil {
		s.onError(err)
		return
	}
	logger.Errorf("[profiling] %s", err)
}
