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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
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
	// under is the worker session the lease was taken under (A2), nil for a
	// lease its holder renews itself. confirmed is when the fence check
	// whose answer set the lease's horizon began.
	under     WorkerSessionSource
	confirmed time.Time
}

// WorkerSessionSource is the worker session this process holds now, if any.
type WorkerSessionSource func() (WorkerSession, bool)

type sessionLeaseAcquirer interface {
	AcquireUnder(context.Context, execution.QueryGroupIdentity, WorkerSession, time.Time, time.Duration) (Lease, error)
}

// OpenSessionUnder takes the Query Group's lease under the worker session
// under names now (A2). With no session held yet it answers ErrLeaseBusy, a
// refusal the caller retries on its next round.
//
// Such a lease has no deadline of its own. Deadline is the earliest of the
// worker session's own deadline -- zero once the session is not the one the
// lease was taken under -- the last confirmation plus the lease's TTL, and the
// effective time of a change that confirmation saw pending for this holder.
// A confirmation is a fence check that answered with the record; one that
// began earlier than the last never replaces it, and its horizon and its cap
// are taken from the one reply together.
func OpenSessionUnder(
	ctx context.Context,
	store LeaseStore,
	queryGroup execution.QueryGroupIdentity,
	under WorkerSessionSource,
	at time.Time,
	ttl time.Duration,
) (*Session, error) {
	acquirer, ok := store.(sessionLeaseAcquirer)
	if store == nil || !ok || under == nil {
		return nil, errors.New("alarmd ownership: a store that holds leases under a worker session is required")
	}
	worker, held := under()
	if !held {
		return nil, ErrNoWorkerSession
	}
	lease, err := acquirer.AcquireUnder(ctx, queryGroup, worker, at, ttl)
	if err != nil {
		return nil, err
	}
	return &Session{store: store, lease: lease, accepting: true, under: under, confirmed: at}, nil
}

// horizonLocked is the lease's deadline as Deadline states it; the caller
// holds mu.
func (session *Session) horizonLocked() time.Time {
	deadline := session.lease.Deadline
	if session.under == nil {
		return deadline
	}
	worker, held := session.under()
	if !held || worker.Epoch != session.lease.SessionEpoch {
		return time.Time{}
	}
	if worker.Deadline.Before(deadline) {
		deadline = worker.Deadline
	}
	return deadline
}

// aliveLocked is whether the lease is still this process's to act on at at:
// for a lease its holder renews, its deadline; for one held under a session,
// the session it was taken under, still held and alive. A lease held under a
// session whose last confirmation has run out is still alive -- its next
// fence check is that confirmation -- and only Deadline stops at the
// confirmation's horizon. The caller holds mu.
func (session *Session) aliveLocked(at time.Time) bool {
	if !session.accepting {
		return false
	}
	if session.under == nil {
		return session.lease.Deadline.After(at)
	}
	worker, held := session.under()
	return held && worker.Epoch == session.lease.SessionEpoch && worker.Deadline.After(at)
}

// Alive reports whether the lease is still this process's to act on at at
// (aliveLocked). A write it guards has to run the fence itself.
func (session *Session) Alive(at time.Time) bool {
	if session == nil {
		return false
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	return session.aliveLocked(at)
}

// SessionEnded reports whether a lease held under a worker session has
// ended with it: the Session stopped accepting, or the worker session it was
// taken under is no longer held -- its own loop let it go -- or is another
// one. A session whose deadline merely passed on this process's clock has not
// ended until that loop says so: a renewal it is about to make may restate
// it, and the lease with it. A lease its holder renews never ends this way.
func (session *Session) SessionEnded() bool {
	if session == nil {
		return true
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	if !session.accepting {
		return true
	}
	if session.under == nil {
		return false
	}
	worker, held := session.under()
	return !held || worker.Epoch != session.lease.SessionEpoch
}

// confirm records what a fence check that began at and answered with record
// says about a lease held under a session: it may act for its TTL from at,
// until a change pending for it takes effect, or, on content the record has
// moved past, not at all. An answer older than the one in hand is dropped
// whole.
func (session *Session) confirm(at time.Time, record AssignmentRecord) {
	if session == nil || session.under == nil {
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if !session.accepting || !at.After(session.confirmed) {
		return
	}
	horizon := at.Add(session.lease.TTL)
	draining := record.DesiredWorkerID != session.lease.Fence.OwnerID
	pending := record.ContentChangePending() && session.declared != record.PendingContentScope
	if draining || pending {
		capAt := at
		if !record.EffectiveAt.IsZero() && !record.ObservedAt.IsZero() {
			capAt = at.Add(record.EffectiveAt.Sub(record.ObservedAt))
		}
		if capAt.Before(horizon) {
			horizon = capAt
		}
	}
	// The content declared is neither the record's nor pending as it: the
	// change it was running toward has been promoted, perhaps with another
	// pending behind it. The fence refuses that content as moved, so nothing
	// it runs may go out, and a cap at the next change would let it.
	if moved := session.declared != "" && record.ContentScope != "" && record.ContentScope != session.declared &&
		!(record.ContentChangePending() && record.PendingContentScope == session.declared); moved {
		horizon = at
	}
	session.confirmed = at
	session.lease.Deadline = horizon
	session.lease.ContentScope = record.ContentScope
	session.lease.PendingContentScope = record.PendingContentScope
	session.lease.EffectiveAt = time.Time{}
	if !record.EffectiveAt.IsZero() && !record.ObservedAt.IsZero() {
		session.lease.EffectiveAt = at.Add(record.EffectiveAt.Sub(record.ObservedAt))
	}
	if record.TimelineRecordRevision > session.lease.TimelineRecordRevision {
		session.lease.TimelineRecordRevision = record.TimelineRecordRevision
	}
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
		// As ValidateCurrentWithAssignment: only the store's answer about the
		// fence ends admission. Failing to reach it keeps the lease it may still
		// hold, and this attempt is refused all the same.
		if IsLeaseDecision(err) {
			session.stopAccepting()
		}
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
	session.confirm(at, record)
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
	lease, alive := session.lease, session.aliveLocked(at)
	session.mu.RUnlock()
	if !alive {
		return Lease{}, stale(ErrStaleFence, lease, at)
	}
	return lease, nil
}

func (session *Session) Renew(ctx context.Context, at time.Time, ttl time.Duration) error {
	if session == nil {
		return ErrStaleFence
	}
	session.mu.RLock()
	lease, accepting, declared, under := session.lease, session.accepting, session.declared, session.under
	session.mu.RUnlock()
	if !accepting {
		return ErrStaleFence
	}
	// A lease held under a session is not renewed: a renewal of it is a
	// confirmation, which moves its horizon on by its TTL.
	if under != nil {
		_, _, err := session.ValidateCurrentWithAssignment(confirmation(ctx), at)
		return err
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
	return session.horizonLocked()
}

// Confirm is a fence check made only to move a lease held under a session on
// by its TTL: the confirmation a long Slot's output asks for before its lease
// runs out (execution.LeaseConfirmer). A lease its holder renews needs none.
func (session *Session) Confirm(ctx context.Context, at time.Time) error {
	if !session.HeldUnderSession() {
		return nil
	}
	_, _, err := session.ValidateCurrentWithAssignment(confirmation(ctx), at)
	return err
}

// confirmation names the one fence check a confirmation makes, on a context of
// its own: the caller's goes on to reads that are not the confirmation's.
func confirmation(ctx context.Context) context.Context {
	return redisfailure.WithCaller(ctx, redisfailure.CallerLeaseConfirmation)
}

// HeldUnderSession reports whether the lease is held under a worker session
// (A2), and so has no renewal of its own to keep.
func (session *Session) HeldUnderSession() bool {
	if session == nil {
		return false
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	return session.under != nil
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
