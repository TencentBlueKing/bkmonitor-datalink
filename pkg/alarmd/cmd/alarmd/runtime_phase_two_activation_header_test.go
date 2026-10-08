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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// headerRound runs one leader round of the given source status against the
// repository and returns what it answered and every renewal observation. An
// unchanged round renews after its activation and reports a failed renewal
// on the renewal's line only; a round waiting for confirmation renews as its
// whole work and answers with it.
func headerRound(t *testing.T, status controlplane.SourceRefreshStatus, repository *fakeProductionCatalogRepository) (phaseTwoControlRefreshResult, []observability.Observation) {
	t.Helper()
	publication := controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-current", PublicationEpoch: 2}
	repository.activation = controlplane.ActivationState{RecordRevision: 2, Current: publication}
	repository.snapshot = controlplane.PublishedSnapshot{Publication: publication, QueryGroups: []controlplane.QueryGroup{{Identity: "query-group-1"}}}
	reconciler := &fakeSourceReconciler{results: []controlplane.SourceRefreshResult{
		{Status: status, Publication: publication},
	}, errs: []error{nil}}
	var renewals []observability.Observation
	control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
		Source: fakeStrategySource{}, Planner: fakePrimaryQueryCompiler{}, Reconciler: reconciler,
		Activator: &fakeInitialScheduleActivator{state: repository.activation}, Repository: repository,
		Schedules: &fakeScheduleProjection{}, Progress: &fakeProductionProgressReader{},
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			if observation.Stage == observability.StageActiveQGSet && observation.DrainingQG == nil {
				renewals = append(renewals, observation)
			}
		}),
		RefreshInterval: time.Second, Wait: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return result, renewals
}

// A renewal that finds the activation header missing has the leader write it
// back and renew again, in the same round: the round is healthy and nothing
// is left for the next one.
func TestALeaderRoundWritesBackAMissingActivationHeaderAndRenews(t *testing.T) {
	repository := &fakeProductionCatalogRepository{
		renewErrs:     []error{controlplane.ErrActivationHeaderMissing, nil},
		headerRebuild: controlplane.ActivationHeaderRebuilt,
	}
	result, renewals := headerRound(t, controlplane.SourceRefreshPendingConfirmation, repository)
	if repository.headerRebuilds != 1 || repository.renewCalls != 2 {
		t.Fatalf("rebuilds %d, renewals %d, want the header written back once and the renewal tried again",
			repository.headerRebuilds, repository.renewCalls)
	}
	if result.Status != phaseTwoControlHealthy {
		t.Fatalf("result = %#v, want a healthy round", result)
	}
	for _, renewal := range renewals {
		if renewal.Result == observability.ResultDegraded {
			t.Fatalf("a renewal the leader repaired was reported degraded: %#v", renewal)
		}
	}
}

// A missing header the leader cannot write back is reported by name, on the
// round and on the renewal's own line. Returning quietly on it - as on a
// conflict with another cutover - is how the fleet went without renewals.
func TestAMissingActivationHeaderThatCannotBeWrittenBackIsReportedByName(t *testing.T) {
	repository := &fakeProductionCatalogRepository{
		renewErr:      controlplane.ErrActivationHeaderMissing,
		headerRebuild: controlplane.ActivationHeaderRebuildBodyPending,
	}
	missing := observability.ReasonCode(contract.ReasonActivationMissing)
	result, renewals := headerRound(t, controlplane.SourceRefreshPendingConfirmation, repository)
	if result.Status != phaseTwoControlDegradedLastGood || result.ReasonCode != missing ||
		!errors.Is(result.Cause, controlplane.ErrActivationHeaderMissing) {
		t.Fatalf("result = %#v, want degraded on the last good activation, named activation missing", result)
	}
	if len(renewals) != 1 || renewals[0].Result != observability.ResultDegraded || renewals[0].ReasonCode != missing {
		t.Fatalf("renewal observations = %#v, want one degraded, named activation missing", renewals)
	}
	unchanged := &fakeProductionCatalogRepository{
		renewErr:      controlplane.ErrActivationHeaderMissing,
		headerRebuild: controlplane.ActivationHeaderRebuildBodyPending,
	}
	if _, renewals := headerRound(t, controlplane.SourceRefreshUnchanged, unchanged); len(renewals) != 1 ||
		renewals[0].Result != observability.ResultDegraded || renewals[0].ReasonCode != missing {
		t.Fatalf("unchanged round renewal observations = %#v, want one degraded, named activation missing", renewals)
	}
}

// Another cutover's header is still the next round's to settle and is not
// reported: only a missing one is.
func TestARenewalConflictWithAnotherCutoverIsStillNotReported(t *testing.T) {
	repository := &fakeProductionCatalogRepository{renewErr: controlplane.ErrActivationConflict}
	_, renewals := headerRound(t, controlplane.SourceRefreshPendingConfirmation, repository)
	if repository.headerRebuilds != 0 {
		t.Fatalf("a conflict asked for the header to be written back %d times", repository.headerRebuilds)
	}
	for _, renewal := range renewals {
		if renewal.Result == observability.ResultDegraded {
			t.Fatalf("a conflict was reported degraded: %#v", renewal)
		}
	}
}

// Only the control leader publishes the standing, and only while the header
// is missing: a replica that lost the leadership keeps a reading that is no
// longer the deployment's.
func TestTheActivationHeaderStandingIsTheLeadersAndOnlyWhileMissing(t *testing.T) {
	bundle := mustPhaseTwoWorkerBundle(t, validGoAccessRuntimeConfig(), newPhaseTwoApplicationHealth(), &fakePhaseTwoControl{}, &fakePhaseTwoOwnership{})
	since := bundle.dependencies.Now().Add(-90 * time.Second)
	reading := controlplane.ActivationHeaderReading{Missing: true, MissingSince: since, LastRebuild: controlplane.ActivationHeaderRebuildBodyPending}
	bundle.dependencies.ActivationHeader = func() controlplane.ActivationHeaderReading { return reading }
	if facts := bundle.activationHeaderFleetFacts(); facts != nil {
		t.Fatalf("a follower published %+v", facts)
	}
	bundle.mu.Lock()
	bundle.controlLeader = true
	bundle.mu.Unlock()
	facts := bundle.activationHeaderFleetFacts()
	if facts == nil || facts.LastRebuild != "body_pending" || facts.MissingSeconds < 90 {
		t.Fatalf("leader facts = %+v, want missing for 90s after body_pending", facts)
	}
	reading = controlplane.ActivationHeaderReading{}
	if facts := bundle.activationHeaderFleetFacts(); facts != nil {
		t.Fatalf("header present, facts = %+v", facts)
	}
}

// A rebuild that conflicted found a header another writer had just put back:
// the renewal is asked again, answers, and the round is healthy - not
// reported degraded for a header that was already there.
func TestARenewalRetriesOnceAfterTheHeaderRebuildConflicts(t *testing.T) {
	repository := &fakeProductionCatalogRepository{
		renewErrs:     []error{controlplane.ErrActivationHeaderMissing, nil},
		headerRebuild: controlplane.ActivationHeaderRebuildConflict,
	}
	result, renewals := headerRound(t, controlplane.SourceRefreshPendingConfirmation, repository)
	if repository.headerRebuilds != 1 || repository.renewCalls != 2 || result.Status != phaseTwoControlHealthy {
		t.Fatalf("rebuilds %d, renewals %d, result %#v: want one rebuild, a second renewal and a healthy round",
			repository.headerRebuilds, repository.renewCalls, result)
	}
	for _, renewal := range renewals {
		if renewal.Result == observability.ResultDegraded {
			t.Fatalf("a round whose header was back was reported degraded: %#v", renewal)
		}
	}
}

// The other side of the retry: the header another writer's conflict implied
// is gone again by the second renewal. The retry answers for it rather than
// swallowing it - the round is degraded on the last good activation, named
// activation missing, and the header is not asked for a second time.
func TestARenewalRetriedAfterAConflictStillReportsAHeaderStillMissing(t *testing.T) {
	repository := &fakeProductionCatalogRepository{
		renewErrs:     []error{controlplane.ErrActivationHeaderMissing, controlplane.ErrActivationHeaderMissing},
		headerRebuild: controlplane.ActivationHeaderRebuildConflict,
	}
	missing := observability.ReasonCode(contract.ReasonActivationMissing)
	result, renewals := headerRound(t, controlplane.SourceRefreshPendingConfirmation, repository)
	if repository.headerRebuilds != 1 || repository.renewCalls != 2 {
		t.Fatalf("rebuilds %d, renewals %d, want one rebuild and one retry", repository.headerRebuilds, repository.renewCalls)
	}
	if result.Status != phaseTwoControlDegradedLastGood || result.ReasonCode != missing ||
		!errors.Is(result.Cause, controlplane.ErrActivationHeaderMissing) {
		t.Fatalf("result = %#v, want degraded on the last good activation, named activation missing", result)
	}
	if len(renewals) != 1 || renewals[0].Result != observability.ResultDegraded || renewals[0].ReasonCode != missing {
		t.Fatalf("renewal observations = %#v, want one degraded, named activation missing", renewals)
	}
}
