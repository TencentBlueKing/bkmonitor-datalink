// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package ownership

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

type LeaseStore interface {
	Acquire(context.Context, execution.QueryGroupIdentity, string, time.Time, time.Duration) (Lease, error)
	Renew(context.Context, execution.OwnerFence, time.Time, time.Duration) (Lease, error)
	CheckFence(context.Context, execution.OwnerFence, time.Time) error
	Release(context.Context, execution.OwnerFence) error
}

// Session owns one Query Group lease. Maintain is intentionally a blocking
// loop so the runtime can run it independently from UQ query goroutines.
type Session struct {
	store LeaseStore

	mu        sync.RWMutex
	lease     Lease
	accepting bool
	released  bool
}

func OpenSession(
	ctx context.Context,
	store LeaseStore,
	queryGroup execution.QueryGroupIdentity,
	workerID string,
	at time.Time,
	ttl time.Duration,
) (*Session, error) {
	if store == nil {
		return nil, errors.New("alarmd ownership: lease store is required")
	}
	lease, err := store.Acquire(ctx, queryGroup, workerID, at, ttl)
	if err != nil {
		return nil, err
	}
	return &Session{store: store, lease: lease, accepting: true}, nil
}

func (session *Session) ValidateCurrent(ctx context.Context, at time.Time) (execution.OwnerFence, error) {
	if session == nil {
		return execution.OwnerFence{}, ErrStaleFence
	}
	session.mu.RLock()
	lease, accepting := session.lease, session.accepting
	session.mu.RUnlock()
	if !accepting || !lease.Deadline.After(at) {
		return execution.OwnerFence{}, ErrStaleFence
	}
	if err := session.store.CheckFence(ctx, lease.Fence, at); err != nil {
		session.stopAccepting()
		return execution.OwnerFence{}, err
	}
	return lease.Fence, nil
}

func (session *Session) Renew(ctx context.Context, at time.Time, ttl time.Duration) error {
	if session == nil {
		return ErrStaleFence
	}
	session.mu.RLock()
	lease, accepting := session.lease, session.accepting
	session.mu.RUnlock()
	if !accepting {
		return ErrStaleFence
	}
	renewed, err := session.store.Renew(ctx, lease.Fence, at, ttl)
	if err != nil {
		session.stopAccepting()
		return err
	}
	session.mu.Lock()
	if session.accepting && session.lease.Fence == lease.Fence {
		session.lease = renewed
	}
	session.mu.Unlock()
	return nil
}

func (session *Session) Maintain(ctx context.Context, interval, ttl time.Duration) error {
	if session == nil || interval <= 0 || ttl <= interval {
		return errors.New("alarmd ownership: invalid lease maintenance parameters")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			session.stopAccepting()
			return ctx.Err()
		case at := <-ticker.C:
			if err := session.Renew(ctx, at, ttl); err != nil {
				return err
			}
		}
	}
}

func (session *Session) Deadline() time.Time {
	if session == nil {
		return time.Time{}
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	return session.lease.Deadline
}

func (session *Session) Release(ctx context.Context) error {
	if session == nil {
		return nil
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.released {
		return nil
	}
	session.accepting = false
	err := session.store.Release(ctx, session.lease.Fence)
	if err == nil || errors.Is(err, ErrStaleFence) {
		session.released = true
		return nil
	}
	return err
}

func (session *Session) stopAccepting() {
	session.mu.Lock()
	session.accepting = false
	session.mu.Unlock()
}
