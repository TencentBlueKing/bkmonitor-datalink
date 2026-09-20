// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

// Package scheduler selects Query Group ownership and starts frozen Slot
// execution. It does not contain Access, evaluation or completion logic.
package scheduler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

var ErrNoEligibleWorker = errors.New("alarmd scheduler: no eligible worker")

type WorkerEligibility interface {
	Eligible(execution.QueryGroupIdentity, ownership.WorkerRegistration, time.Time) bool
}

type staticWorkerEligibility struct {
	required ownership.WorkerCompatibility
}

func NewStaticWorkerEligibility(required ownership.WorkerCompatibility) (WorkerEligibility, error) {
	if err := required.Validate(); err != nil {
		return nil, err
	}
	return staticWorkerEligibility{required: required}, nil
}

func (eligibility staticWorkerEligibility) Eligible(
	_ execution.QueryGroupIdentity,
	worker ownership.WorkerRegistration,
	_ time.Time,
) bool {
	return worker.Compatibility() == eligibility.required
}

type Router struct {
	additionalEligibility WorkerEligibility
}

func NewRouter(additionalEligibility WorkerEligibility) *Router {
	return &Router{additionalEligibility: additionalEligibility}
}

func (router *Router) Select(
	queryGroup execution.QueryGroupIdentity,
	workers []ownership.WorkerRegistration,
	at time.Time,
) (ownership.WorkerRegistration, error) {
	if router == nil || queryGroup == "" || at.IsZero() {
		return ownership.WorkerRegistration{}, errors.New("alarmd scheduler: invalid routing request")
	}
	var selected ownership.WorkerRegistration
	var selectedScore [sha256.Size]byte
	found := false
	seen := make(map[string]struct{}, len(workers))
	for _, worker := range workers {
		if err := worker.Validate(); err != nil {
			return ownership.WorkerRegistration{}, err
		}
		if _, duplicate := seen[worker.WorkerID]; duplicate {
			return ownership.WorkerRegistration{}, errors.New("alarmd scheduler: duplicate worker identity")
		}
		seen[worker.WorkerID] = struct{}{}
		if worker.AssignmentReadiness != ownership.WorkerReady || !worker.ExpiresAt.After(at) {
			continue
		}
		if router.additionalEligibility != nil && !router.additionalEligibility.Eligible(queryGroup, worker, at) {
			continue
		}
		score := sha256.Sum256([]byte(string(queryGroup) + "\x00" + worker.WorkerID))
		if !found || bytes.Compare(score[:], selectedScore[:]) > 0 {
			selected, selectedScore, found = worker, score, true
		}
	}
	if !found {
		return ownership.WorkerRegistration{}, ErrNoEligibleWorker
	}
	return selected, nil
}

// incumbentEligible reports whether the Worker named by workerID is in the
// ready set under the same membership rules Select applies: a valid READY
// registration that has not expired at the given time and, when configured,
// the additional eligibility. Select itself is left untouched.
func (router *Router) incumbentEligible(
	queryGroup execution.QueryGroupIdentity,
	workerID string,
	workers []ownership.WorkerRegistration,
	at time.Time,
) bool {
	if router == nil || workerID == "" {
		return false
	}
	for _, worker := range workers {
		if worker.WorkerID != workerID {
			continue
		}
		if worker.Validate() != nil || worker.AssignmentReadiness != ownership.WorkerReady || !worker.ExpiresAt.After(at) {
			return false
		}
		return router.additionalEligibility == nil || router.additionalEligibility.Eligible(queryGroup, worker, at)
	}
	return false
}

type AssignmentStore interface {
	ListReadyWorkers(context.Context, time.Time) ([]ownership.WorkerRegistration, error)
	ReadAssignment(context.Context, execution.QueryGroupIdentity) (ownership.AssignmentRecord, error)
	PublishAssignment(
		context.Context,
		ownership.PublicationAuthority,
		ownership.AssignmentDecision,
	) (ownership.AssignmentRecord, error)
}

type Reconciler struct {
	router *Router
	store  AssignmentStore
}

func NewReconciler(router *Router, store AssignmentStore) (*Reconciler, error) {
	if router == nil || store == nil {
		return nil, errors.New("alarmd scheduler: router and Assignment store are required")
	}
	return &Reconciler{router: router, store: store}, nil
}

// ListReadyWorkers reads the ready set once for a reconcile round. A round
// reconciles every Query Group against this one set: listing per Query
// Group costs a registry read per Query Group and, worse, lets one round
// decide different Query Groups on different ready sets, a world state that
// never existed at any instant.
func (reconciler *Reconciler) ListReadyWorkers(ctx context.Context, at time.Time) ([]ownership.WorkerRegistration, error) {
	if reconciler == nil {
		return nil, errors.New("alarmd scheduler: initialized reconciler is required")
	}
	return reconciler.store.ListReadyWorkers(ctx, at)
}

// Reconcile settles one Query Group on its own, listing the ready set for
// it. A round over many Query Groups must list once and use ReconcileWith.
func (reconciler *Reconciler) Reconcile(
	ctx context.Context,
	authority ownership.PublicationAuthority,
	queryGroup execution.QueryGroupIdentity,
	at time.Time,
) (ownership.AssignmentRecord, error) {
	workers, err := reconciler.ListReadyWorkers(ctx, at)
	if err != nil {
		return ownership.AssignmentRecord{}, err
	}
	return reconciler.ReconcileWith(ctx, authority, queryGroup, workers, at)
}

// ReconcileWith settles one Query Group against a ready set the caller read
// for the round: a ready, eligible incumbent keeps its Assignment; otherwise
// rendezvous placement over that set publishes a new one under the given
// authority. It never lists workers itself.
func (reconciler *Reconciler) ReconcileWith(
	ctx context.Context,
	authority ownership.PublicationAuthority,
	queryGroup execution.QueryGroupIdentity,
	workers []ownership.WorkerRegistration,
	at time.Time,
) (ownership.AssignmentRecord, error) {
	if reconciler == nil {
		return ownership.AssignmentRecord{}, errors.New("alarmd scheduler: initialized reconciler is required")
	}
	expectedRevision := uint64(0)
	current, err := reconciler.store.ReadAssignment(ctx, queryGroup)
	hasCurrent := err == nil
	if hasCurrent {
		expectedRevision = current.RecordRevision
	} else if !errors.Is(err, ownership.ErrAssignmentAbsent) {
		return ownership.AssignmentRecord{}, err
	}
	if hasCurrent && reconciler.router.incumbentEligible(queryGroup, current.DesiredWorkerID, workers, at) {
		return current, nil
	}
	selected, err := reconciler.router.Select(queryGroup, workers, at)
	if err != nil {
		return ownership.AssignmentRecord{}, err
	}
	return reconciler.store.PublishAssignment(
		ctx, authority, ownership.AssignmentDecision{
			QueryGroup: queryGroup, DesiredWorkerID: selected.WorkerID,
			ExpectedRecordRevision: expectedRevision, PlacementReason: ownership.PlacementRendezvous, DecidedAt: at,
		},
	)
}
