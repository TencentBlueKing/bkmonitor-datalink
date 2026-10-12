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
// publication the fleet executes, and that publication's content.
type contentScopeSource interface {
	LoadActivationHead(context.Context) (controlplane.ActivationState, error)
	// LoadPublishedContent is the publication's content as the view reads
	// it. The repository remembers it per publication; while it does, a read
	// is an existence check of the manifest and the two small reads that say
	// the publication still occurs, never the manifest itself.
	LoadPublishedContent(context.Context, controlplane.SnapshotPublicationRef) (controlplane.PublishedContent, error)
	// ActivationBlocked is the Query Groups a cutover held back; their scope
	// is what their open Segment names, not what the manifest names.
	ActivationBlocked(context.Context) ([]controlplane.BlockedQueryGroup, error)
}

// currentContentScopes reads the content each Query Group is published with
// (decision-016): the ObjectDigest the current activation's manifest names
// for it -- the same digest the Query Group's open Segment carries, which is
// what a worker's Slot declares. It reads the content the way the view does,
// through the repository's memory of the publication, so a round with the
// publication unchanged costs the header check and a few small reads; the
// manifest, which is most of a megabyte on a few thousand Query Groups, is
// read once per publication and not once per round.
func currentContentScopes(source contentScopeSource) func(context.Context) (map[execution.QueryGroupIdentity]string, error) {
	return func(ctx context.Context) (map[execution.QueryGroupIdentity]string, error) {
		if source == nil {
			return nil, errors.New("phase-two content scopes: catalog repository is required")
		}
		state, err := source.LoadActivationHead(ctx)
		if err != nil {
			return nil, fmt.Errorf("phase-two content scopes: read activation: %w", err)
		}
		published, err := source.LoadPublishedContent(ctx, state.Current)
		if err != nil {
			return nil, fmt.Errorf("phase-two content scopes: read published content %s: %w", state.Current.SnapshotRevision, err)
		}
		blocked, err := source.ActivationBlocked(ctx)
		if err != nil {
			return nil, fmt.Errorf("phase-two content scopes: read held-back Query Groups: %w", err)
		}
		// A held-back Query Group is scoped to what its open Segment names,
		// and one with no Segment that can run has no scope: the same rule the
		// view applies, through the same function, so the two cannot differ.
		running := controlplane.ApplyBlockedToContent(published.Groups, blocked)
		digests := make(map[execution.QueryGroupIdentity]string, len(running))
		for identity, entry := range running {
			if identity == "" || entry.Digest == "" {
				continue
			}
			digests[identity] = string(entry.Digest)
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
