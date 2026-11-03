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

// LeaseStore is what a Session needs of the ownership store. The instants
// on Acquire and Renew are the caller's anchors for the Lease it gets back
// (see Lease); whether a lease is live is decided by the store on its own
// clock, which is why CheckFence takes none.
type LeaseStore interface {
	Acquire(context.Context, execution.QueryGroupIdentity, string, time.Time, time.Duration) (Lease, error)
	Renew(context.Context, execution.OwnerFence, time.Time, time.Duration) (Lease, error)
	// RenewDeclaring is Renew saying which content scope the holder runs
	// now; see RedisStore.RenewDeclaring.
	RenewDeclaring(context.Context, execution.OwnerFence, string, time.Time, time.Duration) (Lease, error)
	CheckFence(context.Context, execution.OwnerFence) error
	// CheckFenceWithAssignment is the fence check plus the Assignment record it
	// already had to consult, in one round trip. It is on the interface rather
	// than behind a type assertion because a Session reaches its store only
	// through this interface: an assertion would let a store without it fall
	// back to the two-round-trip path silently, which is exactly the regression
	// this method exists to prevent, and it would do so with no compile error
	// and no failing test.
	CheckFenceWithAssignment(context.Context, execution.OwnerFence) (AssignmentRecord, error)
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
	// declared is the content scope the Query Group's Slot in flight runs,
	// as its Runner noted it; renewals carry it, so a holder already on a
	// pending change's content is not capped at the change.
	declared string
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

// ValidateCurrent is the two halves of a fence validation: this Session
// still admits work on its lease at the caller's instant (the local half,
// on the holder's clock), and the store still confirms the lease (the
// store's half, on the store's clock).
func (session *Session) ValidateCurrent(ctx context.Context, at time.Time) (execution.OwnerFence, error) {
	lease, err := session.admittedLease(at)
	if err != nil {
		return execution.OwnerFence{}, err
	}
	if err := session.store.CheckFence(ctx, lease.Fence); err != nil {
		session.stopAccepting()
		return execution.OwnerFence{}, err
	}
	return lease.Fence, nil
}

// ValidateCurrentWithAssignment answers ValidateCurrent and, from the same
// store round trip, hands back the Assignment record naming the fence owner.
// A caller that needs both -- the scheduler does, before every attempt -- can
// then act on two facts taken at one instant instead of stitching together two
// readings taken at two.
func (session *Session) ValidateCurrentWithAssignment(
	ctx context.Context,
	at time.Time,
) (execution.OwnerFence, AssignmentRecord, error) {
	lease, err := session.admittedLease(at)
	if err != nil {
		return execution.OwnerFence{}, AssignmentRecord{}, err
	}
	record, err := session.store.CheckFenceWithAssignment(ctx, lease.Fence)
	if err != nil {
		// Only an authoritative answer about the fence ends admission here.
		// This call can also fail for reasons that say nothing about the lease:
		// an Assignment record the store read but could not accept, or a caller
		// asking for a record on an identity that carries none. Ending the
		// Session on those would turn one bad record into a rebuild loop -- the
		// Runner is stopped, the control plane reconciles a new Session, it
		// reads the same record and dies again, once per reconcile -- and would
		// report each of those attempts as an ownership rejection, which is
		// precisely what that outcome is supposed to distinguish. Renewal draws
		// the same line for the same reason.
		if IsLeaseDecision(err) {
			session.stopAccepting()
		}
		return execution.OwnerFence{}, AssignmentRecord{}, err
	}
	return lease.Fence, record, nil
}

// admittedLease is the local half of a fence validation: the lease this Session
// still admits work on, or the reason it no longer does. It is separate so the
// two validation entry points cannot drift on when a Session stops admitting,
// which is a correctness rule and not a property of what the caller asked for.
func (session *Session) admittedLease(at time.Time) (Lease, error) {
	if session == nil {
		return Lease{}, ErrStaleFence
	}
	session.mu.RLock()
	lease, accepting := session.lease, session.accepting
	session.mu.RUnlock()
	if !accepting || !lease.Deadline.After(at) {
		return Lease{}, stale(ErrStaleFence, lease, at)
	}
	return lease, nil
}

func (session *Session) Renew(ctx context.Context, at time.Time, ttl time.Duration) error {
	if session == nil {
		return ErrStaleFence
	}
	session.mu.RLock()
	lease, accepting, declared := session.lease, session.accepting, session.declared
	session.mu.RUnlock()
	if !accepting {
		return ErrStaleFence
	}
	renewed, err := session.store.RenewDeclaring(ctx, lease.Fence, declared, at, ttl)
	if err != nil {
		err = stale(err, lease, at)
		// An authoritative store decision or a lease whose deadline has
		// already passed ends admission here. A failure to reach the store
		// while the lease is still inside its TTL does not: the caller
		// retries, and every side effect keeps validating the fence against
		// the store through ValidateCurrent, so nothing runs on a lease the
		// store no longer confirms.
		if IsLeaseDecision(err) || !lease.Deadline.After(at) {
			session.stopAccepting()
		}
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

// Deadline is the instant, on this process's clock, until which the Session
// admits work on its lease: zero once it has stopped accepting - released,
// or its renewal refused by the store - whatever the last renewal had left.
// It is the authority the output sink admits a batch against
// (execution.LeaseAuthority), and a Slot can outlive its lease: a hung
// execution whose Query Group was declined, or one running when the store
// refused the renewal. Answering the stale deadline there admitted that
// Slot's output while the next owner could already hold the lease, and the
// next owner sent the same events again; admittedLease already refused the
// same Session its fence check.
func (session *Session) Deadline() time.Time {
	if session == nil {
		return time.Time{}
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	if !session.accepting {
		return time.Time{}
	}
	return session.lease.Deadline
}

// NoteContentScope records the content scope the Query Group's Slot about to
// run declares: the scope of the Segment it was frozen from, empty for a Slot
// of a Segment that has closed. One Slot of a Query Group runs at a time, so
// this is the content the holder runs until the next note, and a renewal that
// carries it is not capped at a pending change it already runs.
func (session *Session) NoteContentScope(scope string) {
	if session == nil {
		return
	}
	session.mu.Lock()
	session.declared = scope
	session.mu.Unlock()
}

// LeaseEndedAtContentSwitch is a Session's lease running out at a pending
// content change's effective time: the cap the store puts on a holder that
// was still on the old content when the change fell due. It is a stale
// fence, and every branch that asks errors.Is(err, ErrStaleFence) takes it
// as one - the holder lets the Query Group go and takes it again on the new
// content. It is named apart for the reading only: RefusalReason calls it
// CONTENT_SCOPE_MOVED, so a content publication does not read as a lost
// lease (control-plane review, deviation 9).
type LeaseEndedAtContentSwitch struct{ Err error }

func (err *LeaseEndedAtContentSwitch) Error() string {
	return "alarmd ownership: the lease ended at a content switch: " + err.Err.Error()
}

func (err *LeaseEndedAtContentSwitch) Unwrap() error { return err.Err }

// endedAtContentSwitch says lease was capped at a pending content change and
// has run out there by at, on this process's clock.
func endedAtContentSwitch(lease Lease, at time.Time) bool {
	return lease.ContentChangePending() && !lease.Deadline.After(lease.EffectiveAt) && !lease.Deadline.After(at)
}

// stale is the stale fence a Session answers for lease at at: named as the
// content switch when that is where the lease ran out.
func stale(err error, lease Lease, at time.Time) error {
	if errors.Is(err, ErrStaleFence) && endedAtContentSwitch(lease, at) {
		return &LeaseEndedAtContentSwitch{Err: err}
	}
	return err
}

// Current is the lease as the last acquire or renewal left it, and whether
// the session is still accepting. It is the record's side of the Worker's
// executable-view check (decision-016 batch 4): the content scope and the
// timeline revision the renewal brought back, read here rather than from
// the view that previews them.
func (session *Session) Current() (Lease, bool) {
	if session == nil {
		return Lease{}, false
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	return session.lease, session.accepting
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
