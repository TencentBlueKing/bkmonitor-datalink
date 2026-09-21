// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// recordingScopeWriter is the pre-cutover writer of decision-016 batch 3.
// It records what it was told and, at the moment it was told, which
// publication the activation still named -- that is the whole point: the
// record has to name the new content before any Segment carries it.
type recordingScopeWriter struct {
	repository *controlplane.RedisCatalogRepository
	calls      []map[execution.QueryGroupIdentity]execution.ObjectDigest
	activeAt   []controlplane.SnapshotPublicationRef
}

func (writer *recordingScopeWriter) PublishContentScopes(ctx context.Context, changes map[execution.QueryGroupIdentity]execution.ObjectDigest) {
	copied := make(map[execution.QueryGroupIdentity]execution.ObjectDigest, len(changes))
	for identity, digest := range changes {
		copied[identity] = digest
	}
	writer.calls = append(writer.calls, copied)
	state, err := writer.repository.LoadActivation(ctx)
	if err != nil {
		writer.activeAt = append(writer.activeAt, controlplane.SnapshotPublicationRef{})
		return
	}
	writer.activeAt = append(writer.activeAt, state.Current)
}

// A cutover that changes a Query Group's content tells the scope writer the
// new digest before the activation moves: while the writer runs, the
// activation still names the previous publication. A cutover that changes
// no Query Group's content tells it nothing.
func TestTheCutoverNamesChangedContentInTheRecordsBeforeItMovesTheActivation(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:scope-before-cutover", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	both := twoQueryGroupCatalog(t)
	returning, staying := both.QueryGroups[0], both.QueryGroups[1]
	progress := &activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{
		returning.Identity: {Status: execution.ProgressMissing}, staying.Identity: {Status: execution.ProgressMissing},
	}}
	clock := []time.Time{time.Unix(90, 0), time.Unix(180, 0), time.Unix(180, 0), time.Unix(180, 0), time.Unix(180, 0), time.Unix(180, 0)}
	clockCalls := 0
	writer := &recordingScopeWriter{repository: repository}
	reconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(repository, compiler, semantics, progress, func() time.Time {
		at := clock[clockCalls]
		clockCalls++
		return at
	})
	if err != nil {
		t.Fatal(err)
	}
	reconciler.WithContentScopeWriter(writer)

	first, _, err := repository.PublishCatalog(ctx, both)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := controlplane.NewInitialScheduleActivator(repository, compiler, semantics, func() time.Time { return time.Unix(60, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := initial.Ensure(ctx, first.Publication); err != nil {
		t.Fatal(err)
	}

	// Retiring one Query Group changes no content the new publication
	// carries: nothing to name.
	onlyStaying := controlplane.Catalog{QueryGroups: []controlplane.QueryGroup{staying}}
	onlyStaying.SnapshotRevision = execution.SnapshotRevision(mustDigest(t, "alarmd-strategy-snapshot-v1", onlyStaying.QueryGroups))
	second, _, err := repository.PublishCatalog(ctx, onlyStaying)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(ctx, second.Publication); err != nil {
		t.Fatal(err)
	}
	if len(writer.calls) != 0 {
		t.Fatalf("writer told %+v on a publication that changed no content, want nothing", writer.calls)
	}

	// The retired Query Group returns: its content is new to the activation
	// and is named before the activation moves to the publication that
	// carries it.
	progress.byGroup[returning.Identity] = execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Identity: execution.ProgressIdentity{QueryGroup: returning.Identity}, NextSlot: 90, LastFullSlot: 60,
		LastCompletionKind: execution.CompletionFull,
	}}
	third, _, err := repository.PublishCatalog(ctx, both)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := reconciler.Ensure(ctx, third.Publication)
	if err != nil || reopened.Current != third.Publication {
		t.Fatalf("Ensure(third) = (%+v, %v)", reopened, err)
	}
	if len(writer.calls) != 1 {
		t.Fatalf("writer told %d times, want once for the publication that brought new content", len(writer.calls))
	}
	manifest, err := repository.LoadCatalogManifest(ctx, third.Publication.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	var wantDigest execution.ObjectDigest
	for _, entry := range manifest.QueryGroups {
		if entry.QueryGroup == returning.Identity {
			wantDigest = entry.ObjectDigest
		}
	}
	if wantDigest == "" {
		t.Fatalf("manifest %+v names no digest for %s", manifest.QueryGroups, returning.Identity)
	}
	if got := writer.calls[0]; len(got) != 1 || got[returning.Identity] != wantDigest {
		t.Fatalf("writer told %+v, want only %s at its new digest %s; the staying Query Group is unchanged", got, returning.Identity, wantDigest)
	}
	if writer.activeAt[0] != second.Publication {
		t.Fatalf("while the writer ran the activation named %+v, want the previous publication %+v: the record goes first, the Segment second",
			writer.activeAt[0], second.Publication)
	}
}
