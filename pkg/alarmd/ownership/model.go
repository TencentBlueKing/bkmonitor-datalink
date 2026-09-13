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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

var (
	ErrNotDesired         = errors.New("alarmd ownership: worker is not the desired owner")
	ErrLeaseBusy          = errors.New("alarmd ownership: lease is held by another owner")
	ErrStaleFence         = errors.New("alarmd ownership: owner fence is stale")
	ErrAssignmentAbsent   = errors.New("alarmd ownership: assignment is absent")
	ErrAssignmentConflict = errors.New("alarmd ownership: Assignment record revision conflict")
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
)

func (reason PlacementReason) valid() bool {
	return reason == PlacementRendezvous || reason == PlacementRebalance
}

type AssignmentRecord struct {
	QueryGroup           execution.QueryGroupIdentity
	DesiredWorkerID      string
	AssignmentGeneration uint64
	RecordRevision       uint64
	ControlEpoch         uint64
	PlacementReason      PlacementReason
	AssignedAt           time.Time
}

// AssignmentDecision is a Control Leader decision based on one observed
// Assignment revision. ExpectedRecordRevision is zero only for first publish.
type AssignmentDecision struct {
	QueryGroup             execution.QueryGroupIdentity
	DesiredWorkerID        string
	ExpectedRecordRevision uint64
	PlacementReason        PlacementReason
	DecidedAt              time.Time
}

func (decision AssignmentDecision) Validate() error {
	if decision.QueryGroup == "" || decision.DesiredWorkerID == "" || decision.DecidedAt.IsZero() ||
		!decision.PlacementReason.valid() {
		return errors.New("alarmd ownership: invalid Assignment decision")
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

type Lease struct {
	Fence    execution.OwnerFence
	Deadline time.Time
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
)

type FencedCASRequest struct {
	Fence           execution.OwnerFence
	At              time.Time
	Namespace       string
	ExpectedMissing bool
	Expected        []byte
	Value           []byte
	TTL             time.Duration
}
