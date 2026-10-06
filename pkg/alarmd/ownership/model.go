// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

// Package ownership owns the phase-two worker registry, Assignment facts and
// Redis-backed fencing leases. It deliberately carries no query, algorithm or
// Runtime State semantics.
package ownership

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

var (
	ErrNotDesired         = errors.New("alarmd ownership: worker is not the desired owner")
	ErrLeaseBusy          = errors.New("alarmd ownership: lease is held by another owner")
	ErrStaleFence         = errors.New("alarmd ownership: owner fence is stale")
	ErrAssignmentAbsent   = errors.New("alarmd ownership: assignment is absent")
	ErrAssignmentConflict = errors.New("alarmd ownership: Assignment record revision conflict")
	// ErrContentScopeMoved is the fence refusing a writer that declared the
	// content scope it is executing when the Assignment record names another.
	// The lease itself may be perfectly good: it is the view that is behind.
	// It is therefore not a lease decision -- a worker that meets it keeps
	// the Query Group and has to bring its view up to the record, not give
	// the Query Group up.
	ErrContentScopeMoved = errors.New("alarmd ownership: content scope has moved")
)

// IsLeaseDecision reports whether err is an authoritative answer from the
// lease store about the fence (stale, not desired, held by another owner)
// rather than a failure to reach the store. Renewal loops retry only the
// latter while the lease is still inside its TTL.
func IsLeaseDecision(err error) bool {
	return errors.Is(err, ErrStaleFence) || errors.Is(err, ErrNotDesired) || errors.Is(err, ErrLeaseBusy)
}

const ControlLeaderIdentity execution.QueryGroupIdentity = "alarmd-control-leader"

type AssignmentReadiness string

const (
	WorkerStarting AssignmentReadiness = "STARTING"
	WorkerReady    AssignmentReadiness = "READY"
	WorkerDraining AssignmentReadiness = "DRAINING"
)

type DependencyStatus string

const (
	DependencyHealthy  DependencyStatus = "HEALTHY"
	DependencyDegraded DependencyStatus = "DEGRADED"
)

type WorkerRegistration struct {
	WorkerID            string              `json:"worker_id"`
	AssignmentReadiness AssignmentReadiness `json:"assignment_readiness"`
	DependencyStatus    DependencyStatus    `json:"dependency_status"`
	DeploymentProfile   string              `json:"deployment_profile"`
	CapabilitiesDigest  string              `json:"capabilities_digest"`
	ExpiresAt           time.Time           `json:"expires_at"`
	// Capabilities names the control contracts this binary takes part in,
	// by word (CapabilityContentScope, ...). A leader writes a contract's
	// facts only once every ready worker declares it: the digest above says
	// two workers are alike, this says what a worker can do, and a leader
	// deciding whether to start a contract needs the second. Absent from a
	// registration written by a binary from before it, which declares
	// nothing -- the answer a rollout needs.
	Capabilities []string `json:"capabilities,omitempty"`
	// Endpoint is where this worker's control stream is served (host:port
	// of its HTTP listener; the stream shares it), and StreamToken the
	// secret a Worker opening a stream to this worker as Leader must
	// present -- written by the worker into its own registration, so the
	// registry is the trust root (decision-016). Both are absent from a
	// registration written by a binary from before the stream, which
	// neither serves nor joins it. The token is never logged.
	Endpoint    string `json:"endpoint,omitempty"`
	StreamToken string `json:"stream_token,omitempty"`
	// Applied and Load are the worker's acknowledgement and occupancy as of
	// this heartbeat. Both are optional: a registration written by a worker
	// that does not report them decodes with nil, which a reader takes as
	// unknown and never as lagging or idle. Nothing in routing reads them.
	Applied *AppliedControlFacts `json:"applied,omitempty"`
	Load    *WorkerLoad          `json:"load,omitempty"`
}

// AppliedControlFacts says which version of each versioned control fact the
// worker executes by. ActivationRecordRevision is the record revision of the
// Activation it last parsed, zero before it parsed one. SettingsVersion is
// reserved for settings published by the control plane and stays empty until
// they exist.
type AppliedControlFacts struct {
	ActivationRecordRevision uint64 `json:"activation_record_revision"`
	SettingsVersion          string `json:"settings_version,omitempty"`
}

// WorkerLoad is the worker's own occupancy at the heartbeat, copied from the
// capacity its fleet snapshot already reports; it adds no measurement. It is
// written for a scheduler that will weigh it later, and read by nothing yet.
type WorkerLoad struct {
	OwnedQueryGroups int     `json:"owned_query_groups"`
	PermitsHeld      int     `json:"permits_held"`
	PermitBudget     int     `json:"permit_budget"`
	PermitSeconds    float64 `json:"permit_seconds"`
	Waiting          int     `json:"waiting"`
	MemoryUsedBytes  uint64  `json:"memory_used_bytes,omitempty"`
	MemoryLimitBytes uint64  `json:"memory_limit_bytes,omitempty"`
	// RetainedPoolBytes is this Worker's retained-byte pool, the limit its
	// Slots' retained bytes are held under, as the Worker derived it: what
	// the Control Leader judges the byte constraint against (decision-020
	// section 5.7). Zero from a Worker that does not report it, which the
	// Leader reads as "not judged", not as "no pool".
	RetainedPoolBytes uint64 `json:"retained_pool_bytes,omitempty"`
}

func (load *WorkerLoad) validate() error {
	if load == nil {
		return nil
	}
	if load.OwnedQueryGroups < 0 || load.PermitsHeld < 0 || load.PermitBudget < 0 || load.Waiting < 0 ||
		load.PermitSeconds < 0 || load.PermitSeconds != load.PermitSeconds {
		return errors.New("alarmd ownership: invalid worker load")
	}
	return nil
}

// WorkerCompatibility is the static deployment contract used before
// Rendezvous routing. It deliberately excludes runtime health and Worker
// identity so dependency degradation does not cause ownership churn.
type WorkerCompatibility struct {
	DeploymentProfile  string
	CapabilitiesDigest string
}

func (compatibility WorkerCompatibility) Validate() error {
	if compatibility.DeploymentProfile == "" || compatibility.CapabilitiesDigest == "" {
		return errors.New("alarmd ownership: incomplete worker compatibility")
	}
	return nil
}

func (worker WorkerRegistration) Compatibility() WorkerCompatibility {
	return WorkerCompatibility{
		DeploymentProfile: worker.DeploymentProfile, CapabilitiesDigest: worker.CapabilitiesDigest,
	}
}

func (worker WorkerRegistration) Validate() error {
	if worker.WorkerID == "" || worker.ExpiresAt.IsZero() || worker.Compatibility().Validate() != nil {
		return errors.New("alarmd ownership: incomplete worker registration")
	}
	switch worker.AssignmentReadiness {
	case WorkerStarting, WorkerReady, WorkerDraining:
	default:
		return errors.New("alarmd ownership: invalid assignment readiness")
	}
	switch worker.DependencyStatus {
	case DependencyHealthy, DependencyDegraded:
	default:
		return errors.New("alarmd ownership: invalid dependency status")
	}
	return worker.Load.validate()
}

// ControlLeader is who holds the control leader lease, as the lease hash
// names it: the worker id and the term. Read by a Worker looking for the
// Leader's stream endpoint; the lease token is not part of it.
type ControlLeader struct {
	OwnerID    string
	OwnerEpoch uint64
}

// CapabilityContentScope is the decision-016 content contract: a worker
// that declares it names the content each Slot runs under on every fenced
// write, and follows a pending content change from its renewal replies. A
// leader writes content scopes into Assignment records only once every
// ready worker declares it; until then, and again if one that does not
// joins, the records carry no scope and the fence compares what it always
// compared. The gate is about leaders as much as workers: a leader from
// before this contract moves an owner without clearing a pending scope,
// and the fleet it could be elected from is the ready set.
const CapabilityContentScope = "content-scope.v1"

// CapabilityShardAware is the decision-020 split contract (section 4.7.7): a
// worker that declares it indexes activations by (Plan, shard index) and
// executes a strategy split into pieces; one that does not refuses an
// activation naming the same Plan twice, whole, and executes nothing until
// the split is withdrawn. A Leader publishes a split only while every ready
// worker declares it, and withdraws every split to one piece when one that
// does not joins: a rollback is the ordinary case, and a rollback into a
// split fleet without this would stop every rolled-back replica at once.
// The gate is a runtime fact, not a release discipline.
const CapabilityShardAware = "shard-aware.v1"

// ShardSplitHeld is the word for a split the Leader was asked for and did not
// publish because a ready worker does not declare CapabilityShardAware. It
// names the replica; the count of them on a fleet that asked for no split
// is zero, which is the reading a rollout is judged by.
const ShardSplitHeld = "SHARD_SPLIT_HELD"

// ShardSplitGate is the split contract's answer for one ready set.
type ShardSplitGate struct {
	// Admitted says every ready worker declares the contract, so a split
	// may be published. False for an empty set: a split is admitted for a
	// fleet, not for nobody.
	Admitted bool
	// Ready is how many workers were asked; Unaware the ids of those that
	// do not declare the contract, in id order. Unaware is what the page
	// shows while a rollout is in flight and what a rollback puts back.
	Ready   int
	Unaware []string
}

// ShardSplitAdmission decides the gate for a ready set, the same set the
// round's placements use; a registration that has expired is not in it, so
// a replica that is gone does not hold a split.
func ShardSplitAdmission(workers []WorkerRegistration) ShardSplitGate {
	gate := ShardSplitGate{Ready: len(workers)}
	for _, worker := range workers {
		if !worker.Declares(CapabilityShardAware) {
			gate.Unaware = append(gate.Unaware, worker.WorkerID)
		}
	}
	sort.Strings(gate.Unaware)
	gate.Admitted = len(workers) > 0 && len(gate.Unaware) == 0
	return gate
}

// Declares reports whether the registration names the capability.
func (worker WorkerRegistration) Declares(capability string) bool {
	for _, declared := range worker.Capabilities {
		if declared == capability {
			return true
		}
	}
	return false
}

// AllDeclare reports whether every worker in the set names the capability.
// An empty set declares nothing: a contract is started for a fleet, not
// for nobody.
func AllDeclare(workers []WorkerRegistration, capability string) bool {
	if len(workers) == 0 {
		return false
	}
	for _, worker := range workers {
		if !worker.Declares(capability) {
			return false
		}
	}
	return true
}

// PlacementReason says why an Assignment names the worker it names.
// RENDEZVOUS is the sticky hash every Assignment carries today. REBALANCE is
// a move the Control Leader makes to even the owned counts out after the
// ready set changed; nothing writes it yet. Readers accept it one release
// ahead of any writer, so that a rollout never has a new Leader publish a
// record an old Worker refuses.
type PlacementReason string

const (
	PlacementRendezvous PlacementReason = "RENDEZVOUS"
	PlacementRebalance  PlacementReason = "REBALANCE"
	// PlacementByteConstraint is a move the Control Leader makes because
	// the holder's Query Groups' retained-byte peaks summed past its pool's
	// share (decision-020 section 5.7). Accepted by readers first; the
	// writer still says REBALANCE until every reader accepts this word,
	// so a rollout never has a new Leader publish a record an old Worker
	// refuses.
	PlacementByteConstraint PlacementReason = "BYTE_CONSTRAINT"
)

func (reason PlacementReason) valid() bool {
	return reason == PlacementRendezvous || reason == PlacementRebalance || reason == PlacementByteConstraint
}

type AssignmentRecord struct {
	QueryGroup           execution.QueryGroupIdentity
	DesiredWorkerID      string
	AssignmentGeneration uint64
	RecordRevision       uint64
	ControlEpoch         uint64
	PlacementReason      PlacementReason
	// AssignedAt is the leader's own account of when it decided, on the
	// leader's clock, stored as given. It is not on the clock the record's
	// lease deadline and EffectiveAt are on (Redis's, see FenceLua), so a
	// difference between them is a difference between two clocks, not a
	// duration; nothing should compute one.
	AssignedAt time.Time
	// ContentScope names what the desired worker is authorized to execute
	// for this Query Group: the digest of its executable view. Empty until a
	// leader has written one; the fence compares it only against writers
	// that declare theirs (decision-016).
	ContentScope string
	// PendingContentScope and EffectiveAt describe a content change that has
	// been decided but not yet taken effect: the record still authorizes
	// ContentScope, and switches to the pending one at EffectiveAt, which is
	// never earlier than the lease deadline the current holder was renewed
	// to plus ContentSwitchMargin. Renewal does not extend a lease past it.
	// Both are zero when no change is pending. EffectiveAt is on Redis's
	// clock, as the record holds it (FenceLua); a holder that needs it on
	// its own clock reads it from the Lease its renewal returned.
	PendingContentScope string
	EffectiveAt         time.Time
	// TimelineRecordRevision is the revision of this Query Group's Schedule
	// timeline record as the leader last wrote it, copied here so a holder
	// learns it from the same renewal that brings its content scope. The
	// timeline is the authority on which Segment is open; this is the
	// record's word on which timeline that is, and the executable view the
	// holder installed previews the same number (decision-016 batch 4). Zero
	// on a record no leader has written it to yet: a reader treats zero as
	// "not said", never as revision zero, which no timeline has.
	TimelineRecordRevision uint64
}

// ContentChangePending reports whether the record carries a content change
// that has not taken effect yet.
func (record AssignmentRecord) ContentChangePending() bool {
	return record.PendingContentScope != "" && !record.EffectiveAt.IsZero()
}

// AssignmentDecision is a Control Leader decision based on one observed
// Assignment revision. ExpectedRecordRevision is zero only for first publish.
type AssignmentDecision struct {
	QueryGroup             execution.QueryGroupIdentity
	DesiredWorkerID        string
	ExpectedRecordRevision uint64
	PlacementReason        PlacementReason
	DecidedAt              time.Time
	// ContentScope is the executable view this decision authorizes. Empty
	// leaves whatever the record names untouched, so a leader that does not
	// yet compute it publishes exactly as before. A non-empty scope that
	// differs from the record's, for a desired worker that is unchanged and
	// holds a live lease, is written as pending and takes effect after that
	// lease's deadline; with no live lease, or together with a change of
	// desired worker, it is written directly.
	ContentScope string
	// WithdrawContentScope clears the record's scope and any pending change,
	// so the fence compares the lease alone again. It is written directly,
	// not as a pending change: withdrawing the comparison refuses nobody, so
	// there is no holder to protect from it. It is the rollback path of the
	// content contract and what a leader writes when a worker that does not
	// declare the contract joins the fleet. Exclusive with ContentScope.
	WithdrawContentScope bool
	// TimelineRecordRevision, when not zero, is written to the record as the
	// revision of the Query Group's timeline; zero leaves whatever the record
	// holds. A placement names it so a Query Group's first record carries
	// the number the cutover would otherwise have been the only writer of.
	TimelineRecordRevision uint64
}

func (decision AssignmentDecision) Validate() error {
	if decision.QueryGroup == "" || decision.DesiredWorkerID == "" || decision.DecidedAt.IsZero() ||
		!decision.PlacementReason.valid() {
		return errors.New("alarmd ownership: invalid Assignment decision")
	}
	if decision.WithdrawContentScope && decision.ContentScope != "" {
		return errors.New("alarmd ownership: an Assignment decision cannot both name and withdraw a content scope")
	}
	return nil
}

func (record AssignmentRecord) Validate() error {
	if record.QueryGroup == "" || record.DesiredWorkerID == "" || record.AssignmentGeneration == 0 ||
		record.RecordRevision == 0 || record.ControlEpoch == 0 || record.AssignedAt.IsZero() {
		return errors.New("alarmd ownership: incomplete Assignment record")
	}
	if !record.PlacementReason.valid() {
		return fmt.Errorf("alarmd ownership: unsupported placement reason %q", record.PlacementReason)
	}
	return nil
}

// Lease is what a holder was admitted to, expressed on the holder's own
// clock. The store mints deadlines on Redis's clock and judges expiry there
// (FenceLua); what comes back to the holder is the remaining duration, added
// to the instant the holder passed in when it asked -- an instant from before
// the round trip, so the holder's deadline is always a little earlier than
// the server's and never later. Deadline and EffectiveAt are therefore
// comparable with the holder's clock and with nothing else; the same
// instants as Redis holds them are on the AssignmentRecord, which stays on
// Redis's clock.
type Lease struct {
	Fence    execution.OwnerFence
	Deadline time.Time
	// ContentScope is what the Assignment record authorized this lease for
	// at the moment it was acquired or renewed, and PendingContentScope /
	// EffectiveAt the change it will switch to, when one is pending. A
	// renewal under a pending change is capped at EffectiveAt, which is how
	// the holder learns it must be on the new content by then: under the cap
	// Deadline and EffectiveAt are the same instant. All empty for the
	// control leader identity, which has no Assignment record.
	ContentScope        string
	PendingContentScope string
	EffectiveAt         time.Time
	// TimelineRecordRevision is the record's word on which timeline revision
	// the Query Group is on, as of this renewal; zero when the record does
	// not say. See AssignmentRecord.TimelineRecordRevision.
	TimelineRecordRevision uint64
}

// ContentChangePending reports whether the lease was renewed under a content
// change that has not taken effect yet.
func (lease Lease) ContentChangePending() bool {
	return lease.PendingContentScope != "" && !lease.EffectiveAt.IsZero()
}

type PublicationAuthority struct {
	Fence    execution.OwnerFence
	Deadline time.Time
}

type FencedCASStatus string

const (
	FencedCASApplied    FencedCASStatus = "APPLIED"
	FencedCASConflict   FencedCASStatus = "CONFLICT"
	FencedCASStaleOwner FencedCASStatus = "STALE_OWNER"
	// FencedCASContentMoved is the fence refusing a writer whose declared
	// content scope is no longer the one the Assignment record names. The
	// error beside it is ErrContentScopeMoved, not ErrStaleFence.
	FencedCASContentMoved FencedCASStatus = "CONTENT_MOVED"
)

// FencedCASRequest is one fenced write of a control value. It names no
// instant: whether the fence's lease is live is decided on Redis's clock
// inside the script (FenceLua), not from anything the writer says.
type FencedCASRequest struct {
	Fence           execution.OwnerFence
	Namespace       string
	ExpectedMissing bool
	Expected        []byte
	Value           []byte
	TTL             time.Duration
	// ContentScope, when set, is the executable view the writer is acting
	// on; the fence then also refuses a record that names another. Empty
	// keeps the fence as it was before the field existed.
	ContentScope string
}
