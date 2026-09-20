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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// pendingRefreshFacts drives one real pending round and returns what the round
// reported. The seam tests elsewhere prove the helpers compute the right values;
// this drives Refresh itself, because the defect being guarded here lived in the
// assembly and every helper involved was already correct.
func pendingRefreshFacts(t *testing.T, activated controlplane.SnapshotPublicationRef,
	queryGroups []controlplane.QueryGroup) *observability.SourceRefreshFacts {
	t.Helper()
	reconciler := &fakeSourceReconciler{results: []controlplane.SourceRefreshResult{
		{Status: controlplane.SourceRefreshPendingConfirmation, Observation: "observation-candidate"},
	}}
	repository := &fakeProductionCatalogRepository{
		activation: controlplane.ActivationState{RecordRevision: 1, Current: activated},
		snapshot:   controlplane.PublishedSnapshot{Publication: activated, QueryGroups: queryGroups},
		renewErrs:  []error{nil},
	}
	var observations []observability.Observation
	control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
		Source: fakeStrategySource{}, Planner: fakePrimaryQueryCompiler{}, Reconciler: reconciler,
		Activator: &fakeInitialScheduleActivator{}, Repository: repository,
		Schedules: &fakeScheduleProjection{}, Progress: &fakeProductionProgressReader{},
		RefreshInterval: time.Second,
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observations = append(observations, observation)
		}),
		Wait: func(context.Context, time.Duration) error { return errors.New("unexpected wait") },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Refresh(context.Background()); err != nil {
		t.Fatalf("pending Refresh() error = %v", err)
	}
	for _, observation := range observations {
		if observation.Stage == observability.StageSnapshotRefreshed && observation.SourceRefresh != nil {
			return observation.SourceRefresh
		}
	}
	t.Fatal("pending round reported no source refresh facts")
	return nil
}

// A round that publishes nothing still knows which publication the fleet is
// executing, and that value used to be written into snapshot_revision -- the
// field every other status fills with the publication just produced. One name
// over two objects made a lagging activation read as a stalled publication, and
// nothing in the record could tell them apart.
func TestAPendingRoundNamesTheActivatedRevisionApartFromThePublishedOne(t *testing.T) {
	activated := controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-activated", PublicationEpoch: 7}
	facts := pendingRefreshFacts(t, activated, []controlplane.QueryGroup{{Identity: "query-group-1"}})

	if facts.ActivatedRevision != "snapshot-activated" || facts.ActivatedEpoch != 7 {
		t.Fatalf("activated pair = (%q,%d), want the activation the fleet is executing",
			facts.ActivatedRevision, facts.ActivatedEpoch)
	}
	// The reconciler returns no publication on this branch, so naming one here
	// would be inventing it. observability/log_test.go already forbids this in
	// the emitter; the assembly was filling it anyway.
	if facts.SnapshotRevision != "" || facts.PublicationEpoch != 0 {
		t.Fatalf("pending round claimed a publication it did not make: (%q,%d)",
			facts.SnapshotRevision, facts.PublicationEpoch)
	}
}

// The size of the active set is worth reporting. A change against a previous set
// is not available on this branch -- there is only the current activation -- and
// differencing it against itself yields added=0, retired=0 and old==new every
// time. Those zeroes read as a measured finding about the source and are
// arithmetic, which is exactly how they were used as evidence.
func TestAPendingRoundReportsTheActiveSetSizeAndNotAChangeAgainstItself(t *testing.T) {
	activated := controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-activated", PublicationEpoch: 7}
	facts := pendingRefreshFacts(t, activated, []controlplane.QueryGroup{
		{Identity: "query-group-1"}, {Identity: "query-group-2"},
	})

	if !facts.ActiveQueryGroupsKnown || facts.ActiveQueryGroups != 2 {
		t.Fatalf("active set size = (%d,known=%v), want the 2 groups counted",
			facts.ActiveQueryGroups, facts.ActiveQueryGroupsKnown)
	}
	if facts.CountsKnown {
		t.Fatalf("pending round published a change it cannot have measured: old=%d new=%d added=%d retired=%d",
			facts.OldQueryGroups, facts.NewQueryGroups, facts.AddedQueryGroups, facts.RetiredQueryGroups)
	}
}

// Guards the other half of the split: the statuses that do publish must keep
// filling snapshot_revision, or the change above would have quietly emptied the
// field for every reader.
func TestAPublishingRoundStillNamesThePublicationItMade(t *testing.T) {
	publication := controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-published", PublicationEpoch: 9}
	facts := sourceRefreshIdentity(controlplane.SourceRefreshResult{
		Status: controlplane.SourceRefreshPublished, Observation: "observation-published",
		Publication: publication,
	}, publication)

	if facts.SnapshotRevision != "snapshot-published" || facts.PublicationEpoch != 9 {
		t.Fatalf("published pair = (%q,%d), want the publication this round made",
			facts.SnapshotRevision, facts.PublicationEpoch)
	}
	if facts.ActivatedRevision != "" || facts.ActivatedEpoch != 0 {
		t.Fatalf("publishing round filled the activated pair: (%q,%d)",
			facts.ActivatedRevision, facts.ActivatedEpoch)
	}
}
