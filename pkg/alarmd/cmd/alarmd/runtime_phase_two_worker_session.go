// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// workerSessionStore is the session half of the ownership store
// (ownership.RedisStore). A store without it holds no session.
type workerSessionStore interface {
	AcquireWorkerSession(context.Context, string, time.Time, time.Duration) (ownership.WorkerSession, error)
	RenewWorkerSession(context.Context, ownership.WorkerSession, time.Time, time.Duration) (ownership.WorkerSession, error)
	ReleaseWorkerSession(context.Context, ownership.WorkerSession) error
}

// phaseTwoWorkerSessionMaintainer is the ownership runtime that holds this
// process's worker session (design 012 third batch, A2): one lease per worker
// process, renewed on its own goroutine as the Query Group leases are, and
// released last at shutdown so the Control Leader reads its holder as gone at
// once rather than after the TTL.
type phaseTwoWorkerSessionMaintainer interface {
	MaintainWorkerSession(ctx context.Context, workerID string, interval, ttl time.Duration) error
	ReleaseWorkerSession(context.Context) error
}

// phaseTwoWorkerSessionOpener takes the worker session at startup, before
// the startup Assignment opens a Query Group.
type phaseTwoWorkerSessionOpener interface {
	OpenWorkerSessionAtStartup(ctx context.Context, workerID string, interval, ttl time.Duration)
}

// The production runtime holds the session; a wiring that lost the method
// would leave the worker with none and no failing build.
var (
	_ phaseTwoWorkerSessionMaintainer = (*productionPhaseTwoOwnership)(nil)
	_ phaseTwoWorkerSessionOpener     = (*productionPhaseTwoOwnership)(nil)
)

// workerSessionHolder is the session this process holds, nil-safe and shared
// by the loop that renews it and the shutdown that releases it.
type workerSessionHolder struct {
	mu      sync.Mutex
	session ownership.WorkerSession
	held    bool
	// started is set once this process maintains a session, from then on
	// the session its Query Group leases are taken under.
	started bool
}

func (holder *workerSessionHolder) start() {
	holder.mu.Lock()
	defer holder.mu.Unlock()
	holder.started = true
}

func (holder *workerSessionHolder) running() bool {
	holder.mu.Lock()
	defer holder.mu.Unlock()
	return holder.started
}

func (holder *workerSessionHolder) set(session ownership.WorkerSession, held bool) {
	holder.mu.Lock()
	defer holder.mu.Unlock()
	holder.session, holder.held = session, held
}

func (holder *workerSessionHolder) current() (ownership.WorkerSession, bool) {
	holder.mu.Lock()
	defer holder.mu.Unlock()
	return holder.session, holder.held
}

// MaintainWorkerSession acquires the worker's session and renews it every
// interval until ctx ends. A session it loses -- refused, or past its
// deadline with no renewal through -- is never renewed again: the loop
// acquires a new one, under a new epoch, as soon as the store grants it
// (while the lost one is still alive in the store's eyes, BUSY). Every
// attempt, acquisition or renewal, is a lease_renewed line on the session's
// identity.
func (runtime *productionPhaseTwoOwnership) MaintainWorkerSession(ctx context.Context, workerID string, interval, ttl time.Duration) error {
	if runtime == nil || workerID == "" || interval <= 0 || ttl <= interval {
		return errors.New("phase-two worker session cadence is invalid")
	}
	store, ok := runtime.dependencies.Store.(workerSessionStore)
	if !ok {
		return nil
	}
	runtime.workerSession.start()
	leaseCtx := observability.ContextWithTraceFields(ctx, observability.TraceFields{
		QueryGroupKey: string(ownership.WorkerSessionIdentity(workerID)), OwnerID: workerID,
	})
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		session, held := runtime.workerSession.current()
		if !held {
			acquired, err := store.AcquireWorkerSession(ctx, workerID, runtime.dependencies.Now(), ttl)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			observeProductionOwnership(leaseCtx, runtime.dependencies.Observer, observability.StageLeaseRenewed, err)
			if err == nil {
				runtime.workerSession.set(acquired, true)
			}
		} else {
			var renewed ownership.WorkerSession
			err := renewPhaseTwoWithinInterval(ctx, interval, func(attemptCtx context.Context) error {
				var renewErr error
				renewed, renewErr = store.RenewWorkerSession(attemptCtx, session, runtime.dependencies.Now(), ttl)
				return renewErr
			}, func(err error) {
				observeProductionRenewalFailure(leaseCtx, runtime.dependencies.Observer, observability.StageLeaseRenewed, err)
			}, func() bool {
				return session.Deadline.After(runtime.dependencies.Now())
			})
			switch {
			case ctx.Err() != nil:
				return ctx.Err()
			case err == nil:
				observeProductionOwnership(leaseCtx, runtime.dependencies.Observer, observability.StageLeaseRenewed, nil)
				runtime.workerSession.set(renewed, true)
			case ownership.IsLeaseDecision(err) || !session.Deadline.After(runtime.dependencies.Now()):
				if ownership.IsLeaseDecision(err) {
					observeProductionOwnership(leaseCtx, runtime.dependencies.Observer, observability.StageLeaseRenewed, err)
				}
				runtime.workerSession.set(ownership.WorkerSession{}, false)
				continue
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// OpenWorkerSessionAtStartup makes every Query Group lease this process takes
// one held under its worker session, the first ones too: the startup
// Assignment opens its Query Groups before the session's loop has run, and a
// lease opened then was taken as one its holder renews itself, and stayed one.
// It asks for the session once. One it cannot have now -- the last process of
// this worker id still alive in the store's eyes, as after a container
// restart, or the store not answering -- is the loop's to take, and the Query
// Groups wait for it rather than open without it. The cadence is checked as
// the loop checks it, so a session that will never be maintained is never
// waited for.
func (runtime *productionPhaseTwoOwnership) OpenWorkerSessionAtStartup(ctx context.Context, workerID string, interval, ttl time.Duration) {
	if runtime == nil || workerID == "" || interval <= 0 || ttl <= interval {
		return
	}
	store, ok := runtime.dependencies.Store.(workerSessionStore)
	if !ok {
		return
	}
	runtime.workerSession.start()
	if _, held := runtime.workerSession.current(); held {
		return
	}
	leaseCtx := observability.ContextWithTraceFields(ctx, observability.TraceFields{
		QueryGroupKey: string(ownership.WorkerSessionIdentity(workerID)), OwnerID: workerID,
	})
	acquired, err := store.AcquireWorkerSession(ctx, workerID, runtime.dependencies.Now(), ttl)
	observeProductionOwnership(leaseCtx, runtime.dependencies.Observer, observability.StageLeaseRenewed, err)
	if err == nil {
		runtime.workerSession.set(acquired, true)
	}
}

// ReleaseWorkerSession ends the session this process holds, if any.
func (runtime *productionPhaseTwoOwnership) ReleaseWorkerSession(ctx context.Context) error {
	if runtime == nil {
		return nil
	}
	session, held := runtime.workerSession.current()
	store, ok := runtime.dependencies.Store.(workerSessionStore)
	if !held || !ok {
		return nil
	}
	runtime.workerSession.set(ownership.WorkerSession{}, false)
	if err := store.ReleaseWorkerSession(ctx, session); err != nil && !errors.Is(err, ownership.ErrStaleFence) {
		return err
	}
	return nil
}
