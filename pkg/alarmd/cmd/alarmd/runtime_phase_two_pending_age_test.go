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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The round hands the source refresh's own status to whoever applies it:
// the pending age is kept from it, and a round that dropped it would leave
// the age at zero while a change waits.
func TestTheRefreshRoundCarriesTheSourceStatus(t *testing.T) {
	activated := controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-activated", PublicationEpoch: 7}
	status := controlplane.SourceRefreshPendingConfirmation
	reconciler := &fakeSourceReconciler{results: []controlplane.SourceRefreshResult{
		{Status: status, Observation: "observation-1", Latest: activated, Publication: activated},
	}}
	repository := &fakeProductionCatalogRepository{
		activation: controlplane.ActivationState{RecordRevision: 1, Current: activated},
		snapshot: controlplane.PublishedSnapshot{Publication: activated,
			QueryGroups: []controlplane.QueryGroup{{Identity: "query-group-1"}}},
		renewErrs: []error{nil},
	}
	control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
		Source: fakeStrategySource{}, Planner: fakePrimaryQueryCompiler{}, Reconciler: reconciler,
		Activator: &fakeInitialScheduleActivator{}, Repository: repository,
		Schedules: &fakeScheduleProjection{}, Progress: &fakeProductionProgressReader{},
		RefreshInterval: time.Second, Observer: observability.NopObserver{},
		Wait: func(context.Context, time.Duration) error { return errors.New("unexpected wait") },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.Refresh(context.Background())
	if err != nil || result.SourceRefreshStatus != status {
		t.Fatalf("%s round = (%q, %v), want the status carried", status, result.SourceRefreshStatus, err)
	}
}

func sourceRound(status controlplane.SourceRefreshStatus) phaseTwoControlRefreshResult {
	return phaseTwoControlRefreshResult{QueryGroups: []execution.QueryGroupIdentity{"query-group-1"},
		Status: phaseTwoControlHealthy, SourceRefreshStatus: status}
}

// A change that stays pending is what the pending age reads: it rises from
// the first pending round across the ones after it, a failed round in
// between does not reset it, and PUBLISHED or UNCHANGED puts it back to
// zero. Every one of those pending rounds is a successful refresh, so the
// success age says nothing here.
func TestThePendingAgeRisesWhileAChangeWaitsAndClearsWhenItGoesLive(t *testing.T) {
	pending := sourceRound(controlplane.SourceRefreshPendingConfirmation)
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"},
		initialResult: sourceRound(controlplane.SourceRefreshUnchanged),
		refreshResults: []phaseTwoControlRefreshResult{pending, pending, degradedBy(errors.New("source down")), pending,
			sourceRound(controlplane.SourceRefreshPublished), pending, sourceRound(controlplane.SourceRefreshUnchanged)}}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: newFakePhaseTwoQueryGroup()}
	fixture := newControlSourceFixture(t, control, owner)
	fixture.start()
	read := func(wantAge float64, wantRounds int) {
		t.Helper()
		facts := fixture.bundle.controlSourceFleetFacts()
		stats := fixture.bundle.controlSourceStats()
		if facts == nil || facts.PendingConfirmationAgeSeconds == nil || *facts.PendingConfirmationAgeSeconds != wantAge ||
			facts.PendingConfirmationRounds != wantRounds || !stats.Leading || stats.PendingConfirmationAgeSeconds != wantAge {
			t.Fatalf("facts %+v stats %+v, want age %v over %d rounds", facts, stats, wantAge, wantRounds)
		}
	}
	read(0, 0)
	step := func() {
		fixture.clock = fixture.clock.Add(time.Minute)
		fixture.tick()
	}
	step() // pending
	read(0, 1)
	step() // pending
	read(60, 2)
	step() // failed round
	read(120, 2)
	step() // pending
	read(180, 3)
	step() // published
	read(0, 0)
	step() // pending again, a new episode
	read(0, 1)
	step() // unchanged
	read(0, 0)
}

// A process that is not leading refreshes nothing and reports no pending
// age at all, rather than a zero that reads as "nothing waiting".
func TestAFollowerReportsNoPendingAge(t *testing.T) {
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"},
		initialResult: sourceRound(controlplane.SourceRefreshPendingConfirmation)}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: newFakePhaseTwoQueryGroup()}
	fixture := newControlSourceFixture(t, control, owner)
	fixture.start()
	fixture.bundle.noteControlRole(false, nil)
	if facts := fixture.bundle.controlSourceFleetFacts(); facts == nil || facts.PendingConfirmationAgeSeconds != nil {
		t.Fatalf("follower facts = %+v, want no pending age", facts)
	}
	if stats := fixture.bundle.controlSourceStats(); stats.Leading {
		t.Fatalf("follower stats = %+v, want not leading", stats)
	}
}

// A leader that loses the lease and wins it back starts the pending age from
// its new term: the stretch it spent as a follower refreshed nothing and is
// not part of any wait it can speak for.
func TestThePendingAgeStartsAgainInANewTerm(t *testing.T) {
	pending := sourceRound(controlplane.SourceRefreshPendingConfirmation)
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"query-group-1"},
		initialResult: pending, refreshResults: []phaseTwoControlRefreshResult{pending}}
	owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"query-group-1"}, runner: newFakePhaseTwoQueryGroup()}
	fixture := newControlSourceFixture(t, control, owner)
	fixture.start()
	fixture.clock = fixture.clock.Add(10 * time.Minute)
	fixture.bundle.noteControlRole(false, nil)
	fixture.clock = fixture.clock.Add(10 * time.Minute)
	fixture.bundle.noteControlRole(true, nil)
	fixture.tick()
	facts := fixture.bundle.controlSourceFleetFacts()
	if facts == nil || facts.PendingConfirmationAgeSeconds == nil || *facts.PendingConfirmationAgeSeconds != 0 || facts.PendingConfirmationRounds != 1 {
		t.Fatalf("facts after a new term = %+v, want the age counted from the new term's first pending round", facts)
	}
}
