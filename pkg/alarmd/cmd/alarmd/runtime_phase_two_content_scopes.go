// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// contentScopeSource is what the reconcile round needs of the catalog to
// name each Query Group's content: the activation that says which
// publication the fleet executes, and that publication's manifest.
type contentScopeSource interface {
	LoadActivation(context.Context) (controlplane.ActivationState, error)
	LoadCatalogManifest(context.Context, execution.SnapshotRevision) (controlplane.CatalogManifest, error)
}

// currentContentScopes reads the content each Query Group is published with
// (decision-016): the ObjectDigest the current activation's manifest names
// for it -- the same digest the Query Group's open Segment carries, which is
// what a worker's Slot declares. Both reads are the repository's cached ones,
// so a round costs the header check they already cost.
func currentContentScopes(source contentScopeSource) func(context.Context) (map[execution.QueryGroupIdentity]string, error) {
	return func(ctx context.Context) (map[execution.QueryGroupIdentity]string, error) {
		if source == nil {
			return nil, errors.New("phase-two content scopes: catalog repository is required")
		}
		state, err := source.LoadActivation(ctx)
		if err != nil {
			return nil, fmt.Errorf("phase-two content scopes: read activation: %w", err)
		}
		manifest, err := source.LoadCatalogManifest(ctx, state.Current.SnapshotRevision)
		if err != nil {
			return nil, fmt.Errorf("phase-two content scopes: read manifest %s: %w", state.Current.SnapshotRevision, err)
		}
		digests := make(map[execution.QueryGroupIdentity]string, len(manifest.QueryGroups))
		for _, entry := range manifest.QueryGroups {
			if entry.QueryGroup == "" || entry.ObjectDigest == "" {
				continue
			}
			digests[entry.QueryGroup] = string(entry.ObjectDigest)
		}
		return digests, nil
	}
}

// PublishContentScopes is the pre-cutover half of the content contract
// (controlplane.ContentScopeWriter): before the activation reconciler cuts
// the Segments of a new publication, it names the new content in the
// Assignment record of every Query Group whose content changes, under the
// same gate the reconcile round uses -- every ready worker declares the
// contract -- and against the record revision it just read, like the round.
// A Query Group with no record yet is placed with its content by the round.
//
// Advisory: a failure here is reported and the cutover proceeds, because
// the reconcile round writes the same scopes within one round and a
// publication must not fail for it. A lost authority is not retried here;
// the round re-acquires it.
func (runtime *productionPhaseTwoOwnership) PublishContentScopes(
	ctx context.Context,
	changes map[execution.QueryGroupIdentity]execution.ObjectDigest,
) {
	if runtime == nil || len(changes) == 0 {
		return
	}
	at := runtime.dependencies.Now()
	report := func(err error) {
		observeRuntime(ctx, runtime.dependencies.Observer, observability.Observation{
			Component: observability.ComponentOwnership, Stage: observability.StageAssignmentAcquired,
			Result: observability.ResultDegraded, Operation: observability.OperationTransition,
			Direction: observability.DirectionInternal, ReasonCode: ownershipObservationReason(err), Err: err,
		})
	}
	authority, err := runtime.ensureControlAuthority(ctx, at)
	if err != nil {
		if !errors.Is(err, ownership.ErrLeaseBusy) {
			report(fmt.Errorf("phase-two content scopes before cutover: %w", err))
		}
		return
	}
	workers, _, err := runtime.reconciler.ListReadyWorkers(ctx, at)
	if err != nil {
		report(fmt.Errorf("phase-two content scopes before cutover: list ready workers: %w", err))
		return
	}
	if !ownership.AllDeclare(workers, ownership.CapabilityContentScope) {
		return
	}
	queryGroups := make([]execution.QueryGroupIdentity, 0, len(changes))
	for queryGroup := range changes {
		queryGroups = append(queryGroups, queryGroup)
	}
	sort.Slice(queryGroups, func(left, right int) bool { return queryGroups[left] < queryGroups[right] })
	records, _, err := runtime.dependencies.Store.ReadAssignments(ctx, queryGroups)
	if err != nil {
		report(fmt.Errorf("phase-two content scopes before cutover: read records: %w", err))
		return
	}
	for _, queryGroup := range queryGroups {
		record, exists := records[queryGroup]
		digest := string(changes[queryGroup])
		if !exists || record.PendingContentScope == digest || (record.ContentScope == digest && record.PendingContentScope == "") {
			continue
		}
		if _, err := runtime.dependencies.Store.PublishAssignment(ctx, authority, ownership.AssignmentDecision{
			QueryGroup: queryGroup, DesiredWorkerID: record.DesiredWorkerID, ExpectedRecordRevision: record.RecordRevision,
			PlacementReason: record.PlacementReason, DecidedAt: at, ContentScope: digest,
		}); err != nil {
			if errors.Is(err, ownership.ErrStaleFence) {
				runtime.clearControlAuthority(authority)
				report(fmt.Errorf("phase-two content scopes before cutover: %w", err))
				return
			}
			if ctx.Err() != nil {
				return
			}
			// A revision conflict is the round or another leader having
			// written this record meanwhile; whatever it wrote is newer.
			// Anything else is reported and the next Query Group is tried.
			if !errors.Is(err, ownership.ErrAssignmentConflict) {
				report(fmt.Errorf("phase-two content scopes before cutover: %s: %w", queryGroup, err))
			}
		}
	}
}
