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
}

func (worker WorkerRegistration) Validate() error {
	if worker.WorkerID == "" || worker.DeploymentProfile == "" || worker.CapabilitiesDigest == "" || worker.ExpiresAt.IsZero() {
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
	return nil
}

type PlacementReason string

const PlacementRendezvous PlacementReason = "RENDEZVOUS"

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
		decision.PlacementReason != PlacementRendezvous {
		return errors.New("alarmd ownership: invalid Assignment decision")
	}
	return nil
}

func (record AssignmentRecord) Validate() error {
	if record.QueryGroup == "" || record.DesiredWorkerID == "" || record.AssignmentGeneration == 0 ||
		record.RecordRevision == 0 || record.ControlEpoch == 0 || record.AssignedAt.IsZero() {
		return errors.New("alarmd ownership: incomplete Assignment record")
	}
	if record.PlacementReason != PlacementRendezvous {
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
